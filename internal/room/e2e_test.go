package room

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestE2ERelay は実サーバ(wrangler dev かデプロイ済み Workers)に対して
// 作成→2 クライアント参加→送信→両方(送信者含む)に届く、を通しで確認する。
// 環境変数 URATALK_E2E_SERVER が無ければスキップする:
//
//	cd server && npx wrangler dev --port 8787   # 別端末で
//	URATALK_E2E_SERVER=http://localhost:8787 go test ./internal/room -run E2E -v
func TestE2ERelay(t *testing.T) {
	server := os.Getenv("URATALK_E2E_SERVER")
	if server == "" {
		t.Skip("URATALK_E2E_SERVER が未設定のためスキップ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := Create(ctx, server, false, "", "")
	if err != nil {
		t.Fatalf("ルーム作成失敗: %v", err)
	}
	t.Logf("room URL: %s", r.URL())

	newClient := func(name string) (*Client, chan Payload) {
		got := make(chan Payload, 4)
		c := &Client{OnMessage: func(p Payload) { got <- p }}
		c.Join(r)
		t.Cleanup(c.Leave)
		return c, got
	}
	c1, got1 := newClient("c1")
	_, got2 := newClient("c2")

	// 両クライアントの接続完了を待つ。
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !c1.Connected() {
		time.Sleep(100 * time.Millisecond)
	}
	if !c1.Connected() {
		t.Fatal("c1 が接続できない")
	}

	want := Payload{Text: "それな", Color: "#ffcc00"}
	// c2 の接続がまだの可能性があるので、届くまで数回送る。
	var p1, p2 Payload
	ok1, ok2 := false, false
	for i := 0; i < 20 && !(ok1 && ok2); i++ {
		_ = c1.Send(want)
		select {
		case p1 = <-got1:
			ok1 = true
		case <-time.After(300 * time.Millisecond):
		}
		select {
		case p2 = <-got2:
			ok2 = true
		case <-time.After(300 * time.Millisecond):
		}
	}
	if !ok1 {
		t.Error("送信者自身にエコーが届かない")
	}
	if !ok2 {
		t.Error("もう一方の参加者に届かない")
	}
	if ok1 && p1 != want {
		t.Errorf("c1 受信内容が不一致: %+v", p1)
	}
	if ok2 && p2 != want {
		t.Errorf("c2 受信内容が不一致: %+v", p2)
	}
}

// TestE2ERevoke は実サーバに対して 作成→参加→無効化→切断+再参加不可 を通しで確認する。
// 実行方法は TestE2ERelay と同じ(URATALK_E2E_SERVER が無ければスキップ)。
func TestE2ERevoke(t *testing.T) {
	server := os.Getenv("URATALK_E2E_SERVER")
	if server == "" {
		t.Skip("URATALK_E2E_SERVER が未設定のためスキップ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := Create(ctx, server, false, "", "")
	if err != nil {
		t.Fatalf("ルーム作成失敗: %v", err)
	}
	if r.AdminSecret == "" {
		t.Fatal("サーバが adminSecret を返さない")
	}

	c := &Client{}
	c.Join(r)
	t.Cleanup(c.Leave)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !c.Connected() {
		time.Sleep(100 * time.Millisecond)
	}
	if !c.Connected() {
		t.Fatal("接続できない")
	}

	if err := Revoke(ctx, r); err != nil {
		t.Fatalf("無効化失敗: %v", err)
	}

	// 無効化済みルームへの参加は 410 で拒否され、OnFatal 経由で退出する。
	fatal := make(chan string, 1)
	c2 := &Client{OnFatal: func(reason string) { fatal <- reason }}
	c2.Join(&Room{Server: r.Server, Token: r.Token, Key: r.Key})
	t.Cleanup(c2.Leave)
	select {
	case reason := <-fatal:
		t.Logf("無効化後の参加拒否: %s", reason)
	case <-time.After(8 * time.Second):
		t.Error("無効化済みルームに参加できてしまう(OnFatal が呼ばれない)")
	}

	// 間違ったシークレットでは無効化できないことも実サーバで確認しておく。
	r2, err := Create(ctx, server, false, "", "")
	if err != nil {
		t.Fatalf("ルーム作成失敗: %v", err)
	}
	bad := *r2
	bad.AdminSecret = "AAAAAAAAAAAAAAAAAAAAAA"
	if err := Revoke(ctx, &bad); err == nil {
		t.Error("間違ったシークレットで無効化できてしまった")
	}
}
