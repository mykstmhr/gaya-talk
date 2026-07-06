// voicebar の ObjC 実装。クラス定義を .m に置く理由は inputbar_darwin.m と同じ
// (cgo preamble に外部リンケージの ObjC クラスを書くと重複シンボルになる)。
//
// バーはオーバーレイと同じく「接続中の全モニター」に出す。カーソルがあるモニター
// だけに出すと、見ているモニターと違うことがあり「出たり出なかったり」に見えるため。
// cgo は ARC なしでコンパイルされるので、パネルは作り直さず使い回す(毎回作ると
// 解放されずリークする)。モニターが増えたぶんは表示時に追加し、減ったぶんは伏せる。
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>

static const CGFloat kVBW = 340, kVBH = 36;
enum { kVBBars = 24, kVBMaxScreens = 8 };
static const CGFloat kVBBarW = 3, kVBBarGap = 2;
static const CGFloat kVBLevelW = kVBBars * (kVBBarW + kVBBarGap) - kVBBarGap;

// 音量のミニ波形。直近 kVBBars 個のレベル(0..1)を右詰めで流す。
@interface UTLevelView : NSView {
@public
    CGFloat levels[kVBBars];
}
- (void)push:(CGFloat)v;
- (void)clear;
@end

@implementation UTLevelView
- (void)push:(CGFloat)v {
    memmove(levels, levels + 1, sizeof(CGFloat) * (kVBBars - 1));
    levels[kVBBars - 1] = v;
    self.needsDisplay = YES;
}
- (void)clear {
    memset(levels, 0, sizeof(levels));
    self.needsDisplay = YES;
}
- (void)drawRect:(NSRect)dirty {
    [[NSColor colorWithWhite:1 alpha:0.85] setFill];
    CGFloat maxH = self.bounds.size.height;
    for (int i = 0; i < kVBBars; i++) {
        CGFloat h = MAX(2, levels[i] * maxH);
        NSRect r = NSMakeRect(i * (kVBBarW + kVBBarGap), (maxH - h) / 2, kVBBarW, h);
        [[NSBezierPath bezierPathWithRoundedRect:r xRadius:1.5 yRadius:1.5] fill];
    }
}
@end

// 文字起こし中バッジ。ロボの顔(旧メニューバーの transcribe アイコンと同じモチーフ)を
// 白で描き、2 件以上並行しているときは ×N を添える。絵文字ではなく自前描画にして、
// バーの他の要素(波形・ドット)とトーンを揃える。
@interface UTBotBadge : NSView {
@public
    int count; // 並行中の文字起こし件数(表示/非表示は呼び出し側が hidden で制御)
}
- (void)setCount:(int)n;
@end

@implementation UTBotBadge
- (void)setCount:(int)n {
    if (count == n) return;
    count = n;
    self.needsDisplay = YES;
}
- (void)drawRect:(NSRect)dirty {
    NSColor *fg = [NSColor colorWithWhite:1 alpha:0.9];
    CGFloat h = self.bounds.size.height;
    CGFloat by = (h - 17) / 2; // ロボ全高 17(顔 13 + アンテナ 4)

    // アンテナ(柄+玉)と顔(丸角の四角)。
    [fg setFill];
    [[NSBezierPath bezierPathWithRect:NSMakeRect(7.4, by + 12, 1.2, 3)] fill];
    [[NSBezierPath bezierPathWithOvalInRect:NSMakeRect(6.6, by + 14.2, 2.8, 2.8)] fill];
    [[NSBezierPath bezierPathWithRoundedRect:NSMakeRect(0, by, 16, 13) xRadius:3 yRadius:3] fill];

    // 目と口は抜きで表現する(背景=バーの地の色が見える)。
    [[NSGraphicsContext currentContext] setCompositingOperation:NSCompositingOperationDestinationOut];
    [[NSBezierPath bezierPathWithOvalInRect:NSMakeRect(3.2, by + 6.2, 3.2, 3.2)] fill];
    [[NSBezierPath bezierPathWithOvalInRect:NSMakeRect(9.6, by + 6.2, 3.2, 3.2)] fill];
    [[NSBezierPath bezierPathWithRoundedRect:NSMakeRect(4.4, by + 2.4, 7.2, 1.8) xRadius:0.9 yRadius:0.9] fill];
    [[NSGraphicsContext currentContext] setCompositingOperation:NSCompositingOperationSourceOver];

    if (count > 1) {
        NSString *s = [NSString stringWithFormat:@"×%d", count];
        [s drawAtPoint:NSMakePoint(19, (h - 14) / 2)
            withAttributes:@{ NSFontAttributeName: [NSFont systemFontOfSize:11],
                              NSForegroundColorAttributeName: fg }];
    }
}
@end

