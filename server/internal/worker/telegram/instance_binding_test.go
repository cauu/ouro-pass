package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/crypto"
)

// TestActivate_InstanceBinding proves S0005 p2-2 / TC-4: an activation code bound
// to one instance is rejected by another instance's bot, accepted by its own
// (writing the right channel_id), and the same channel user holds independent
// subscriptions across instances.
func TestActivate_InstanceBinding(t *testing.T) {
	ctx := context.Background()
	st := newSupStore(t) // migrated store, pool "pool1"
	elig := mockElig{state: membership.StateActive, tier: "gold"}
	procA := NewInstanceProcessor(st, elig, "pool1abc", "chA")
	procB := NewInstanceProcessor(st, elig, "pool1abc", "chB")

	seed := func(code, sch, channelID string) {
		t.Helper()
		if err := st.ActivationCodes().Create(ctx, domain.ActivationCode{
			Code: crypto.HashToken(code), StakeCredentialHash: sch, ChannelID: channelID, ChannelType: "telegram",
			Status: domain.ActivationActive, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("seed code %s: %v", code, err)
		}
	}

	// Code bound to instance chA.
	seed("codeA", "sch-1", "chA")

	// Instance B's bot must NOT redeem instance A's code.
	if r := procB.Handle(ctx, Update{UserID: "u1", ChatID: "u1", Text: "/start codeA"}); !strings.Contains(r, "Invalid") {
		t.Fatalf("instance B should reject instance A's code, got %q", r)
	}
	// The code is still redeemable by its own instance afterwards.
	if r := procA.Handle(ctx, Update{UserID: "u1", ChatID: "u1", Text: "/start codeA"}); !strings.Contains(r, "Subscribed") {
		t.Fatalf("instance A activate: %q", r)
	}
	sa, err := st.Subscriptions().GetByInstanceUser(ctx, "chA", "u1")
	if err != nil || sa.ChannelID != "chA" {
		t.Fatalf("subscription A channel_id wrong: %v %+v", err, sa)
	}

	// The same user activates on instance B with a B-bound code → independent row.
	seed("codeB", "sch-1", "chB")
	if r := procB.Handle(ctx, Update{UserID: "u1", ChatID: "u1", Text: "/start codeB"}); !strings.Contains(r, "Subscribed") {
		t.Fatalf("instance B activate: %q", r)
	}
	sb, err := st.Subscriptions().GetByInstanceUser(ctx, "chB", "u1")
	if err != nil || sb.ChannelID != "chB" {
		t.Fatalf("subscription B channel_id wrong: %v %+v", err, sb)
	}
	if sb.SessionID == sa.SessionID {
		t.Fatal("same user on two instances must be two independent subscriptions")
	}

	// Both coexist: each bot's /status sees its own instance subscription.
	if r := procA.Handle(ctx, Update{UserID: "u1", ChatID: "u1", Text: "/status"}); !strings.Contains(r, "gold") {
		t.Fatalf("status on A: %q", r)
	}
	if r := procB.Handle(ctx, Update{UserID: "u1", ChatID: "u1", Text: "/status"}); !strings.Contains(r, "gold") {
		t.Fatalf("status on B: %q", r)
	}
}

// TestDecodeUsername round-trips the public bot username used for deep links.
func TestDecodeUsername(t *testing.T) {
	cipher, err := crypto.NewFieldCipherHex("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := EncodeConfig(cipher, "123:token", "MembersBot")
	if err != nil {
		t.Fatal(err)
	}
	if u := DecodeUsername(blob); u != "MembersBot" {
		t.Fatalf("DecodeUsername = %q, want MembersBot", u)
	}
	// Token still decodes (config carries both).
	if tok, _ := DecodeToken(cipher, blob); tok != "123:token" {
		t.Fatalf("DecodeToken = %q", tok)
	}
}
