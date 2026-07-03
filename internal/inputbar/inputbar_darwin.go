//go:build darwin

// Package inputbar はホットキーで呼び出す Spotlight 風のコメント入力欄を提供する。
//
// 方式: 非アクティブ化パネル(NSWindowStyleMaskNonactivatingPanel)を使い、
// 会議アプリなど前面アプリをアクティブのままキー入力だけ受け取る(Spotlight と同じ)。
// Enter で確定して閉じ、Esc/再度ホットキーでキャンセルする。日本語 IME の Enter は
// まず変換確定に使われ、確定済みの状態での Enter だけが送信になる(NSTextField の標準動作)。
// パネルは sharingType=None にしてあり、入力途中の文面は画面共有に映らない。
//
// ObjC 実装は inputbar_darwin.m 側(cgo preamble に ObjC クラスを書くと重複シンボルになるため)。
package inputbar

/*
#cgo LDFLAGS: -framework AppKit -framework QuartzCore
void inputbarToggle(void);
*/
import "C"

import "sync/atomic"

// onSubmit は Enter 確定時に呼ばれるコールバック(メインスレッドから呼ばれる)。
var onSubmit atomic.Pointer[func(string)]

//export inputbarGoSubmit
func inputbarGoSubmit(text *C.char) {
	if cb := onSubmit.Load(); cb != nil {
		(*cb)(C.GoString(text))
	}
}

// SetOnSubmit は入力確定時のコールバックを設定する(重い処理は呼び出し側で逃がすこと)。
func SetOnSubmit(fn func(text string)) {
	onSubmit.Store(&fn)
}

// Toggle は入力バーの表示/非表示を切り替える(ホットキーから呼ぶ)。
func Toggle() {
	C.inputbarToggle()
}
