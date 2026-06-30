package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BotAPITransport is the production Telegram Bot API transport (long polling).
// Real network use is exercised under the integration build tag (D5).
//
// The token is resolved per call via a function, so it is hot-reloadable: an
// admin configuring the bot token (encrypted ChannelConfig) takes effect on the
// next poll with no restart. An empty token means "not configured yet".
type BotAPITransport struct {
	token   func() string
	timeout int // long-poll seconds
	client  *http.Client
}

// NewBotAPITransport builds a transport whose token is resolved per call by tokenFn.
func NewBotAPITransport(tokenFn func() string) *BotAPITransport {
	return &BotAPITransport{token: tokenFn, timeout: 30, client: &http.Client{Timeout: 45 * time.Second}}
}

type tgUpdate struct {
	UpdateID int `json:"update_id"`
	Message  struct {
		Text string `json:"text"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

// GetUpdates long-polls the Telegram getUpdates endpoint. With no token
// configured it is a quiet no-op (the worker just sleeps and retries).
func (b *BotAPITransport) GetUpdates(ctx context.Context, offset int) ([]Update, error) {
	if b.token() == "" {
		return nil, nil
	}
	// Only message updates are handled; restricting allowed_updates avoids
	// receiving edited_message/channel_post/callback_query that carry no
	// message.from and would otherwise be mis-dispatched (p12-10).
	q := url.Values{
		"timeout":         {strconv.Itoa(b.timeout)},
		"offset":          {strconv.Itoa(offset)},
		"allowed_updates": {`["message"]`},
	}
	var resp struct {
		OK     bool       `json:"ok"`
		Result []tgUpdate `json:"result"`
	}
	if err := b.call(ctx, "getUpdates", q, &resp); err != nil {
		return nil, err
	}
	out := make([]Update, 0, len(resp.Result))
	for _, u := range resp.Result {
		out = append(out, Update{
			UpdateID: u.UpdateID,
			UserID:   strconv.FormatInt(u.Message.From.ID, 10),
			ChatID:   strconv.FormatInt(u.Message.Chat.ID, 10),
			Text:     u.Message.Text,
		})
	}
	return out, nil
}

// SendMessage posts a text reply via sendMessage.
func (b *BotAPITransport) SendMessage(ctx context.Context, chatID, text string) error {
	q := url.Values{"chat_id": {chatID}, "text": {text}}
	var resp struct {
		OK bool `json:"ok"`
	}
	return b.call(ctx, "sendMessage", q, &resp)
}

func (b *BotAPITransport) call(ctx context.Context, method string, q url.Values, out any) error {
	tok := b.token()
	if tok == "" {
		return ErrNotConfigured
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/%s", tok, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(q.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &APIStatusError{Method: method, Status: resp.StatusCode}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// APIStatusError is a non-200 response from the Telegram Bot API. It carries the status
// code so callers can react to specific codes (e.g. 409 Conflict) via errors.As instead of
// matching the message string (S0014 p3-1-fix1). Its Error() text is unchanged for logs.
type APIStatusError struct {
	Method string
	Status int
}

func (e *APIStatusError) Error() string {
	return fmt.Sprintf("telegram %s: status %d", e.Method, e.Status)
}
