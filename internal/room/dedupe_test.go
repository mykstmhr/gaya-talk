package room

import (
	"testing"
	"time"
)

func TestDeduperSeen(t *testing.T) {
	d := NewDeduper(5 * time.Minute)
	if d.Seen("a") {
		t.Error("初見の ID が既知扱いされた")
	}
	if !d.Seen("a") {
		t.Error("2 回目の ID が未知扱いされた(二重表示になる)")
	}
	if d.Seen("b") {
		t.Error("別 ID が既知扱いされた")
	}
}

func TestDeduperEmptyIDNeverDedupes(t *testing.T) {
	// ID を付けない旧クライアントのコメントを落とさない。
	d := NewDeduper(5 * time.Minute)
	if d.Seen("") || d.Seen("") {
		t.Error("空 ID は常に未知扱いのはず")
	}
}

func TestDeduperExpiresByTTL(t *testing.T) {
	d := NewDeduper(5 * time.Minute)
	t0 := time.Now()
	if d.seenAt("a", t0) {
		t.Fatal("初見の ID が既知扱いされた")
	}
	// TTL 内は重複と判定する。
	if !d.seenAt("a", t0.Add(4*time.Minute)) {
		t.Error("TTL 内の再配信が未知扱いされた")
	}
	// TTL を過ぎたエントリは掃除され、同じ ID でも再表示される(仕様)。
	// 掃除は Seen のたびに走るので、別 ID の呼び出しで発火させる。
	if d.seenAt("b", t0.Add(6*time.Minute)) {
		t.Fatal("別 ID が既知扱いされた")
	}
	if d.seenAt("a", t0.Add(6*time.Minute)) {
		t.Error("TTL 経過後のエントリが残っている")
	}
}
