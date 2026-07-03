// Package voicegate は「今、音声入力を受け付けてよいか」の状態を 1 か所で持つ。
//
// voice.input が "auto" のときは出力デバイス(スピーカー/イヤホン)に応じてオン/オフが
// 動的に変わり、しかも「リッスン中にスピーカーへ切り替わったら止める」必要がある。
// この判定・通知が録音ループやメニュー表示に散らばらないよう、状態と遷移をここに集約する。
package voicegate

import "sync/atomic"

// Gate は音声入力の可否を保持する。ゼロ値は不許可。New / NewAlwaysOn で作る。
type Gate struct {
	allowed atomic.Bool
	revoke  chan struct{} // 許可→不許可の遷移をリッスン中のループへ伝える
}

// New は初期状態 allowed の可変ゲートを返す(auto モード用)。
func New(allowed bool) *Gate {
	g := &Gate{revoke: make(chan struct{}, 1)}
	g.allowed.Store(allowed)
	return g
}

// NewAlwaysOn は常に許可のゲートを返す(on モード用)。Set しても変化しない。
func NewAlwaysOn() *Gate {
	g := &Gate{revoke: make(chan struct{}, 1)}
	g.allowed.Store(true)
	return g
}

// Allowed は現在音声入力を受け付けてよいかを返す。
func (g *Gate) Allowed() bool { return g.allowed.Load() }

// Revoked は「許可→不許可」に変わったときに 1 度だけ通知されるチャネルを返す。
// リッスン中のループはこれを受けて録音を止める。
func (g *Gate) Revoked() <-chan struct{} { return g.revoke }

// DrainRevoked は溜まっている revoke 通知を捨てる(リッスン開始直前に呼ぶ)。
func (g *Gate) DrainRevoked() {
	select {
	case <-g.revoke:
	default:
	}
}

// Set は可否を allowed に更新し、変化したら true を返す。許可→不許可のときは
// Revoked() に通知する(バッファ 1・非ブロッキング)。
func (g *Gate) Set(allowed bool) (changed bool) {
	prev := g.allowed.Swap(allowed)
	if prev == allowed {
		return false
	}
	if !allowed {
		select {
		case g.revoke <- struct{}{}:
		default:
		}
	}
	return true
}
