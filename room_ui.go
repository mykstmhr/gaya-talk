// room 出力(ニコニコ風オーバーレイ共有)のメニュー配線と送受信。
// output が "room" のときだけ setupRoom で有効化される。
package main

import (
	"context"
	"log"
	"math/rand/v2"
	"os/exec"
	"strings"
	"time"

	"github.com/mykstmhr/ura-talk/internal/config"
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
	mRoomState  *systray.MenuItem
	mRoomCreate *systray.MenuItem
	mRoomJoin   *systray.MenuItem
	mRoomLeave  *systray.MenuItem
)

// addRoomMenuItems はメニュー項目を(隠したまま)作る。onReady から呼ぶ。
func addRoomMenuItems() {
	mRoomState = systray.AddMenuItem("ルーム : 未参加", "現在のルーム接続状態")
	mRoomState.Disable()
	mRoomState.Hide()
	mRoomCreate = systray.AddMenuItem("ルームを作成して URL をコピー", "中継サーバにルームを作り、共有 URL をクリップボードへ")
	mRoomCreate.Hide()
	mRoomJoin = systray.AddMenuItem("クリップボードの URL で参加", "コピー済みの共有 URL のルームに参加する")
	mRoomJoin.Hide()
	mRoomLeave = systray.AddMenuItem("ルームから退出", "ルームとの接続を切る")
	mRoomLeave.Hide()
}

// setupRoom は room 出力の初期化: オーバーレイ・入力バー・メニューの配線。serve から一度だけ呼ぶ。
func setupRoom(cfg *config.Config) {
	myColor = commentPalette[rand.IntN(len(commentPalette))]
	overlay.Start()

	roomClient.OnMessage = func(p room.Payload) {
		overlay.Show(displayText(p), p.Color)
	}
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
	mRoomLeave.Show()
	mRoomLeave.Disable()

	go func() {
		for range mRoomCreate.ClickedCh {
			createAndJoinRoom(cfg)
		}
	}()
	go func() {
		for range mRoomJoin.ClickedCh {
			joinRoomFromClipboard()
		}
	}()
	go func() {
		for range mRoomLeave.ClickedCh {
			roomClient.Leave()
			log.Println("ルームから退出しました。")
		}
	}()
}

// sendRoomComment はコメントをルームへ流す。表示は全員分をサーバのエコーで
// 揃えるため自分では描画せず、未参加・切断中だけ自分の画面へ直接流す(ソロモード)。
func sendRoomComment(cfg *config.Config, text string) error {
	p := room.Payload{Text: text, Color: myColor}
	if r := roomClient.Room(); r != nil && r.Named {
		p.Name = cfg.Room.DisplayName // 空なら記名ルームでも匿名のまま
	}
	if err := roomClient.Send(p); err != nil {
		overlay.Show(displayText(p), p.Color)
	}
	return nil
}

// displayText は記名モードのとき "[名前] 本文" の形にする。
func displayText(p room.Payload) string {
	if p.Name != "" {
		return "[" + p.Name + "] " + p.Text
	}
	return p.Text
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

// joinRoomFromClipboard はクリップボードの共有 URL のルームへ参加する。
func joinRoomFromClipboard() {
	raw, err := pbpaste()
	if err != nil || raw == "" {
		log.Println("⚠️ クリップボードが空です。共有 URL をコピーしてから選んでください。")
		return
	}
	r, err := room.Parse(raw)
	if err != nil {
		log.Printf("⚠️ %v", err)
		return
	}
	roomClient.Join(r)
}

// setRoomState は接続状態をメニューへ反映する。
func setRoomState(s room.State) {
	if mRoomState == nil {
		return
	}
	switch s {
	case room.StateConnected:
		mRoomState.SetTitle("ルーム : 参加中")
		mRoomLeave.Enable()
	case room.StateConnecting:
		mRoomState.SetTitle("ルーム : 接続中…")
		mRoomLeave.Enable()
	default:
		mRoomState.SetTitle("ルーム : 未参加(ソロモード)")
		mRoomLeave.Disable()
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
