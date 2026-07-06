// バージョン表示と自己アップデート(リリース版のみ)。
// 「うまく動かないのがバージョンのせいか」「何が起動しているか」を
// メニューとログから判別できるようにする。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/mykstmhr/gaya-talk/internal/dialog"

	"fyne.io/systray"
)

// version / buildKind はビルド時に -ldflags -X で埋め込む(Makefile 参照)。
// version: ローカルは git describe(例 v0.2.4-3-gc42e34d-dirty)、リリースはタグ。
// buildKind: "release"(CI のタグビルド)or "local"。
var (
	version   = "dev"
	buildKind = "local"
)

// releaseRepo は自己アップデートの取得元(gh release の --repo に渡す)。
const releaseRepo = "mykstmhr/gaya-talk"

// versionString はメニュー・ログ表示用の「v0.2.4(リリース版)」を返す。
func versionString() string {
	kind := "ローカルビルド"
	if buildKind == "release" {
		kind = "リリース版"
	}
	return fmt.Sprintf("%s(%s)", version, kind)
}

// ghBin は gh(GitHub CLI)のパスを解決する。.app 起動時は PATH が乏しいため
// Homebrew の定番パスもみる(ollamaBin と同じ発想)。見つからなければ空。
func ghBin() string {
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// latestReleaseTag は GitHub Release の最新タグを gh 経由で取得する
// (private repo のため素の API ではなく、インストールにも使う gh の認証に相乗りする)。
func latestReleaseTag(ctx context.Context, gh string) (string, error) {
	out, err := exec.CommandContext(ctx, gh, "release", "view",
		"--repo", releaseRepo, "--json", "tagName").Output()
	if err != nil {
		return "", fmt.Errorf("gh release view に失敗(gh auth login 済みか確認): %w", err)
	}
	var v struct {
		TagName string `json:"tagName"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	if v.TagName == "" {
		return "", fmt.Errorf("最新タグを取得できませんでした")
	}
	return v.TagName, nil
}

// checkAndUpdate はメニューの「アップデートを確認…」の本体。最新タグを確認し、
// 現在と異なれば確認のうえ自己アップデートする(メニューの goroutine から呼ぶ)。
func checkAndUpdate() {
	gh := ghBin()
	if gh == "" {
		log.Println("⚠️ gh(GitHub CLI)が見つかりません。brew install gh && gh auth login のあと再度お試しください。")
		dialog.Alert("アップデートを確認できません",
			"gh(GitHub CLI)が見つかりません。\nbrew install gh && gh auth login のあと再度お試しください。", "OK")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	latest, err := latestReleaseTag(ctx, gh)
	if err != nil {
		log.Printf("⚠️ アップデート確認に失敗: %v", err)
		dialog.Alert("アップデート確認に失敗", err.Error(), "OK")
		return
	}
	if latest == version {
		log.Printf("✅ 最新です(%s)。", version)
		dialog.Alert("最新です", "現在のバージョン "+version+" が最新です。", "OK")
		return
	}
	if !dialog.Confirm("アップデート",
		"新しいバージョン "+latest+" があります(現在 "+version+")。\n\n"+
			"ダウンロードして /Applications を差し替え、アプリを再起動します。",
		"アップデートして再起動") {
		return
	}
	if err := selfUpdate(gh); err != nil {
		log.Printf("⚠️ アップデートを開始できませんでした: %v", err)
		dialog.Alert("アップデートに失敗", err.Error(), "OK")
		return
	}
	log.Printf("⬇️ %s へアップデートします(ダウンロード後に自動で再起動)…", latest)
	quitApp() // 後始末(専用 Ollama の停止)をしてから終了。差し替えは子プロセスが行う
}

// selfUpdate は README のインストールワンライナーと同じ手順を子プロセスで実行する。
// 自分自身を差し替えるため、(1) 起動中にダウンロード → (2) 本体の終了を待つ →
// (3) /Applications を差し替えて開き直す、の順で行う(呼び出し側が quitApp する)。
func selfUpdate(gh string) error {
	script := `
d="$(mktemp -d)" || exit 1
"$1" release download --repo ` + releaseRepo + ` --pattern gaya-talk.app.zip --dir "$d" || exit 1
i=0; while pgrep -x gaya-talk >/dev/null 2>&1 && [ $i -lt 30 ]; do sleep 1; i=$((i+1)); done
rm -rf /Applications/gaya-talk.app
ditto -x -k "$d/gaya-talk.app.zip" /Applications && open /Applications/gaya-talk.app
`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", gh)
	// 本体の終了(quitApp)に巻き込まれないようプロセスグループを切り離す。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd.Start()
}

// addVersionMenuItems はメニュー下部にバージョン情報行と(リリース版のみ)
// アップデート確認の項目を作る。onReady から呼ぶ。
func addVersionMenuItems() {
	setInfo(newInfoItem(), "バージョン : "+versionString())
	if buildKind != "release" {
		return // ローカルビルドの更新は make で行う(自己アップデートの対象外)
	}
	mUpdate := systray.AddMenuItem("アップデートを確認…", "GitHub Release の最新バージョンを確認して更新する")
	go func() {
		for range mUpdate.ClickedCh {
			checkAndUpdate()
		}
	}()
}
