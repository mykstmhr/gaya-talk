//go:build darwin

package tokenstore

import "testing"

// TestRoundTrip は Save→Load→Delete が実 Keychain 上で正しく往復するかを確認する。
// 本番と同じ属性(service/account)を使うため、既存トークンがあれば退避し、
// テスト後に必ず元の状態へ戻す(削除済みだったら削除済みに戻す)。
func TestRoundTrip(t *testing.T) {
	// 既存状態を退避。
	orig, err := Load()
	if err != nil {
		t.Fatalf("事前 Load 失敗: %v", err)
	}
	// テスト終了時に元の状態へ復元する。
	t.Cleanup(func() {
		if orig == "" {
			_ = Delete()
			return
		}
		if err := Save(orig); err != nil {
			t.Errorf("元トークンの復元に失敗: %v", err)
		}
	})

	const want = "xoxp-test-1234567890-roundtrip"
	if err := Save(want); err != nil {
		t.Fatalf("Save 失敗: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load 失敗: %v", err)
	}
	if got != want {
		t.Fatalf("Load = %q, want %q", got, want)
	}

	// 上書き(更新)も効くか。
	const want2 = "xoxp-test-updated"
	if err := Save(want2); err != nil {
		t.Fatalf("Save(更新) 失敗: %v", err)
	}
	got2, err := Load()
	if err != nil {
		t.Fatalf("Load(更新後) 失敗: %v", err)
	}
	if got2 != want2 {
		t.Fatalf("更新後 Load = %q, want %q", got2, want2)
	}

	// 削除後は空になる。
	if err := Delete(); err != nil {
		t.Fatalf("Delete 失敗: %v", err)
	}
	got3, err := Load()
	if err != nil {
		t.Fatalf("Load(削除後) 失敗: %v", err)
	}
	if got3 != "" {
		t.Fatalf("削除後 Load = %q, want empty", got3)
	}
}
