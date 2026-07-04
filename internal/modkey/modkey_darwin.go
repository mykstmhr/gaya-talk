//go:build darwin

// Package modkey は単体の修飾キー(右コマンド等)の押下/解放を CGEventTap で検出する。
// golang.design/x/hotkey(Carbon RegisterEventHotKey)は修飾キー単体をホットキーに
// できないため、こちらを使う。検出には macOS のアクセシビリティ権限が必要。
//
// 修飾キー 2 つのコード(例: 右Shift を押しながら右⌘)にも対応する(WatchChord)。
// 同じキーに単体監視とコード監視の両方があるときは、コード成立時はコード側だけに
// 届く(素の押下と区別され、音声トリガ等に誤爆しない)。
package modkey

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>

// modkeyTrusted はアクセシビリティ権限が有効かを返す(prompt 非 0 でシステムの
// 許可ダイアログも出す)。権限が無いとイベントタップは「自分宛のイベント」しか
// 受け取れず、非アクティブ時にホットキーが沈黙する(エラーにはならない)。
static int modkeyTrusted(int prompt) {
    if (!prompt) return AXIsProcessTrusted() ? 1 : 0;
    CFStringRef keys[] = { kAXTrustedCheckOptionPrompt };
    CFBooleanRef vals[] = { kCFBooleanTrue };
    CFDictionaryRef opts = CFDictionaryCreate(NULL, (const void **)keys, (const void **)vals, 1,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    int ok = AXIsProcessTrustedWithOptions(opts) ? 1 : 0;
    CFRelease(opts);
    return ok;
}

extern void modkeyGoCallback(int keycode, unsigned long long flags);

static CFMachPortRef gTap = NULL;

static CGEventRef modkeyTap(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *ctx) {
    // OS にタップが無効化されたら再有効化する(これをしないと以後イベントが来なくなる)。
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (gTap) CGEventTapEnable(gTap, true);
        return event;
    }
    if (type == kCGEventFlagsChanged) {
        // flagsChanged は修飾キーの押下/解放時にしか来ないので全件 Go へ渡し、
        // どのキーをどう扱うかは Go 側の watcher 表で決める。
        int64_t kc = CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
        modkeyGoCallback((int)kc, (unsigned long long)CGEventGetFlags(event));
    }
    return event;
}

// modkeyEnsureTap はイベントタップを(まだ無ければ)作る。成功で 0。
static int modkeyEnsureTap(void) {
    if (gTap) return 0;
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

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// debug は URATALK_DEBUG 時に flagsChanged の受信と watcher への配送をログに出す
// (「キーは押されているのにバーが出ない」型の切り分け用)。
var debug = os.Getenv("URATALK_DEBUG") != ""

// watcher は監視 1 件。held が 0 なら単体キー、非 0 なら「held のビットが
// 立っている(=その修飾キーを押しながら)ときの押下だけ」を流すコード監視。
type watcher struct {
	code int
	mask uint64 // 自キーのデバイス依存ビット
	held uint64 // コード条件(0 なら単体)
	ch   chan bool
}

var (
	watchMu  sync.Mutex
	watchers []*watcher
)

// 修飾キーの押下/解放(flagsChanged)ごとに呼ばれる。送信は非ブロッキングなので
// watchMu を保持したまま処理してよい(スナップショットの確保は不要)。
//
//export modkeyGoCallback
func modkeyGoCallback(keycode C.int, flags C.ulonglong) {
	kc, f := int(keycode), uint64(flags)
	if debug {
		log.Printf("modkey: flagsChanged keycode=%d flags=%#x", kc, f)
	}
	watchMu.Lock()
	defer watchMu.Unlock()

	// 同じキーのコード監視が成立しているか(成立時は単体監視へ届けない)。
	chordActive := false
	for _, w := range watchers {
		if w.code == kc && w.held != 0 && f&w.held != 0 {
			chordActive = true
			break
		}
	}
	for _, w := range watchers {
		if w.code != kc {
			continue
		}
		down := f&w.mask != 0
		if w.held != 0 {
			// コード監視: 押下は held 成立時のみ。解放は常に流す(押しっぱなし対策)。
			if down && f&w.held == 0 {
				continue
			}
		} else if down && chordActive {
			continue // コードに横取りされた押下
		}
		if debug {
			log.Printf("modkey: → watcher code=%d held=%#x down=%v", w.code, w.held, down)
		}
		select {
		case w.ch <- down:
		default: // バッファが詰まっていたら捨てる(取りこぼしは許容)
		}
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
	"leftshift":     {56, 0x02},
	"fn":            {63, 0x800000},
}

// Trusted はアクセシビリティ権限がこのビルドに対して有効かを返す。
// prompt=true ならシステムの許可ダイアログも出す。権限が無くてもイベントタップの
// 作成は成功してしまい、非アクティブ時だけホットキーが沈黙する(自分宛のイベントは
// 届くため、アクティブ時は動いて見える)。起動時にこれで検査して警告する。
func Trusted(prompt bool) bool {
	p := C.int(0)
	if prompt {
		p = 1
	}
	return C.modkeyTrusted(p) != 0
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
	return add(name, "")
}

// WatchChord は「held を押しながらの base」の押下だけを流す監視を開始する。
// 例: WatchChord("rightcmd", "rightshift") = 右Shift+右⌘。
// 同じ base の Watch とは排他で、コード成立時の押下はこちらだけに届く。
func WatchChord(base, held string) (<-chan bool, error) {
	if held == "" {
		return nil, fmt.Errorf("held が空です")
	}
	return add(base, held)
}

func add(base, held string) (<-chan bool, error) {
	b, ok := keys[base]
	if !ok {
		return nil, fmt.Errorf("未対応の修飾キー: %q", base)
	}
	var heldMask uint64
	if held != "" {
		h, ok := keys[held]
		if !ok {
			return nil, fmt.Errorf("未対応の修飾キー: %q", held)
		}
		heldMask = h.mask
	}
	if C.modkeyEnsureTap() != 0 {
		return nil, fmt.Errorf("イベントタップの作成に失敗しました(アクセシビリティ権限が必要)")
	}
	w := &watcher{code: b.code, mask: b.mask, held: heldMask, ch: make(chan bool, 32)}
	watchMu.Lock()
	watchers = append(watchers, w)
	watchMu.Unlock()
	return w.ch, nil
}
