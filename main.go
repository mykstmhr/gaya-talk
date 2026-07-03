// ura-talk: グローバルなホットキーでマイクを録音し、ローカルの whisper-cli で
// 文字起こしして、ニコニコ動画風の「流れるコメント」を画面全体のオーバーレイに出す
// 常駐ツール。同じルームに参加したメンバー間でコメントを共有できる(中継サーバ経由)。
//
// 発話・文字起こし・整形はすべてローカル完結。ルーム共有時も本文は E2E 暗号化され、
// 中継サーバには暗号文しか渡らない(internal/room 参照)。
//
// 使い方:
//
//	ura-talk              常駐を開始する(メニューバーに常駐)
//	ura-talk dryrun       送信せず、文字起こし結果をログに出すだけ(動作確認用)
//	ura-talk devices      利用可能な入力デバイス(マイク)の一覧を表示する
//	ura-talk keys         ホットキーに指定できるキー名の一覧を表示する
//	ura-talk overlay-demo ルームを使わずオーバーレイの見た目だけ確認する
package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mykstmhr/ura-talk/internal/config"
	"github.com/mykstmhr/ura-talk/internal/enhance"
	"github.com/mykstmhr/ura-talk/internal/modkey"
	"github.com/mykstmhr/ura-talk/internal/overlay"
	"github.com/mykstmhr/ura-talk/internal/recorder"
	"github.com/mykstmhr/ura-talk/internal/transcribe"
	"github.com/mykstmhr/ura-talk/internal/trayicon"
	"github.com/mykstmhr/ura-talk/internal/vad"

	"fyne.io/systray"
	"golang.design/x/hotkey"
)

// setupLogging は、.app バンドルから起動された場合にログを ~/Library/Logs/ura-talk.log へ出す。
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
	logPath := filepath.Join(dir, "ura-talk.log")
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
	default:
		fmt.Fprintf(os.Stderr, "不明なサブコマンド: %s\n使い方: ura-talk [run|dryrun|devices|keys|overlay-demo]\n", cmd)
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
	fmt.Println("\nconfig.json の input_device に上記の名前(部分一致でOK)を設定してください。")
	fmt.Println("例: Bluetooth イヤホンの再生音を切らさないために内蔵マイクを指定する。")
}