// 1 モニターぶんのバー(パネルと中身)。
@interface UTVoiceBarUnit : NSObject
@property (strong) NSPanel *panel;
@property (strong) NSTextField *label;
@property (strong) NSView *dot;
@property (strong) UTLevelView *level;
@property (strong) UTBotBadge *badge;
@end
@implementation UTVoiceBarUnit
@end

static NSMutableArray<UTVoiceBarUnit *> *gUnits = nil; // 作成済みのバー(使い回す)
static long gGen = 0; // Flash の自動クローズが後続の表示を消さないための世代カウンタ
static BOOL gFlashing = NO; // Flash 表示中は voicebarHide を無視する(自動クローズに任せる)

// バーを 1 つ作る(位置は表示時に合わせる)。
static UTVoiceBarUnit* vbBuildUnit(void) {
    NSPanel *p = [[NSPanel alloc] initWithContentRect:NSMakeRect(0, 0, kVBW, kVBH)
        styleMask:NSWindowStyleMaskBorderless
        backing:NSBackingStoreBuffered defer:NO];
    p.opaque = NO;
    p.backgroundColor = [NSColor clearColor];
    p.hasShadow = YES;
    p.level = NSScreenSaverWindowLevel;
    // NSPanel は既定 hidesOnDeactivate=YES で、非アクティブなアプリのパネルを隠す。
    // gaya-talk は常駐の背景アプリ(ほぼ常に非アクティブ)なので、切らないとバーが出ない。
    p.hidesOnDeactivate = NO;
    p.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces
        | NSWindowCollectionBehaviorFullScreenAuxiliary
        | NSWindowCollectionBehaviorStationary;
    p.sharingType = NSWindowSharingNone;   // 画面共有に映さない(裏トークを隠す)
    p.ignoresMouseEvents = YES;            // クリックは下のアプリへ素通し
    p.releasedWhenClosed = NO;

    NSView *bg = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, kVBW, kVBH)];
    bg.wantsLayer = YES;
    bg.layer.backgroundColor = [[NSColor colorWithWhite:0.1 alpha:0.85] CGColor];
    bg.layer.cornerRadius = kVBH / 2;
    p.contentView = bg;

    NSView *dot = [[NSView alloc] initWithFrame:NSMakeRect(16, (kVBH - 10) / 2, 10, 10)];
    dot.wantsLayer = YES;
    dot.layer.cornerRadius = 5;
    [bg addSubview:dot];

    UTLevelView *lv = [[UTLevelView alloc]
        initWithFrame:NSMakeRect(kVBW - kVBLevelW - 16, (kVBH - 20) / 2, kVBLevelW, 20)];
    [bg addSubview:lv];

    // 文字起こしバッジは波形の左に置く(×N のぶん幅 44)。
    UTBotBadge *badge = [[UTBotBadge alloc]
        initWithFrame:NSMakeRect(kVBW - kVBLevelW - 16 - 52, (kVBH - 20) / 2, 44, 20)];
    badge.hidden = YES;
    [bg addSubview:badge];

    NSTextField *label = [NSTextField labelWithString:@""];
    label.frame = NSMakeRect(34, (kVBH - 18) / 2, kVBW - 34 - kVBLevelW - 24 - 52, 18);
    label.font = [NSFont systemFontOfSize:13];
    label.textColor = [NSColor colorWithWhite:1 alpha:0.92];
    label.lineBreakMode = NSLineBreakByTruncatingTail;
    [bg addSubview:label];

    UTVoiceBarUnit *u = [[UTVoiceBarUnit alloc] init];
    u.panel = p;
    u.label = label;
    u.dot = dot;
    u.level = lv;
    u.badge = badge;
    return u;
}

