// history の ObjC 実装。クラス定義を .m に置く理由は inputbar_darwin.m と同じ
// (cgo preamble に外部リンケージの ObjC クラスを書くと重複シンボルになる)。
//
// 流れて消えたコメントを後から見返すためのログパネル。履歴はこのプロセスの
// メモリ内だけに直近 kHPMaxEntries 件を持ち、終了で消える(サーバにも
// ディスクにも残さない、という room の設計方針に合わせる)。
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>
#include <stdlib.h> // getenv(GAYATALK_CAPTURE)

// borderless パネルは既定でキーウィンドウになれないため上書きする
// (Esc で閉じる・Cmd+C でコピーするのにキーウィンドウである必要がある)。
@interface UTHistoryPanel : NSPanel
@end
@implementation UTHistoryPanel
- (BOOL)canBecomeKeyWindow { return YES; }
@end

static const CGFloat kHPW = 380, kHPH = 440;
enum { kHPMaxEntries = 100 };

static UTHistoryPanel *gPanel = nil;
static NSTextView *gText = nil;
// 表示済みコメントの整形済み行(古い順)。パネルが閉じていても積み続ける。
static NSMutableArray<NSAttributedString *> *gEntries = nil;

// currentScreen はいまマウスカーソルがあるモニターを返す(取れなければ mainScreen)。
// 履歴は「見返したいと思った瞬間に作業していたモニター」に出したい(inputbar と同じ理由)。
static NSScreen* currentScreen(void) {
    NSPoint mouse = [NSEvent mouseLocation];
    for (NSScreen *s in [NSScreen screens]) {
        if (NSMouseInRect(mouse, s.frame, NO)) return s;
    }
    return [NSScreen mainScreen];
}

// hpReload は保持中の全行をテキストビューへ流し込み、最下部(最新)へスクロールする。
// 高々 kHPMaxEntries 件・コメントは人間の会話速度でしか増えないので、差分更新はせず
// 毎回作り直す(トリム時の行削除を考えなくてよい)。
static void hpReload(void) {
    if (!gText) return;
    NSMutableAttributedString *all = [[NSMutableAttributedString alloc] init];
    if (!gEntries || gEntries.count == 0) {
        NSAttributedString *empty = [[NSAttributedString alloc]
            initWithString:@"まだコメントはありません"
            attributes:@{ NSFontAttributeName: [NSFont systemFontOfSize:12.5],
                          NSForegroundColorAttributeName: [NSColor colorWithWhite:1 alpha:0.4] }];
        [all appendAttributedString:empty];
        [empty release];
    }
    for (NSAttributedString *e in gEntries) {
        [all appendAttributedString:e];
    }
    [gText.textStorage setAttributedString:all];
    [all release];
    [gText scrollRangeToVisible:NSMakeRange(gText.textStorage.length, 0)];
}

