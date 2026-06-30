// Package tokenstore は user token を macOS Keychain に安全に保存・読み出しする。
// 平文ファイルに置かないことで、トークン漏洩リスクを下げる。
package tokenstore

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	service = "ura-talk"
	account = "slack-user-token"
)

// Save はトークンを Keychain に保存する(既存があれば更新)。
func Save(token string) error {
	cmd := exec.Command("security", "add-generic-password",
		"-s", service, "-a", account, "-w", token, "-U")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Keychain への保存失敗: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Load はトークンを Keychain から読み出す。未登録なら空文字を返す(エラーにしない)。
func Load() (string, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", service, "-a", account, "-w")
	out, err := cmd.Output()
	if err != nil {
		// 未登録時も非ゼロ終了するため、空として扱う。
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// Delete は保存済みトークンを削除する。
func Delete() error {
	cmd := exec.Command("security", "delete-generic-password",
		"-s", service, "-a", account)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Keychain からの削除失敗: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
