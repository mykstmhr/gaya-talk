// room(ニコニコ風オーバーレイ共有)のメニュー配線と送受信。
// serve から setupRoom で初期化される(dryRun 時を除く)。
package main

import (
	"context"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mykstmhr/ura-talk/internal/config"
	"github.com/mykstmhr/ura-talk/internal/dialog"
	"github.com/mykstmhr/ura-talk/internal/inputbar"
	"github.com/mykstmhr/ura-talk/internal/overlay"
	"github.com/mykstmhr/ura-talk/internal/room"
	"github.com/mykstmhr/ura-talk/internal/slack"

	"fyne.io/systray"
)

// roomClient は参加中ルームへの接続(未参加でも安全に使える)。
var roomClient = &room.Client{}

// myColor は自分のコメント色。起動ごとにランダムに選ぶ。匿名でも
// 同一人物の発言は色で追える(ニコニコの流儀)。
var myColor string

// commentPalette は黒フチ+暗背景の上で読みやすい明色だけを集めた色候補。
var commentPalette = []string{
	"#ffffff", "#ffcc00", "#66ccff", "#99ff99",
	"#ff9999", "#cc99ff", "#66ffcc", "#ffb366",
}

// room 用メニュー項目(onReady で作成し、room 出力のときだけ表示)。
var (
	mRoomState       *systray.MenuItem
	mRoomCreateAnon  *systray.MenuItem
	mRoomCreateNamed *systray.MenuItem
	mRoomJoin        *systray.MenuItem
	mRoomCopyURL     *systray.MenuItem
	mRoomLeave       *systray.MenuItem
	mRoomName        *systray.MenuItem
	mSlackMirror     *systray.MenuItem
)

// addRoomMenuItems はメニュー項目を(隠したまま)作る。onReady から呼ぶ。
// 上段=今のルームに対する操作、下段=別ルームへの入り口、で区切る。
func addRoomMenuItems() {
	mRoomState = systray.AddMenuItem("ルーム : 未参加", "現在のルーム接続状態")
	mRoomState.Disable()
	mRoomState.Hide()
	mRoomCopyURL = systray.AddMenuItem("このルームの URL をコピー", "今参加しているルームの共有 URL をクリップボードへ(後から来る人を招く)")
	mRoomCopyURL.Hide()
	mSlackMirror = systray.AddMenuItemCheckbox("Slack に記録", "このルームのコメントを Slack チャンネルにスレッドで転送する", false)
	mSlackMirror.Hide()
	mRoomLeave = systray.AddMenuItem("ルームから退出", "ルームとの接続を切る")
	mRoomLeave.Hide()

	systray.AddSeparator()

	mRoomJoin = systray.AddMenuItem("ルームに URL で参加…", "共有 URL を入力してルームに参加する(コピー済みなら自動で入る)")
	mRoomJoin.Hide()
	mRoomCreateAnon = systray.AddMenuItem("新規ルームを作成 — 匿名", "匿名ルームを作り、共有 URL をクリップボードへ(名前は出ない)")
	mRoomCreateAnon.Hide()
	mRoomCreateNamed = systray.AddMenuItem("新規ルームを作成 — 記名", "記名ルームを作り、共有 URL をクリップボードへ(各自の表示名が付く)")
	mRoomCreateNamed.Hide()

	systray.AddSeparator()

	mRoomName = systray.AddMenuItem("表示名を変更…", "記名ルームで名乗る表示名を変更する")
	mRoomName.Hide()
}

