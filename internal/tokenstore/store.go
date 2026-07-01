//go:build darwin

// Package tokenstore は user token を macOS Keychain に安全に保存・読み出しする。
// 平文ファイルに置かないことで、トークン漏洩リスクを下げる。
//
// 保存は Security.framework の SecItem API を cgo で直接呼ぶ。以前は
// `security add-generic-password -w <token>` を使っていたが、トークンを
// プロセス引数として渡すため保存の一瞬 `ps` で他プロセスから盗み見できた。
// API 直呼びなら引数に載らず、その露出を無くせる。
// 属性(service/account)は従来の `security` CLI 実装と同一なので、
// 旧実装で保存済みのトークンもそのまま読める。
package tokenstore

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

// kcService / kcAccount は Keychain 項目を一意に定める属性。
static const char *kcService = "ura-talk";
static const char *kcAccount = "slack-user-token";

// baseQuery は service/account で generic password を特定する検索辞書を作る。
// 呼び出し側が CFRelease する。
static CFMutableDictionaryRef baseQuery(void) {
    CFMutableDictionaryRef q = CFDictionaryCreateMutable(
        NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
    CFStringRef svc = CFStringCreateWithCString(NULL, kcService, kCFStringEncodingUTF8);
    CFStringRef acc = CFStringCreateWithCString(NULL, kcAccount, kCFStringEncodingUTF8);
    CFDictionarySetValue(q, kSecAttrService, svc);
    CFDictionarySetValue(q, kSecAttrAccount, acc);
    CFRelease(svc);
    CFRelease(acc);
    return q;
}

// kcSave はトークンを保存する(既存があれば更新)。成功で 0、失敗で非 0(OSStatus)。
// token/len は引数ベクタ経由で来る Go の文字列なので、プロセス引数には載らない。
static int kcSave(const char *token, int len) {
    CFMutableDictionaryRef q = baseQuery();
    CFDataRef data = CFDataCreate(NULL, (const UInt8 *)token, len);

    // まず更新を試み、項目が無ければ追加する。
    CFMutableDictionaryRef upd = CFDictionaryCreateMutable(
        NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(upd, kSecValueData, data);
    OSStatus st = SecItemUpdate(q, upd);
    if (st == errSecItemNotFound) {
        CFDictionarySetValue(q, kSecValueData, data);
        st = SecItemAdd(q, NULL);
    }
    CFRelease(upd);
    CFRelease(data);
    CFRelease(q);
    return (int)st;
}

// kcLoad はトークンを読み出す。見つかれば malloc したバッファ(呼び出し側が free)を
// 返し、*outLen に長さを入れて 0 を返す。未登録なら *outLen=0 で 0 を返す(エラー扱いしない)。
// それ以外の失敗は非 0(OSStatus)を返す。
static int kcLoad(char **out, int *outLen) {
    *out = NULL;
    *outLen = 0;
    CFMutableDictionaryRef q = baseQuery();
    CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);

    CFTypeRef res = NULL;
    OSStatus st = SecItemCopyMatching(q, &res);
    CFRelease(q);
    if (st == errSecItemNotFound) {
        return 0; // 未登録は空として扱う
    }
    if (st != errSecSuccess) {
        return (int)st;
    }
    CFDataRef data = (CFDataRef)res;
    CFIndex n = CFDataGetLength(data);
    char *buf = (char *)malloc((size_t)n);
    if (buf != NULL && n > 0) {
        memcpy(buf, CFDataGetBytePtr(data), (size_t)n);
    }
    CFRelease(res);
    *out = buf;
    *outLen = (int)n;
    return 0;
}

// kcDelete は保存済みトークンを削除する。成功か「元々無い」で 0、それ以外は非 0。
static int kcDelete(void) {
    CFMutableDictionaryRef q = baseQuery();
    OSStatus st = SecItemDelete(q);
    CFRelease(q);
    if (st == errSecItemNotFound) {
        return 0;
    }
    return (int)st;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Save はトークンを Keychain に保存する(既存があれば更新)。
func Save(token string) error {
	// C.CString はコピーを渡すのでプロセス引数には載らない。空でも len=0 で扱える。
	ctok := C.CString(token)
	defer C.free(unsafe.Pointer(ctok))
	if st := C.kcSave(ctok, C.int(len(token))); st != 0 {
		return fmt.Errorf("Keychain への保存失敗 (OSStatus %d)", int(st))
	}
	return nil
}

// Load はトークンを Keychain から読み出す。未登録なら空文字を返す(エラーにしない)。
func Load() (string, error) {
	var out *C.char
	var n C.int
	if st := C.kcLoad(&out, &n); st != 0 {
		return "", fmt.Errorf("Keychain からの読み出し失敗 (OSStatus %d)", int(st))
	}
	if out == nil || n == 0 {
		return "", nil
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoStringN(out, n), nil
}

// Delete は保存済みトークンを削除する。
func Delete() error {
	if st := C.kcDelete(); st != 0 {
		return fmt.Errorf("Keychain からの削除失敗 (OSStatus %d)", int(st))
	}
	return nil
}
