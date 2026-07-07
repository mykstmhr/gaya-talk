// gaya-talk: グローバルなホットキーでマイクを録音し、ローカルの whisper-cli で
// 文字起こしして、画面全体のオーバーレイにライブコメントとして流す
// 常駐ツール。同じルームに参加したメンバー間でコメントを共有できる(中継サーバ経由)。
//
// 発話・文字起こし・整形はすべてローカル完結。ルーム共有時も本文は E2E 暗号化され、
// 中継サーバには暗号文しか渡らない(internal/room 参照)。
//
// 使い方:
//
//	gaya-talk              常駐を開始する(メニューバーに常駐)
//	gaya-talk dryrun       送信せず、文字起こし結果をログに出すだけ(動作確認用)
//	gaya-talk devices      利用可能な入力デバイス(マイク)の一覧を表示する
//	gaya-talk keys         ホットキーに指定できるキー名の一覧を表示する
//	gaya-talk overlay-demo ルームを使わずオーバーレイの見た目だけ確認する
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mykstmhr/gaya-talk/internal/audioout"
	"github.com/mykstmhr/gaya-talk/internal/config"
	"github.com/mykstmhr/gaya-talk/internal/dialog"
	"github.com/mykstmhr/gaya-talk/internal/enhance"
	"github.com/mykstmhr/gaya-talk/internal/inputbar"
	"github.com/mykstmhr/gaya-talk/internal/modkey"
	"github.com/mykstmhr/gaya-talk/internal/overlay"
	"github.com/mykstmhr/gaya-talk/internal/recorder"
	"github.com/mykstmhr/gaya-talk/internal/transcribe"
	"github.com/mykstmhr/gaya-talk/internal/trayicon"
	"github.com/mykstmhr/gaya-talk/internal/vad"
	"github.com/mykstmhr/gaya-talk/internal/voicebar"
	"github.com/mykstmhr/gaya-talk/internal/voicegate"

	"fyne.io/systray"
	"golang.design/x/hotkey"
)

// exampleConfig はコメント付きの設定ファイルの雛形。.app に埋め込み、初回起動時に
// 生成する(.app の zip だけ受け取った人が make setup なしで始められるように)。
//
//go:embed config.example.json
var exampleConfig []byte

// ensureDefaultConfig は設定ファイルが無ければ雛形(config.example.json)を生成する。
func ensureDefaultConfig() {
	path := config.Path()
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		// 以前の 0o644 で生成されたファイルに備え、所有者のみへ絞る(ログと同じ遡及パターン)。
		_ = os.Chmod(path, 0o600)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// 後から room.slack_bot_token 等の秘密を書き込むファイルなので所有者のみ。
	if err := os.WriteFile(path, exampleConfig, 0o600); err != nil {
		log.Printf("設定ファイルの生成に失敗: %v", err)
		return
	}
	log.Printf("初回起動: 設定ファイルを生成しました: %s", path)
}

// openConfigFile は設定ファイルを既定のテキストエディタで開く(無ければ生成してから)。
func openConfigFile() {
	ensureDefaultConfig()
	path := config.Path()
	if path == "" {
		return
	}
	if err := exec.Command("open", "-t", path).Start(); err != nil {
		log.Printf("設定ファイルを開けません: %v", err)
	}
}

// restartApp はアプリを終了して開き直す(設定変更の反映用)。多重起動防止の
// ファイルロックが解放されるのを待つ必要があるため、少し眠ってから開き直す
// 子プロセスに任せて自分は終了する。
func restartApp() {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("再起動できません: %v", err)
		return
	}
	var cmd *exec.Cmd
	if i := strings.Index(exe, ".app/Contents/MacOS/"); i >= 0 {
		cmd = exec.Command("/bin/sh", "-c", `sleep 1; open "$0"`, exe[:i+len(".app")])
	} else {
		cmd = exec.Command("/bin/sh", "-c", `sleep 1; exec "$0"`, exe)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("再起動できません: %v", err)
		return
	}
	log.Println("再起動します…")
	quitApp()
}

// warnIfTranslocated は Gatekeeper の App Translocation(パスランダム化)下で
// 動いていたら警告ダイアログを出す。quarantine 付きの zip を解凍してその場から
// 開くと起動ごとに変わる読み取り専用パスで実行され、アクセシビリティ権限が
// アプリに紐づかずホットキー(CGEventTap)が一切拾えなくなる。
// 移動して開き直せば解消するので、起動は止めない(オーバーレイ表示などは動く)。
func warnIfTranslocated() {
	exe, err := os.Executable()
	if err != nil || !strings.Contains(exe, "/AppTranslocation/") {
		return
	}
	log.Println("⚠️ App Translocation 下で起動しています(quarantine 付きのまま開いた)。ホットキーが使えません。アプリを移動して開き直してください。")
	dialog.Alert("gaya-talk を移動してください",
		"Gatekeeper により一時的なパスから起動されているため、ホットキー(右⌘ など)が使えません。\n\n"+
			"gaya-talk.app を Finder で「アプリケーション」フォルダへ移動してから、開き直してください。",
		"OK")
}

// setupLogging は、.app バンドルから起動された場合にログを ~/Library/Logs/gaya-talk.log へ出す。
// Finder/launchd 起動では stdout が /dev/null(文字デバイス)になり stderr も失われるため、
// 端末判定ではなく「実行ファイルが .app の中にあるか」で判定する。
func setupLogging() {
	exe, err := os.Executable()
	if err != nil || !strings.Contains(exe, ".app/Contents/MacOS/") {
		return // .app 以外(端末から起動)は通常どおり stderr
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, "Library", "Logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	logPath := filepath.Join(dir, "gaya-talk.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	// 既存ファイルが以前の 0o644 で作られている場合に備え、所有者のみへ絞る
	// (発話由来の内容を含みうるため、他ユーザから読めないようにする)。
	_ = os.Chmod(logPath, 0o600)
	log.SetOutput(f)
}

func main() {
	setupLogging()

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "", "run":
		startTray(false)
	case "dryrun":
		startTray(true)
	case "devices":
		runDevices()
	case "keys":
		runKeys()
	case "overlay-demo":
		runOverlayDemo()
	case "version":
		fmt.Println("gaya-talk " + versionString())
	default:
		fmt.Fprintf(os.Stderr, "不明なサブコマンド: %s\n使い方: gaya-talk [run|dryrun|devices|keys|overlay-demo|version]\n", cmd)
		os.Exit(2)
	}
}

