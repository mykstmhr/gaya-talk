//go:build darwin

// Package voicebar は音声リッスン/録音中に画面下部へ出す小さな状態バーを提供する。
//
// VoiceInk のように「いまマイクが生きている」ことを画面上で示すのが目的。
// ピル型のバーに状態ドット(リッスン=オレンジ / 録音・音声検出=赤)とラベル、
// 音量のミニ波形を表示する。クリックは下のアプリへ素通しし(ignoresMouseEvents)、
// sharingType=None なので画面共有には映らない(裏トークが相手に見えない)。
//
// ObjC 実装は voicebar_darwin.m 側(inputbar と同じく、cgo preamble に ObjC
// クラスを書くと重複シンボルになるため)。
package voicebar

/*
#cgo LDFLAGS: -framework AppKit -framework QuartzCore
#include <stdlib.h>
void voicebarShow(const char *label, int hot, int wave, int busy);
void voicebarFlash(const char *label);
void voicebarHide(void);
void voicebarLevel(double v);
*/
import "C"

import (
	"math"
	"sync/atomic"
	"time"
	"unsafe"
)

// Show はバーを表示する(表示中ならラベル等の更新だけ)。
// hot は録音/音声検出中(赤ドット)。false は待ち(オレンジドット)。
// wave=false で波形を隠す(マイクが生きていない「文字起こし中…」用)。
// busy は並行中の文字起こし件数で、1 以上でロボのバッジ(2 以上は ×N)を出す。
func Show(label string, hot, wave bool, busy int) {
	c := C.CString(label)
	defer C.free(unsafe.Pointer(c))
	b := func(v bool) C.int {
		if v {
			return 1
		}
		return 0
	}
	C.voicebarShow(c, b(hot), b(wave), C.int(busy))
}

// Flash は通知としてバーを短時間だけ出す(自動で消える)。
// 「キーを押したのに音声オフで始まらなかった」など、無反応に見える場面の
// フィードバックに使う。表示中に Show/Hide が来たらそちらが優先される。
func Flash(label string) {
	c := C.CString(label)
	defer C.free(unsafe.Pointer(c))
	C.voicebarFlash(c)
}

// Hide はバーを非表示にする(波形もクリアする)。
func Hide() {
	C.voicebarHide()
}

// lastLevelNano は Level の間引き用(最後にメインスレッドへ送った時刻)。
var lastLevelNano atomic.Int64

// Level は音量(RMS 0..1)を波形表示に送る。オーディオスレッドから呼ばれる想定で、
// メインスレッドを詰まらせないよう 40ms 間隔に間引く。
func Level(rms float64) {
	now := time.Now().UnixNano()
	last := lastLevelNano.Load()
	if now-last < 40*int64(time.Millisecond) || !lastLevelNano.CompareAndSwap(last, now) {
		return
	}
	// RMS は小声で 0.01 程度と小さいので、平方根で持ち上げて見た目の振れを作る。
	C.voicebarLevel(C.double(math.Min(1, math.Sqrt(rms)*1.8)))
}
