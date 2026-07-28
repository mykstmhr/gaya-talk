//go:build darwin

// Package overlay はライブコメントを画面全体に重ね、右から左へスクロールさせて表示する。
//
// 方式: 背景透明・クリック貫通・最前面の NSWindow を接続中の全モニターに置き、
// コメントごとにテキストを画像化した CALayer を Core Animation で右から左へ流す
// (会議ウィンドウがどのモニターにあっても見えるよう、同じコメントが全画面に流れる)。
// アニメーションは GPU 合成なので常駐しても CPU をほぼ使わない。
// ウィンドウは sharingType=None にしてあり、画面共有・収録には映らない
// (コメントが会議の相手に見えてしまわないための安全弁)。
//
// モニターの抜き差しには自動で追従する(増えたぶんは追加、減ったぶんは伏せる)。
// 追従しないと、外したモニターのウィンドウを macOS が残りの画面へ移動させ、
// 同じコメントが二重に流れてしまう。
//
// 本文のコピーはコメント履歴パネル(internal/history)から行う(かつてあった
// ⌥+クリックのコピーは、流れている数秒しか使えないうえグローバルのクリック監視が
// 要るため、履歴パネルの追加を機に廃止した)。
package overlay

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework QuartzCore -framework CoreFoundation
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>
#include <float.h>
#include <stdlib.h>

static const CGFloat kFontSize  = 32;  // コメントの文字サイズ(pt)
static const CGFloat kSpeed     = 240; // 流れる速度(px/s)。全コメント同速なので追い越し=重なりが起きない
static const CGFloat kGap       = 48;  // 同一レーンで次のコメントとの最小間隔(px)
static const CGFloat kTopMargin = 44;  // 画面上端からの余白(メニューバーを避ける)

#define OVERLAY_MAX_SCREENS 8
#define OVERLAY_MAX_LANES   16

static NSMutableArray<NSWindow *> *gWins = nil;

// gShared は画面共有・収録にオーバーレイを映すか。既定 NO(映さない)が安全側:
// 再起動でも必ず NO に戻る(オンのまま忘れて次の会議でコメントが映る事故を防ぐ)。
static BOOL gShared = NO;

// モニターごとのレーン状態。「次のコメントを流し始めてよい時刻」を持つ。
// 全コメント同速なので、前のコメントの尻尾(+kGap)が画面右端に入り切る時刻まで
// 待てば重ならない。
static int gLanesN[OVERLAY_MAX_SCREENS];
static CFTimeInterval gLaneFree[OVERLAY_MAX_SCREENS][OVERLAY_MAX_LANES];

// オーバーレイウィンドウを 1 枚作る(位置は overlaySync が合わせる)。
static NSWindow* overlayBuildWindow(void) {
	NSWindow *w = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1, 1)
		styleMask:NSWindowStyleMaskBorderless
		backing:NSBackingStoreBuffered defer:NO];
	w.opaque = NO;
	w.backgroundColor = [NSColor clearColor];
	w.ignoresMouseEvents = YES;
	w.hasShadow = NO;
	w.level = NSScreenSaverWindowLevel;
	// 全 Space + フルスクリーンアプリの上にも出す(会議アプリはフルスクリーンが多い)。
	w.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces
		| NSWindowCollectionBehaviorFullScreenAuxiliary
		| NSWindowCollectionBehaviorStationary;
	// 画面共有・スクリーンショットに映すかは gShared に従う(既定は映さない =
	// コメントを会議相手に見せない。メニューから切り替え可)。
	w.sharingType = gShared ? NSWindowSharingReadOnly : NSWindowSharingNone;
	w.releasedWhenClosed = NO;
	NSView *v = [[NSView alloc] initWithFrame:w.frame];
	v.wantsLayer = YES;
	w.contentView = v;
	return w;
}

