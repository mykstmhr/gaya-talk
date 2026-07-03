package adminstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPutGetDeleteRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	if _, ok := s.Get("tok1"); ok {
		t.Error("初期状態で Get が見つけてしまう")
	}
	e := Entry{Server: "https://relay.example", Secret: "sec1", CreatedAt: time.Now().Truncate(time.Second)}
	if err := s.Put("tok1", e); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("tok1")
	if !ok || got.Server != e.Server || got.Secret != e.Secret || !got.CreatedAt.Equal(e.CreatedAt) {
		t.Errorf("Get = %+v, %v; want %+v", got, ok, e)
	}
	// 複数エントリを保持できる。
	if err := s.Put("tok2", Entry{Server: "https://other.example", Secret: "sec2"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("tok1"); !ok {
		t.Error("tok2 追加で tok1 が消えた")
	}
	if err := s.Delete("tok1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("tok1"); ok {
		t.Error("Delete 後も Get が見つけてしまう")
	}
	if _, ok := s.Get("tok2"); !ok {
		t.Error("Delete が別の token まで消した")
	}
	// 存在しない token の Delete はエラーにしない。
	if err := s.Delete("missing"); err != nil {
		t.Errorf("存在しない token の Delete がエラー: %v", err)
	}
}

func TestPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	if err := New(dir).Put("tok", Entry{Secret: "sec"}); err != nil {
		t.Fatal(err)
	}
	// 別インスタンス(=アプリ再起動相当)でも読める。
	if got, ok := New(dir).Get("tok"); !ok || got.Secret != "sec" {
		t.Errorf("再起動相当で読めない: %+v, %v", got, ok)
	}
}

func TestEmptyDirIsNoop(t *testing.T) {
	s := New("")
	if err := s.Put("tok", Entry{Secret: "sec"}); err != nil {
		t.Errorf("空ディレクトリの Put がエラー: %v", err)
	}
	if _, ok := s.Get("tok"); ok {
		t.Error("空ディレクトリの Store が値を返した")
	}
}

func TestCorruptFileIsTreatedAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "admin_secrets.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if _, ok := s.Get("tok"); ok {
		t.Error("壊れたファイルから値が返った")
	}
	// 上書き保存で復旧できる。
	if err := s.Put("tok", Entry{Secret: "sec"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("tok"); !ok {
		t.Error("復旧後に読めない")
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.Put("tok", Entry{Secret: "sec"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "admin_secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("シークレットファイルの権限が %o(want 600)", perm)
	}
}