// setupRoom は room 出力の初期化: オーバーレイ・入力バー・メニューの配線。serve から一度だけ呼ぶ。
func setupRoom(cfg *config.Config) {
	myColor = commentPalette[rand.IntN(len(commentPalette))]
	// 表示名を初期化する。config が優先、無ければ前回入力を保存した内部ファイルから。
	// どちらも空なら記名ルームの作成/参加時に入力を促す(macOS ユーザー名は使わない)。
	if n := strings.TrimSpace(cfg.Room.DisplayName); n != "" {
		setDisplayName(n)
	} else {
		setDisplayName(loadStoredName())
	}
	overlay.Start()

	roomClient.OnMessage = displayComment
	roomClient.OnState = func(s room.State) { setRoomState(s) }

	// 文字入力バー: 専用ホットキーでトグルし、Enter で音声と同じ経路に流す。
	inputbar.SetOnSubmit(func(text string) {
		go func() {
			if err := sendRoomComment(text); err != nil {
				log.Printf("コメント送信失敗: %v", err)
			}
		}()
	})
	if down, err := watchDown(cfg.Room.InputHotkey); err != nil {
		log.Printf("⚠️ 入力バーのホットキー(%s)を登録できません: %v", cfg.Room.InputHotkey, err)
	} else {
		go func() {
			for range down {
				inputbar.Toggle()
			}
		}()
	}

	mRoomState.Show()
	mRoomCreateAnon.Show()
	mRoomCreateNamed.Show()
	mRoomJoin.Show()
	mRoomCopyURL.Show()
	mRoomCopyURL.Disable()
	mRoomLeave.Show()
	mRoomLeave.Disable()

	go func() {
		for range mRoomCreateAnon.ClickedCh {
			createAndJoinRoom(cfg, false)
		}
	}()
	go func() {
		for range mRoomCreateNamed.ClickedCh {
			createAndJoinRoom(cfg, true)
		}
	}()
	go func() {
		for range mRoomJoin.ClickedCh {
			joinRoomWithDialog(cfg)
		}
	}()
	go func() {
		for range mRoomCopyURL.ClickedCh {
			copyCurrentRoomURL()
		}
	}()
	go func() {
		for range mRoomLeave.ClickedCh {
			stopMirror() // 退出したら記録も止める
			roomClient.Leave()
			log.Println("ルームから退出しました。")
		}
	}()

	mRoomName.Show()
	updateNameMenu(cfg)
	go func() {
		for range mRoomName.ClickedCh {
			changeDisplayName(cfg)
		}
	}()

	// Slack ミラーは bot token とチャンネルが設定されている人にだけ出す(ミラー役)。
	if strings.TrimSpace(cfg.Room.SlackBotToken) != "" && strings.TrimSpace(cfg.Room.SlackChannel) != "" {
		mSlackMirror.Show()
		mSlackMirror.Disable() // ルームに入るまで無効
		go func() {
			for range mSlackMirror.ClickedCh {
				toggleSlackMirror(cfg)
			}
		}()
	}
}

// changeDisplayName は表示名の変更ダイアログを出し、内部ファイルに保存する。
// config の display_name が設定されている場合はそちらが優先なので何もしない。
func changeDisplayName(cfg *config.Config) {
	if strings.TrimSpace(cfg.Room.DisplayName) != "" {
		log.Println("ℹ️ 表示名は config の display_name で設定されているため、メニューからは変更できません。")
		return
	}
	entered, ok := dialog.Prompt("表示名を変更",
		"記名ルームで各コメントの先頭に付く名前です。",
		"例: myk", currentDisplayName(), "保存")
	if !ok {
		return
	}
	n := strings.TrimSpace(entered)
	if n == "" {
		return // 空は変更なし扱い(誤って消さないため)
	}
	setDisplayName(n)
	saveStoredName(n)
	updateNameMenu(cfg)
	log.Printf("✅ 表示名を「%s」に変更しました。", n)
}

// updateNameMenu は「表示名を変更」項目のラベルと有効/無効を現状に合わせる。
func updateNameMenu(cfg *config.Config) {
	if mRoomName == nil {
		return
	}
	if strings.TrimSpace(cfg.Room.DisplayName) != "" {
		mRoomName.SetTitle("表示名 : " + cfg.Room.DisplayName + "(config)")
		mRoomName.Disable()
		return
	}
	cur := currentDisplayName()
	if cur == "" {
		cur = "未設定"
	}
	mRoomName.SetTitle("表示名を変更… (" + truncRunes(cur, 12) + ")")
	mRoomName.Enable()
}

// copyCurrentRoomURL は参加中ルームの共有 URL をクリップボードへコピーする。
func copyCurrentRoomURL() {
	r := roomClient.Room()
	if r == nil {
		log.Println("⚠️ ルームに参加していません。")
		return
	}
	if err := pbcopy(r.URL()); err != nil {
		log.Printf("⚠️ クリップボードへコピーできませんでした。URL: %s", r.URL())
		return
	}
	log.Println("✅ このルームの URL をクリップボードへコピーしました。")
}

