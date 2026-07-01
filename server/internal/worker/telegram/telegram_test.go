package telegram

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

type mockElig struct {
	state membership.State
	tier  string
}

func (m mockElig) Attest(context.Context, string) (membership.State, string, error) {
	return m.state, m.tier, nil
}

func newProc(t *testing.T, elig Attester) (*Processor, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewProcessor(st, elig, "pool1abc"), st
}

func seedCode(t *testing.T, st *store.Store, code, sch string) {
	t.Helper()
	if err := st.ActivationCodes().Create(context.Background(), domain.ActivationCode{
		Code: crypto.HashToken(code), StakeCredentialHash: sch, ChannelType: "telegram",
		Status: domain.ActivationActive, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestActivate_BindsSubscription(t *testing.T) {
	ctx := context.Background()
	proc, st := newProc(t, mockElig{state: membership.StateActive, tier: "gold"})
	seedCode(t, st, "abc123", "sch-1")

	reply := proc.Handle(ctx, Update{UserID: "tg-42", ChatID: "tg-42", Text: "/start abc123"})
	if !strings.Contains(reply, "Subscribed") || !strings.Contains(reply, "gold") {
		t.Fatalf("activate reply: %q", reply)
	}
	sess, err := st.Subscriptions().GetByChannelUser(ctx, "pool1abc", "telegram", "tg-42")
	if err != nil || sess.Tier != "gold" || sess.Status != domain.SubActive {
		t.Fatalf("session: %v %+v", err, sess)
	}

	// Re-using the same code → already used.
	reply = proc.Handle(ctx, Update{UserID: "tg-42", ChatID: "tg-42", Text: "/start abc123"})
	if !strings.Contains(reply, "already been used") {
		t.Fatalf("reused code reply: %q", reply)
	}
}

func TestActivate_InvalidAndIneligible(t *testing.T) {
	ctx := context.Background()
	// Invalid code.
	proc, _ := newProc(t, mockElig{state: membership.StateActive, tier: "gold"})
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/start nope"}); !strings.Contains(r, "Invalid") {
		t.Errorf("invalid code: %q", r)
	}
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/activate"}); !strings.Contains(r, "provide an activation code") {
		t.Errorf("missing arg: %q", r)
	}
	// Ineligible at bind time.
	proc2, st2 := newProc(t, mockElig{state: membership.StateNone})
	seedCode(t, st2, "code2", "sch-2")
	if r := proc2.Handle(ctx, Update{UserID: "u", Text: "/start code2"}); !strings.Contains(r, "no longer qualifies") {
		t.Errorf("ineligible: %q", r)
	}
}

func TestStatusAndUnsubscribe(t *testing.T) {
	ctx := context.Background()
	proc, st := newProc(t, mockElig{state: membership.StateActive, tier: "silver"})

	// Not subscribed yet.
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/status"}); !strings.Contains(r, "not subscribed") {
		t.Errorf("status pre: %q", r)
	}
	seedCode(t, st, "c", "sch")
	proc.Handle(ctx, Update{UserID: "u", Text: "/start c"})

	// TC-5: status shows tier, the informational "Valid through" date, and the last
	// verification — not a bare "Expires".
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/status"}); !strings.Contains(r, "silver") ||
		!strings.Contains(r, "Valid through") || !strings.Contains(r, "Last verified") {
		t.Errorf("status: %q", r)
	}
	// A session in grace surfaces the expiring warning + re-delegate guidance.
	sess, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1abc", "telegram", "u")
	grace := time.Now().Add(120 * time.Hour)
	sess.GraceUntil = &grace
	if err := st.Subscriptions().Upsert(ctx, *sess); err != nil {
		t.Fatal(err)
	}
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/status"}); !strings.Contains(r, "Expiring") ||
		!strings.Contains(r, "Re-delegate") {
		t.Errorf("grace status: %q", r)
	}
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/unsubscribe"}); !strings.Contains(r, "unsubscribed") {
		t.Errorf("unsubscribe: %q", r)
	}
	sess, _ = st.Subscriptions().GetByChannelUser(ctx, "pool1abc", "telegram", "u")
	if sess.Status != domain.SubCancelled {
		t.Errorf("status after unsub = %s", sess.Status)
	}
}

// TestSessionTTLSharedWithDomain (S0019 p3-5 / TC-13): the activation-display TTL
// binds to the single domain source of truth (same const the reconciler slides).
func TestSessionTTLSharedWithDomain(t *testing.T) {
	if sessionTTL != domain.SubscriptionTTL {
		t.Fatalf("sessionTTL %v != domain.SubscriptionTTL %v", sessionTTL, domain.SubscriptionTTL)
	}
}

func TestParseCommand_StripsBotSuffix(t *testing.T) {
	cmd, arg := parseCommand("/start@PaoBot xyz")
	if cmd != "/start" || arg != "xyz" {
		t.Fatalf("parse = %q %q", cmd, arg)
	}
	if r := proc_help(t); !strings.Contains(r, "/help") {
		t.Errorf("help: %q", r)
	}
}

func proc_help(t *testing.T) string {
	proc, _ := newProc(t, mockElig{})
	return proc.Handle(context.Background(), Update{Text: "/help"})
}

// fakeTransport delivers a fixed batch once, then blocks until ctx done.
type fakeTransport struct {
	updates []Update
	sent    []string
	served  bool
}

func (f *fakeTransport) GetUpdates(ctx context.Context, offset int) ([]Update, error) {
	if f.served {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	f.served = true
	return f.updates, nil
}

func (f *fakeTransport) SendMessage(_ context.Context, chatID, text string) error {
	f.sent = append(f.sent, chatID+": "+text)
	return nil
}

func TestWorker_RunDispatchesAndReplies(t *testing.T) {
	proc, st := newProc(t, mockElig{state: membership.StateActive, tier: "gold"})
	seedCode(t, st, "wc", "sch")
	ft := &fakeTransport{updates: []Update{{UpdateID: 5, UserID: "u1", ChatID: "u1", Text: "/start wc"}}}
	w := NewWorker(proc, ft)
	w.interval = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if len(ft.sent) != 1 || !strings.Contains(ft.sent[0], "Subscribed") {
		t.Fatalf("worker sent = %v", ft.sent)
	}
}
