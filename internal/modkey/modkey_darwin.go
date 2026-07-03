//go:build darwin

// Package modkey は単体の修飾キー(右コマンド等)の押下/解放を CGEventTap で検出する。
// golang.design/x/hotkey(Carbon RegisterEventHotKey)は修飾キー単体をホットキーに
// できないため、こちらを使う。検出には macOS のアクセシビリティ権限が必要。
package modkey

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>

extern void modkeyGoCallback(int idx, int down);

// 監視対象のキーは複数登録できる(メイントリガ + 入力バー等)。タップは 1 本を共有する。
#define MODKEY_MAX 8
static int gKeycodes[MODKEY_MAX];
static unsigned long long gMasks[MODKEY_MAX];
static int gNumKeys = 0;
static CFMachPortRef gTap = NULL;

static CGEventRef modkeyTap(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *ctx) {
    // OS にタップが無効化されたら再有効化する(これをしないと以後イベントが来なくなる)。
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (gTap) CGEventTapEnable(gTap, true);
        return event;
    }
    if (type == kCGEventFlagsChanged) {
        int64_t kc = CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        CGEventFlags flags = CGEventGetFlags(event);
        for (int i = 0; i < gNumKeys; i++) {
            if ((int)kc == gKeycodes[i]) {
                modkeyGoCallback(i, (flags & gMasks[i]) ? 1 : 0); // デバイス依存ビットで押下/解放を判定
            }
        }
    }
    return event;
}

// modkeyAdd は監視キーを 1 つ登録し、その添字を返す(満杯/タップ作成失敗は -1)。
// 初回だけイベントタップを作り、以降は共有する。
static int modkeyAdd(int keycode, unsigned long long mask) {
    if (gNumKeys >= MODKEY_MAX) return -1;
    __block int rc = 0;
    if (!gTap) {
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
        if (rc != 0) return -1;
    }
    int idx = gNumKeys;
    gKeycodes[idx] = keycode;
    gMasks[idx] = mask;
    gNumKeys = idx + 1;
    return idx;
}
*/
import "C"

import (
	"fmt"
	"sync"
)

// watchers は登録順のチャネル(C 側の添字と対応)。コールバックと Watch の競合を守る。
var (
	watchMu  sync.Mutex
	watchers []chan bool
)

//export modkeyGoCallback
func modkeyGoCallback(idx, down C.int) {
	watchMu.Lock()
	var ch chan bool
	if int(idx) < len(watchers) {
		ch = watchers[idx]
	}
	watchMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- (down != 0):
	default: // バッファが詰まっていたら捨てる(取りこぼしは許容)
	}
}

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

// Watch は name の修飾キー監視を開始し、押下(true)/解放(false)を流すチャネルを返す
// (要アクセシビリティ権限)。複数キーを個別に監視できる(イベントタップは共有)。
func Watch(name string) (<-chan bool, error) {
	k, ok := keys[name]
	if !ok {
		return nil, fmt.Errorf("未対応の修飾キー: %q", name)
	}
	watchMu.Lock()
	defer watchMu.Unlock()
	idx := int(C.modkeyAdd(C.int(k.code), C.ulonglong(k.mask)))
	if idx < 0 {
		return nil, fmt.Errorf("イベントタップの作成に失敗しました(アクセシビリティ権限が必要)")
	}
	ch := make(chan bool, 32)
	for len(watchers) <= idx {
		watchers = append(watchers, nil)
	}
	watchers[idx] = ch
	return ch, nil
}
