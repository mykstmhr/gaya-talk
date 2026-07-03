package voicegate

import "testing"

func TestNewInitialState(t *testing.T) {
	if !New(true).Allowed() {
		t.Error("New(true) は許可のはず")
	}
	if New(false).Allowed() {
		t.Error("New(false) は不許可のはず")
	}
	if !NewAlwaysOn().Allowed() {
		t.Error("NewAlwaysOn は許可のはず")
	}
}

func TestSetReportsChange(t *testing.T) {
	g := New(false)
	if !g.Set(true) {
		t.Error("false→true は changed=true のはず")
	}
	if g.Set(true) {
		t.Error("true→true は changed=false のはず")
	}
	if !g.Set(false) {
		t.Error("true→false は changed=true のはず")
	}
	if g.Allowed() {
		t.Error("最後は不許可のはず")
	}
}

func TestRevokeOnDisallow(t *testing.T) {
	g := New(true)
	// 許可→不許可で revoke が飛ぶ。
	g.Set(false)
	select {
	case <-g.Revoked():
	default:
		t.Error("許可→不許可で revoke が通知されるはず")
	}
	// 不許可→許可では revoke は飛ばない。
	g.Set(true)
	select {
	case <-g.Revoked():
		t.Error("不許可→許可では revoke は飛ばないはず")
	default:
	}
}

func TestRevokeCoalesces(t *testing.T) {
	// revoke がバッファ 1 で、連続遷移でもブロックしない(取りこぼしは許容)。
	g := New(true)
	g.Set(false)
	g.Set(true)
	g.Set(false) // 2 回目の revoke。誰も読んでいなくてもブロックしない
	g.DrainRevoked()
	select {
	case <-g.Revoked():
		t.Error("DrainRevoked 後は空のはず")
	default:
	}
}

func TestDrainRevokedWhenEmpty(t *testing.T) {
	g := New(true)
	g.DrainRevoked() // 空でも安全
}
