//go:build darwin

// Package audioout は既定の音声出力デバイスが「イヤホン等のプライベート出力」か
// 「スピーカー等の外に音が出る出力」かを判定し、その変化を監視する。
//
// 用途: スピーカーで会議音声を流していると内蔵マイクが相手の声を拾ってしまうため、
// その状態を検知して音声入力を自動でオフにする(main の voice_input:"auto")。
package audioout

/*
#cgo LDFLAGS: -framework CoreAudio -framework CoreFoundation
#include <CoreAudio/CoreAudio.h>

extern void goAudioOutChanged(void);

static AudioObjectPropertyAddress kDefaultOut = {
    kAudioHardwarePropertyDefaultOutputDevice,
    kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain };
static AudioObjectPropertyAddress kDataSource = {
    kAudioDevicePropertyDataSource,
    kAudioObjectPropertyScopeOutput, kAudioObjectPropertyElementMain };

static AudioObjectID defaultOutputDevice(void) {
    AudioObjectID dev = kAudioObjectUnknown;
    UInt32 sz = sizeof(dev);
    AudioObjectGetPropertyData(kAudioObjectSystemObject, &kDefaultOut, 0, NULL, &sz, &dev);
    return dev;
}

static UInt32 transportType(AudioObjectID dev) {
    UInt32 t = 0, sz = sizeof(t);
    AudioObjectPropertyAddress a = { kAudioDevicePropertyTransportType,
        kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain };
    if (AudioObjectGetPropertyData(dev, &a, 0, NULL, &sz, &t) != noErr) return 0;
    return t;
}

static UInt32 dataSource(AudioObjectID dev) {
    UInt32 s = 0, sz = sizeof(s);
    if (AudioObjectGetPropertyData(dev, &kDataSource, 0, NULL, &sz, &s) != noErr) return 0;
    return s;
}

// audiooutPrivate は既定出力がプライベート(イヤホン/ヘッドホン)なら 1 を返す。
// スピーカー・HDMI・AirPlay・不明は 0(外に音が出る)。判定に迷うときは 0 に倒す
// (会議音声を拾わない安全側)。USB/Bluetooth はヘッドセット用途が主なので 1。
static int audiooutPrivate(void) {
    AudioObjectID dev = defaultOutputDevice();
    if (dev == kAudioObjectUnknown) return 0;
    switch (transportType(dev)) {
        case kAudioDeviceTransportTypeBluetooth:
        case kAudioDeviceTransportTypeBluetoothLE:
        case kAudioDeviceTransportTypeUSB:
            return 1;
        case kAudioDeviceTransportTypeBuiltIn:
            // 内蔵出力は「内蔵スピーカー(ispk)」か「ヘッドホン端子(hdpn)」かをデータソースで判別。
            return dataSource(dev) == 'hdpn' ? 1 : 0;
        default:
            return 0; // HDMI/DisplayPort/AirPlay/仮想/不明 → 外に音が出る扱い
    }
}

static AudioObjectID gWatched = kAudioObjectUnknown;

static OSStatus onProp(AudioObjectID obj, UInt32 n,
                       const AudioObjectPropertyAddress addrs[], void *ctx) {
    (void)obj; (void)n; (void)addrs; (void)ctx;
    // 既定出力が変わったら、監視対象デバイスのデータソース購読を張り替える
    // (端子へのヘッドホン抜き差しはデバイス変更ではなくデータソース変更として来るため)。
    AudioObjectID cur = defaultOutputDevice();
    if (cur != gWatched) {
        if (gWatched != kAudioObjectUnknown)
            AudioObjectRemovePropertyListener(gWatched, &kDataSource, onProp, NULL);
        gWatched = cur;
        if (gWatched != kAudioObjectUnknown)
            AudioObjectAddPropertyListener(gWatched, &kDataSource, onProp, NULL);
    }
    goAudioOutChanged();
    return noErr;
}

static void audiooutStartWatch(void) {
    AudioObjectAddPropertyListener(kAudioObjectSystemObject, &kDefaultOut, onProp, NULL);
    gWatched = defaultOutputDevice();
    if (gWatched != kAudioObjectUnknown)
        AudioObjectAddPropertyListener(gWatched, &kDataSource, onProp, NULL);
}
*/
import "C"

import "sync/atomic"

// onChange は出力構成が変わったときに呼ぶコールバック(CoreAudio スレッドから呼ばれる)。
var onChange atomic.Pointer[func()]

//export goAudioOutChanged
func goAudioOutChanged() {
	if f := onChange.Load(); f != nil {
		(*f)()
	}
}

// Private は既定の音声出力がイヤホン/ヘッドホン等の「外に音が漏れない出力」なら true。
// スピーカー・HDMI・AirPlay・不明は false。
func Private() bool {
	return C.audiooutPrivate() != 0
}

// Watch は既定出力デバイス(と内蔵出力のヘッドホン端子)の変化監視を開始し、
// 変化のたびに fn を呼ぶ。二重登録は避けること(起動時に一度だけ呼ぶ想定)。
func Watch(fn func()) {
	onChange.Store(&fn)
	C.audiooutStartWatch()
}