// runDevices は利用可能な入力デバイスの一覧を表示する。
func runDevices() {
	rec, err := recorder.New("")
	if err != nil {
		log.Fatalf("録音初期化エラー: %v", err)
	}
	defer rec.Close()
	names, err := rec.InputDevices()
	if err != nil {
		log.Fatalf("デバイス列挙エラー: %v", err)
	}
	fmt.Println("利用可能な入力デバイス:")
	for _, n := range names {
		fmt.Println("  - " + n)
	}
	fmt.Println("\nconfig.json の voice.device に上記の名前(部分一致でOK)を設定してください。")
	fmt.Println("例: Bluetooth イヤホンの再生音を切らさないために内蔵マイクを指定する。")
}

// runKeys は config の hotkey で指定できる修飾キー/メインキーの一覧を表示する。
func runKeys() {
	keys := make([]string, 0, len(keyMap))
	for k := range keyMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("ホットキーは config の \"voice.hotkey\" / \"input_hotkey\" で変更できます:")
	fmt.Println(`  "hotkey": { "mods": ["ctrl","shift"], "key": "space" }`)
	fmt.Println()
	fmt.Println("修飾キー(mods): ctrl, shift, option(alt), cmd")
	fmt.Println("メインキー(key):")
	fmt.Println("  " + strings.Join(keys, ", "))
	mk := modkey.Names()
	sort.Strings(mk)
	fmt.Println()
	fmt.Println("単体修飾キー(mods は空にして key に指定):")
	fmt.Println("  " + strings.Join(mk, ", "))
	fmt.Println(`  例: "hotkey": { "mods": [], "key": "rightcmd" }`)
	fmt.Println(`  修飾キー2つのコードも可: "hotkey": { "mods": ["rightshift"], "key": "rightcmd" }`)
	fmt.Println()
	fmt.Println("変更後はアプリを再起動してください(.app はメニューバー→終了→再 open)。")
}

// runOverlayDemo は room 機能を使わずにオーバーレイの見た目だけ確認する。
// サンプルコメントをランダム間隔で流し続ける(メニューバーの「終了」で止める)。
func runOverlayDemo() {
	systray.Run(func() {
		systray.SetTemplateIcon(trayicon.Idle, trayicon.Idle)
		systray.SetTooltip("gaya-talk overlay demo")
		mQuit := systray.AddMenuItem("終了", "デモを終了する")
		go func() {
			<-mQuit.ClickedCh
			systray.Quit()
		}()

		overlay.Start()
		go func() {
			samples := []struct{ text, color string }{
				{"それな", "#ffffff"},
				{"wwww", "#ffcc00"},
				{"たしかに", "#66ccff"},
				{"この案よさそう", "#99ff99"},
				{"あとで聞いてみよう", "#ff9999"},
				{"888888", "#ffffff"},
				{"ちょっと待って、それ先週決まったやつでは？", "#cc99ff"},
				{"賛成です", "#66ffcc"},
			}
			for i := 0; ; i++ {
				s := samples[i%len(samples)]
				overlay.Show(s.text, s.color)
				time.Sleep(time.Duration(600+rand.IntN(1400)) * time.Millisecond)
			}
		}()
	}, func() { os.Exit(0) })
}

// output は文字起こし結果の送り先。send が nil ならドライラン(表示のみ)。
type output struct {
	send func(text string) error
	name string
}

// voiceUnavailable は音声入力に必要な whisper が使えない理由を返す(使えるなら空)。
// 起動を止めるほどのことではないので、呼び出し側は文字入力のみへフォールバックする。
func voiceUnavailable(cfg *config.Config) string {
	if cfg.Whisper.Model == "" {
		return "whisper.model が未設定"
	}
	if _, err := os.Stat(cfg.Whisper.Model); err != nil {
		return "whisper モデルが見つかりません: " + cfg.Whisper.Model
	}
	if _, err := exec.LookPath(cfg.Whisper.Bin); err != nil {
		return "whisper-cli が見つかりません: " + cfg.Whisper.Bin
	}
	return ""
}

// buildSink は送り先を決める。オーバーレイ(ルーム共有)固定。dryRun は送らず表示のみ。
func buildSink(cfg *config.Config, dryRun bool) output {
	if dryRun {
		return output{name: "ドライラン(送信しません)"} // send は nil
	}
	log.Println("ℹ️ 発話・入力はライブコメントとして画面のオーバーレイに流れます。メニューバーからルームを作成/参加できます。")
	if cfg.Room.Server == "" {
		log.Println("   room.server が未設定なのでソロモード(自分の画面のみ)で動きます。")
	}
	return output{
		name: "オーバーレイ",
		send: func(text string) error { sendRoomComment(text); return nil },
	}
}

// lockFile は多重起動防止のロックを保持する(プロセス終了まで開いたままにする)。
var lockFile *os.File

// mInfoKeys はメニュー下部のキー情報行(例「入力バー : 右⌘ / 音声 : 右⇧+右⌘」)。
// 状態(待機/聞き取り…)はメニューには出さず、アイコン色・画面下部のバー・ツールチップが担う。
var mInfoKeys *systray.MenuItem

// enhancer は文字起こし結果のローカル LLM 整形(無効なら素通し)。serve で初期化する。
// serve goroutine が書き、quitApp(シグナル/メニューの goroutine)が読むため atomic。
// Load() が返す nil は enhance 側の各メソッドが安全に扱う。
var enhancer atomic.Pointer[enhance.Enhancer]

// startTray はメニューバー常駐(systray)を起動する。systray が NSApplication の
// メインループを回し、その中でホットキー登録・録音・文字起こしを動かす。
func startTray(dryRun bool) {
	if !acquireSingleInstance() {
		log.Println("gaya-talk は既に起動しています(多重起動を防止しました)。")
		fmt.Fprintln(os.Stderr, "gaya-talk は既に起動しています。")
		return
	}
	// SIGTERM / Ctrl-C でもメニューの「終了」と同じ後始末を通す
	// (make restart 等の pkill で専用 Ollama が孤児にならないように)。
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM, os.Interrupt)
		<-c
		quitApp()
	}()
	systray.Run(onReady(dryRun), func() { os.Exit(0) })
}

