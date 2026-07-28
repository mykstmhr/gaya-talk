//go:build darwin

// Package history は流れて消えたコメントを後から見返すためのローカル履歴パネルを提供する。
//
// 方式: 表示済みコメント(重複排除後)を直近 100 件までメモリ内に持ち、ホットキーか
// メニューで呼び出すログパネル(ゲームのチャットログ風)に時刻付きで一覧する。
// 「本文の履歴はどこにも残らない」という room の設計方針に合わせ、ディスクにも
// サーバにも書かず、アプリを終了すると消える。パネルは sharingType=None にしてあり
// 画面共有には映らない(例外: GAYATALK_CAPTURE 起動時だけドキュメント撮影用に映る)。
//
// 例外は自前の「再起動」「アップデート」で、その直前だけ履歴をファイルへ書き出し、
// 次回起動が読み込んで即削除する(handoff.go)。ファイルが存在するのは再起動を
// またぐ数十秒だけで、通常の「終了」では書き出さず、これまでどおり消える。
//
// ObjC 実装は history_darwin.m 側(cgo preamble に ObjC クラスを書くと重複シンボルになるため)。
package history

/*
#cgo LDFLAGS: -framework AppKit -framework QuartzCore
#include <stdlib.h>
void historyToggle(void);
void historyAppend(const char *ts, const char *name, const char *text, double r, double g, double b);
*/
import "C"

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// maxEntries は保持する履歴の上限(history_darwin.m の kHPMaxEntries と揃えること)。
const maxEntries = 100

// entries はパネル表示用(ObjC 側)とは別に持つ生データ。再起動ハンドオフの
// 直列化に使う(ObjC 側は描画済みの NSAttributedString しか持っていないため)。
var (
	entriesMu sync.Mutex
	entries   []entry
)

// entry は履歴 1 件の生データ。
type entry struct {
	Name   string `json:"name,omitempty"`
	Text   string `json:"text"`
	Color  string `json:"color"`
	SentAt int64  `json:"sent_at"`
}

// Append はコメントを 1 件、履歴へ積む(パネルが閉じていても積まれる)。
// name は表示名(匿名ルームなら空)、color は "#rrggbb"(不正なら白)、
// sentAtMilli は送信時刻の unix ミリ秒(0 なら受信時刻で代用)。
func Append(name, text, color string, sentAtMilli int64) {
	ts := time.Now()
	if sentAtMilli > 0 {
		ts = time.UnixMilli(sentAtMilli)
	}
	entriesMu.Lock()
	entries = append(entries, entry{Name: name, Text: text, Color: color, SentAt: ts.UnixMilli()})
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
	entriesMu.Unlock()
	r, g, b := parseHexColor(color)
	cts := C.CString(ts.Format("15:04"))
	cname := C.CString(name)
	ctext := C.CString(text)
	// historyAppend は NSString への変換を済ませてから main queue へ渡すので、
	// 戻ってきたら即 free してよい。
	C.historyAppend(cts, cname, ctext, C.double(r), C.double(g), C.double(b))
	C.free(unsafe.Pointer(cts))
	C.free(unsafe.Pointer(cname))
	C.free(unsafe.Pointer(ctext))
}

// Toggle は履歴パネルの表示/非表示を切り替える(ホットキー・メニューから呼ぶ)。
func Toggle() {
	C.historyToggle()
}

// parseHexColor は "#rrggbb" を 0..1 の RGB に変換する。パースできなければ白
// (internal/overlay と同じ規則。色はオーバーレイと見た目を揃える)。
func parseHexColor(s string) (r, g, b float64) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 1, 1, 1
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 1, 1, 1
	}
	return float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255
}
