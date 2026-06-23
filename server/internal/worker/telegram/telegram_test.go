package telegram

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/core/rules"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

type mockElig struct {
	eligible bool
	tier     string
	ents     []string
}

func (m mockElig) Eligibility(context.Context, string) (bool, rules.Decision, error) {
	return m.eligible, rules.Decision{Eligible: m.eligible, Tier: m.tier, Entitlements: m.ents}, nil
}

func newProc(t *testing.T, elig Eligibilizer) (*Processor, *store.Store) {
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
	proc, st := newProc(t, mockElig{eligible: true, tier: "gold", ents: []string{"news"}})
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
	proc, _ := newProc(t, mockElig{eligible: true, tier: "gold"})
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/start nope"}); !strings.Contains(r, "Invalid") {
		t.Errorf("invalid code: %q", r)
	}
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/activate"}); !strings.Contains(r, "provide an activation code") {
		t.Errorf("missing arg: %q", r)
	}
	// Ineligible at bind time.
	proc2, st2 := newProc(t, mockElig{eligible: false})
	seedCode(t, st2, "code2", "sch-2")
	if r := proc2.Handle(ctx, Update{UserID: "u", Text: "/start code2"}); !strings.Contains(r, "no longer meets") {
		t.Errorf("ineligible: %q", r)
	}
}

func TestStatusAndUnsubscribe(t *testing.T) {
	ctx := context.Background()
	proc, st := newProc(t, mockElig{eligible: true, tier: "silver", ents: []string{"news"}})

	// Not subscribed yet.
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/status"}); !strings.Contains(r, "not subscribed") {
		t.Errorf("status pre: %q", r)
	}
	seedCode(t, st, "c", "sch")
	proc.Handle(ctx, Update{UserID: "u", Text: "/start c"})

	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/status"}); !strings.Contains(r, "silver") {
		t.Errorf("status: %q", r)
	}
	if r := proc.Handle(ctx, Update{UserID: "u", Text: "/unsubscribe"}); !strings.Contains(r, "unsubscribed") {
		t.Errorf("unsubscribe: %q", r)
	}
	sess, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1abc", "telegram", "u")
	if sess.Status != domain.SubCancelled {
		t.Errorf("status after unsub = %s", sess.Status)
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
	proc, st := newProc(t, mockElig{eligible: true, tier: "gold", ents: []string{"news"}})
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
