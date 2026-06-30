//go:build darwin

// Package modkey は単体の修飾キー(右コマンド等)の押下/解放を CGEventTap で検出する。
// golang.design/x/hotkey(Carbon RegisterEventHotKey)は修飾キー単体をホットキーに
// できないため、こちらを使う。検出には macOS のアクセシビリティ権限が必要。
package modkey

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>

extern void modkeyGoCallback(int down);

static int gKeycode = -1;
static unsigned long long gMask = 0;
static CFMachPortRef gTap = NULL;

static CGEventRef modkeyTap(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *ctx) {
    // OS にタップが無効化されたら再有効化する(これをしないと以後イベントが来なくなる)。
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (gTap) CGEventTapEnable(gTap, true);
        return event;
    }
    if (type == kCGEventFlagsChanged) {
        int64_t kc = CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        if ((int)kc == gKeycode) {
            CGEventFlags flags = CGEventGetFlags(event);
            modkeyGoCallback((flags & gMask) ? 1 : 0); // デバイス依存ビットで押下/解放を判定
        }
    }
    return event;
}

static int modkeyStart(int keycode, unsigned long long mask) {
    gKeycode = keycode;
    gMask = mask;
    __block int rc = 0;
    dispatch_sync(dispatch_get_main_queue(), ^{
        CGEventMask m = CGEventMaskBit(kCGEventFlagsChanged);
        CFMachPortRef tap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
            kCGEventTapOptionListenOnly, m, modkeyTap, NULL);
        if (!tap) { rc = -1; return; }
        gTap = tap;
        CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(NULL, tap, 0);
        CFRunLoopAddSource(CFRunLoopGetMain(), src, kCFRunLoopCommonModes);
        CGEventTapEnable(tap, true);
    });
    return rc;
}
*/
import "C"

import "fmt"

var events = make(chan bool, 32)

//export modkeyGoCallback
func modkeyGoCallback(down C.int) {
	select {
	case events <- (down != 0):
	default: // バッファが詰まっていたら捨てる(取りこぼしは許容)
	}
}

// Events は押下(true)/解放(false)を流すチャネル。
func Events() <-chan bool { return events }

type key struct {
	code int
	mask uint64 // NX_DEVICE*KEYMASK(左右を区別するデバイス依存ビット)
}

// keys は対応する単体修飾キー名 → (keycode, デバイス依存マスク)。
var keys = map[string]key{
	"rightcmd":      {54, 0x10},
	"right-command": {54, 0x10},
	"rightcommand":  {54, 0x10},
	"leftcmd":       {55, 0x08},
	"rightoption":   {61, 0x40},
	"rightalt":      {61, 0x40},
	"leftoption":    {58, 0x20},
	"rightshift":    {60, 0x04},
	"fn":            {63, 0x800000},
}

// Is は name が対応する単体修飾キーかを返す。
func Is(name string) bool { _, ok := keys[name]; return ok }

// Names は対応する修飾キー名の一覧を返す。
func Names() []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	return out
}

// Start は name の修飾キー監視を開始する(要アクセシビリティ権限)。
func Start(name string) error {
	k, ok := keys[name]
	if !ok {
		return fmt.Errorf("未対応の修飾キー: %q", name)
	}
	if C.modkeyStart(C.int(k.code), C.ulonglong(k.mask)) != 0 {
		return fmt.Errorf("イベントタップの作成に失敗しました(アクセシビリティ権限が必要)")
	}
	return nil
}