// quitApp は後始末をしてからアプリを終了する。すべての終了経路はここを通ること。
// macOS の systray.Quit は NSApp terminate で即プロセスが死に、systray.Run の
// 終了コールバックまで戻ってこないため、Quit の「前」に後始末する必要がある。
func quitApp() {
	// 管理下(専用ポート)の Ollama は道連れにする(共有インスタンスは触らない)。
	if err := enhancer.Load().StopServer(); err != nil {
		log.Printf("⚠️ 専用 Ollama の停止に失敗: %v", err)
	}
	systray.Quit()
}

// onReady はメニューバーアイコンとメニューを用意し、本体処理を別 goroutine で開始する。
func onReady(dryRun bool) func() {
	return func() {
		systray.SetTemplateIcon(trayicon.Idle, trayicon.Idle)
		systray.SetTooltip("gaya-talk " + version)

		// 主軸のルーム操作を最上部に(serve から Show する)。
		addRoomMenuItems()

		systray.AddSeparator()
		// キー情報は下部に 1 行だけ(内容は serve で確定して Show する)。
		mInfoKeys = newInfoItem()
		// 何が起動しているか(バージョン・リリース版/ローカルビルド)を常に見えるように。
		addVersionMenuItems()
		addNameMenuItem()
		mOpenConfig := systray.AddMenuItem("設定ファイルを開く…", "設定(config.json)をテキストエディタで開く。変更の反映は「再起動」")
		go func() {
			for range mOpenConfig.ClickedCh {
				openConfigFile()
			}
		}()
		// 画面共有への表示は毎回オフで始める(オンのまま忘れて次の会議でコメントが
		// 映る事故を防ぐため、あえて永続化しない)。入力バー・音声バーは常に映らない。
		mShareOverlay := systray.AddMenuItemCheckbox("画面共有にコメントを映す",
			"オンにするとオーバーレイのコメントが画面共有・収録に映る(視聴者に見せながら発表する用)。再起動でオフに戻る", false)
		go func() {
			for range mShareOverlay.ClickedCh {
				if mShareOverlay.Checked() {
					mShareOverlay.Uncheck()
					overlay.SetShared(false)
					log.Println("🙈 コメントは画面共有に映りません(既定)。")
					overlay.Show("画面共有への表示: オフ", "#ffcc00")
				} else {
					mShareOverlay.Check()
					overlay.SetShared(true)
					log.Println("📺 コメントを画面共有・収録に映します(オフに戻すまで)。")
					overlay.Show("📺 画面共有への表示: オン(コメントが相手に見えます)", "#ffcc00")
				}
			}
		}()

		systray.AddSeparator()
		mRestart := systray.AddMenuItem("再起動", "アプリを開き直す(設定変更の反映)")
		go func() {
			<-mRestart.ClickedCh
			restartApp()
		}()
		mQuit := systray.AddMenuItem("終了", "gaya-talk を終了する")
		go func() {
			<-mQuit.ClickedCh
			quitApp()
		}()

		go serve(dryRun)
	}
}

// iconState は音声入力の基本状態(メニューバーとバーの表示のもとになる)。
type iconState int

const (
	iconIdle   iconState = iota // 待機
	iconListen                  // リッスン中(無音待ち)
	iconRec                     // 録音/音声検出中
)

// truncRunes は表示が長くなりすぎないよう name を max 文字に丸める(超過分は …)。
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// newInfoItem は選択できない情報行を 1 つ作る。無効・初期は非表示。
// 内容が確定する serve で setInfo により文言をセットして表示する。
func newInfoItem() *systray.MenuItem {
	it := systray.AddMenuItem("", "")
	it.Disable()
	it.Hide()
	return it
}

// setInfo は動作情報行の文言をセットして表示する。
func setInfo(item *systray.MenuItem, text string) {
	if item == nil {
		return
	}
	item.SetTitle(text)
	item.Show()
}

// tray は「基本状態(待機/リッスン/録音)」と、並行する「文字起こし」の件数を持ち、
// メニューバーアイコン・ドロップダウンの状態テキスト・画面下部のバーをまとめて更新する。
//
// 表示の方針:
//   - メニューバーは「マイクの生死」だけ(待機=黒テンプレート ⇄ リッスン/録音=オレンジ)。
//     赤点滅やロボへの切り替えはやめ、詳細な状態はバー側に集約する。
//   - バーはリッスン系の状態を主ラベルに出し、並行する文字起こしはロボのバッジ(×N)で示す
//     (1 つ目を文字起こししながら 2 つ目を話せる=両立していることを表現する)。
//   - リッスンを止めても文字起こしが残っていればバーを「文字起こし中…」で出し続け、
//     全部流れ終わったら消す(止めた→流れるまでの空白を無くす)。
var tray = &trayStatus{baseIcon: iconIdle, baseText: "待機中…"}

type trayStatus struct {
	mu           sync.Mutex
	cfg          *config.Config // バー表示の可否(voice.bar)。serve で設定する
	baseIcon     iconState
	baseText     string // ドロップダウンの状態テキスト
	barText      string // バーの主ラベル(リッスン系の状態のときだけ非空)
	transcribing int
	inputOpen    bool // 文字入力バーが開いている間は音声バーを引っ込める(同じ場所に出るため)
}

// setInputOpen は文字入力バーの開閉を反映する(開いている間は音声バーを出さない)。
func (t *trayStatus) setInputOpen(open bool) {
	t.mu.Lock()
	t.inputOpen = open
	t.mu.Unlock()
	t.apply()
}

// setConfig はバー表示の設定を渡す(serve の先頭で一度呼ぶ)。
func (t *trayStatus) setConfig(cfg *config.Config) {
	t.mu.Lock()
	t.cfg = cfg
	t.mu.Unlock()
}