// 画面構成に合わせてウィンドウを並べ直す(voicebar と同じ使い回し方式)。
// モニターが増えたぶんは追加し、減ったぶんは伏せる。追従しないと、外した
// モニターのウィンドウを macOS が残りの画面へ移動させ、コメントが二重に流れる。
static void overlaySync(void) {
	NSArray<NSScreen *> *screens = [NSScreen screens];
	while (gWins.count < screens.count && gWins.count < OVERLAY_MAX_SCREENS) {
		[gWins addObject:overlayBuildWindow()];
	}
	for (NSUInteger i = 0; i < gWins.count; i++) {
		NSWindow *w = gWins[i];
		if (i >= screens.count) { // モニターが減ったぶんは伏せる
			[w orderOut:nil];
			continue;
		}
		NSRect f = screens[i].frame;
		if (!NSEqualRects(w.frame, f)) {
			[w setFrame:f display:NO];
			CGFloat laneH = kFontSize * 1.5;
			// 画面の上 8 割だけを使う(下端は字幕・Dock と被りやすいので空ける)。
			int lanes = (int)((f.size.height * 0.8 - kTopMargin) / laneH);
			if (lanes < 1) lanes = 1;
			if (lanes > OVERLAY_MAX_LANES) lanes = OVERLAY_MAX_LANES;
			gLanesN[i] = lanes;
			for (int l = 0; l < OVERLAY_MAX_LANES; l++) gLaneFree[i][l] = 0;
		}
		[w orderFrontRegardless];
	}
}

// overlaySetShared は画面共有・収録への表示を切り替える(既存ウィンドウにも即反映)。
static void overlaySetShared(int on) {
	dispatch_async(dispatch_get_main_queue(), ^{
		gShared = on ? YES : NO;
		if (!gWins) return;
		for (NSWindow *w in gWins) {
			w.sharingType = gShared ? NSWindowSharingReadOnly : NSWindowSharingNone;
		}
	});
}

static void overlayStart(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (gWins) return;
		// cgo は ARC なしでコンパイルされるため、autorelease される +[NSMutableArray array]
		// を static に置いてはいけない(プール解放後にダングリングする)。alloc で所有する。
		gWins = [[NSMutableArray alloc] init];
		overlaySync();
		// モニターの抜き差し・解像度変更に追従する。トークンは retain して持ち続ける
		// (ARC なしなので、放置すると autorelease されて観測が止まりうる)。
		static id gScreenObs = nil;
		gScreenObs = [[[NSNotificationCenter defaultCenter]
			addObserverForName:NSApplicationDidChangeScreenParametersNotification
			object:nil queue:[NSOperationQueue mainQueue]
			usingBlock:^(NSNotification *note) { overlaySync(); }] retain];
		(void)gScreenObs;
	});
}

