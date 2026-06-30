// Package slack は user token を使って chat.postMessage で投稿する。
// user token(xoxp)で投稿するため、メッセージは bot ではなく本人名義で表示される。
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client は chat.postMessage 用クライアント。
type Client struct {
	token   string
	channel string
	http    *http.Client
}

// New はクライアントを生成する。token は user token(xoxp)、channel は投稿先。
func New(token, channel string) *Client {
	return &Client{
		token:   token,
		channel: channel,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Post はテキストを Slack に投稿する。本人名義で表示される。
func (c *Client) Post(ctx context.Context, text string) error {
	payload, err := json.Marshal(map[string]any{
		"channel": c.channel,
		"text":    text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/chat.postMessage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("レスポンス解析失敗: %w", err)
	}
	if !out.OK {
		return fmt.Errorf("chat.postMessage エラー: %s", out.Error)
	}
	return nil
}