static void historyCreate(void) {
    if (gPanel) return;

    // 非アクティブ化パネル: 前面アプリ(会議等)をアクティブのままスクロール・コピー
    // だけ受ける(inputbar と同じ Spotlight 方式)。
    UTHistoryPanel *p = [[UTHistoryPanel alloc] initWithContentRect:NSMakeRect(0, 0, kHPW, kHPH)
        styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
        backing:NSBackingStoreBuffered defer:NO];
    p.opaque = NO;
    p.backgroundColor = [NSColor clearColor];
    p.hasShadow = YES;
    p.level = NSScreenSaverWindowLevel;
    // NSPanel は既定 hidesOnDeactivate=YES で、非アクティブな常駐アプリではパネルが
    // 出ない(inputbar / voicebar と同じ罠)ので切る。
    p.hidesOnDeactivate = NO;
    p.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces
        | NSWindowCollectionBehaviorFullScreenAuxiliary;
    // 履歴は自分用のカンペなので画面共有には映さない。GAYATALK_CAPTURE 起動時だけ
    // スクリーンショットに映す(README 等のドキュメント撮影用。既定は安全側のまま)。
    const char *cap = getenv("GAYATALK_CAPTURE");
    p.sharingType = (cap && *cap) ? NSWindowSharingReadOnly : NSWindowSharingNone;
    p.releasedWhenClosed = NO;

    NSView *bg = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, kHPW, kHPH)];
    bg.wantsLayer = YES;
    bg.layer.backgroundColor = [[NSColor colorWithWhite:0.1 alpha:0.92] CGColor];
    bg.layer.cornerRadius = 12;
    p.contentView = bg;

    // ヘッダ: タイトルと閉じ方のヒント(パネル上端に固定)。
    NSTextField *title = [NSTextField labelWithString:@"コメント履歴"];
    title.frame = NSMakeRect(16, kHPH - 30, 160, 18);
    title.font = [NSFont boldSystemFontOfSize:12];
    title.textColor = [NSColor colorWithWhite:1 alpha:0.92];
    title.autoresizingMask = NSViewMinYMargin;
    [bg addSubview:title];

    NSTextField *hint = [NSTextField labelWithString:@"Esc で閉じる"];
    hint.frame = NSMakeRect(kHPW - 116, kHPH - 30, 100, 18);
    hint.font = [NSFont systemFontOfSize:11];
    hint.textColor = [NSColor colorWithWhite:1 alpha:0.4];
    hint.alignment = NSTextAlignmentRight;
    hint.autoresizingMask = NSViewMinYMargin | NSViewMinXMargin;
    [bg addSubview:hint];

    NSView *rule = [[NSView alloc] initWithFrame:NSMakeRect(12, kHPH - 38, kHPW - 24, 1)];
    rule.wantsLayer = YES;
    rule.layer.backgroundColor = [[NSColor colorWithWhite:1 alpha:0.15] CGColor];
    rule.autoresizingMask = NSViewWidthSizable | NSViewMinYMargin;
    [bg addSubview:rule];

    NSScrollView *sv = [[NSScrollView alloc]
        initWithFrame:NSMakeRect(12, 10, kHPW - 24, kHPH - 54)];
    sv.hasVerticalScroller = YES;
    sv.drawsBackground = NO;
    sv.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;

    NSTextView *tv = [[NSTextView alloc] initWithFrame:NSMakeRect(0, 0, kHPW - 24, kHPH - 54)];
    tv.editable = NO;
    tv.selectable = YES; // 選択して Cmd+C でコピーできる(オーバーレイの ⌥クリックと同用途)
    tv.drawsBackground = NO;
    tv.textContainerInset = NSMakeSize(2, 4);
    tv.minSize = NSMakeSize(0, 0);
    tv.maxSize = NSMakeSize(FLT_MAX, FLT_MAX);
    tv.verticallyResizable = YES;
    tv.horizontallyResizable = NO;
    tv.autoresizingMask = NSViewWidthSizable;
    tv.textContainer.widthTracksTextView = YES;
    sv.documentView = tv;
    [bg addSubview:sv];

    gText = tv;
    gPanel = p;

    // メニューバー常駐アプリには標準の「編集」メニューが無く Cmd+C/A がルーティング
    // されないため、パネルがキーの間だけ自前で拾う(inputbar_darwin.m と同じ手法)。
    // Esc もここで拾って閉じる(NSTextView は cancelOperation を消費しないため)。
    [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown
        handler:^NSEvent *(NSEvent *e) {
            if (!gPanel || !gPanel.isVisible || !gPanel.isKeyWindow) return e;
            if (e.keyCode == 53) { // Esc
                [gPanel orderOut:nil];
                return nil;
            }
            if ((e.modifierFlags & NSEventModifierFlagCommand) &&
                !(e.modifierFlags & (NSEventModifierFlagControl | NSEventModifierFlagOption))) {
                NSString *k = e.charactersIgnoringModifiers.lowercaseString;
                SEL sel = NULL;
                if ([k isEqualToString:@"c"]) sel = @selector(copy:);
                else if ([k isEqualToString:@"a"]) sel = @selector(selectAll:);
                if (sel && [gPanel.firstResponder tryToPerform:sel with:nil]) {
                    return nil; // 消費(下のアプリへの二重送出を防ぐ)
                }
            }
            return e;
        }];
}

