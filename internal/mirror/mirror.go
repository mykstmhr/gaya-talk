// Package mirror はルームのコメントを Slack チャンネルへスレッドで転送する状態を持つ。
//
// ミラー役 1 人のクライアントで動く。開始時に親メッセージを 1 本立て、以降のコメントを
// その thread_ts へぶら下げる。投稿は Poster 経由なのでテストで差し替えられる。
package mirror

import (
	"context"
	"sync"
)

// Poster は Slack への投稿口(*slack.Client が満たす)。threadTS が空なら新規、
// 非空ならそのスレッドへ返信し、投稿した message ts を返す。
type Poster interface {
	PostMessage(ctx context.Context, channel, text, threadTS string) (string, error)
}

// Mirror は 1 つのルームの Slack 記録状態(有効/無効・投稿先・スレッド)を保持する。
// ゼロ値で使える。メソッドは複数 goroutine から安全に呼べる。
type Mirror struct {
	mu      sync.Mutex
	active  bool
	poster  Poster
	channel string
	thread  string // 親メッセージの ts
}

// Start は channel への記録を開始する。header を親メッセージとして投稿し、その ts を
// スレッドの起点にする。既に有効なら一度止めてから開始し直す。
func (m *Mirror) Start(ctx context.Context, p Poster, channel, header string) error {
	ts, err := p.PostMessage(ctx, channel, header, "")
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.active = true
	m.poster = p
	m.channel = channel
	m.thread = ts
	m.mu.Unlock()
	return nil
}

// Stop は記録を止める。既に無効なら false を返す。
func (m *Mirror) Stop() (wasActive bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wasActive = m.active
	m.active = false
	m.poster = nil
	m.thread = ""
	return wasActive
}

// Active は記録中かを返す。
func (m *Mirror) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// Post は text をスレッドへ転送する。無効なら何もしない(nil)。
// 呼び出し側が goroutine で呼ぶ前提(ネットワーク I/O を含む)。
func (m *Mirror) Post(ctx context.Context, text string) error {
	m.mu.Lock()
	active, p, ch, th := m.active, m.poster, m.channel, m.thread
	m.mu.Unlock()
	if !active || p == nil {
		return nil
	}
	_, err := p.PostMessage(ctx, ch, text, th)
	return err
}
