//go:build darwin

package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetEntries はテストごとに履歴バッファを空へ戻す。
func resetEntries() {
	entriesMu.Lock()
	entries = nil
	entriesMu.Unlock()
}

func count() int {
	entriesMu.Lock()
	defer entriesMu.Unlock()
	return len(entries)
}

func TestHandoffRoundTrip(t *testing.T) {
	resetEntries()
	defer resetEntries()
	path := filepath.Join(t.TempDir(), "handoff.json")

	Append("たろう", "こんにちは", "#66ccff", time.Now().UnixMilli())
	Append("", "匿名コメント", "#ffcc00", 0) // SentAt=0 は受信時刻で補完される
	if err := SaveHandoff(path); err != nil {
		t.Fatalf("SaveHandoff: %v", err)
	}
	// 本文入りのファイルは所有者だけが読める 0600 で置く(CLAUDE.md の規約)。
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", st.Mode().Perm())
	}

	resetEntries()
	if n := LoadHandoff(path, time.Minute); n != 2 {
		t.Errorf("LoadHandoff = %d, want 2", n)
	}
	if count() != 2 {
		t.Errorf("復元後の件数 = %d, want 2", count())
	}
	entriesMu.Lock()
	got := append([]entry(nil), entries...)
	entriesMu.Unlock()
	if got[0].Name != "たろう" || got[0].Text != "こんにちは" || got[0].Color != "#66ccff" {
		t.Errorf("復元内容が一致しない: %+v", got[0])
	}
	if got[1].SentAt == 0 {
		t.Errorf("SentAt=0 が受信時刻で補完されていない")
	}
	// 読み込んだら本文をディスクに残さない(即削除)。
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ハンドオフファイルが削除されていない")
	}
}

func TestLoadHandoffIgnoresStaleFile(t *testing.T) {
	resetEntries()
	defer resetEntries()
	path := filepath.Join(t.TempDir(), "handoff.json")

	Append("たろう", "古い履歴", "#66ccff", time.Now().UnixMilli())
	if err := SaveHandoff(path); err != nil {
		t.Fatalf("SaveHandoff: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	resetEntries()
	if n := LoadHandoff(path, 10*time.Minute); n != 0 {
		t.Errorf("古いファイルを読んでしまった: n=%d", n)
	}
	// 読まなくても削除はする(残留した本文を掃除する)。
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("古いハンドオフファイルが削除されていない")
	}
}

func TestSaveHandoffEmptyRemovesLeftover(t *testing.T) {
	resetEntries()
	defer resetEntries()
	path := filepath.Join(t.TempDir(), "handoff.json")

	// 消費されずに残ったファイルがある状態で空の履歴を保存すると、残留分を消すだけ。
	if err := os.WriteFile(path, []byte(`[{"text":"残留"}]`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := SaveHandoff(path); err != nil {
		t.Fatalf("SaveHandoff: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("残留ファイルが削除されていない")
	}
	if n := LoadHandoff(path, time.Minute); n != 0 {
		t.Errorf("存在しないファイルから復元された: n=%d", n)
	}
}

func TestAppendTrimsToMaxEntries(t *testing.T) {
	resetEntries()
	defer resetEntries()
	for i := 0; i < maxEntries+10; i++ {
		Append("", "コメント", "#ffffff", time.Now().UnixMilli())
	}
	if count() != maxEntries {
		t.Errorf("件数 = %d, want %d", count(), maxEntries)
	}
}