// runKeys は config の hotkey で指定できる修飾キー/メインキーの一覧を表示する。
func runKeys() {
	keys := make([]string, 0, len(keyMap))
	for k := range keyMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("ホットキーは config の \"hotkey\" / \"room.input_hotkey\" で変更できます:")
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
		systray.SetTooltip("ura-talk overlay demo")
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

// buildSink は送り先を決める。オーバーレイ(ルーム共有)固定。dryRun は送らず表示のみ。
func buildSink(cfg *config.Config, dryRun bool) output {
	if cfg.WhisperModel == "" {
		log.Fatalf("設定エラー: whisper_model が未設定です(ggml モデルのパス)")
	}
	if dryRun {
		return output{name: "ドライラン(送信しません)"} // send は nil
	}
	log.Println("ℹ️ 発話・入力はニコニコ風オーバーレイに流れます。メニューバーからルームを作成/参加できます。")
	if cfg.Room.Server == "" {
		log.Println("   room.server が未設定なのでソロモード(自分の画面のみ)で動きます。")
	}
	return output{
		name: "オーバーレイ",
		send: func(text string) error { return sendRoomComment(cfg, text) },
	}
}

// lockFile は多重起動防止のロックを保持する(プロセス終了まで開いたままにする)。
var lockFile *os.File

// mStatus は状態(待機/聞き取り/録音…)。その下に動作情報(方式/キー)を常時表示する。
var (
	mStatus   *systray.MenuItem
	mInfoMode *systray.MenuItem
	mInfoKey  *systray.MenuItem
)

// enhancer は文字起こし結果のローカル LLM 整形(無効なら素通し)。serve で初期化。
var enhancer *enhance.Enhancer

// startTray はメニューバー常駐(systray)を起動する。systray が NSApplication の
// メインループを回し、その中でホットキー登録・録音・文字起こしを動かす。
func startTray(dryRun bool) {
	if !acquireSingleInstance() {
		log.Println("ura-talk は既に起動しています(多重起動を防止しました)。")
		fmt.Fprintln(os.Stderr, "ura-talk は既に起動しています。")
		return
	}
	systray.Run(onReady(dryRun), func() { os.Exit(0) })
}

// onReady はメニューバーアイコンとメニューを用意し、本体処理を別 goroutine で開始する。
func onReady(dryRun bool) func() {
	return func() {
		systray.SetTemplateIcon(trayicon.Idle, trayicon.Idle)
		systray.SetTooltip("ura-talk")

		mStatus = systray.AddMenuItem("起動中…", "現在の状態")
		mStatus.Disable()
		// 動作情報(方式/キー)は状態の下に常時表示。内容は serve で確定して Show する。
		mInfoMode = newInfoItem()
		mInfoKey = newInfoItem()

		systray.AddSeparator()

		// ルーム操作メニュー(serve から Show する)。
		addRoomMenuItems()

		systray.AddSeparator()
		mQuit := systray.AddMenuItem("終了", "ura-talk を終了する")
		go func() {
			<-mQuit.ClickedCh
			systray.Quit()
		}()

		go serve(dryRun)
	}
}

// iconState はメニューバーに出す状態アイコンの種別。
type iconState int

const (
	iconIdle       iconState = iota // 待機(一時停止)
	iconListen                      // リッスン中(マイク)
	iconRec                         // 録音/音声検出中(赤・点滅)
	iconTranscribe                  // 文字起こし中(吹き出し)
)

// truncRunes は表示が長くなりすぎないよう name を max 文字に丸める(超過分は …)。
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// newInfoItem は状態下の動作情報行(方式/キー)を 1 つ作る。無効・初期は非表示。
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

// setState は状態アイコンとドロップダウンの状態テキストを更新する。
func setState(state iconState, text string) {
	applyIcon(state)
	if mStatus != nil {
		mStatus.SetTitle(text)
	}
}

// applyIcon は状態に応じてメニューバーアイコンを差し替える。録音中だけ赤の点滅にする。
func applyIcon(state iconState) {
	if state == iconRec {
		startBlink()
		return
	}
	stopBlink()
	switch state {
	case iconListen:
		// リッスン中は黄色(カラー=非テンプレート)で目立たせる。待機時は Idle の黒テンプレートに戻る。
		systray.SetIcon(trayicon.ListenOn)
	case iconTranscribe:
		systray.SetTemplateIcon(trayicon.Transcribe, trayicon.Transcribe)
	default: // iconIdle
		systray.SetTemplateIcon(trayicon.Idle, trayicon.Idle)
	}
}

// blink は録音中の赤丸点滅(● ⇄ ○)を制御する goroutine のスイッチ。
var (
	blinkMu   sync.Mutex
	blinkStop chan struct{}
)

// startBlink は録音中の点滅を開始する(多重起動しない)。rec はカラーなので SetIcon を使う。
func startBlink() {
	blinkMu.Lock()
	defer blinkMu.Unlock()
	if blinkStop != nil {
		return
	}
	stop := make(chan struct{})
	blinkStop = stop
	systray.SetIcon(trayicon.Rec) // まず点灯
	go func() {
		on := true
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				on = !on
				if on {
					systray.SetIcon(trayicon.Rec)
				} else {
					systray.SetIcon(trayicon.RecRing)
				}
			}
		}
	}()
}

// stopBlink は点滅を止める。
func stopBlink() {
	blinkMu.Lock()
	defer blinkMu.Unlock()
	if blinkStop != nil {
		close(blinkStop)
		blinkStop = nil
	}
}

