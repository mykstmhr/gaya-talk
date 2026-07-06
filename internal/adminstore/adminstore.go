// Package adminstore は自分が作成したルームの管理シークレットを内部ファイルに
// 永続化する。シークレットは共有 URL には載らないため、アプリを再起動しても
// 「自分が作ったルームを無効化できる」ようにここで覚えておく。
package adminstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry は作成したルーム 1 件分の管理情報。
type Entry struct {
	Server    string    `json:"server"`
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
}

// Store は管理シークレットを保存するディレクトリを保持する
// (ファイルは dir/admin_secrets.json、token → Entry のマップ)。
type Store struct {
	mu  sync.Mutex
	dir string
}

// New はディレクトリ dir を使う Store を返す。dir が空なら保存しない(常に空)。
func New(dir string) *Store { return &Store{dir: dir} }

// DefaultDir は ~/Library/Application Support/gaya-talk を返す(namestore と同じ場所)。
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "gaya-talk"), nil
}

func (s *Store) path() string { return filepath.Join(s.dir, "admin_secrets.json") }

// load はファイルを読む。無い・壊れている場合は空マップ(エラーにしない)。
func (s *Store) load() map[string]Entry {
	m := map[string]Entry{}
	b, err := os.ReadFile(s.path())
	if err != nil {
		return m
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]Entry{}
	}
	return m
}

func (s *Store) save(m map[string]Entry) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), b, 0o600)
}

// Get は token に対応する管理情報を返す(無ければ ok=false)。
func (s *Store) Get(token string) (Entry, bool) {
	if s == nil || s.dir == "" {
		return Entry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.load()[token]
	return e, ok
}

// Put は token の管理情報を保存する(既存は上書き)。
func (s *Store) Put(token string, e Entry) error {
	if s == nil || s.dir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	m[token] = e
	return s.save(m)
}

// Delete は token の管理情報を消す(無効化済みルームの後片付け)。無ければ何もしない。
func (s *Store) Delete(token string) error {
	if s == nil || s.dir == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	if _, ok := m[token]; !ok {
		return nil
	}
	delete(m, token)
	return s.save(m)
}
