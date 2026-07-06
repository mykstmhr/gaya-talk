package roomstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadClear(t *testing.T) {
	s := New(t.TempDir())
	if got := s.Load(); got != "" {
		t.Errorf("未保存なのに Load = %q", got)
	}
	url := "https://relay.example/r/abcdefghijKLMNOPQRST12#k=xxx"
	if err := s.Save(url); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); got != url {
		t.Errorf("Load = %q, want %q", got, url)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); got != "" {
		t.Errorf("Clear 後に Load = %q", got)
	}
	if err := s.Clear(); err != nil {
		t.Errorf("二重 Clear はエラーにしない: %v", err)
	}
}

func TestFilePermission(t *testing.T) {
	// URL には復号鍵が入るため所有者のみ(0600)で保存されること。
	dir := t.TempDir()
	s := New(dir)
	if err := s.Save("https://relay.example/r/x#k=secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "last_room_url"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("パーミッション = %o, want 600", perm)
	}
}

func TestNilAndEmptyDirSafe(t *testing.T) {
	var nilStore *Store
	if nilStore.Load() != "" || nilStore.Save("x") != nil || nilStore.Clear() != nil {
		t.Error("nil Store は何もしないはず")
	}
	empty := New("")
	if empty.Load() != "" || empty.Save("x") != nil || empty.Clear() != nil {
		t.Error("dir 空の Store は何もしないはず")
	}
}
