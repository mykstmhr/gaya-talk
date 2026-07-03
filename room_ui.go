// room 出力(ニコニコ風オーバーレイ共有)のメニュー配線と送受信。
// output が "room" のときだけ setupRoom で有効化される。
package main

import (
	"context"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mykstmhr/ura-talk/internal/config"
	"github.com/mykstmhr/ura-talk/internal/dialog"
	"github.com/mykstmhr/ura-talk/internal/inputbar"
	"github.com/mykstmhr/ura-talk/internal/modkey"
	"github.com/mykstmhr/ura-talk/internal/overlay"
	"github.com/mykstmhr/ura-talk/internal/room"

	"fyne.io/systray"
	"golang.design/x/hotkey"
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
	mRoomState   *systray.MenuItem
	mRoomCreate  *systray.MenuItem
	mRoomJoin    *systray.MenuItem
	mRoomCopyURL *systray.MenuItem
	mRoomLeave   *systray.MenuItem
)

// addRoomMenuItems はメニュー項目を(隠したまま)作る。onReady から呼ぶ。
func addRoomMenuItems() {
	mRoomState = systray.AddMenuItem("ルーム : 未参加", "現在のルーム接続状態")
	mRoomState.Disable()
	mRoomState.Hide()
	mRoomCreate = systray.AddMenuItem("ルームを作成して URL をコピー", "中継サーバにルームを作り、共有 URL をクリップボードへ")
	mRoomCreate.Hide()
	mRoomJoin = systray.AddMenuItem("URL で参加…", "共有 URL を入力してルームに参加する(コピー済みなら自動で入る)")
	mRoomJoin.Hide()
	mRoomCopyURL = systray.AddMenuItem("このルームの URL をコピー", "今参加しているルームの共有 URL をクリップボードへ(後から来る人を招く)")
	mRoomCopyURL.Hide()
	mRoomLeave = systray.AddMenuItem("ルームから退出", "ルームとの接続を切る")
	mRoomLeave.Hide()
}

// setupRoom は room 出力の初期化: オーバーレイ・入力バー・メニューの配線。serve から一度だけ呼ぶ。
func setupRoom(cfg *config.Config) {
	myColor = commentPalette[rand.IntN(len(commentPalette))]
	overlay.Start()

	roomClient.OnMessage = displayComment
	roomClient.OnState = func(s room.State) { setRoomState(s) }

	// 文字入力バー: 専用ホットキーでトグルし、Enter で音声と同じ経路に流す。
	inputbar.SetOnSubmit(func(text string) {
		go func() {
			if err := sendRoomComment(cfg, text); err != nil {
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
	mRoomCreate.Show()
	mRoomJoin.Show()
	mRoomCopyURL.Show()
	mRoomCopyURL.Disable()
	mRoomLeave.Show()
	mRoomLeave.Disable()

	go func() {
		for range mRoomCreate.ClickedCh {
			createAndJoinRoom(cfg)
		}
	}()
	go func() {
		for range mRoomJoin.ClickedCh {
			joinRoomWithDialog()
		}
	}()
	go func() {
		for range mRoomCopyURL.ClickedCh {
			copyCurrentRoomURL()
		}
	}()
	go func() {
		for range mRoomLeave.ClickedCh {
			roomClient.Leave()
			log.Println("ルームから退出しました。")
		}
	}()
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
func sendRoomComment(cfg *config.Config, text string) error {
	p := room.Payload{ID: room.NewID(), Text: text, Color: myColor}
	if r := roomClient.Room(); r != nil && r.Named {
		p.Name = cfg.Room.DisplayName // 空なら記名ルームでも匿名のまま
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

// displayComment は重複を除いてコメントをオーバーレイに流す。
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
}

// createAndJoinRoom はルームを作成して共有 URL をコピーし、自分も参加する。
func createAndJoinRoom(cfg *config.Config) {
	if cfg.Room.Server == "" {
		log.Println("⚠️ room.server が未設定です(config.json に中継サーバの URL を設定してください)")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := room.Create(ctx, cfg.Room.Server, false)
	if err != nil {
		log.Printf("⚠️ %v", err)
		return
	}
	if err := pbcopy(r.URL()); err != nil {
		// コピーできなくても URL は必要なのでログに出す(鍵入りだが自分のログなので許容)。
		log.Printf("⚠️ クリップボードへコピーできませんでした。URL: %s", r.URL())
	} else {
		log.Println("✅ ルームを作成し、共有 URL をクリップボードへコピーしました。メンバーに共有してください。")
	}
	roomClient.Join(r)
}

// joinRoomWithDialog は共有 URL の入力ダイアログを出して参加する。
// クリップボードに有効な共有 URL があればプリフィルするので、コピー済みなら
// そのまま Enter するだけでよい。
func joinRoomWithDialog() {
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
	roomClient.Join(r)
}

// setRoomState は接続状態をメニューへ反映する。参加中はルーム ID(トークンの先頭)も
// 併記し、メンバー間で「同じルームにいるか」を突き合わせられるようにする。
func setRoomState(s room.State) {
	if mRoomState == nil {
		return
	}
	id := ""
	if r := roomClient.Room(); r != nil {
		id = " (" + truncRunes(r.Token, 8) + ")"
	}
	// URL コピーは接続中でなくても、ルームに属している間(接続試行中含む)は使える。
	joined := roomClient.Room() != nil
	toggle(mRoomCopyURL, joined)
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

// watchDown はホットキーの押下だけを流すチャネルを返す(入力バー用)。
// buildTrigger と同じく、単体修飾キーは CGEventTap、組み合わせは hotkey ライブラリを使う。
// 修飾キー 2 つのコード(例 mods:["rightshift"], key:"rightcmd")にも対応する。
func watchDown(h config.Hotkey) (<-chan struct{}, error) {
	d := make(chan struct{}, 8)
	keyName := strings.ToLower(h.Key)
	if modkey.Is(keyName) && (len(h.Mods) == 0 || (len(h.Mods) == 1 && modkey.Is(strings.ToLower(h.Mods[0])))) {
		var events <-chan bool
		var err error
		if len(h.Mods) == 1 {
			events, err = modkey.WatchChord(keyName, strings.ToLower(h.Mods[0]))
		} else {
			events, err = modkey.Watch(keyName)
		}
		if err != nil {
			return nil, err
		}
		go func() {
			for pressed := range events {
				if pressed {
					select {
					case d <- struct{}{}:
					default:
					}
				}
			}
		}()
		return d, nil
	}
	mods, key, err := parseHotkey(h)
	if err != nil {
		return nil, err
	}
	hk := hotkey.New(mods, key)
	if err := hk.Register(); err != nil {
		return nil, err
	}
	go func() {
		for range hk.Keydown() {
			select {
			case d <- struct{}{}:
			default:
			}
		}
	}()
	return d, nil
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