// setBase は基本状態を更新する。barText はバーの主ラベル(待機は空)。
func (t *trayStatus) setBase(state iconState, menuText, barText string) {
	t.mu.Lock()
	t.baseIcon = state
	t.baseText = menuText
	t.barText = barText
	t.mu.Unlock()
	t.apply()
}

// beginTranscribe / endTranscribe は並行する文字起こしの件数を数える。
func (t *trayStatus) beginTranscribe() {
	t.mu.Lock()
	t.transcribing++
	t.mu.Unlock()
	t.apply()
}

func (t *trayStatus) endTranscribe() {
	t.mu.Lock()
	if t.transcribing > 0 {
		t.transcribing--
	}
	t.mu.Unlock()
	t.apply()
}

// apply は現在の状態をメニューバー・ドロップダウン・バーへ反映する。
func (t *trayStatus) apply() {
	t.mu.Lock()
	icon, menuText, barText, n, cfg := t.baseIcon, t.baseText, t.barText, t.transcribing, t.cfg
	inputOpen := t.inputOpen
	t.mu.Unlock()

	// メニューバー: マイクが生きているときだけオレンジ。それ以外は黒テンプレート。
	if icon == iconListen || icon == iconRec {
		systray.SetIcon(trayicon.ListenOn)
	} else {
		systray.SetTemplateIcon(trayicon.Idle, trayicon.Idle)
	}

	// 状態テキストはメニューに出さず、アイコンのツールチップに載せる
	// (文字起こし中はそちらを優先表示)。
	st := menuText
	if n > 0 {
		st = "文字起こし中…"
	}
	systray.SetTooltip("gaya-talk — " + st)

	// 画面下部のバー。文字入力バーが開いている間は同じ場所を譲る(閉じたら復帰)。
	if cfg == nil || !cfg.Voice.Bar {
		return
	}
	if inputOpen {
		voicebar.Hide()
		return
	}
	switch {
	case barText != "": // リッスン/録音中(文字起こしが並行していればロボのバッジが付く)
		voicebar.Show(barText, icon == iconRec, true, n)
	case n > 0: // リッスンは止めたが文字起こしが流れ終わっていない
		voicebar.Show("文字起こし中…", false, false, n)
	default:
		voicebar.Hide()
	}
}

// acquireSingleInstance はファイルロックで多重起動を防ぐ。取得できれば true。
func acquireSingleInstance() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return true // 判定できないときは通す
	}
	dir := filepath.Join(home, "Library", "Application Support", "gaya-talk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return true
	}
	f, err := os.OpenFile(filepath.Join(dir, "gaya-talk.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return false // 既に他プロセスが保持 = 起動中
	}
	lockFile = f // 開いたまま保持してロックを維持する
	return true
}

