//go:build darwin

// Package keystroke はフォーカス中のアプリへテキストを「合成入力」する。
//
// 方式: テキストを一時的にクリップボードへ置き、Cmd+V のキーイベントを合成して
// 貼り付ける(日本語も確実)。元のクリップボードは退避・復元する。任意で Enter も送る。
// CGEvent の送出には macOS のアクセシビリティ権限が必要(グローバルホットキーと同じ)。
package keystroke

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>

// isTrusted はこのプロセスにアクセシビリティ権限があるか(=キーイベントを送れるか)を返す。
static int isTrusted() {
    return AXIsProcessTrusted() ? 1 : 0;
}

// promptTrust は未許可なら macOS の許可プロンプトを出し、アプリを
// アクセシビリティ一覧に(正しい署名で)登録する。許可済みなら 1。
static int promptTrust() {
    const void* keys[] = { (const void*)kAXTrustedCheckOptionPrompt };
    const void* vals[] = { (const void*)kCFBooleanTrue };
    CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    Boolean t = AXIsProcessTrustedWithOptions(opts);
    CFRelease(opts);
    return t ? 1 : 0;
}

static void sendKey(CGKeyCode key, int withCmd) {
    CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    CGEventRef down = CGEventCreateKeyboardEvent(src, key, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(src, key, false);
    if (withCmd) {
        CGEventSetFlags(down, kCGEventFlagMaskCommand);
        CGEventSetFlags(up,   kCGEventFlagMaskCommand);
    }
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    if (src) CFRelease(src);
}
*/
import "C"

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// macOS の仮想キーコード。
const (
	keyV      = 9  // kVK_ANSI_V
	keyReturn = 36 // kVK_Return
)

// Trusted はアクセシビリティ権限があるか(キーイベントを送れる状態か)を返す。
func Trusted() bool {
	return C.isTrusted() != 0
}

// PromptAccessibility は未許可なら macOS の許可ダイアログを表示し、
// このアプリをアクセシビリティ一覧に登録する。許可済みなら true。
func PromptAccessibility() bool {
	return C.promptTrust() != 0
}

// injectMu はクリップボード退避→上書き→Cmd+V→復元の一連を直列化する。
// 発話ごとに handle が並行起動されるため(特に VAD)、同時実行でクリップボードが
// 交錯して誤テキストの貼り付けや復元失敗が起きるのを防ぐ。
var injectMu sync.Mutex

// Inject はフォーカス中のフィールドへ text を貼り付ける。autoEnter が true なら続けて Enter を送る。
func Inject(text string, autoEnter bool) error {
	if text == "" {
		return nil
	}
	injectMu.Lock()
	defer injectMu.Unlock()

	prev, prevErr := readClipboard() // 退避(失敗しても続行)
	if err := writeClipboard([]byte(text)); err != nil {
		return fmt.Errorf("クリップボード設定失敗: %w", err)
	}
	time.Sleep(20 * time.Millisecond) // クリップボード反映待ち
	C.sendKey(C.CGKeyCode(keyV), 1)   // Cmd+V

	if autoEnter {
		time.Sleep(40 * time.Millisecond)
		C.sendKey(C.CGKeyCode(keyReturn), 0)
	}

	// 貼り付け完了を待ってから元のクリップボードへ戻す。
	time.Sleep(120 * time.Millisecond)
	if prevErr == nil {
		_ = writeClipboard(prev)
	}
	return nil
}

func readClipboard() ([]byte, error) {
	cmd := exec.Command("pbpaste")
	cmd.Env = utf8Env()
	return cmd.Output()
}

func writeClipboard(b []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(b)
	cmd.Env = utf8Env()
	return cmd.Run()
}

// utf8Env は pbcopy/pbpaste に UTF-8 で入出力させるためのロケールを足した環境を返す。
// .app(launchd)起動では LANG が空になり、pbcopy が UTF-8 を誤って解釈して文字化けするため。
func utf8Env() []string {
	return append(os.Environ(), "LANG=en_US.UTF-8", "LC_CTYPE=en_US.UTF-8")
}