// tray は「基本状態(待機/リッスン/録音)」と、それに重ねる「文字起こし中」表示を管理する。
// 文字起こしは別 goroutine で並行しうるので件数で数え、0 になったら基本状態へ戻す。
var tray = &trayStatus{baseIcon: iconIdle, baseText: "待機中…"}

type trayStatus struct {
	mu           sync.Mutex
	baseIcon     iconState
	baseText     string
	transcribing int
}

// setBase は基本状態を更新する。文字起こし中(💬表示中)は表示を上書きしない。
func (t *trayStatus) setBase(state iconState, text string) {
	t.mu.Lock()
	t.baseIcon = state
	t.baseText = text
	show := t.transcribing == 0
	t.mu.Unlock()
	if show {
		setState(state, text)
	}
}

// beginTranscribe は「文字起こし中」を表示する。
func (t *trayStatus) beginTranscribe() {
	t.mu.Lock()
	t.transcribing++
	t.mu.Unlock()
	setState(iconTranscribe, "文字起こし中…")
}

// endTranscribe は文字起こし完了を反映し、全て終わったら基本状態へ戻す。
func (t *trayStatus) endTranscribe() {
	t.mu.Lock()
	if t.transcribing > 0 {
		t.transcribing--
	}
	done := t.transcribing == 0
	s, txt := t.baseIcon, t.baseText
	t.mu.Unlock()
	if done {
		setState(s, txt)
	}
}

