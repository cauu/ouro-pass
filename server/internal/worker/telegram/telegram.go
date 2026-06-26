// Package telegram is the Telegram bot worker (detailed §9.7). It is a
// long-running worker, not a REST handler: it pulls updates (long-poll) and
// dispatches the command grammar (/start|/activate|/status|/unsubscribe|/help),
// binding members to channel subscriptions. The Telegram transport is an
// interface so the command logic is unit-tested without a live bot (D5).
package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

// sessionTTL is how long a subscription stays valid before reconciliation must
// re-verify it.
const sessionTTL = 30 * 24 * time.Hour

// Update is the subset of a Telegram update the bot acts on.
type Update struct {
	UpdateID int
	UserID   string // message.from.id (Telegram-authenticated)
	ChatID   string // message.chat.id (reply target)
	Text     string
}

// Transport abstracts the Telegram Bot API (long-poll receive + send).
type Transport interface {
	GetUpdates(ctx context.Context, offset int) ([]Update, error)
	SendMessage(ctx context.Context, chatID, text string) error
}

// Attester re-derives a credential's membership state + first-party tier at bind
// time (D8): only members may subscribe, and the tier seeds channel targeting.
type Attester interface {
	Attest(ctx context.Context, stakeCredentialHash string) (membership.State, string, error)
}

// Processor handles the command grammar; it is transport-agnostic.
type Processor struct {
	poolID     string
	channelID  string // S0005: the instance this bot serves ("" = legacy single-instance)
	activation *store.ActivationCodeRepo
	subs       *store.SubscriptionRepo
	elig       Attester
	now        func() time.Time
}

// NewProcessor builds a legacy single-instance command processor (no channel_id).
func NewProcessor(st *store.Store, elig Attester, poolID string) *Processor {
	return NewInstanceProcessor(st, elig, poolID, "")
}

// NewInstanceProcessor builds a command processor bound to a specific channel
// instance: it writes subscriptions with that channel_id and looks members up by
// the instance-scoped (channel_id, channel_user_id) key (S0005 p2-1).
func NewInstanceProcessor(st *store.Store, elig Attester, poolID, channelID string) *Processor {
	return &Processor{
		poolID:     poolID,
		channelID:  channelID,
		activation: st.ActivationCodes(),
		subs:       st.Subscriptions(),
		elig:       elig,
		now:        time.Now,
	}
}

// lookup loads the member's session by the instance-scoped key when this
// processor serves a specific instance, else by the legacy (pool, type, user).
func (p *Processor) lookup(ctx context.Context, userID string) (*domain.SubscriptionSession, error) {
	if p.channelID != "" {
		return p.subs.GetByInstanceUser(ctx, p.channelID, userID)
	}
	return p.subs.GetByChannelUser(ctx, p.poolID, "telegram", userID)
}

// Handle processes one update and returns the reply text.
func (p *Processor) Handle(ctx context.Context, up Update) string {
	cmd, arg := parseCommand(up.Text)
	switch cmd {
	case "/start", "/activate":
		return p.activate(ctx, up, arg)
	case "/status":
		return p.status(ctx, up)
	case "/unsubscribe":
		return p.unsubscribe(ctx, up)
	case "/help", "":
		return helpText
	default:
		return helpText
	}
}

const helpText = "Commands:\n/start <code> — activate your membership\n/status — show your subscription\n/unsubscribe — stop receiving messages\n/help — this message"

func (p *Processor) activate(ctx context.Context, up Update, code string) string {
	if code == "" {
		return "Please provide an activation code: /start <code>"
	}
	rec, err := p.activation.Consume(ctx, crypto.HashToken(code), "telegram", p.channelID, p.now())
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrConsumed):
			return "This activation code has already been used."
		case errors.Is(err, domain.ErrExpired):
			return "This activation code has expired. Please generate a new one."
		default:
			return "Invalid activation code."
		}
	}
	// Re-derive membership + first-party tier to seed the subscription (D8).
	state, tier, err := p.elig.Attest(ctx, rec.StakeCredentialHash)
	if err != nil {
		return "Could not verify membership right now. Please try again later."
	}
	if state == membership.StateNone {
		return "Your stake no longer qualifies for membership."
	}
	now := p.now()
	// Bind to the activation code's instance when present (S0005 p2-2), else this
	// bot's own instance, else legacy "" (single-instance).
	channelID := p.channelID
	if rec.ChannelID != "" {
		channelID = rec.ChannelID
	}
	sess := domain.SubscriptionSession{
		SessionID: crypto.RandomID(), PoolID: p.poolID, StakeCredentialHash: rec.StakeCredentialHash,
		ChannelID: channelID, ChannelType: "telegram", ChannelUserID: up.UserID, Status: domain.SubActive, Tier: tier,
		Topics: nil, Entitlements: nil,
		CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(sessionTTL),
	}
	if err := p.subs.Upsert(ctx, sess); err != nil {
		return "Could not save your subscription. Please try again later."
	}
	return fmt.Sprintf("Subscribed! Tier: %s. You'll receive updates here. (zero on-chain lookup at delivery)", tier)
}

func (p *Processor) status(ctx context.Context, up Update) string {
	sess, err := p.lookup(ctx, up.UserID)
	if err != nil {
		return "You are not subscribed. Use /start <code> to activate."
	}
	return fmt.Sprintf("Tier: %s\nStatus: %s\nExpires: %s", sess.Tier, sess.Status, sess.ExpiresAt.Format("2006-01-02"))
}

func (p *Processor) unsubscribe(ctx context.Context, up Update) string {
	sess, err := p.lookup(ctx, up.UserID)
	if err != nil {
		return "You are not subscribed."
	}
	if err := p.subs.SetStatus(ctx, sess.SessionID, domain.SubCancelled); err != nil {
		return "Could not unsubscribe. Please try again later."
	}
	return "You have been unsubscribed."
}

// parseCommand splits "/cmd arg..." into (command, argument).
func parseCommand(text string) (cmd, arg string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	cmd = strings.ToLower(parts[0])
	// Strip a @botname suffix Telegram appends in groups.
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	if len(parts) == 2 {
		arg = strings.TrimSpace(parts[1])
	}
	return cmd, arg
}

// Worker drives a Processor over a Transport via long polling.
type Worker struct {
	proc      *Processor
	transport Transport
	interval  time.Duration
}

// NewWorker builds a long-poll worker.
func NewWorker(proc *Processor, transport Transport) *Worker {
	return &Worker{proc: proc, transport: transport, interval: time.Second}
}

// Run polls for updates until ctx is cancelled, replying to each.
func (w *Worker) Run(ctx context.Context) {
	offset := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := w.transport.GetUpdates(ctx, offset)
		if err != nil {
			slog.Warn("telegram getUpdates failed", "err", err)
			if !sleep(ctx, w.interval) {
				return
			}
			continue
		}
		for _, up := range updates {
			if up.UpdateID >= offset {
				offset = up.UpdateID + 1
			}
			// Skip updates with no authenticated sender (non-message updates carry
			// from.id=0) so we never reply to chat "0" (p12-10).
			if up.UserID == "" || up.UserID == "0" {
				continue
			}
			reply := w.proc.Handle(ctx, up)
			if err := w.transport.SendMessage(ctx, up.ChatID, reply); err != nil {
				slog.Warn("telegram sendMessage failed", "err", err)
			}
		}
		if len(updates) == 0 && !sleep(ctx, w.interval) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
