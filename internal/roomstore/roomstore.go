// Package roomstore は「最後に参加していたルームの共有 URL」を内部ファイルに永続化する。
// アップデート・再起動をまたいでルームへ自動で入り直すためのもの。URL には
// 復号鍵(フラグメント)が入るため、ファイルは所有者のみ読める 0600 で置く。
package roomstore

import (
	"os"
	"path/filepath"
	"strings"
)

// Store は保存先ディレクトリを保持する。
type Store struct {
	dir string
}

// New はディレクトリ dir を使う Store を返す(ファイルは dir/last_room_url)。
func New(dir string) *Store { return &Store{dir: dir} }

// DefaultDir は ~/Library/Application Support/gaya-talk を返す(表示名・管理シークレットと同じ場所)。
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "gaya-talk"), nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "last_room_url") }

// Load は保存済みの共有 URL を返す(無ければ空)。
func (s *Store) Load() string {
	if s == nil || s.dir == "" {
		return ""
	}
	b, err := os.ReadFile(s.path())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Save は共有 URL を保存する(ディレクトリが無ければ作る)。鍵入りなので 0600。
func (s *Store) Save(url string) error {
	if s == nil || s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path(), []byte(url), 0o600)
}

// Clear は保存済みの URL を消す(退出・無効化・失効時に呼ぶ)。無くてもエラーにしない。
func (s *Store) Clear() error {
	if s == nil || s.dir == "" {
		return nil
	}
	err := os.Remove(s.path())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
