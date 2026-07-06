//go:build darwin

// Package dialog はメニューバー常駐アプリ用の簡単なモーダル入力ダイアログを提供する。
package dialog

/*
#cgo LDFLAGS: -framework AppKit
#include <stdlib.h>
char* dialogPrompt(const char *title, const char *message, const char *placeholder,
                   const char *initial, const char *okLabel);
void dialogAlert(const char *title, const char *message, const char *okLabel);
int dialogConfirm(const char *title, const char *message, const char *okLabel);
*/
import "C"

import "unsafe"

// Prompt は入力ダイアログを出し、OK なら (入力文字列, true)、キャンセルなら ("", false)。
// メインスレッドで実行されるまでブロックする(AppKit のメインループが動いていること)。
func Prompt(title, message, placeholder, initial, okLabel string) (string, bool) {
	ct := C.CString(title)
	cm := C.CString(message)
	cp := C.CString(placeholder)
	ci := C.CString(initial)
	co := C.CString(okLabel)
	defer func() {
		C.free(unsafe.Pointer(ct))
		C.free(unsafe.Pointer(cm))
		C.free(unsafe.Pointer(cp))
		C.free(unsafe.Pointer(ci))
		C.free(unsafe.Pointer(co))
	}()
	res := C.dialogPrompt(ct, cm, cp, ci, co)
	if res == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(res))
	return C.GoString(res), true
}

// Confirm は OK/キャンセルの確認ダイアログを出し、OK なら true を返す。
// 閉じられるまでブロックする(AppKit のメインループが動いていること)。
func Confirm(title, message, okLabel string) bool {
	ct := C.CString(title)
	cm := C.CString(message)
	co := C.CString(okLabel)
	defer func() {
		C.free(unsafe.Pointer(ct))
		C.free(unsafe.Pointer(cm))
		C.free(unsafe.Pointer(co))
	}()
	return C.dialogConfirm(ct, cm, co) == 1
}

// Alert は入力欄なしの警告ダイアログを出す(OK のみ)。閉じられるまでブロックする。
func Alert(title, message, okLabel string) {
	ct := C.CString(title)
	cm := C.CString(message)
	co := C.CString(okLabel)
	defer func() {
		C.free(unsafe.Pointer(ct))
		C.free(unsafe.Pointer(cm))
		C.free(unsafe.Pointer(co))
	}()
	C.dialogAlert(ct, cm, co)
}
