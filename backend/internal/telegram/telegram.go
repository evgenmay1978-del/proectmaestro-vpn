// Package telegram sends owner notifications via the bot API. Only SENDS — it
// never polls getUpdates, so it does not conflict with the existing vpn_bot that
// owns the same token's polling. The one-tap confirm button's callback is handled
// by that vpn_bot (callback_data "moconf:<order_id>").
package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client sends messages as the bot.
type Client struct {
	token string
	http  *http.Client
}

// New returns a client; an empty token makes every call a no-op.
func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 15 * time.Second}}
}

// NotifyOrder alerts the owner about a claimed payment, with a one-tap confirm
// button. No-op if the token or chatID is empty.
func (c *Client) NotifyOrder(chatID, text, orderID string) error {
	if c.token == "" || chatID == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
		"reply_markup": map[string]any{
			"inline_keyboard": [][]map[string]any{{
				{"text": "✅ Подтвердить оплату", "callback_data": "moconf:" + orderID},
				{"text": "❌ Отменить", "callback_data": "mocancel:" + orderID},
			}},
		},
	})
	resp, err := c.http.Post("https://api.telegram.org/bot"+c.token+"/sendMessage",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: send HTTP %d", resp.StatusCode)
	}
	return nil
}
