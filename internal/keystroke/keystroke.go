//go:build darwin

// Package keystroke はフォーカス中のアプリへテキストを「合成入力」する。
//
// 方式: テキストを一時的にクリップボードへ置き、Cmd+V のキーイベントを合成して
// 貼り付ける(日本語も確実)。元のクリップボードは退避・復元する。任意で Enter も送る。
// CGEvent の送出には macOS のアクセシビリティ権限が必要(グローバルホットキーと同じ)。
package keystroke

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework CoreFoundation -framework AppKit
#include <ApplicationServices/ApplicationServices.h>
#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

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

// sendKey は key を flags(修飾キーのマスク)付きで合成送出する。flags=0 なら修飾なし。
// flags は常に明示設定する。設定しないと HID のハードウェア修飾状態(直前の Cmd や
// ホットキーの残り)を引き継ぎ、素の Enter が ⌘/⇧+Enter と誤解釈されてしまう
// (Notion/Cosense でリスト継続にならない原因)。
static void sendKey(CGKeyCode key, CGEventFlags flags) {
    CGEventSourceRef src = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    CGEventRef down = CGEventCreateKeyboardEvent(src, key, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(src, key, false);
    CGEventSetFlags(down, flags); // 0 でも明示的に設定し、余計な修飾を確実に外す
    CGEventSetFlags(up,   flags);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    if (src) CFRelease(src);
}

// frontmostPID は今アクティブ(最前面)なアプリの PID を返す。取れなければ 0。
static pid_t frontmostPID() {
    NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
    if (app == nil) return 0;
    return app.processIdentifier;
}

// appName は pid のアプリ表示名を返す(呼び出し側が free する)。取れなければ NULL。
static char* appName(pid_t pid) {
    NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
    if (app == nil || app.localizedName == nil) return NULL;
    return strdup([app.localizedName UTF8String]);
}

// appBundleID は pid のアプリの bundle id を返す(呼び出し側が free する)。取れなければ NULL。
static char* appBundleID(pid_t pid) {
    NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
    if (app == nil || app.bundleIdentifier == nil) return NULL;
    return strdup([app.bundleIdentifier UTF8String]);
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
	"unsafe"
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

// Target は貼り付け先のアプリ。PID<=0 は「未取得」を表す。
type Target struct {
	PID      int
	Name     string // アプリ表示名(ログ・メニュー表示・override 照合用)
	BundleID string // bundle id(override 照合用)
}

// CaptureFrontmost は今最前面のアプリを取得する。
// グローバルホットキーで呼ばれる前提なので、ユーザーがフォーカスしていたチャット欄のアプリが取れる。
func CaptureFrontmost() Target {
	pid := int(C.frontmostPID())
	if pid <= 0 {
		return Target{}
	}
	t := Target{PID: pid}
	if c := C.appName(C.pid_t(pid)); c != nil {
		t.Name = C.GoString(c)
		C.free(unsafe.Pointer(c))
	}
	if c := C.appBundleID(C.pid_t(pid)); c != nil {
		t.BundleID = C.GoString(c)
		C.free(unsafe.Pointer(c))
	}
	return t
}

// injectMu はクリップボード退避→上書き→Cmd+V→復元の一連を直列化する。
// 発話ごとに handle が並行起動されるため(特に VAD)、同時実行でクリップボードが
// 交錯して誤テキストの貼り付けや復元失敗が起きるのを防ぐ。
var injectMu sync.Mutex

// postKeyFor は送信キートークン(none|enter|shift+enter|cmd+enter)を、
// 送出すべきキーと修飾マスクへ変換する。send=false なら何も送らない。
func postKeyFor(sendKey string) (send bool, key C.CGKeyCode, flags C.CGEventFlags) {
	switch sendKey {
	case "enter", "return":
		return true, C.CGKeyCode(keyReturn), 0
	case "shift+enter":
		return true, C.CGKeyCode(keyReturn), C.kCGEventFlagMaskShift
	case "cmd+enter":
		return true, C.CGKeyCode(keyReturn), C.kCGEventFlagMaskCommand
	default: // "none" / "" / 不明
		return false, 0, 0
	}
}

// defaultSendDelayMs は貼り付けから送信キーまでの既定待ち時間。
const defaultSendDelayMs = 40

// Inject はフォーカス中のフィールドへ text を貼り付け、sendKey で指定された送信キーを続けて送る。
// sendKey は none|enter|shift+enter|cmd+enter(none は貼り付けのみ)。
// sendDelayMs は貼り付けから送信キーまでの待ち(0 以下で既定)。ブラウザ製エディタでは
// ペースト確定前に Enter が届くとリスト継続にならないため、長めが要ることがある。
func Inject(text string, sendKey string, sendDelayMs int) error {
	if text == "" {
		return nil
	}
	injectMu.Lock()
	defer injectMu.Unlock()

	prev, prevErr := readClipboard() // 退避(失敗しても続行)
	if err := writeClipboard([]byte(text)); err != nil {
		return fmt.Errorf("クリップボード設定失敗: %w", err)
	}
	time.Sleep(20 * time.Millisecond)                       // クリップボード反映待ち
	C.sendKey(C.CGKeyCode(keyV), C.kCGEventFlagMaskCommand) // Cmd+V

	if send, key, flags := postKeyFor(sendKey); send {
		delay := sendDelayMs
		if delay <= 0 {
			delay = defaultSendDelayMs
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
		C.sendKey(key, flags)
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
