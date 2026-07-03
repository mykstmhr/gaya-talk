package mirror

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// fakePoster は投稿を記録するテスト用の Poster。
type fakePoster struct {
	mu    sync.Mutex
	calls []call
	ts    string // PostMessage が返す ts
	err   error  // 返すエラー
}

type call struct {
	channel, text, threadTS string
}

func (f *fakePoster) PostMessage(_ context.Context, channel, text, threadTS string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{channel, text, threadTS})
	return f.ts, f.err
}

func (f *fakePoster) snapshot() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func TestStartPostsParentAndActivates(t *testing.T) {
	p := &fakePoster{ts: "1700000000.000100"}
	var m Mirror
	if m.Active() {
		t.Fatal("初期は非アクティブのはず")
	}
	if err := m.Start(context.Background(), p, "C0123", "記録開始"); err != nil {
		t.Fatal(err)
	}
	if !m.Active() {
		t.Error("Start 後はアクティブのはず")
	}
	calls := p.snapshot()
	if len(calls) != 1 {
		t.Fatalf("親メッセージが 1 本のはず: %d", len(calls))
	}
	if calls[0].channel != "C0123" || calls[0].text != "記録開始" || calls[0].threadTS != "" {
		t.Errorf("親メッセージが不正: %+v", calls[0])
	}
}

func TestStartFailureDoesNotActivate(t *testing.T) {
	p := &fakePoster{err: errors.New("slack down")}
	var m Mirror
	if err := m.Start(context.Background(), p, "C0123", "hdr"); err == nil {
		t.Error("親メッセージ投稿失敗ならエラーのはず")
	}
	if m.Active() {
		t.Error("開始に失敗したらアクティブにしないこと")
	}
}

func TestPostGoesToThread(t *testing.T) {
	p := &fakePoster{ts: "1700000000.000100"}
	var m Mirror
	if err := m.Start(context.Background(), p, "C0123", "hdr"); err != nil {
		t.Fatal(err)
	}
	if err := m.Post(context.Background(), "それな"); err != nil {
		t.Fatal(err)
	}
	calls := p.snapshot()
	if len(calls) != 2 {
		t.Fatalf("親 + コメントで 2 件のはず: %d", len(calls))
	}
	if calls[1].text != "それな" || calls[1].threadTS != "1700000000.000100" {
		t.Errorf("コメントは親スレッドへ: %+v", calls[1])
	}
}

func TestPostWhenInactiveIsNoop(t *testing.T) {
	p := &fakePoster{}
	var m Mirror
	if err := m.Post(context.Background(), "x"); err != nil {
		t.Fatalf("非アクティブでの Post は no-op のはず: %v", err)
	}
	if len(p.snapshot()) != 0 {
		t.Error("非アクティブでは投稿しないこと")
	}
}

func TestStopEndsMirroring(t *testing.T) {
	p := &fakePoster{ts: "t1"}
	var m Mirror
	_ = m.Start(context.Background(), p, "C0123", "hdr")
	if !m.Stop() {
		t.Error("アクティブからの Stop は wasActive=true のはず")
	}
	if m.Active() {
		t.Error("Stop 後は非アクティブのはず")
	}
	if m.Stop() {
		t.Error("二重 Stop は wasActive=false のはず")
	}
	// 停止後の Post は投稿しない。
	_ = m.Post(context.Background(), "y")
	if len(p.snapshot()) != 1 { // 親メッセージのみ
		t.Error("停止後は転送しないこと")
	}
}
