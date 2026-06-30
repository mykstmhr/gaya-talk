// ura-talk: グローバルなホットキーを押している間だけマイクを録音し、
// 離すとローカルの whisper-cli で文字起こしして Slack に「自分名義」で投稿する常駐ツール。
//
// 投稿には OAuth で取得した user token(xoxp)を使う。初回は `ura-talk login`
// で認可してトークンを Keychain に保存する。
//
// 使い方:
//
//	ura-talk login     OAuth で認可し user token を取得・保存する
//	ura-talk logout    保存済みトークンを削除する
//	ura-talk           push-to-talk 常駐を開始する
//	ura-talk dryrun    Slack に投稿せず、文字起こし結果をコンソールに出すだけ(動作確認用)
//	ura-talk devices   利用可能な入力デバイス(マイク)の一覧を表示する
package main

import (
	"context"
	"fmt"
	"log"
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
	"github.com/mykstmhr/ura-talk/internal/keystroke"
	"github.com/mykstmhr/ura-talk/internal/modkey"
	"github.com/mykstmhr/ura-talk/internal/oauth"
	"github.com/mykstmhr/ura-talk/internal/recorder"
	"github.com/mykstmhr/ura-talk/internal/slack"
	"github.com/mykstmhr/ura-talk/internal/tokenstore"
	"github.com/mykstmhr/ura-talk/internal/transcribe"
	"github.com/mykstmhr/ura-talk/internal/vad"

	"fyne.io/systray"
	"golang.design/x/hotkey"
)

// userScopes は投稿に必要な user scope。
var userScopes = []string{"chat:write"}

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
	f, err := os.OpenFile(filepath.Join(dir, "ura-talk.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(f)
}

func main() {
	setupLogging()

	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "login":
		runLogin()
	case "logout":
		runLogout()
	case "", "run":
		startTray(false)
	case "dryrun":
		startTray(true)
	case "devices":
		runDevices()
	case "keys":
		runKeys()
	default:
		fmt.Fprintf(os.Stderr, "不明なサブコマンド: %s\n使い方: ura-talk [login|logout|run|dryrun|devices|keys]\n", cmd)
		os.Exit(2)
	}
}

// runLogin は OAuth フローを実行し、user token を Keychain に保存する。
func runLogin() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("設定エラー: %v", err)
	}
	if err := cfg.ValidateForLogin(); err != nil {
		log.Fatalf("設定エラー: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := oauth.Login(ctx, cfg.SlackClientID, cfg.SlackClientSecret,
		cfg.RedirectURI(), userScopes, cfg.OAuthRedirectPort)
	if err != nil {
		log.Fatalf("OAuth 失敗: %v", err)
	}
	if err := tokenstore.Save(res.UserToken); err != nil {
		log.Fatalf("トークン保存失敗: %v", err)
	}
	fmt.Printf("✅ 認証完了: %s (user: %s)。トークンを Keychain に保存しました。\n", res.TeamName, res.UserID)
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
	fmt.Println("ホットキーは config の \"hotkey\" で変更できます:")
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
	fmt.Println()
	fmt.Println("変更後はアプリを再起動してください(.app はメニューバー→終了→再 open)。")
}

// runLogout は保存済みトークンを削除する。
func runLogout() {
	if err := tokenstore.Delete(); err != nil {
		log.Fatalf("ログアウト失敗: %v", err)
	}
	fmt.Println("✅ トークンを削除しました。")
}

// output は文字起こし結果の出力先。send が nil ならドライラン(表示のみ)。
type output struct {
	send   func(ctx context.Context, text string) error
	name   string
	prefix string // 本文の前に付ける文字列。keystroke では付けない(素のテキスト)。
}

