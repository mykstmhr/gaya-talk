// inputbar の ObjC 実装。cgo は .go の preamble を複数の翻訳単位へ複製するため、
// 外部リンケージを持つ ObjC クラスを preamble に書くと重複シンボルになる。
// クラス定義はこの .m に置き、.go からは C 関数だけを呼ぶ。
#import <AppKit/AppKit.h>
#import <QuartzCore/QuartzCore.h>

extern void inputbarGoSubmit(char *text);

// borderless パネルは既定でキーウィンドウになれないため上書きする。
@interface UTPanel : NSPanel
@end
@implementation UTPanel
- (BOOL)canBecomeKeyWindow { return YES; }
@end

@interface UTBarController : NSObject <NSTextFieldDelegate>
@property (strong) UTPanel *panel;
@property (strong) NSTextField *field;
@end

static UTBarController *gBar = nil;

@implementation UTBarController
- (void)submit:(id)sender {
    NSString *text = [self.field.stringValue
        stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
    self.field.stringValue = @"";
    [self.panel orderOut:nil];
    if (text.length > 0) inputbarGoSubmit((char *)[text UTF8String]);
}
- (BOOL)control:(NSControl *)control textView:(NSTextView *)tv doCommandBySelector:(SEL)sel {
    if (sel == @selector(cancelOperation:)) { // Esc でキャンセル
        self.field.stringValue = @"";
        [self.panel orderOut:nil];
        return YES;
    }
    return NO;
}
@end

// currentScreen はいまマウスカーソルがあるモニターを返す(取れなければ mainScreen)。
// 入力バーは「呼び出した瞬間に作業していたモニター」に出したいので、キーフォーカス
// ではなくカーソル位置で決める。
static NSScreen* currentScreen(void) {
    NSPoint mouse = [NSEvent mouseLocation];
    for (NSScreen *s in [NSScreen screens]) {
        if (NSMouseInRect(mouse, s.frame, NO)) return s;
    }
    return [NSScreen mainScreen];
}

static const CGFloat kBarW = 560, kBarH = 46;

static void inputbarCreate(void) {
    if (gBar) return;
    NSScreen *scr = currentScreen();
    if (!scr) return;
    NSRect sf = scr.frame;
    CGFloat w = kBarW, h = kBarH;
    NSRect frame = NSMakeRect(sf.origin.x + (sf.size.width - w) / 2,
                              sf.origin.y + 140, w, h);

    // 非アクティブ化パネル: 前面アプリ(会議等)をアクティブのままキー入力だけ受ける(Spotlight 方式)。
    UTPanel *p = [[UTPanel alloc] initWithContentRect:frame
        styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
        backing:NSBackingStoreBuffered defer:NO];
    p.opaque = NO;
    p.backgroundColor = [NSColor clearColor];
    p.hasShadow = YES;
    p.level = NSScreenSaverWindowLevel;
    p.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces
        | NSWindowCollectionBehaviorFullScreenAuxiliary;
    p.sharingType = NSWindowSharingNone; // 入力途中の文面を画面共有に映さない
    p.releasedWhenClosed = NO;

    NSView *bg = [[NSView alloc] initWithFrame:NSMakeRect(0, 0, w, h)];
    bg.wantsLayer = YES;
    bg.layer.backgroundColor = [[NSColor colorWithWhite:0.1 alpha:0.85] CGColor];
    bg.layer.cornerRadius = 12;
    p.contentView = bg;

    UTBarController *c = [[UTBarController alloc] init];
    NSTextField *f = [[NSTextField alloc] initWithFrame:NSMakeRect(16, 9, w - 32, h - 18)];
    f.bezeled = NO;
    f.bordered = NO;
    f.drawsBackground = NO;
    f.focusRingType = NSFocusRingTypeNone;
    f.font = [NSFont systemFontOfSize:20];
    f.textColor = [NSColor whiteColor];
    f.placeholderAttributedString = [[NSAttributedString alloc]
        initWithString:@"コメントを流す…"
        attributes:@{ NSForegroundColorAttributeName: [NSColor colorWithWhite:1 alpha:0.4],
                      NSFontAttributeName: [NSFont systemFontOfSize:20] }];
    f.target = c;
    f.action = @selector(submit:);
    f.delegate = c;
    [bg addSubview:f];

    c.panel = p;
    c.field = f;
    gBar = c;
}

void inputbarToggle(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        inputbarCreate();
        if (!gBar) return;
        if (gBar.panel.isVisible) {
            gBar.field.stringValue = @"";
            [gBar.panel orderOut:nil];
            return;
        }
        // 呼び出すたびに、いまカーソルがあるモニターの下部中央へ移動してから出す。
        NSScreen *scr = currentScreen();
        if (scr) {
            NSRect sf = scr.frame;
            [gBar.panel setFrame:NSMakeRect(sf.origin.x + (sf.size.width - kBarW) / 2,
                                            sf.origin.y + 140, kBarW, kBarH)
                         display:NO];
        }
        [gBar.panel makeKeyAndOrderFront:nil];
        [gBar.panel makeFirstResponder:gBar.field];
    });
}