// historyAppend はコメントを 1 件、履歴へ積む(ts は "HH:MM" 形式の受信時刻、
// name は表示名で匿名なら空、r/g/b は送信者色 0..1)。パネル表示中なら即反映する。
void historyAppend(const char *ts, const char *name, const char *text, double r, double g, double b) {
    NSString *t = [NSString stringWithUTF8String:ts ?: ""];
    NSString *n = [NSString stringWithUTF8String:name ?: ""];
    NSString *body = [NSString stringWithUTF8String:text ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{
        if (!gEntries) gEntries = [[NSMutableArray alloc] init];

        // 行間を少し空け、折り返し行は時刻の分だけ字下げして各コメントの頭を揃える。
        NSMutableParagraphStyle *ps = [[NSMutableParagraphStyle alloc] init];
        ps.paragraphSpacing = 5;
        ps.headIndent = 40;
        NSFont *f = [NSFont systemFontOfSize:12.5];

        NSMutableAttributedString *line = [[NSMutableAttributedString alloc] init];
        NSAttributedString *time = [[NSAttributedString alloc]
            initWithString:[t stringByAppendingString:@" "]
            attributes:@{ NSFontAttributeName: [NSFont monospacedDigitSystemFontOfSize:12.5
                                                    weight:NSFontWeightRegular],
                          NSForegroundColorAttributeName: [NSColor colorWithWhite:1 alpha:0.45],
                          NSParagraphStyleAttributeName: ps }];
        [line appendAttributedString:time];
        [time release];
        if (n.length > 0) {
            // 記名ルームの送信者名。オーバーレイと同じく送信者色で塗る(匿名ルームでも
            // 本文の色で同一人物を追えるよう、名前が無ければ本文側に色を移す)。
            NSAttributedString *who = [[NSAttributedString alloc]
                initWithString:[NSString stringWithFormat:@"[%@] ", n]
                attributes:@{ NSFontAttributeName: f,
                              NSForegroundColorAttributeName:
                                  [NSColor colorWithSRGBRed:r green:g blue:b alpha:1],
                              NSParagraphStyleAttributeName: ps }];
            [line appendAttributedString:who];
            [who release];
        }
        NSColor *bodyColor = (n.length > 0)
            ? [NSColor colorWithWhite:1 alpha:0.92]
            : [NSColor colorWithSRGBRed:r green:g blue:b alpha:1];
        NSAttributedString *msg = [[NSAttributedString alloc]
            initWithString:[body stringByAppendingString:@"\n"]
            attributes:@{ NSFontAttributeName: f,
                          NSForegroundColorAttributeName: bodyColor,
                          NSParagraphStyleAttributeName: ps }];
        [line appendAttributedString:msg];
        [msg release];
        [ps release];

        [gEntries addObject:line];
        [line release]; // 配列が保持する
        if (gEntries.count > kHPMaxEntries) [gEntries removeObjectAtIndex:0];

        if (gPanel && gPanel.isVisible) hpReload();
    });
}

// historyToggle はパネルの表示/非表示を切り替える(ホットキー・メニューから呼ぶ)。
void historyToggle(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        historyCreate();
        if (!gPanel) return;
        if (gPanel.isVisible) {
            [gPanel orderOut:nil];
            return;
        }
        // 呼び出すたびに、いまカーソルがあるモニターの右端へ寄せて出す
        // (オーバーレイが流れる中央部や、下部中央の入力バー・音声バーを塞がない)。
        NSScreen *scr = currentScreen();
        if (scr) {
            NSRect sf = scr.visibleFrame; // メニューバー・Dock は避ける
            CGFloat h = MIN(kHPH, sf.size.height - 40);
            [gPanel setFrame:NSMakeRect(NSMaxX(sf) - kHPW - 16,
                                        sf.origin.y + (sf.size.height - h) / 2, kHPW, h)
                     display:NO];
        }
        hpReload();
        // makeKeyAndOrderFront は非アクティブなアプリからだと画面に出ないことがある
        // (inputbar と同じ)。orderFrontRegardless で出してからキーウィンドウにする。
        [gPanel orderFrontRegardless];
        [gPanel makeKeyWindow];
    });
}
