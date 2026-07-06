package room

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// TestE2ENoDuplicates は 2 クライアントで数通送り、各メッセージが各クライアントに
// ちょうど 1 回ずつ届くこと(重複・欠落なし)を確認する診断テスト。
// 再接続を挟んだ場合の挙動も見るため、途中で片方を Leave→Join し直す。
func TestE2ENoDuplicates(t *testing.T) {
	server := os.Getenv("GAYATALK_E2E_SERVER")
	if server == "" {
		t.Skip("GAYATALK_E2E_SERVER が未設定のためスキップ")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := Create(ctx, server, false, "", "")
	if err != nil {
		t.Fatalf("ルーム作成失敗: %v", err)
	}

	type counter struct{ counts [64]atomic.Int32 }
	newClient := func() (*Client, *counter) {
		c := &counter{}
		cl := &Client{OnMessage: func(p Payload) {
			var i int
			fmt.Sscanf(p.Text, "msg-%d", &i)
			if i >= 0 && i < 64 {
				c.counts[i].Add(1)
			}
		}}
		cl.Join(r)
		t.Cleanup(cl.Leave)
		return cl, c
	}
	a, ca := newClient()
	b, cb := newClient()

	waitConn := func(c *Client) {
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) && !c.Connected() {
			time.Sleep(50 * time.Millisecond)
		}
		if !c.Connected() {
			t.Fatal("接続できない")
		}
	}
	waitConn(a)
	waitConn(b)

	send := func(i int) {
		if err := a.Send(Payload{Text: fmt.Sprintf("msg-%d", i), Color: "#fff"}); err != nil {
			t.Fatalf("送信失敗 msg-%d: %v", i, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		send(i)
	}
	// 再接続(参加し直し)を挟む: 古い接続の残骸が二重配信を生まないか。
	b.Leave()
	b.Join(r)
	waitConn(b)
	for i := 5; i < 10; i++ {
		send(i)
	}
	time.Sleep(1 * time.Second) // 遅延分を回収

	for i := 0; i < 10; i++ {
		if got := ca.counts[i].Load(); got != 1 {
			t.Errorf("送信者A: msg-%d が %d 回届いた(期待 1)", i, got)
		}
		want := int32(1)
		if got := cb.counts[i].Load(); got != want {
			t.Errorf("受信者B: msg-%d が %d 回届いた(期待 %d)", i, got, want)
		}
	}
}