static void vbHideAll(void) {
    for (UTVoiceBarUnit *u in gUnits) {
        [u.panel orderOut:nil];
        [u.level clear];
    }
}

// 全モニターへバーを出す(表示中ならラベル等の更新だけ)。
// showLevel=NO のとき波形を隠す(音声が来ない状態)。busy は並行中の文字起こし件数で、
// 1 以上でロボのバッジを出す(2 以上は ×N 付き)。
static void vbApply(NSString *text, NSColor *dotColor, BOOL pulse, BOOL showLevel, int busy) {
    if (!gUnits) gUnits = [[NSMutableArray alloc] init];
    NSArray<NSScreen *> *screens = [NSScreen screens];
    // モニターが増えていたらバーを足す(パネルは以後使い回す)。
    while (gUnits.count < screens.count && gUnits.count < kVBMaxScreens) {
        [gUnits addObject:vbBuildUnit()];
    }
    for (NSUInteger i = 0; i < gUnits.count; i++) {
        UTVoiceBarUnit *u = gUnits[i];
        if (i >= screens.count) { // モニターが減ったぶんは伏せる
            [u.panel orderOut:nil];
            continue;
        }
        NSRect sf = screens[i].frame;
        [u.panel setFrame:NSMakeRect(sf.origin.x + (sf.size.width - kVBW) / 2,
                                     sf.origin.y + 88, kVBW, kVBH) // 文字入力バーと同じ場所(排他で入れ替わる)
                  display:NO];
        u.label.stringValue = text;
        u.level.hidden = !showLevel;
        u.badge.hidden = (busy <= 0);
        [u.badge setCount:busy];
        // ラベル幅: 右側に波形かバッジがあればそこまで、どちらも無ければ端まで広げる。
        NSRect lf = u.label.frame;
        lf.size.width = (showLevel || busy > 0 ? kVBW - 34 - kVBLevelW - 24 - 52
                                               : kVBW - 34 - 20);
        u.label.frame = lf;
        u.dot.layer.backgroundColor = dotColor.CGColor;
        // 「生きている」ことを示すドットの明滅(orderOut で消えるため表示のたびに付け直す)。
        [u.dot.layer removeAnimationForKey:@"pulse"];
        if (pulse) {
            CABasicAnimation *a = [CABasicAnimation animationWithKeyPath:@"opacity"];
            a.fromValue = @1.0;
            a.toValue = @0.35;
            a.duration = 0.7;
            a.autoreverses = YES;
            a.repeatCount = HUGE_VALF;
            [u.dot.layer addAnimation:a forKey:@"pulse"];
        }
        [u.panel orderFrontRegardless];
    }
}

void voicebarShow(const char *label, int hot, int wave, int busy) {
    NSString *text = [NSString stringWithUTF8String:label ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{
        gGen++;
        gFlashing = NO;
        vbApply(text, hot ? [NSColor systemRedColor] : [NSColor systemOrangeColor],
                YES, wave != 0, busy);
    });
}

// voicebarFlash は通知としてバーを短時間だけ出す(自動で消える)。
// 表示中に本来の Show/Hide が来たら世代が進むので、古い自動クローズは何もしない。
void voicebarFlash(const char *label) {
    NSString *text = [NSString stringWithUTF8String:label ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{
        gGen++;
        gFlashing = YES;
        long my = gGen;
        vbApply(text, [NSColor systemGrayColor], NO, NO, 0);
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1.8 * NSEC_PER_SEC)),
                       dispatch_get_main_queue(), ^{
            if (gGen == my) {
                gFlashing = NO;
                vbHideAll();
            }
        });
    });
}

void voicebarHide(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        // Flash(音声オフ通知)は 1.8 秒で自動で消えるので、状態遷移由来の Hide が
        // 直後に来ても握りつぶす(通知がユーザーに見えないまま消えるのを防ぐ)。
        if (gFlashing) return;
        gGen++;
        vbHideAll();
    });
}

void voicebarLevel(double v) {
    dispatch_async(dispatch_get_main_queue(), ^{
        for (UTVoiceBarUnit *u in gUnits) {
            if (u.panel.isVisible) [u.level push:(CGFloat)v];
        }
    });
}