// buildOutput は設定とドライランフラグから出力先を決める。設定不備は fatal で止める。
func buildOutput(cfg *config.Config, dryRun bool) output {
	// 前置詞(🗣 等)は Slack 投稿のときだけ付ける。keystroke は自分が打ったままにしたいので付けない。
	out := output{}
	if cfg.Output == "slack" || cfg.Output == "" {
		out.prefix = cfg.MessagePrefix
	}

	if dryRun {
		if cfg.WhisperModel == "" {
			log.Fatalf("設定エラー: whisper_model が未設定です(ggml モデルのパス)")
		}
		out.name = "dryrun"
		return out // 表示のみ(send は nil)
	}

	switch cfg.Output {
	case "keystroke":
		if cfg.WhisperModel == "" {
			log.Fatalf("設定エラー: whisper_model が未設定です(ggml モデルのパス)")
		}
		if !keystroke.Trusted() {
			log.Println("⚠️ アクセシビリティ権限がありません。許可ダイアログを表示します(システム設定が開きます)。")
			log.Println("   一覧で ura-talk をオンにしてから、メニューバーの「終了」→ 再起動してください。")
			keystroke.PromptAccessibility() // 一覧に正しい署名で自動登録し、システムの許可ダイアログを出す
		}
		log.Println("ℹ️ keystroke 出力: 貼り付けは「発話が終わった時点で最前面のアプリ」に入ります。")
		log.Println("   入力したいチャットの入力欄にフォーカスを当てた状態で喋ってください(ターミナルを前面にしない)。")
		out.name = "keystroke"
		out.send = func(_ context.Context, text string) error {
			return keystroke.Inject(text, cfg.Keystroke.AutoEnter)
		}
		return out
	case "slack", "":
		if err := cfg.ValidateForPost(); err != nil {
			log.Fatalf("設定エラー: %v", err)
		}
		token, err := tokenstore.Load()
		if err != nil {
			log.Fatalf("トークン読み込みエラー: %v", err)
		}
		if token == "" {
			log.Fatalf("user token がありません。先に `ura-talk login` で認可してください。")
		}
		sl := slack.New(token, cfg.SlackChannel)
		out.name = "slack"
		out.send = sl.Post
		return out
	default:
		log.Fatalf("設定エラー: output は \"slack\" か \"keystroke\" を指定してください(現在: %q)", cfg.Output)
		return out
	}
}

// lockFile は多重起動防止のロックを保持する(プロセス終了まで開いたままにする)。
var lockFile *os.File

// mStatus はメニューバーの状態表示メニュー項目。
var mStatus *systray.MenuItem

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
		systray.SetTitle("🎙")
		systray.SetTooltip("ura-talk")

		mStatus = systray.AddMenuItem("起動中…", "現在の状態")
		mStatus.Disable()
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("終了", "ura-talk を終了する")
		go func() {
			<-mQuit.ClickedCh
			systray.Quit()
		}()

		go serve(dryRun)
	}
}

// setState はメニューバーのアイコン(emoji)と状態テキストを更新する。
func setState(emoji, text string) {
	systray.SetTitle(emoji)
	if mStatus != nil {
		mStatus.SetTitle(text)
	}
}

// tray は「基本状態(待機/リッスン/音声検出中)」と、それに重ねる「文字起こし中」表示を管理する。
// 文字起こしは別 goroutine で並行しうるので件数で数え、0 になったら基本状態へ戻す。
var tray = &trayStatus{base: [2]string{"🎙", "待機中…"}}

type trayStatus struct {
	mu           sync.Mutex
	base         [2]string // 基本状態(emoji, text)
	transcribing int       // 進行中の文字起こし数
}

// setBase は基本状態を更新する。文字起こし中(💬表示中)は表示を上書きしない。
func (t *trayStatus) setBase(emoji, text string) {
	t.mu.Lock()
	t.base = [2]string{emoji, text}
	show := t.transcribing == 0
	t.mu.Unlock()
	if show {
		setState(emoji, text)
	}
}

// beginTranscribe は「💬 文字起こし中」を表示する。
func (t *trayStatus) beginTranscribe() {
	t.mu.Lock()
	t.transcribing++
	t.mu.Unlock()
	setState("💬", "文字起こし中…")
}

