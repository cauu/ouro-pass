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
// Real network use is exercised under the integration build tag (D5); the type
// compiles and is wired by main when a bot token is configured.
type BotAPITransport struct {
	token   string
	timeout int // long-poll seconds
	client  *http.Client
}

// NewBotAPITransport builds a transport for the given bot token.
func NewBotAPITransport(token string) *BotAPITransport {
	return &BotAPITransport{token: token, timeout: 30, client: &http.Client{Timeout: 45 * time.Second}}
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

// GetUpdates long-polls the Telegram getUpdates endpoint.
func (b *BotAPITransport) GetUpdates(ctx context.Context, offset int) ([]Update, error) {
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
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.token, method)
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
		return fmt.Errorf("telegram %s: status %d", method, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
