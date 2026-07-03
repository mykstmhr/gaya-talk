package namestore

import "testing"

func TestSaveLoadRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	if got := s.Load(); got != "" {
		t.Errorf("初期状態は空のはず: %q", got)
	}
	if err := s.Save("myk"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); got != "myk" {
		t.Errorf("Load = %q, want myk", got)
	}
	// 上書きできる。
	if err := s.Save("みやこし"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); got != "みやこし" {
		t.Errorf("Load = %q, want みやこし", got)
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save("  myk\n"); err != nil {
		t.Fatal(err)
	}
	if got := s.Load(); got != "myk" {
		t.Errorf("Load = %q, want myk(前後空白は落ちる)", got)
	}
}

func TestLoadMissingDir(t *testing.T) {
	// 存在しないディレクトリでも Load は空を返す(エラーにしない)。
	s := New(t.TempDir() + "/does-not-exist")
	if got := s.Load(); got != "" {
		t.Errorf("Load = %q, want empty", got)
	}
	// Save はディレクトリを作って成功する。
	if err := s.Save("x"); err != nil {
		t.Fatalf("Save がディレクトリを作れない: %v", err)
	}
	if got := s.Load(); got != "x" {
		t.Errorf("Load = %q, want x", got)
	}
}