// acquireSingleInstance はファイルロックで多重起動を防ぐ。取得できれば true。
func acquireSingleInstance() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return true // 判定できないときは通す
	}
	dir := filepath.Join(home, "Library", "Application Support", "ura-talk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return true
	}
	f, err := os.OpenFile(filepath.Join(dir, "ura-talk.lock"), os.O_CREATE|os.O_RDWR, 0o644)
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
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("設定エラー: %v", err)
	}

	// オーバーレイ・入力バー・ルームメニューを配線する(dryRun では画面に出さない)。
	if !dryRun {
		setupRoom(cfg)
	}

	// 送り先(sink)を決める。dryRun は送らず表示のみ(out.send == nil)。
	out := buildSink(cfg, dryRun)

	// 文字起こし整形/絵文字付与(ローカル LLM)。無効でも New は安全。
	enhancer = enhance.New(enhance.Config{
		Enabled:     cfg.Enhance.Enabled,
		Endpoint:    cfg.Enhance.Endpoint,
		Model:       cfg.Enhance.Model,
		Prompt:      cfg.Enhance.Prompt,
		EmojiMode:   cfg.Emoji.Mode,
		AllowRemote: cfg.Enhance.AllowRemote,
	})
	// 整形か絵文字のどちらかが有効なら Ollama を用意する。
	llmNeeded := cfg.Enhance.Enabled || (cfg.Emoji.Mode != "" && cfg.Emoji.Mode != "off")
	if llmNeeded {
		// Ollama が起動していなければ自動起動する(出力は破棄)。
		ictx, icancel := context.WithTimeout(context.Background(), 20*time.Second)
		if started, err := enhancer.EnsureServer(ictx); err != nil {
			log.Printf("⚠️ Ollama を起動できませんでした: %v", err)
		} else if started {
			log.Println("Ollama を自動起動しました")
		}
		icancel()

		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := enhancer.Check(cctx); err != nil {
			log.Printf("⚠️ Ollama が使えません: %v → 整形/絵文字はスキップします", err)
		} else {
			log.Printf("✅ Ollama 有効: model=%s endpoint=%s(整形=%v / 絵文字=%s)",
				cfg.Enhance.Model, cfg.Enhance.Endpoint, cfg.Enhance.Enabled, cfg.Emoji.Mode)
			// モデルを先読みして初回のコールドスタート待ちを無くす(バックグラウンド)。
			go func() {
				wctx, wcancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer wcancel()
				if err := enhancer.Warmup(wctx); err != nil {
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

	rec, err := recorder.New(cfg.InputDevice)
	if err != nil {
		log.Fatalf("録音初期化エラー: %v", err)
	}
	defer rec.Close()

	wh := transcribe.Whisper{
		Bin:           cfg.WhisperBin,
		Model:         cfg.WhisperModel,
		Lang:          cfg.Language,
		BeamSize:      cfg.WhisperBeamSize,
		Prompt:        cfg.WhisperPrompt,
		NoSpeechThold: cfg.WhisperNoSpeechThold,
	}

	down, up, stopTrigger, err := buildTrigger(cfg)
	if err != nil {
		log.Fatalf("ホットキー設定エラー: %v", err)
	}
	defer stopTrigger()

	// 状態の下に動作情報を常時表示(方式/キー)。全角キーで幅を揃える。
	setInfo(mInfoKey, "キー : "+cfg.Hotkey.String())

	if cfg.ListenMode == "vad" {
		log.Printf("ura-talk 起動 [%s / VAD]。[%s] で聞き取り開始/停止。話すと無音で自動区切りして流します。", out.name, cfg.Hotkey)
		setInfo(mInfoMode, "方式 : VAD(自動区切り)")
		tray.setBase(iconIdle, "待機中…")
		vadLoop(rec, wh, out, cfg, down)
		return
	}

	log.Printf("ura-talk 起動 [%s / PTT]。[%s] を押している間だけ録音します。", out.name, cfg.Hotkey)
	setInfo(mInfoMode, "方式 : PTT(押している間だけ)")
	tray.setBase(iconIdle, "待機中…")
	pttLoop(rec, wh, out, cfg, down, up)
}

// buildTrigger はホットキーを設定から組み立て、押下(down)/解放(up)を流すチャネルを返す。
// 単体修飾キー(rightcmd 等)や修飾キー 2 つのコードなら CGEventTap、それ以外は
// golang.design/x/hotkey を使う。
func buildTrigger(cfg *config.Config) (down, up <-chan struct{}, stop func(), err error) {
	d := make(chan struct{}, 8)
	u := make(chan struct{}, 8)
	keyName := strings.ToLower(cfg.Hotkey.Key)

	// send は詰まっていたら捨てる(消費されないチャネルで relay が止まらないように)。
	// 例: VAD では up(離す)を誰も読まないので、ブロッキング送信だと relay が固まる。
	send := func(ch chan struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	// 単体修飾キー、または修飾キー 2 つのコード(例 mods:["rightshift"], key:"rightcmd")。
	if modkey.Is(keyName) && (len(cfg.Hotkey.Mods) == 0 ||
		(len(cfg.Hotkey.Mods) == 1 && modkey.Is(strings.ToLower(cfg.Hotkey.Mods[0])))) {
		var events <-chan bool
		var err error
		if len(cfg.Hotkey.Mods) == 1 {
			events, err = modkey.WatchChord(keyName, strings.ToLower(cfg.Hotkey.Mods[0]))
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

	mods, key, err := parseHotkey(cfg.Hotkey)
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

// playOn / playOff は有効化/無効化の効果音を鳴らす(設定で無効なら何もしない)。
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

// pttLoop は push-to-talk:押している間だけ録音し、離したら 1 回流す。
func pttLoop(rec *recorder.Recorder, wh transcribe.Whisper, out output, cfg *config.Config, down, up <-chan struct{}) {
	for {
		<-down
		if err := rec.Start(); err != nil {
			log.Printf("録音開始失敗: %v", err)
			continue
		}
		log.Println("● 録音中...")
		tray.setBase(iconRec, "● 録音中…")
		playOn(cfg)

		<-up
		pcm, durMs, err := rec.Stop()
		tray.setBase(iconIdle, "待機中…")
		playOff(cfg)
		if err != nil {
			log.Printf("録音停止失敗: %v", err)
			continue
		}
		log.Printf("録音完了 (%d ms)", durMs)

		if durMs < cfg.MinDurationMs {
			log.Printf("短すぎるのでスキップ (<%d ms)", cfg.MinDurationMs)
			continue
		}
		// 文字起こし〜出力は数秒かかるので、次の発話を妨げないよう別 goroutine で処理する。
		go handle(wh, out, cfg, pcm)
	}
}

// vadLoop はキーでリッスンをトグルし、その間ストリームを無音で発話単位に区切って流す。
func vadLoop(rec *recorder.Recorder, wh transcribe.Whisper, out output, cfg *config.Config, down <-chan struct{}) {
	debug := os.Getenv("URATALK_DEBUG") != ""
	seg := vad.New(vad.Config{
		SampleRate:   recorder.SampleRate,
		Threshold:    cfg.VAD.Threshold,
		MinSpeechMs:  cfg.VAD.MinSpeechMs,
		SilenceMs:    cfg.VAD.SilenceMs,
		MaxSegmentMs: cfg.VAD.MaxSegmentMs,
		PrerollMs:    cfg.VAD.PrerollMs,
		Debug:        debug,
	}, func(pcm []byte, durMs int) {
		// オーディオスレッドから呼ばれるので、出力は別 goroutine に逃がす。
		log.Printf("…発話を検出 (%d ms)", durMs)
		go handle(wh, out, cfg, pcm)
	})

	// 発話の検出/終了をアイコンに反映する(リッスン中のみ意味を持つ)。
	seg.OnActivity = func(active bool) {
		if active {
			tray.setBase(iconRec, "● 音声検出中…")
		} else {
			tray.setBase(iconListen, "聞き取り中(無音待ち)…")
		}
	}

	listening := false
	for {
		if !listening {
			<-down
			if err := rec.StartStream(seg.Feed); err != nil {
				log.Printf("リッスン開始失敗: %v", err)
				continue
			}
			listening = true
			log.Println("🎙 リッスン開始(話すと自動で区切って流す。もう一度キーで停止)")
			tray.setBase(iconListen, "聞き取り中(無音待ち)…")
			playOn(cfg)
			continue
		}
		<-down // 手動でトグル停止
		log.Println("■ リッスン停止")
		rec.StopStream() // コールバックを止めてから Flush する(同時アクセスを避ける)
		seg.Flush()
		listening = false
		tray.setBase(iconIdle, "待機中…")
		playOff(cfg)
	}
}

// logBodyEnabled は発話本文をログに出してよいか(URATALK_DEBUG が設定されているか)。
// 既定では発話内容(会議・機微な発言になりうる)を永続ログ ~/Library/Logs/ura-talk.log に
// 平文で残さない。デバッグ時のみ本文を出す。
func logBodyEnabled() bool {
	return os.Getenv("URATALK_DEBUG") != ""
}

// bodyForLog は本文をログ用に整形する。デバッグ時は本文そのもの、通常時は
// 内容を伏せて文字数のみ(例: "(12文字)")を返す。
func bodyForLog(s string) string {
	if logBodyEnabled() {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("(%d文字)", len([]rune(s)))
}

// handle は 1 回の発話(PCM)を正規化・文字起こしして送り先へ渡す。
// out.send が nil のときは送らず、文字起こし結果を表示するだけ(ドライラン)。
func handle(wh transcribe.Whisper, out output, cfg *config.Config, pcm []byte) {
	// 文字起こし中はアイコンを「💬」にし、終わったら基本状態へ戻す。
	tray.beginTranscribe()
	defer tray.endTranscribe()

	// 小声・ボソボソ対策: Whisper に渡す前に音量を持ち上げる。
	if cfg.Gain.Enabled {
		pcm = recorder.NormalizePCM(pcm, cfg.Gain.TargetPeak, cfg.Gain.MaxGain)
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
		enhanced, eerr := enhancer.Enhance(ectx, text)
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
		em, eerr := enhancer.Emoji(emctx, text)
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
	// 発話本文は既定でログに残さない(URATALK_DEBUG のときだけ本文を出す)。
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
		return nil, 0, fmt.Errorf("未知のキー: %q(対応キーは `ura-talk keys` で確認)", h.Key)
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
