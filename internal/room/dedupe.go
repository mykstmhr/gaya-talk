package room

import (
	"sync"
	"time"
)

// Deduper は表示済みコメント ID を TTL 付きで覚え、二重表示を防ぐ。
// 「送信エラー時のローカル表示 + あとから届くサーバエコー」の二重や、
// 万一の再配信を防ぐ用途。メソッドは複数 goroutine から安全に呼べる。
type Deduper struct {
	mu   sync.Mutex
	ttl  time.Duration
	seen map[string]time.Time
}

// NewDeduper は ttl の間 ID を覚える Deduper を返す。
func NewDeduper(ttl time.Duration) *Deduper {
	return &Deduper{ttl: ttl, seen: map[string]time.Time{}}
}

// Seen は id が既知(表示済み)かを返し、未知なら覚える。空 ID は常に未知扱い
// (ID を付けない旧クライアントのコメントを落とさない)。
func (d *Deduper) Seen(id string) bool {
	return d.seenAt(id, time.Now())
}

// seenAt は Seen の実体(テストから時刻を注入できるよう分離)。
func (d *Deduper) seenAt(id string, now time.Time) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, dup := d.seen[id]; dup {
		return true
	}
	d.seen[id] = now
	for k, at := range d.seen { // 小規模なので毎回全走査で十分
		if now.Sub(at) > d.ttl {
			delete(d.seen, k)
		}
	}
	return false
}
