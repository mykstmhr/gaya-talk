// Package namestore は記名ルームの表示名を config とは別の内部ファイルに永続化する。
// config(JSONC・コメント付き)を書き換えずに「前回入力した名前」を覚えるためのもの。
package namestore

import (
	"os"
	"path/filepath"
	"strings"
)

// Store は表示名を保存するディレクトリを保持する。
type Store struct {
	dir string
}

// New はディレクトリ dir を使う Store を返す(ファイルは dir/display_name)。
func New(dir string) *Store { return &Store{dir: dir} }

// DefaultDir は ~/Library/Application Support/gaya を返す(多重起動ロックと同じ場所)。
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "gaya"), nil
}

// Load は保存済みの表示名を返す(無ければ空)。前後の空白は落とす。
func (s *Store) Load() string {
	if s == nil || s.dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "display_name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Save は表示名を保存する(ディレクトリが無ければ作る)。失敗時はエラーを返す。
func (s *Store) Save(name string) error {
	if s == nil || s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "display_name"), []byte(name), 0o600)
}
