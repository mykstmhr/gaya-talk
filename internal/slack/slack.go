// Package slack は bot token(xoxb)で chat.postMessage を叩く最小クライアント。
// room の Slack ミラー(コメントをチャンネルへ転送)で使う。アプリ名義で投稿するので
// 匿名ルームでも投稿者名は出ない。スレッド(thread_ts)にぶら下げてチャンネルを汚さない。
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client は chat.postMessage 用クライアント(bot token)。
type Client struct {
	token string
	http  *http.Client
}

// New はクライアントを生成する。token は bot token(xoxb)。
func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 10 * time.Second}}
}

// PostMessage は channel に text を投稿する。threadTS が非空ならそのスレッドに返信する。
// 投稿したメッセージの ts を返す(親メッセージの ts を以降の threadTS に使う)。
func (c *Client) PostMessage(ctx context.Context, channel, text, threadTS string) (string, error) {
	body := map[string]any{"channel": channel, "text": text}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/chat.postMessage", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		TS    string `json:"ts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("レスポンス解析失敗: %w", err)
	}
	if !out.OK {
		return "", fmt.Errorf("slack: %s", out.Error)
	}
	return out.TS, nil
}