static void overlayShow(const char *utf8, double r, double g, double b) {
	NSString *text = utf8 ? [NSString stringWithUTF8String:utf8] : @"";
	dispatch_async(dispatch_get_main_queue(), ^{
		if (!gWins || gWins.count == 0 || text.length == 0) return;

		// 黒フチ+塗りの 2 パス描画。フチと塗りを同時に描く(strokeWidth 負値)方式は
		// フチを太くするほど文字の中身が痩せて読みにくくなるため、先に太いフチだけを
		// 描き、その上に塗りだけを重ねる(ライブ配信字幕の定石)。日本語の heavy は
		// ヒラギノ W8 相当になり、明るい背景・ごちゃついた背景の上でも輪郭が立つ。
		NSFont *font = [NSFont systemFontOfSize:kFontSize weight:NSFontWeightHeavy];
		NSDictionary *strokeAttrs = @{
			NSFontAttributeName: font,
			NSStrokeColorAttributeName: [NSColor blackColor],
			NSStrokeWidthAttributeName: @(10.0), // 正値 = フチのみ(フォントサイズの 10%)
		};
		NSDictionary *fillAttrs = @{
			NSFontAttributeName: font,
			NSForegroundColorAttributeName: [NSColor colorWithSRGBRed:r green:g blue:b alpha:1],
		};
		NSAttributedString *stroke = [[NSAttributedString alloc] initWithString:text attributes:strokeAttrs];
		NSAttributedString *fill = [[NSAttributedString alloc] initWithString:text attributes:fillAttrs];
		NSSize sz = [fill size]; // フチは字送りを変えないので塗りの寸法+余白で足りる
		sz.width = ceil(sz.width) + 8;
		sz.height = ceil(sz.height) + 8;

		// CATextLayer はフチ取り(stroke)を描けないため、一度ビットマップに描いて
		// CALayer.contents に貼る。Retina で滲まないよう 2x で描き、全モニターで共有する。
		CGFloat scale = 2.0;
		NSBitmapImageRep *rep = [[NSBitmapImageRep alloc]
			initWithBitmapDataPlanes:NULL
			pixelsWide:(NSInteger)ceil(sz.width * scale)
			pixelsHigh:(NSInteger)ceil(sz.height * scale)
			bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES isPlanar:NO
			colorSpaceName:NSCalibratedRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
		if (!rep) { [stroke release]; [fill release]; return; }
		rep.size = sz;
		[NSGraphicsContext saveGraphicsState];
		[NSGraphicsContext setCurrentContext:
			[NSGraphicsContext graphicsContextWithBitmapImageRep:rep]];
		[stroke drawAtPoint:NSMakePoint(4, 4)];
		[fill drawAtPoint:NSMakePoint(4, 4)];
		[NSGraphicsContext restoreGraphicsState];
		// ARC なし: 描画し終えたら所有権を手放す(放置するとコメントごとにリークする)
		[stroke release];
		[fill release];

		for (NSUInteger wi = 0; wi < gWins.count; wi++) {
			NSWindow *win = gWins[wi];
			if (!win.isVisible) continue; // 外したモニターのぶんは伏せてある
			NSView *host = win.contentView;
			CGFloat W = host.bounds.size.width;
			CGFloat H = host.bounds.size.height;

			// レーン選択: 空きがあれば即流す。全部ふさがっていれば一番早く空くレーンで
			// 空き時刻まで開始を遅らせる(beginTime)。同速なので開始をずらせば重ならない。
			CFTimeInterval now = CACurrentMediaTime();
			int lane = -1;
			for (int i = 0; i < gLanesN[wi]; i++) {
				if (gLaneFree[wi][i] <= now) { lane = i; break; }
			}
			CFTimeInterval start = now;
			if (lane < 0) {
				CFTimeInterval best = DBL_MAX;
				for (int i = 0; i < gLanesN[wi]; i++) {
					if (gLaneFree[wi][i] < best) { best = gLaneFree[wi][i]; lane = i; }
				}
				start = best;
			}
			gLaneFree[wi][lane] = start + (sz.width + kGap) / kSpeed;

			CGFloat laneH = kFontSize * 1.5;
			CGFloat y = H - kTopMargin - ((CGFloat)lane + 0.5) * laneH;
			CFTimeInterval dur = (W + sz.width) / kSpeed;

			CALayer *layer = [CALayer layer];
			layer.bounds = CGRectMake(0, 0, sz.width, sz.height);
			layer.contents = (__bridge id)rep.CGImage;
			layer.contentsScale = scale;
			layer.position = CGPointMake(-sz.width / 2, y); // モデル値は流れ終わりの位置(画面左外)
			[host.layer addSublayer:layer];

			CABasicAnimation *a = [CABasicAnimation animationWithKeyPath:@"position.x"];
			a.fromValue = @(W + sz.width / 2);
			a.toValue = @(-sz.width / 2);
			a.beginTime = start;
			a.duration = dur;
			a.timingFunction = [CAMediaTimingFunction functionWithName:kCAMediaTimingFunctionLinear];
			a.fillMode = kCAFillModeBackwards; // 開始待ちの間は右端の外(見えない)に置く
			[layer addAnimation:a forKey:@"flow"];

			// 流れ終わったらレイヤーを破棄する。rep をブロックに捕まえて、
			// contents が参照するビットマップをアニメーション中ずっと生かしておく
			// (ブロックの copy が retain するので、下で alloc 分を release しても安全)。
			dispatch_after(dispatch_time(DISPATCH_TIME_NOW,
				(int64_t)(((start - now) + dur + 0.5) * NSEC_PER_SEC)),
				dispatch_get_main_queue(), ^{
					[layer removeFromSuperlayer];
					(void)rep;
				});
		}
		[rep release]; // ARC なし: alloc の +1 を手放す(ブロックが retain 済みなのでアニメーション中は生きる)
	});
}
*/
import "C"

import (
	"strconv"
	"strings"
	"unsafe"
)

// Start は透過オーバーレイウィンドウを接続中の全モニターに作り、以後の
// モニターの抜き差しにも追従する。二重呼び出しは無害。
// AppKit のメインループ(systray.Run)が動いていることが前提。
func Start() {
	C.overlayStart()
}

// SetShared は画面共有・収録にコメントを映すかを切り替える(既定は映さない)。
// 入力バー・音声バーには影響しない(それらは常に映らない)。
func SetShared(on bool) {
	v := C.int(0)
	if on {
		v = 1
	}
	C.overlaySetShared(v)
}

// Show はコメントを 1 件、全モニターで右から左へ流す。color は "#rrggbb"(不正なら白)。
func Show(text, color string) {
	r, g, b := parseHexColor(color)
	ct := C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	C.overlayShow(ct, C.double(r), C.double(g), C.double(b))
}

// parseHexColor は "#rrggbb" を 0..1 の RGB に変換する。パースできなければ白。
func parseHexColor(s string) (r, g, b float64) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 1, 1, 1
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 1, 1, 1
	}
	return float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255
}