// endTranscribe は文字起こし完了を反映し、全て終わったら基本状態へ戻す。
func (t *trayStatus) endTranscribe() {
	t.mu.Lock()
	if t.transcribing > 0 {
		t.transcribing--
	}
	done := t.transcribing == 0
	emoji, text := t.base[0], t.base[1]
	t.mu.Unlock()
	if done {
		setState(emoji, text)
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

// serve は本体処理:設定読込→出力先決定→録音・ホットキー→常駐ループ。
// systray のメインループ上(別 goroutine)で動く。dryRun のときは出力せず表示のみ。
func serve(dryRun bool) {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("設定エラー: %v", err)
	}

	// 出力先(sink)を決める。dryRun は出力せず表示のみ(out.send == nil)。
	out := buildOutput(cfg, dryRun)

	// 文字起こし整形(ローカル LLM)。無効でも New は安全(Enhance がそのまま返す)。
	enhancer = enhance.New(enhance.Config{
		Enabled:  cfg.Enhance.Enabled,
		Endpoint: cfg.Enhance.Endpoint,
		Model:    cfg.Enhance.Model,
		Prompt:   cfg.Enhance.Prompt,
	})
	if cfg.Enhance.Enabled {
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
			log.Printf("⚠️ 整形(Ollama)が使えません: %v → 生テキストのまま出力します", err)
		} else {
			log.Printf("✅ 整形(Ollama)有効: model=%s endpoint=%s", cfg.Enhance.Model, cfg.Enhance.Endpoint)
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

	dst := out.name
	if dryRun {
		dst = "ドライラン(出力しません)"
	}

	if cfg.ListenMode == "vad" {
		log.Printf("ura-talk 起動 [%s / VAD]。[%s] でリッスン開始/停止。話すと無音で自動区切りして出力します。", dst, cfg.Hotkey)
		tray.setBase("🎙", fmt.Sprintf("待機中 (%s/VAD) — %s でリッスン", dst, cfg.Hotkey))
		vadLoop(rec, wh, out, cfg, down)
		return
	}

	log.Printf("ura-talk 起動 [%s / PTT]。[%s] を押している間だけ録音します。", dst, cfg.Hotkey)
	tray.setBase("🎙", fmt.Sprintf("待機中 (%s/PTT) — %s で録音", dst, cfg.Hotkey))
	pttLoop(rec, wh, out, cfg, down, up)
}

// buildTrigger はホットキーを設定から組み立て、押下(down)/解放(up)を流すチャネルを返す。
// 単体修飾キー(rightcmd 等)なら CGEventTap、組み合わせなら golang.design/x/hotkey を使う。
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

	if len(cfg.Hotkey.Mods) == 0 && modkey.Is(keyName) {
		if err := modkey.Start(keyName); err != nil {
			return nil, nil, nil, err
		}
		go func() {
			for pressed := range modkey.Events() {
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

// pttLoop は push-to-talk:押している間だけ録音し、離したら 1 回出力する。
func pttLoop(rec *recorder.Recorder, wh transcribe.Whisper, out output, cfg *config.Config, down, up <-chan struct{}) {
	for {
		<-down
		if err := rec.Start(); err != nil {
			log.Printf("録音開始失敗: %v", err)
			continue
		}
		log.Println("● 録音中...")
		tray.setBase("🔴", "● 録音中…")
		playOn(cfg)

		<-up
		pcm, durMs, err := rec.Stop()
		tray.setBase("🎙", "待機中…")
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

// vadLoop はキーでリッスンをトグルし、その間ストリームを無音で発話単位に区切って出力する。
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
			tray.setBase("🔴", "● 音声検出中…")
		} else {
			tray.setBase("👂", "リッスン中(無音待ち)…")
		}
	}

	listening := false
	for {
		<-down
		if !listening {
			if err := rec.StartStream(seg.Feed); err != nil {
				log.Printf("リッスン開始失敗: %v", err)
				continue
			}
			listening = true
			log.Println("🎙 リッスン開始(話すと自動で区切って投稿。もう一度キーで停止)")
			tray.setBase("👂", "リッスン中(無音待ち)…")
			playOn(cfg)
		} else {
			rec.StopStream() // コールバックを止めてから Flush する(同時アクセスを避ける)
			seg.Flush()
			listening = false
			log.Println("■ リッスン停止")
			tray.setBase("🎙", "待機中…")
			playOff(cfg)
		}
	}
}

// handle は 1 回の発話(PCM)を正規化・文字起こしして出力先へ渡す。
// out.send が nil のときは出力せず、文字起こし結果を表示するだけ(ドライラン)。
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
			log.Printf("整形 ✏️ %q → %q", text, enhanced)
		default:
			log.Println("整形: 変化なし(そのまま)")
		}
		text = enhanced
		if strings.TrimSpace(text) == "" {
			return
		}
	}

	msg := out.prefix + text
	if out.send == nil {
		log.Printf("(ドライラン)文字起こし結果: %s", msg)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := out.send(ctx, msg); err != nil {
		log.Printf("出力失敗(%s): %v", out.name, err)
		return
	}
	log.Printf("→ %s: %s", out.name, msg)
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