// serve は本体処理:設定読込→送り先決定→録音・ホットキー→常駐ループ。
// systray のメインループ上(別 goroutine)で動く。dryRun のときは送らず表示のみ。
func serve(dryRun bool) {
	// 「どのビルドが動いているか」を調査の起点にできるよう、必ずログの先頭付近に残す。
	log.Printf("gaya-talk %s 起動", versionString())
	warnIfTranslocated()
	ensureDefaultConfig()
	cfg, err := config.Load()
	if err != nil {
		// log.Fatalf(os.Exit)は使わない: すべての終了経路は quitApp を通す規約
		// (専用 Ollama の後始末等)。以下の致命エラーも同様。
		log.Printf("⛔ 設定エラー: %v", err)
		quitApp()
		return
	}
	tray.setConfig(cfg)

	// 文字入力バーの開閉を追う。開いたら効果音を鳴らし、開いている間は音声バーを
	// 引っ込め(同じ場所に出るため)、開いた通知は inputBarShown 経由で vadLoop へ
	// 渡してリッスンを止める(排他)。
	// コールバックはメインスレッドから来るのでブロックしないこと。
	inputbar.SetOnShown(func() {
		if logBodyEnabled() {
			log.Println("inputbar: 表示コールバック(OnShown)")
		}
		if cfg.Sound.Enabled {
			playSound(cfg.Sound.InputOpen)
		}
		tray.setInputOpen(true)
		select {
		case inputBarShown <- struct{}{}:
		default:
		}
	})
	inputbar.SetOnHidden(func() {
		if cfg.Sound.Enabled {
			playSound(cfg.Sound.InputClose)
		}
		tray.setInputOpen(false)
	})

	// アクセシビリティ権限が無くてもイベントタップの作成は成功してしまい、
	// 「アクティブなときだけホットキーが効く」という不可解な挙動になる。
	// 起動時に検査してシステムの許可ダイアログを出し、付与されたら自動で再起動する
	// (権限は起動中のタップに遡って効かないため、再起動が要る)。
	// 設定のトグルが ON に見えるのに false になる場合は、署名の違う旧ビルドへの
	// 許可が残っている(設定から一度削除して追加し直すと直る)。
	if !modkey.Trusted(true) {
		log.Println("⚠️ アクセシビリティ権限が有効ではありません。ホットキーはアプリが非アクティブの間は反応しません。" +
			"許可されたら自動で再起動します(トグルが ON なのに直らない場合は、設定から一度削除して追加し直してください)。")
		go func() {
			for range time.Tick(3 * time.Second) {
				if modkey.Trusted(false) {
					log.Println("✅ アクセシビリティ権限を確認しました。反映のため再起動します。")
					restartApp()
					return
				}
			}
		}()
	}

	// オーバーレイ・入力バー・ルームメニューを配線する(dryRun では画面に出さない)。
	if !dryRun {
		setupRoom(cfg)
	}

	// voice.input が "off" なら、マイク・whisper・Ollama を一切使わず文字入力バーだけで動く。
	if cfg.Voice.Input == config.VoiceOff {
		log.Println("ℹ️ 音声入力は無効です(文字入力バーのみ)。")
		updateKeyInfo(cfg)
		tray.setBase(iconIdle, "待機中(文字入力のみ)…", "")
		return
	}

	// 音声を使う設定でも whisper が揃っていなければ、落とさず文字入力のみで動く
	// (.app の zip だけ受け取って config なしで起動したケースを含む)。
	if reason := voiceUnavailable(cfg); reason != "" {
		log.Printf("ℹ️ 音声入力は無効です(%s)。文字入力のみで起動します。声も使う手順は README の「声でも参加する」を参照。", reason)
		cfg.Voice.Input = config.VoiceOff
		updateKeyInfo(cfg)
		tray.setBase(iconIdle, "待機中(文字入力のみ)…", "")
		return
	}

	// "auto" は出力デバイスで音声入力の可否を決める(スピーカー出力中は相手の声を
	// 拾わないよう自動オフ)。"on" は常に許可。出力の抜き差しに追従する。
	if cfg.Voice.Input == config.VoiceAuto {
		voice = voicegate.New(audioout.Private())
		audioout.Watch(func() { applyVoiceAuto(cfg) })
	} else {
		voice = voicegate.NewAlwaysOn()
	}

	// 送り先(sink)を決める。dryRun は送らず表示のみ(out.send == nil)。
	out := buildSink(cfg, dryRun)

	// 文字起こし整形/絵文字付与(ローカル LLM)。無効でも New は安全。
	enh := enhance.New(enhance.Config{
		Enabled:     cfg.Enhance.Enabled,
		Endpoint:    cfg.Enhance.Endpoint,
		Model:       cfg.Enhance.Model,
		Prompt:      cfg.Enhance.Prompt,
		EmojiMode:   cfg.Emoji.Mode,
		AllowRemote: cfg.Enhance.AllowRemote,
	})
	enhancer.Store(enh)
	// 整形か絵文字のどちらかが有効なら Ollama を用意する。
	llmNeeded := cfg.Enhance.Enabled || (cfg.Emoji.Mode != "" && cfg.Emoji.Mode != "off")
	if llmNeeded {
		// Ollama が起動していなければ自動起動する(出力は破棄)。
		ictx, icancel := context.WithTimeout(context.Background(), 20*time.Second)
		if started, err := enh.EnsureServer(ictx); err != nil {
			log.Printf("⚠️ Ollama を起動できませんでした: %v", err)
		} else if started {
			log.Println("Ollama を自動起動しました")
		}
		icancel()

		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := enh.Check(cctx); err != nil {
			log.Printf("⚠️ Ollama が使えません: %v → 整形/絵文字はスキップします", err)
		} else {
			log.Printf("✅ Ollama 有効: model=%s endpoint=%s(整形=%v / 絵文字=%s)",
				cfg.Enhance.Model, cfg.Enhance.Endpoint, cfg.Enhance.Enabled, cfg.Emoji.Mode)
			// モデルを先読みして初回のコールドスタート待ちを無くす(バックグラウンド)。
			go func() {
				wctx, wcancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer wcancel()
				if err := enh.Warmup(wctx); err != nil {
					log.Printf("整形モデルのウォームアップ失敗(初回は遅くなる可能性): %v", err)
				} else {
					log.Println("整形モデルをウォームアップしました(初回から高速)")
				}
			}()
		}
		ccancel()
	}

	// 前回クラッシュ等で残った古い一時 WAV を掃除する(10分より古いものだけ)。
	if n, err := transcribe.CleanupStaleTempFiles(os.TempDir(), 10*time.Minute); err == nil && n > 0 {
		log.Printf("古い一時ファイルを %d 件削除しました", n)
	}

	rec, err := recorder.New(cfg.Voice.Device)
	if err != nil {
		log.Printf("⛔ 録音初期化エラー: %v", err)
		quitApp()
		return
	}
	defer rec.Close()

	wh := transcribe.Whisper{
		Bin:           cfg.Whisper.Bin,
		Model:         cfg.Whisper.Model,
		Lang:          cfg.Whisper.Language,
		BeamSize:      cfg.Whisper.BeamSize,
		Prompt:        cfg.Whisper.Prompt,
		NoSpeechThold: cfg.Whisper.NoSpeechThold,
	}

	down, up, stopTrigger, err := buildTrigger(cfg.Voice.Hotkey)
	if err != nil {
		log.Printf("⛔ ホットキー設定エラー: %v", err)
		quitApp()
		return
	}
	defer stopTrigger()

	// メニュー下部のキー情報行を確定して表示する。
	updateKeyInfo(cfg)
	if cfg.Voice.Input == config.VoiceAuto {
		if voice.Allowed() {
			log.Println("🎧 イヤホン出力を検出 → 音声入力は有効です(スピーカーに切り替えると自動オフ)。")
		} else {
			log.Println("🔈 スピーカー出力を検出 → 音声入力は自動オフ中です(イヤホンにすると自動オン)。")
		}
	}

	if cfg.Voice.ListenMode == "vad" {
		log.Printf("gaya-talk 起動 [%s / VAD]。[%s] で聞き取り開始/停止。話すと無音で自動区切りして流します。", out.name, cfg.Voice.Hotkey)
		tray.setBase(iconIdle, "待機中…", "")
		vadLoop(rec, wh, out, cfg, down)
		return
	}

	log.Printf("gaya-talk 起動 [%s / PTT]。[%s] を押している間だけ録音します。", out.name, cfg.Voice.Hotkey)
	tray.setBase(iconIdle, "待機中…", "")
	pttLoop(rec, wh, out, cfg, down, up)
}

// voice は音声入力を今受け付けてよいかの状態(auto では出力デバイスに追従)。serve で構築。
var voice *voicegate.Gate

// inputBarShown は文字入力バーが開いたことを vadLoop へ伝える(音声リッスンとの排他用)。
var inputBarShown = make(chan struct{}, 1)

// applyVoiceAuto は出力構成の変化に応じて音声入力の可否を切り替える(auto 時のみ)。
// audioout の監視コールバックから呼ばれる。
func applyVoiceAuto(cfg *config.Config) {
	priv := audioout.Private()
	if !voice.Set(priv) {
		return // 変化なし
	}
	if priv {
		log.Println("🎧 イヤホン出力を検出 → 音声入力を有効化しました。")
	} else {
		log.Println("🔈 スピーカー出力を検出 → 音声入力を無効化しました(相手の声をオーバーレイに拾わないため)。")
	}
	updateKeyInfo(cfg)
}