// sendRoomComment はコメントをルームへ流す。表示は全員分をサーバのエコーで
// 揃えるため自分では描画せず、未参加・切断中だけ自分の画面へ直接流す(ソロモード)。
func sendRoomComment(text string) error {
	p := room.Payload{ID: room.NewID(), Text: text, Color: myColor}
	if r := roomClient.Room(); r != nil && r.Named {
		p.Name = currentDisplayName() // 作成/参加時に確定済み
	}
	if err := roomClient.Send(p); err != nil {
		// 送信エラーでも実際には届いていることがある(タイムアウト等)。
		// displayComment が ID で重複排除するので、あとからエコーが来ても二重にならない。
		displayComment(p)
	}
	return nil
}

// seenIDs は表示済みコメント ID(重複排除用)。ローカル表示とサーバエコーの二重や、
// 万一の再配信を防ぐ。エントリは一定時間で掃除する。
var (
	seenMu  sync.Mutex
	seenIDs = map[string]time.Time{}
)

// displayComment は重複を除いてコメントをオーバーレイに流す(ミラー有効なら Slack へも)。
func displayComment(p room.Payload) {
	if p.ID != "" {
		seenMu.Lock()
		if _, dup := seenIDs[p.ID]; dup {
			seenMu.Unlock()
			if os.Getenv("URATALK_DEBUG") != "" {
				log.Printf("重複コメントをスキップ: id=%s", p.ID)
			}
			return
		}
		now := time.Now()
		seenIDs[p.ID] = now
		for id, at := range seenIDs { // 小規模なので毎回全走査で十分
			if now.Sub(at) > 5*time.Minute {
				delete(seenIDs, id)
			}
		}
		seenMu.Unlock()
	}
	text := p.Text
	if p.Name != "" {
		text = "[" + p.Name + "] " + text
	}
	overlay.Show(text, p.Color)
	mirrorToSlack(text)
}

// mirror は Slack ミラーの状態。ミラー役 1 人のクライアントで、受信した全コメント
// (重複排除済み)を親メッセージのスレッドへ転送する。
var (
	mirrorMu      sync.Mutex
	mirroring     bool
	mirrorClient  *slack.Client
	mirrorChannel string
	mirrorThread  string // 親メッセージの ts。以降のコメントはここへぶら下げる
)

// toggleSlackMirror は Slack 記録の開始/停止を切り替える。
func toggleSlackMirror(cfg *config.Config) {
	mirrorMu.Lock()
	on := mirroring
	mirrorMu.Unlock()
	if on {
		stopMirror()
		return
	}
	startMirror(cfg, roomClient.Room())
}

// startMirror は参加中ルームの記録先チャンネルへの転送を開始する(親メッセージを 1 本立てる)。
// チャンネルはルーム(URL)由来。記録対象でない・トークンが無いルームでは何もしない。
func startMirror(cfg *config.Config, r *room.Room) {
	if r == nil || r.SlackChannel == "" {
		log.Println("⚠️ このルームは Slack 記録対象ではありません(作成時にチャンネルを指定してください)。")
		return
	}
	if strings.TrimSpace(cfg.Room.SlackBotToken) == "" {
		log.Println("⚠️ Slack bot token が未設定のため記録できません。")
		return
	}
	cl := slack.New(cfg.Room.SlackBotToken)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ts, err := cl.PostMessage(ctx, r.SlackChannel,
		"📝 ura-talk のコメントをこのスレッドに記録します(room "+truncRunes(r.Token, 8)+")", "")
	cancel()
	if err != nil {
		log.Printf("⚠️ Slack 記録を開始できませんでした: %v", err)
		return
	}
	mirrorMu.Lock()
	mirroring = true
	mirrorClient = cl
	mirrorChannel = r.SlackChannel
	mirrorThread = ts
	mirrorMu.Unlock()
	if mSlackMirror != nil {
		mSlackMirror.Check()
	}
	log.Println("✅ Slack 記録を開始しました(以降のコメントをスレッドに転送します)。")
}

// stopMirror は Slack 記録を止める(退出時にも呼ぶ)。
func stopMirror() {
	mirrorMu.Lock()
	was := mirroring
	mirroring = false
	mirrorClient = nil
	mirrorThread = ""
	mirrorMu.Unlock()
	if was {
		if mSlackMirror != nil {
			mSlackMirror.Uncheck()
		}
		log.Println("■ Slack 記録を停止しました。")
	}
}

