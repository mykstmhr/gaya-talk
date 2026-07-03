//go:build darwin

// Package inputbar はホットキーで呼び出す Spotlight 風のコメント入力欄を提供する。
//
// 方式: 非アクティブ化パネル(NSWindowStyleMaskNonactivatingPanel)を使い、
// 会議アプリなど前面アプリをアクティブのままキー入力だけ受け取る(Spotlight と同じ)。
// Enter で流したあともバーは開いたままで続けて打てる(連投用。空の Enter は何もしない)。
// 閉じるのは Esc か再度ホットキー。日本語 IME の Enter はまず変換確定に使われ、
// 確定済みの状態での Enter だけが送信になる(NSTextField の標準動作)。
// パネルは sharingType=None にしてあり、入力途中の文面は画面共有に映らない。
//
// ObjC 実装は inputbar_darwin.m 側(cgo preamble に ObjC クラスを書くと重複シンボルになるため)。
package inputbar

/*
#cgo LDFLAGS: -framework AppKit -framework QuartzCore
void inputbarToggle(void);
void inputbarDismiss(void);
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

// onShown / onHidden はバーの表示/非表示時に呼ばれるコールバック(メインスレッドから)。
// 音声リッスンとの排他制御(文字入力を開いたら音声を止める)と、
// 開いている間だけ音声状態バーを引っ込める(同じ場所に出るため)のに使う。
var (
	onShown  atomic.Pointer[func()]
	onHidden atomic.Pointer[func()]
)

//export inputbarGoShown
func inputbarGoShown() {
	if cb := onShown.Load(); cb != nil {
		(*cb)()
	}
}

//export inputbarGoHidden
func inputbarGoHidden() {
	if cb := onHidden.Load(); cb != nil {
		(*cb)()
	}
}

// SetOnShown はバー表示時のコールバックを設定する(ブロックしないこと)。
func SetOnShown(fn func()) {
	onShown.Store(&fn)
}

// SetOnHidden はバー非表示時のコールバックを設定する(ブロックしないこと)。
// Enter 確定・Esc・トグル・Dismiss のどの閉じ方でも呼ばれる。
func SetOnHidden(fn func()) {
	onHidden.Store(&fn)
}

// Toggle は入力バーの表示/非表示を切り替える(ホットキーから呼ぶ)。
func Toggle() {
	C.inputbarToggle()
}

// Dismiss は入力途中の文面を捨てて閉じる(表示されていなければ何もしない)。
// 音声リッスン/録音の開始時に、文字入力との排他のため呼ばれる。
func Dismiss() {
	C.inputbarDismiss()
}