// updateKeyInfo はメニュー下部のキー情報行を現在の状態に合わせて更新する。
// 例「入力バー : 右⌘ / 音声 : 右⇧+右⌘」。音声が使えない理由(スピーカー出力中)も
// この行に集約する。
func updateKeyInfo(cfg *config.Config) {
	line := "入力バー : " + prettyHotkey(cfg.InputHotkey)
	switch {
	case cfg.Voice.Input == config.VoiceOff:
		// 文字入力のみ(音声のキーは出さない)
	case cfg.Voice.Input == config.VoiceAuto && !voice.Allowed():
		line += " / 音声 : オフ(スピーカー出力中)"
	default:
		line += " / 音声 : " + prettyHotkey(cfg.Voice.Hotkey)
	}
	setInfo(mInfoKeys, line)
}

// prettyHotkey はホットキーをメニュー表示用の記号(右⌘ 等)に変換する。
// 未知のキー名はそのまま出す(config の表記と突き合わせられるように)。
func prettyHotkey(h config.Hotkey) string {
	symbols := map[string]string{
		"rightcmd": "右⌘", "leftcmd": "左⌘", "cmd": "⌘",
		"rightshift": "右⇧", "leftshift": "左⇧", "shift": "⇧",
		"rightoption": "右⌥", "leftoption": "左⌥", "option": "⌥", "alt": "⌥",
		"ctrl": "⌃",
	}
	parts := append(append([]string{}, h.Mods...), h.Key)
	for i, p := range parts {
		if s, ok := symbols[strings.ToLower(p)]; ok {
			parts[i] = s
		}
	}
	return strings.Join(parts, "+")
}