// mirrorToSlack はミラー有効時、コメントをスレッドへ転送する(失敗しても表示は止めない)。
func mirrorToSlack(text string) {
	mirrorMu.Lock()
	on, cl, ch, th := mirroring, mirrorClient, mirrorChannel, mirrorThread
	mirrorMu.Unlock()
	if !on || cl == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := cl.PostMessage(ctx, ch, text, th); err != nil {
			log.Printf("Slack 転送失敗: %v", err)
		}
	}()
}

// createAndJoinRoom はルームを作成して共有 URL をコピーし、自分も参加する。
// named=true なら記名ルーム(URL に &n=1。各参加者の表示名が付く)。
func createAndJoinRoom(cfg *config.Config, named bool) {
	if cfg.Room.Server == "" {
		log.Println("⚠️ room.server が未設定です(config.json に中継サーバの URL を設定してください)")
		return
	}
	// 記名ルームは作成前に表示名を確定する(未設定ならダイアログ)。キャンセルなら中止。
	if named {
		if _, ok := ensureDisplayName(cfg); !ok {
			log.Println("⚠️ 表示名が未入力のため記名ルームの作成を中止しました。")
			return
		}
	}
	// Slack トークンを持つ人には、このルームの記録先チャンネルを尋ねる(空欄で記録なし)。
	// チャンネルは URL に載り、全参加者が「記録対象」であることを知れる。
	channel := ""
	if strings.TrimSpace(cfg.Room.SlackBotToken) != "" {
		entered, ok := dialog.Prompt("Slack 記録(任意)",
			"このルームを記録する Slack チャンネル ID(空欄で記録しない)。全参加者に「記録対象」と表示されます。",
			"例: C0123ABCD", strings.TrimSpace(cfg.Room.SlackChannel), "作成")
		if ok {
			channel = strings.TrimSpace(entered)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := room.Create(ctx, cfg.Room.Server, named)
	if err != nil {
		log.Printf("⚠️ %v", err)
		return
	}
	r.SlackChannel = channel
	kind := "匿名"
	if named {
		kind = "記名(表示名: " + currentDisplayName() + ")"
	}
	if channel != "" {
		kind += "・Slack記録"
	}
	if err := pbcopy(r.URL()); err != nil {
		// コピーできなくても URL は必要なのでログに出す(鍵入りだが自分のログなので許容)。
		log.Printf("⚠️ クリップボードへコピーできませんでした。URL: %s", r.URL())
	} else {
		log.Printf("✅ %sルームを作成し、共有 URL をクリップボードへコピーしました。メンバーに共有してください。", kind)
	}
	roomClient.Join(r)
	// 作成者は記録先を指定していれば自動でミラーを開始する(他のトークン保持者は手動)。
	if channel != "" {
		startMirror(cfg, r)
	}
}

// displayName は記名ルームで名乗る現在の表示名(空=未設定)。config か内部ファイル、
// または作成/参加時のダイアログ入力で確定する。sendRoomComment(別 goroutine)からも
// 読むので mutex で保護する。
var (
	displayNameMu sync.Mutex
	displayName   string
)

func currentDisplayName() string {
	displayNameMu.Lock()
	defer displayNameMu.Unlock()
	return displayName
}

func setDisplayName(n string) {
	displayNameMu.Lock()
	displayName = strings.TrimSpace(n)
	displayNameMu.Unlock()
}

// ensureDisplayName は記名ルームに入る直前に表示名を確定する。config が設定されていれば
// それを優先し、未設定で今も空ならダイアログで入力を促す(入力は内部ファイルに保存して
// 次回以降は聞かない)。入力をキャンセルしたら (_,false) を返し、呼び出し側は中止する。
func ensureDisplayName(cfg *config.Config) (string, bool) {
	if n := strings.TrimSpace(cfg.Room.DisplayName); n != "" {
		setDisplayName(n) // config が最優先(内部ファイルより優先)
		return n, true
	}
	if n := currentDisplayName(); n != "" {
		return n, true
	}
	entered, ok := dialog.Prompt("表示名を入力",
		"記名ルームでは各コメントの先頭にこの名前が付きます。",
		"例: myk", "", "決定")
	if !ok {
		return "", false
	}
	n := strings.TrimSpace(entered)
	if n == "" {
		return "", false
	}
	setDisplayName(n)
	saveStoredName(n) // 次回以降は聞かない(config とは別の内部ファイル)
	updateNameMenu(cfg)
	return n, true
}

// stateDir は表示名などの内部状態を置くディレクトリ(多重起動ロックと同じ場所)。
func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(home, "Library", "Application Support", "ura-talk")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	return d, nil
}

// loadStoredName は前回入力した表示名を読む(無ければ空)。
func loadStoredName() string {
	d, err := stateDir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(d, "display_name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// saveStoredName は表示名を内部ファイルに保存する(config には書かない)。
func saveStoredName(name string) {
	d, err := stateDir()
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(d, "display_name"), []byte(name), 0o600)
}

// joinRoomWithDialog は共有 URL の入力ダイアログを出して参加する。
// クリップボードに有効な共有 URL があればプリフィルするので、コピー済みなら
// そのまま Enter するだけでよい。
func joinRoomWithDialog(cfg *config.Config) {
	initial := ""
	if raw, err := pbpaste(); err == nil {
		if _, err := room.Parse(raw); err == nil {
			initial = raw
		}
	}
	raw, ok := dialog.Prompt("ルームに参加",
		"共有された URL を貼り付けてください。",
		"https://…/r/…#k=…", initial, "参加")
	if !ok || strings.TrimSpace(raw) == "" {
		return
	}
	r, err := room.Parse(raw)
	if err != nil {
		log.Printf("⚠️ %v", err)
		return
	}
	if r.Named {
		if _, ok := ensureDisplayName(cfg); !ok {
			log.Println("⚠️ 表示名が未入力のため記名ルームへの参加を中止しました。")
			return
		}
		log.Printf("ℹ️ 記名ルームに参加します(あなたの表示名: %s)。", currentDisplayName())
	}
	if r.SlackChannel != "" {
		// 記録対象であることを本人にもはっきり知らせる(オーバーレイにも一度出す)。
		log.Println("🔴 このルームは Slack に記録されます。")
		overlay.Show("🔴 このルームは Slack に記録されます", "#ff6666")
	}
	roomClient.Join(r)
}

// setRoomState は接続状態をメニューへ反映する。参加中はルーム ID(トークンの先頭)も
// 併記し、メンバー間で「同じルームにいるか」を突き合わせられるようにする。
func setRoomState(s room.State) {
	if mRoomState == nil {
		return
	}
	id := ""
	recorded := false
	if r := roomClient.Room(); r != nil {
		tag := "匿名"
		if r.Named {
			tag = "記名"
		}
		id = " (" + truncRunes(r.Token, 8) + " / " + tag + ")"
		if r.SlackChannel != "" {
			id += " 🔴Slack記録対象"
			recorded = true
		}
	}
	// URL コピーはルームに属している間は使える。Slack 記録トグルは「記録対象ルーム」でのみ。
	joined := roomClient.Room() != nil
	toggle(mRoomCopyURL, joined)
	toggle(mSlackMirror, joined && recorded)
	if !joined {
		stopMirror() // 切断されたら記録も止める
	}
	switch s {
	case room.StateConnected:
		mRoomState.SetTitle("ルーム : 参加中" + id)
		mRoomLeave.Enable()
	case room.StateConnecting:
		mRoomState.SetTitle("ルーム : 接続中…" + id)
		mRoomLeave.Enable()
	default:
		mRoomState.SetTitle("ルーム : 未参加(ソロモード)")
		mRoomLeave.Disable()
	}
}

// toggle はメニュー項目の有効/無効を切り替える。
func toggle(item *systray.MenuItem, on bool) {
	if item == nil {
		return
	}
	if on {
		item.Enable()
	} else {
		item.Disable()
	}
}

// watchDown はホットキー h の押下だけを流すチャネルを返す(入力バー用)。
// 分岐(単体修飾キー / コード / hotkey ライブラリ)は buildTrigger に一本化してあり、
// ここでは解放(up)と停止(stop)を捨てて押下だけを使う(常駐終了まで有効なので stop 不要)。
func watchDown(h config.Hotkey) (<-chan struct{}, error) {
	down, _, _, err := buildTrigger(h)
	return down, err
}

// pbcopy / pbpaste は URL(ASCII)のやり取りに使う。日本語を通さないので
// 外部コマンド経由でも LANG 依存の文字化けは起きない。
func pbcopy(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

func pbpaste() (string, error) {
	out, err := exec.Command("pbpaste").Output()
	return strings.TrimSpace(string(out)), err
}
