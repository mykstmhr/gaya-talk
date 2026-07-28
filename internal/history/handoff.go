//go:build darwin

// 再起動ハンドオフ: 自前の「再起動」「アップデート」をまたいで履歴を引き継ぐ。
// 常時永続化はしない(本文を平文でディスクに残し続けないため)。書き出すのは
// 再起動の直前だけで、次回起動の LoadHandoff が成否によらずファイルを即削除し、
// 古いファイル(クラッシュ等で消費されずに残ったもの)は読まずに捨てる。
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// HandoffPath は既定のハンドオフファイルのパスを返す
// (~/Library/Application Support/gaya-talk/history_handoff.json。他ストアと同じ場所)。
func HandoffPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "gaya-talk", "history_handoff.json"), nil
}

// SaveHandoff は現在の履歴を path へ書き出す(再起動・アップデートの直前に呼ぶ)。
// 本文を含むので 0600。履歴が空なら書かない(残留ファイルがあれば消すだけ)。
func SaveHandoff(path string) error {
	entriesMu.Lock()
	snapshot := append([]entry(nil), entries...)
	entriesMu.Unlock()
	if len(snapshot) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadHandoff は path のハンドオフファイルから履歴を復元し、復元した件数を返す
// (無ければ 0)。ファイルは読めたかどうかによらず必ず削除する(本文を残さない)。
// maxAge より古いファイルは再起動をまたいだものではないので読まずに捨てる。
func LoadHandoff(path string, maxAge time.Duration) int {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil || time.Since(st.ModTime()) > maxAge {
		return 0
	}
	var es []entry
	if json.Unmarshal(data, &es) != nil {
		return 0
	}
	for _, e := range es {
		Append(e.Name, e.Text, e.Color, e.SentAt)
	}
	return len(es)
}
