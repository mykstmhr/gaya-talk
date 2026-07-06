package enhance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnhance_DisabledReturnsRaw(t *testing.T) {
	e := New(Config{Enabled: false, Model: "x"})
	got, err := e.Enhance(context.Background(), "なるほど")
	if got != "なるほど" || err != nil {
		t.Errorf("無効時は素通し・err無し: got %q err=%v", got, err)
	}
}

func TestEnhance_NoModelReturnsRaw(t *testing.T) {
	e := New(Config{Enabled: true, Model: ""})
	got, err := e.Enhance(context.Background(), "テスト")
	if got != "テスト" || err != nil {
		t.Errorf("モデル未指定は素通し: got %q err=%v", got, err)
	}
}

func TestEnhance_UnreachableFallsBack(t *testing.T) {
	// 到達不能なエンドポイント → 元テキストを返し、err を返す(壊さない)。
	e := New(Config{Enabled: true, Model: "qwen2.5:7b", Endpoint: "http://127.0.0.1:1"})
	raw := "これはフォールバックの確認。"
	got, err := e.Enhance(context.Background(), raw)
	if got != raw {
		t.Errorf("到達不能時は元テキストを返すべき: got %q", got)
	}
	if err == nil {
		t.Error("到達不能時は err を返すべき")
	}
}

func TestTooLong(t *testing.T) {
	// 会話化の安全網: 入力の2倍+20文字超は「長すぎ」と判定する。
	raw := "うまく使えるかな。"
	answer := "はい、問題なく使えます。具体的な内容を教えていただけますか？さらに詳しく説明します。"
	if !tooLong(raw, answer) {
		t.Errorf("会話化した長い出力は破棄対象のはず")
	}
	cleaned := "うまく使えるかな?"
	if tooLong(raw, cleaned) {
		t.Errorf("通常の整形は破棄しないはず: %q", cleaned)
	}
}

func TestEnhance_NilSafe(t *testing.T) {
	var e *Enhancer
	got, err := e.Enhance(context.Background(), "x")
	if got != "x" || err != nil {
		t.Errorf("nil でも素通し: got %q err=%v", got, err)
	}
}

func TestIsLocalEndpoint(t *testing.T) {
	cases := []struct {
		endpoint string
		want     bool
	}{
		{"http://localhost:11434", true},
		{"http://127.0.0.1:11434", true},
		{"http://[::1]:11434", true},
		{"http://localhost", true},
		{"https://LOCALHOST:11434", true}, // ホスト名は大文字小文字を無視
		{"http://192.168.1.5:11434", false},
		{"http://10.0.0.1:11434", false},
		{"https://example.com", false},
		{"https://ollama.example.com:11434", false},
		{"http://0.0.0.0:11434", false}, // 全アドレス bind はローカル限定ではない
		{"://bad-url", false},           // パース不能は非ローカル扱い(安全側)
		{"", false},
	}
	for _, c := range cases {
		if got := isLocalEndpoint(c.endpoint); got != c.want {
			t.Errorf("isLocalEndpoint(%q) = %v, want %v", c.endpoint, got, c.want)
		}
	}
}

func TestEnhance_RemoteEndpointBlockedByCheck(t *testing.T) {
	// AllowRemote 未設定で非ローカル endpoint → Check はエラーを返す(発話は送られない)。
	e := New(Config{Enabled: true, Model: "qwen2.5:7b", Endpoint: "https://example.com"})
	if err := e.Check(context.Background()); err == nil {
		t.Error("非ローカル endpoint は AllowRemote 無効時に Check で拒否されるべき")
	}
}

func TestEnhance_RemoteEndpointAllowedWithOptIn(t *testing.T) {
	// AllowRemote=true なら非ローカル endpoint でもガードは通過する
	// (到達不能なので接続エラーにはなるが、ローカル限定ガードでの拒否ではない)。
	e := New(Config{Enabled: true, Model: "qwen2.5:7b", Endpoint: "http://192.0.2.1:1", AllowRemote: true})
	err := e.ensureLocalOrAllowed()
	if err != nil {
		t.Errorf("AllowRemote=true なら非ローカル endpoint を許可すべき: %v", err)
	}
}

func TestEnsureLocalOrAllowed_LocalPasses(t *testing.T) {
	e := New(Config{Enabled: true, Model: "x", Endpoint: "http://localhost:11434"})
	if err := e.ensureLocalOrAllowed(); err != nil {
		t.Errorf("ローカル endpoint は常に許可すべき: %v", err)
	}
}

// fakeOllama は /api/chat に固定の content を返す Ollama の代役を立てる。
// httptest は 127.0.0.1 で立つのでローカル限定ガードも通る。
func fakeOllama(t *testing.T, content string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("予期しないパス: %s", r.URL.Path)
		}
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			t.Error("stream=false で呼ぶべき")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"content": content},
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestEnhance_Success(t *testing.T) {
	ts := fakeOllama(t, "これ、うまく動くのかな?")
	e := New(Config{Enabled: true, Model: "qwen2.5:3b", Endpoint: ts.URL})
	got, err := e.Enhance(context.Background(), "あー、えーっと これうまく動くのかな")
	if err != nil {
		t.Fatal(err)
	}
	if got != "これ、うまく動くのかな?" {
		t.Errorf("got %q", got)
	}
}

func TestEnhance_TrimsWhitespace(t *testing.T) {
	ts := fakeOllama(t, "  整形結果です。\n")
	e := New(Config{Enabled: true, Model: "m", Endpoint: ts.URL})
	got, err := e.Enhance(context.Background(), "整形結果です")
	if err != nil || got != "整形結果です。" {
		t.Errorf("got %q err=%v", got, err)
	}
}

func TestEnhance_DiscardsTooLongOutput(t *testing.T) {
	// 会話化(質問に答えてしまった)出力は破棄して生テキストを使う統合動作。
	ts := fakeOllama(t, strings.Repeat("はい、使えます。", 20))
	e := New(Config{Enabled: true, Model: "m", Endpoint: ts.URL})
	raw := "うまく使えるかな。"
	got, err := e.Enhance(context.Background(), raw)
	if got != raw {
		t.Errorf("会話化出力は破棄して raw を返すべき: got %q", got)
	}
	if err == nil {
		t.Error("破棄した理由を err で返すべき")
	}
}

func TestEnhance_EmptyOutputFallsBack(t *testing.T) {
	ts := fakeOllama(t, "   ")
	e := New(Config{Enabled: true, Model: "m", Endpoint: ts.URL})
	raw := "そのままのテキスト"
	got, err := e.Enhance(context.Background(), raw)
	if got != raw || err == nil {
		t.Errorf("空の整形結果は raw + err を返すべき: got %q err=%v", got, err)
	}
}
