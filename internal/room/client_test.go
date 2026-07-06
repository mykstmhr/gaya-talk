package room

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateParsesTokenAndAdminSecret(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rooms" {
			t.Errorf("予期しないリクエスト: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"abcdefghijKLMNOPQRST12","adminSecret":"secretsecretsecretsec1"}`))
	}))
	defer ts.Close()

	r, err := Create(context.Background(), ts.URL+"/", false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Token != "abcdefghijKLMNOPQRST12" {
		t.Errorf("Token = %q", r.Token)
	}
	if r.AdminSecret != "secretsecretsecretsec1" {
		t.Errorf("AdminSecret = %q", r.AdminSecret)
	}
	if r.URL() == "" || len(r.Key) != 32 {
		t.Error("鍵が生成されていない")
	}
	// 管理シークレットが共有 URL に漏れないこと。
	if got := r.URL(); strings.Contains(got, r.AdminSecret) {
		t.Errorf("共有 URL に管理シークレットが漏れている: %s", got)
	}
}

func TestCreateWithoutAdminSecret(t *testing.T) {
	// 旧サーバ(adminSecret を返さない)でも作成は成功する。
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"abcdefghijKLMNOPQRST12"}`))
	}))
	defer ts.Close()

	r, err := Create(context.Background(), ts.URL, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.AdminSecret != "" {
		t.Errorf("AdminSecret = %q, want empty", r.AdminSecret)
	}
}

func TestRevoke(t *testing.T) {
	var gotAuth atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/r/abcdefghijKLMNOPQRST12" {
			t.Errorf("予期しないリクエスト: %s %s", r.Method, r.URL.Path)
		}
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	r := &Room{Server: ts.URL, Token: "abcdefghijKLMNOPQRST12", AdminSecret: "sec"}
	if err := Revoke(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := gotAuth.Load(); got != "Bearer sec" {
		t.Errorf("Authorization = %v", got)
	}
}

func TestRevokeErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"シークレット不一致", http.StatusForbidden},
		{"ルームなし", http.StatusNotFound},
		{"サーバエラー", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer ts.Close()
			r := &Room{Server: ts.URL, Token: "abcdefghijKLMNOPQRST12", AdminSecret: "sec"}
			if err := Revoke(context.Background(), r); err == nil {
				t.Error("エラーにならない")
			}
		})
	}
}

func TestRevokeRequiresAdminSecret(t *testing.T) {
	r := &Room{Server: "https://relay.example", Token: "abcdefghijKLMNOPQRST12"}
	if err := Revoke(context.Background(), r); err == nil {
		t.Error("シークレットなしでエラーにならない")
	}
}

// TestClientGivesUpOnGone は 410(無効化・失効)を受けたら再接続を諦めて
// 退出することを確認する。
func TestClientGivesUpOnGone(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "Room revoked or expired", http.StatusGone)
	}))
	defer ts.Close()

	fatal := make(chan string, 1)
	c := &Client{OnFatal: func(reason string) { fatal <- reason }}
	key, _ := GenerateKey()
	c.Join(&Room{Server: ts.URL, Token: "abcdefghijKLMNOPQRST12", Key: key})
	defer c.Leave()

	select {
	case reason := <-fatal:
		if reason == "" {
			t.Error("理由が空")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnFatal が呼ばれない")
	}
	// 退出済み(再接続ループが止まっている)こと。
	// 注: 「その後リクエストが増えない」の sleep 検証はしない。バックオフの最小値
	// (1 秒 ×0.75)より短い待ちでは絶対に失敗せず実効性がないため、Room() == nil
	// (leaveIf 済み = run が return 済み)の確認で代える。
	waitUntil(t, func() bool { return c.Room() == nil })
}

// TestClientRetriesOnServerError は一時的なエラー(5xx)では諦めずに
// 再接続し続けることを確認する。
func TestClientRetriesOnServerError(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := &Client{OnFatal: func(reason string) { t.Errorf("5xx で OnFatal が呼ばれた: %s", reason) }}
	key, _ := GenerateKey()
	c.Join(&Room{Server: ts.URL, Token: "abcdefghijKLMNOPQRST12", Key: key})
	defer c.Leave()

	// 初回 + バックオフ(1 秒 ±25%)後の再試行で 2 回以上になる。
	waitUntil(t, func() bool { return hits.Load() >= 2 })
	if c.Room() == nil {
		t.Error("5xx で退出してしまった")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("waitUntil timeout")
}
