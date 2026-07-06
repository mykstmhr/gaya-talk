package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient は httptest サーバへ向けたクライアントを返す。
func newTestClient(ts *httptest.Server) *Client {
	c := New("xoxb-test")
	c.baseURL = ts.URL
	return c
}

func TestPostMessage(t *testing.T) {
	var got struct {
		Channel  string `json:"channel"`
		Text     string `json:"text"`
		ThreadTS string `json:"thread_ts"`
	}
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat.postMessage" {
			t.Errorf("予期しないリクエスト: %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("リクエストボディの解析失敗: %v", err)
		}
		w.Write([]byte(`{"ok":true,"ts":"1234.5678"}`))
	}))
	defer ts.Close()

	tsRet, err := newTestClient(ts).PostMessage(context.Background(), "C012345", "こんにちは", "")
	if err != nil {
		t.Fatal(err)
	}
	if tsRet != "1234.5678" {
		t.Errorf("ts = %q", tsRet)
	}
	if gotAuth != "Bearer xoxb-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if got.Channel != "C012345" || got.Text != "こんにちは" {
		t.Errorf("channel=%q text=%q", got.Channel, got.Text)
	}
	if got.ThreadTS != "" {
		t.Errorf("threadTS 未指定なのに thread_ts=%q が付いた", got.ThreadTS)
	}
}

func TestPostMessageThread(t *testing.T) {
	var gotThread string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotThread, _ = body["thread_ts"].(string)
		w.Write([]byte(`{"ok":true,"ts":"1234.9999"}`))
	}))
	defer ts.Close()

	if _, err := newTestClient(ts).PostMessage(context.Background(), "C012345", "返信", "1234.5678"); err != nil {
		t.Fatal(err)
	}
	if gotThread != "1234.5678" {
		t.Errorf("thread_ts = %q", gotThread)
	}
}

func TestPostMessageEscapesControlSequences(t *testing.T) {
	// ルーム参加者由来の本文に <!channel> 等を仕込まれても通知を強制できないこと。
	var gotText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotText, _ = body["text"].(string)
		w.Write([]byte(`{"ok":true,"ts":"1"}`))
	}))
	defer ts.Close()

	if _, err := newTestClient(ts).PostMessage(context.Background(), "C1", "<!channel> A&B <@U123>", ""); err != nil {
		t.Fatal(err)
	}
	want := "&lt;!channel&gt; A&amp;B &lt;@U123&gt;"
	if gotText != want {
		t.Errorf("text = %q, want %q", gotText, want)
	}
}

func TestPostMessageAPIError(t *testing.T) {
	// HTTP 200 でも ok:false ならエラーコードを含むエラーになる。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	defer ts.Close()

	_, err := newTestClient(ts).PostMessage(context.Background(), "C1", "x", "")
	if err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Errorf("err = %v, want channel_not_found を含む", err)
	}
}

func TestPostMessageRateLimited(t *testing.T) {
	// 429 は非 JSON ボディで返るため、「レスポンス解析失敗」ではなく
	// レートリミットと分かるエラーになること。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("Too Many Requests"))
	}))
	defer ts.Close()

	_, err := newTestClient(ts).PostMessage(context.Background(), "C1", "x", "")
	if err == nil || !strings.Contains(err.Error(), "レートリミット") || !strings.Contains(err.Error(), "30") {
		t.Errorf("err = %v, want レートリミットと Retry-After を含む", err)
	}
}