// buildTrigger はホットキー h を組み立て、押下(down)/解放(up)を流すチャネルを返す。
// 単体修飾キー(rightcmd 等)や修飾キー 2 つのコードなら CGEventTap、それ以外は
// golang.design/x/hotkey を使う。押下だけ欲しい入力バーは watchDown から呼ぶ。
func buildTrigger(h config.Hotkey) (down, up <-chan struct{}, stop func(), err error) {
	d := make(chan struct{}, 8)
	u := make(chan struct{}, 8)
	keyName := strings.ToLower(h.Key)

	// send は詰まっていたら捨てる(消費されないチャネルで relay が止まらないように)。
	// 例: VAD では up(離す)を誰も読まないので、ブロッキング送信だと relay が固まる。
	send := func(ch chan struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	// 単体修飾キー、または修飾キー 2 つのコード(例 mods:["rightshift"], key:"rightcmd")。
	if modkey.Is(keyName) && (len(h.Mods) == 0 ||
		(len(h.Mods) == 1 && modkey.Is(strings.ToLower(h.Mods[0])))) {
		var events <-chan bool
		if len(h.Mods) == 1 {
			events, err = modkey.WatchChord(keyName, strings.ToLower(h.Mods[0]))
		} else {
			events, err = modkey.Watch(keyName)
		}
		if err != nil {
			return nil, nil, nil, err
		}
		go func() {
			for pressed := range events {
				if pressed {
					send(d)
				} else {
					send(u)
				}
			}
		}()
		return d, u, func() {}, nil
	}

	mods, key, err := parseHotkey(h)
	if err != nil {
		return nil, nil, nil, err
	}
	hk := hotkey.New(mods, key)
	if err := hk.Register(); err != nil {
		return nil, nil, nil, fmt.Errorf("ホットキー登録に失敗(他アプリと競合?): %w", err)
	}
	go func() {
		for range hk.Keydown() {
			send(d)
		}
	}()
	go func() {
		for range hk.Keyup() {
			send(u)
		}
	}()
	return d, u, func() { hk.Unregister() }, nil
}

// playOn / playOff は音声リッスンの開始/停止の効果音を鳴らす(設定で無効なら何もしない)。
func playOn(cfg *config.Config) {
	if cfg.Sound.Enabled {
		playSound(cfg.Sound.On)
	}
}

func playOff(cfg *config.Config) {
	if cfg.Sound.Enabled {
		playSound(cfg.Sound.Off)
	}
}

// playSound は /System/Library/Sounds/<name>.aiff を非同期再生する。
func playSound(name string) {
	if name == "" {
		return
	}
	path := "/System/Library/Sounds/" + name + ".aiff"
	if _, err := os.Stat(path); err != nil {
		return
	}
	cmd := exec.Command("afplay", path)
	if err := cmd.Start(); err != nil {
		return
	}
	// 終了を待たないと afplay がゾンビプロセスとして溜まり続ける(常駐で発話のたびに発生)。
	// 再生完了は待たない(待つのは OS によるプロセス回収のためだけ)ので別 goroutine に逃がす。
	go func() { _ = cmd.Wait() }()
}

// flashVoiceBar は「押したのに始まらなかった」場面の通知をバーで一瞬出す。
func flashVoiceBar(cfg *config.Config, label string) {
	if cfg.Voice.Bar {
		voicebar.Flash(label)
	}
}

// pttLoop は push-to-talk:押している間だけ録音し、離したら 1 回流す。
func pttLoop(rec *recorder.Recorder, wh transcribe.Whisper, out output, cfg *config.Config, down, up <-chan struct{}) {
	for {
		<-down
		inputbar.Dismiss() // 文字入力バーが開いていたら閉じる(排他。フラッシュとも重ねない)
		if !voice.Allowed() {
			log.Println("🔈 スピーカー出力中のため音声入力は無効です(イヤホンにするか voice.input を \"on\" に)。")
			flashVoiceBar(cfg, "音声オフ(スピーカー出力中)")
			<-up // この押下の解放を消費する(残すと次の正常な録音を即終了させてしまう)
			continue
		}
		voice.DrainRevoked() // 録音していない間に来た revoke は捨てる
		if err := rec.Start(); err != nil {
			log.Printf("録音開始失敗: %v", err)
			<-up // 同上
			continue
		}
		log.Println("● 録音中...")
		tray.setBase(iconRec, "● 録音中…", "録音中…")
		playOn(cfg)

		select {
		case <-up:
		case <-voice.Revoked():
			// 録音中に出力がスピーカーへ変わった → 破棄して停止(vad と同じ安全側の挙動。
			// イヤホンを外した後も相手の声を拾い続けない)。解放(up)も待って捨てる
			// (残すと次の正常な録音を即終了させてしまう)。
			rec.Stop()
			tray.setBase(iconIdle, "待機中…", "")
			playOff(cfg)
			log.Println("🔈 出力がスピーカーに変わったため録音を中止しました。")
			flashVoiceBar(cfg, "音声オフ(スピーカー出力中)")
			<-up
			continue
		}
		pcm, durMs := rec.Stop()
		tray.setBase(iconIdle, "待機中…", "")
		playOff(cfg)
		log.Printf("録音完了 (%d ms)", durMs)

		if durMs < cfg.Voice.MinDurationMs {
			log.Printf("短すぎるのでスキップ (<%d ms)", cfg.Voice.MinDurationMs)
			continue
		}
		// 文字起こし〜出力は数秒かかるので、次の発話を妨げないよう別 goroutine で処理する。
		go handle(wh, out, cfg, pcm)
	}
}

// vadLoop はキーでリッスンをトグルし、その間ストリームを無音で発話単位に区切って流す。
func vadLoop(rec *recorder.Recorder, wh transcribe.Whisper, out output, cfg *config.Config, down <-chan struct{}) {
	debug := os.Getenv("GAYATALK_DEBUG") != ""
	seg := vad.New(vad.Config{
		SampleRate:   recorder.SampleRate,
		Threshold:    cfg.Voice.VAD.Threshold,
		MinSpeechMs:  cfg.Voice.VAD.MinSpeechMs,
		SilenceMs:    cfg.Voice.VAD.SilenceMs,
		MaxSegmentMs: cfg.Voice.VAD.MaxSegmentMs,
		PrerollMs:    cfg.Voice.VAD.PrerollMs,
		Debug:        debug,
	}, func(pcm []byte, durMs int) {
		// オーディオスレッドから呼ばれるので、出力は別 goroutine に逃がす。
		log.Printf("…発話を検出 (%d ms)", durMs)
		go handle(wh, out, cfg, pcm)
	})

	// 発話の検出/終了をアイコンと画面上のバーに反映する(リッスン中のみ意味を持つ)。
	seg.OnActivity = func(active bool) {
		if active {
			tray.setBase(iconRec, "● 音声検出中…", "音声を検出中…")
		} else {
			tray.setBase(iconListen, "聞き取り中(無音待ち)…", "聞き取り中…")
		}
	}

	// リッスン中は音量を画面上のバーの波形へ流す(オーディオスレッドから呼ばれる)。
	feed := seg.Feed
	if cfg.Voice.Bar {
		feed = func(pcm []byte) {
			voicebar.Level(vad.RMS(pcm))
			seg.Feed(pcm)
		}
	}

	listening := false
	stop := func() {
		rec.StopStream() // コールバックを止めてから Flush する(同時アクセスを避ける)
		seg.Flush()
		listening = false
		tray.setBase(iconIdle, "待機中…", "")
		playOff(cfg)
	}
	for {
		if !listening {
			voice.DrainRevoked() // リッスンしていない間に来た revoke は捨てる
			<-down
			inputbar.Dismiss() // 文字入力バーが開いていたら閉じる(排他。フラッシュとも重ねない)
			if !voice.Allowed() {
				log.Println("🔈 スピーカー出力中のため音声入力は無効です(イヤホンにするか voice.input を \"on\" に)。")
				flashVoiceBar(cfg, "音声オフ(スピーカー出力中)")
				continue
			}
			if err := rec.StartStream(feed); err != nil {
				log.Printf("リッスン開始失敗: %v", err)
				continue
			}
			listening = true
			select { // リッスンしていない間に開かれた通知は捨てる
			case <-inputBarShown:
			default:
			}
			log.Println("🎙 リッスン開始(話すと自動で区切って流す。もう一度キーで停止)")
			tray.setBase(iconListen, "聞き取り中(無音待ち)…", "聞き取り中…")
			playOn(cfg)
			continue
		}
		select {
		case <-down: // 手動でトグル停止
			log.Println("■ リッスン停止")
			stop()
		case <-inputBarShown: // 文字入力バーが開いた → 排他で停止
			log.Println("⌨️ 文字入力バーを開いたため音声リッスンを停止しました。")
			stop()
		case <-voice.Revoked(): // リッスン中に出力がスピーカーへ変わった → 自動停止
			log.Println("🔈 出力がスピーカーに変わったため音声入力を停止しました。")
			stop()
			flashVoiceBar(cfg, "音声オフ(スピーカー出力中)")
		}
	}
}

// logBodyEnabled は発話本文をログに出してよいか(GAYATALK_DEBUG が設定されているか)。
// 既定では発話内容(会議・機微な発言になりうる)を永続ログ ~/Library/Logs/gaya-talk.log に
// 平文で残さない。デバッグ時のみ本文を出す。
func logBodyEnabled() bool {
	return os.Getenv("GAYATALK_DEBUG") != ""
}

// bodyForLog は本文をログ用に整形する。デバッグ時は本文そのもの、通常時は
// 内容を伏せて文字数のみ(例: "(12文字)")を返す。
func bodyForLog(s string) string {
	if logBodyEnabled() {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("(%d文字)", len([]rune(s)))
}

// transcribeSem は whisper-cli の同時実行数を絞る(遅いマシンで VAD がセグメントを
// 連発すると、プロセスが積み上がってさらに遅くなる悪循環を防ぐ)。待っている発話は
// 順番に処理される(録音・VAD 側は止めないので取りこぼしはない)。
var transcribeSem = make(chan struct{}, 2)

// handle は 1 回の発話(PCM)を正規化・文字起こしして送り先へ渡す。
// out.send が nil のときは送らず、文字起こし結果を表示するだけ(ドライラン)。
func handle(wh transcribe.Whisper, out output, cfg *config.Config, pcm []byte) {
	// 文字起こし中はアイコンを「💬」にし、終わったら基本状態へ戻す
	// (待ちの間も件数バッジに載せて、詰まっていることが見えるようにする)。
	tray.beginTranscribe()
	defer tray.endTranscribe()
	transcribeSem <- struct{}{}
	defer func() { <-transcribeSem }()

	// 小声・ボソボソ対策: Whisper に渡す前に音量を持ち上げる。
	if cfg.Voice.Gain.Enabled {
		pcm = recorder.NormalizePCM(pcm, cfg.Voice.Gain.TargetPeak, cfg.Voice.Gain.MaxGain)
	}
	text, err := wh.Transcribe(recorder.WAVFromPCM(pcm))
	if err != nil {
		log.Printf("文字起こし失敗: %v", err)
		return
	}
	if strings.TrimSpace(text) == "" {
		log.Println("文字起こし結果が空だったのでスキップ")
		return
	}

	// ローカル LLM で整形(無効/失敗時は元テキストのまま)。何が起きたかをログに出す。
	if cfg.Enhance.Enabled {
		ectx, ecancel := context.WithTimeout(context.Background(), 30*time.Second)
		enhanced, eerr := enhancer.Load().Enhance(ectx, text)
		ecancel()
		switch {
		case eerr != nil:
			log.Printf("整形スキップ(生テキスト使用): %v", eerr)
		case enhanced != text:
			log.Printf("整形 ✏️ %s → %s", bodyForLog(text), bodyForLog(enhanced))
		default:
			log.Println("整形: 変化なし(そのまま)")
		}
		text = enhanced
		if strings.TrimSpace(text) == "" {
			return
		}
	}

	// 絵文字モード: 本文は変えず、内容に合う絵文字を末尾に付ける(off なら何もしない)。
	if cfg.Emoji.Mode != "" && cfg.Emoji.Mode != "off" {
		emctx, emcancel := context.WithTimeout(context.Background(), 15*time.Second)
		em, eerr := enhancer.Load().Emoji(emctx, text)
		emcancel()
		switch {
		case eerr != nil:
			log.Printf("絵文字付与スキップ: %v", eerr)
		case em != "":
			log.Printf("絵文字 %s を付与", em)
			text = enhance.AppendEmoji(text, em)
		}
	}

	if out.send == nil {
		// ドライランは「送らず結果を目視確認する」のが目的なので本文を出す
		// (端末での動作確認用モード。通常運用の常時ログとは別)。
		log.Printf("(ドライラン)文字起こし結果: %s", text)
		return
	}

	if err := out.send(text); err != nil {
		log.Printf("送信失敗(%s): %v", out.name, err)
		return
	}
	// 発話本文は既定でログに残さない(GAYATALK_DEBUG のときだけ本文を出す)。
	log.Printf("→ %s: %s", out.name, bodyForLog(text))
}

// parseHotkey は設定の文字列を hotkey ライブラリの型に変換する。
func parseHotkey(h config.Hotkey) ([]hotkey.Modifier, hotkey.Key, error) {
	var mods []hotkey.Modifier
	for _, m := range h.Mods {
		switch strings.ToLower(m) {
		case "ctrl", "control":
			mods = append(mods, hotkey.ModCtrl)
		case "shift":
			mods = append(mods, hotkey.ModShift)
		case "option", "opt", "alt":
			mods = append(mods, hotkey.ModOption)
		case "cmd", "command", "meta", "super":
			mods = append(mods, hotkey.ModCmd)
		default:
			return nil, 0, fmt.Errorf("未知の修飾キー: %q", m)
		}
	}
	key, ok := keyMap[strings.ToLower(h.Key)]
	if !ok {
		return nil, 0, fmt.Errorf("未知のキー: %q(対応キーは `gaya-talk keys` で確認)", h.Key)
	}
	return mods, key, nil
}

// keyMap は設定 hotkey.key で使えるメインキー名。
var keyMap = map[string]hotkey.Key{
	"space":     hotkey.KeySpace,
	"return":    hotkey.KeyReturn,
	"enter":     hotkey.KeyReturn,
	"tab":       hotkey.KeyTab,
	"escape":    hotkey.KeyEscape,
	"esc":       hotkey.KeyEscape,
	"delete":    hotkey.KeyDelete,
	"del":       hotkey.KeyDelete,
	"backspace": hotkey.KeyDelete,
	"up":        hotkey.KeyUp,
	"down":      hotkey.KeyDown,
	"left":      hotkey.KeyLeft,
	"right":     hotkey.KeyRight,
	"0":         hotkey.Key0, "1": hotkey.Key1, "2": hotkey.Key2, "3": hotkey.Key3,
	"4": hotkey.Key4, "5": hotkey.Key5, "6": hotkey.Key6, "7": hotkey.Key7,
	"8": hotkey.Key8, "9": hotkey.Key9,
	"a": hotkey.KeyA, "b": hotkey.KeyB, "c": hotkey.KeyC, "d": hotkey.KeyD,
	"e": hotkey.KeyE, "f": hotkey.KeyF, "g": hotkey.KeyG, "h": hotkey.KeyH,
	"i": hotkey.KeyI, "j": hotkey.KeyJ, "k": hotkey.KeyK, "l": hotkey.KeyL,
	"m": hotkey.KeyM, "n": hotkey.KeyN, "o": hotkey.KeyO, "p": hotkey.KeyP,
	"q": hotkey.KeyQ, "r": hotkey.KeyR, "s": hotkey.KeyS, "t": hotkey.KeyT,
	"u": hotkey.KeyU, "v": hotkey.KeyV, "w": hotkey.KeyW, "x": hotkey.KeyX,
	"y": hotkey.KeyY, "z": hotkey.KeyZ,
	"f1": hotkey.KeyF1, "f2": hotkey.KeyF2, "f3": hotkey.KeyF3, "f4": hotkey.KeyF4,
	"f5": hotkey.KeyF5, "f6": hotkey.KeyF6, "f7": hotkey.KeyF7, "f8": hotkey.KeyF8,
	"f9": hotkey.KeyF9, "f10": hotkey.KeyF10, "f11": hotkey.KeyF11, "f12": hotkey.KeyF12,
	"f13": hotkey.KeyF13, "f14": hotkey.KeyF14, "f15": hotkey.KeyF15, "f16": hotkey.KeyF16,
	"f17": hotkey.KeyF17, "f18": hotkey.KeyF18, "f19": hotkey.KeyF19, "f20": hotkey.KeyF20,
}
