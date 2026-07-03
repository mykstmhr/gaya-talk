// テキスト入力ダイアログ(NSAlert + 入力欄)。メニューバー常駐アプリから
// ルーム URL の手入力などに使う。
#import <AppKit/AppKit.h>
#include <string.h>

// dialogPrompt はモーダルの入力ダイアログを出し、OK なら入力文字列(呼び出し側が free)、
// キャンセルなら NULL を返す。どのスレッドから呼んでもよい(メインへ同期ディスパッチ)。
char* dialogPrompt(const char *title, const char *message, const char *placeholder,
                   const char *initial, const char *okLabel) {
    __block char *result = NULL;
    dispatch_sync(dispatch_get_main_queue(), ^{
        NSAlert *alert = [[NSAlert alloc] init];
        alert.messageText = [NSString stringWithUTF8String:title ?: ""];
        alert.informativeText = [NSString stringWithUTF8String:message ?: ""];
        [alert addButtonWithTitle:[NSString stringWithUTF8String:okLabel ?: "OK"]];
        [alert addButtonWithTitle:@"キャンセル"];

        NSTextField *input = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 420, 24)];
        input.placeholderString = [NSString stringWithUTF8String:placeholder ?: ""];
        input.stringValue = [NSString stringWithUTF8String:initial ?: ""];
        alert.accessoryView = input;
        alert.window.initialFirstResponder = input;

        // 常駐(accessory)アプリはそのままだとモーダルが背面に出るので前面化する。
        [NSApp activateIgnoringOtherApps:YES];

        // メニューバー常駐アプリには標準の「編集」メニューが無く、Cmd+V などの
        // 編集ショートカットがルーティングされない(貼り付けができない)。
        // モーダル中だけキーイベントを監視し、Cmd+V/C/X/A を first responder
        // (テキスト欄のフィールドエディタ)へ直接転送する。
        id monitor = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown
            handler:^NSEvent *(NSEvent *e) {
                if ((e.modifierFlags & NSEventModifierFlagCommand) &&
                    !(e.modifierFlags & (NSEventModifierFlagControl | NSEventModifierFlagOption))) {
                    NSString *k = e.charactersIgnoringModifiers.lowercaseString;
                    SEL sel = NULL;
                    if ([k isEqualToString:@"v"]) sel = @selector(paste:);
                    else if ([k isEqualToString:@"c"]) sel = @selector(copy:);
                    else if ([k isEqualToString:@"x"]) sel = @selector(cut:);
                    else if ([k isEqualToString:@"a"]) sel = @selector(selectAll:);
                    if (sel && [alert.window.firstResponder tryToPerform:sel with:nil]) {
                        return nil; // 消費(二重入力を防ぐ)
                    }
                }
                return e;
            }];

        NSModalResponse resp = [alert runModal];
        [NSEvent removeMonitor:monitor];
        if (resp == NSAlertFirstButtonReturn) {
            const char *s = [input.stringValue UTF8String];
            if (s) result = strdup(s);
        }
        [input release];
        [alert release];
    });
    return result;
}
