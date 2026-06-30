package config

import "testing"

func TestSendKeyFor(t *testing.T) {
	k := KeystrokeConfig{
		AutoEnter: false, // send_key 未指定時の既定 = none
		SendKey:   "",    // 既定 send_key も未指定
		Overrides: []KeystrokeOverride{
			{App: "Slack", SendKey: "enter"},                 // 名前一致
			{App: "com.google.Chrome", SendKey: "cmd+enter"}, // bundle id 一致 + 表記
			{App: " Notion ", SendKey: "shift+enter"},        // 前後空白
			{App: "Listed", SendKey: ""},                     // app は一致しても send_key 未指定
		},
	}

	cases := []struct {
		name     string
		appName  string
		bundleID string
		want     string
	}{
		{"名前一致 enter", "Slack", "com.tinyspeck.slackmacgap", "enter"},
		{"名前は大文字小文字無視", "slack", "", "enter"},
		{"bundle id 一致 cmd+enter", "Google Chrome", "com.google.Chrome", "cmd+enter"},
		{"空白入り指定 shift+enter", "Notion", "notion.id", "shift+enter"},
		{"override に app はあるが send_key 未指定 → 既定 none", "Listed", "", "none"},
		{"一致なしは既定 none", "TextEdit", "com.apple.TextEdit", "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := k.SendKeyFor(c.appName, c.bundleID); got != c.want {
				t.Errorf("SendKeyFor(%q, %q) = %q, want %q", c.appName, c.bundleID, got, c.want)
			}
		})
	}
}

func TestSendKeyForDefaults(t *testing.T) {
	// 既定 send_key が auto_enter より優先される。
	if got := (KeystrokeConfig{AutoEnter: true, SendKey: "cmd+enter"}).SendKeyFor("X", ""); got != "cmd+enter" {
		t.Errorf("既定 send_key を使うべき: got %q", got)
	}
	// send_key 未指定なら auto_enter=true → enter。
	if got := (KeystrokeConfig{AutoEnter: true}).SendKeyFor("X", ""); got != "enter" {
		t.Errorf("auto_enter=true は enter にフォールバックすべき: got %q", got)
	}
	// auto_enter=false で何も無ければ none。
	if got := (KeystrokeConfig{}).SendKeyFor("X", ""); got != "none" {
		t.Errorf("既定は none: got %q", got)
	}
	// 不明トークンは無視して既定へ。
	if got := (KeystrokeConfig{SendKey: "banana", AutoEnter: true}).SendKeyFor("X", ""); got != "enter" {
		t.Errorf("不明な send_key は auto_enter にフォールバックすべき: got %q", got)
	}
}

func TestNormalizeSendKey(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"none":           "none",
		"off":            "none",
		"Enter":          "enter",
		"return":         "enter",
		"shift+enter":    "shift+enter",
		"shift-enter":    "shift+enter",
		"cmd+enter":      "cmd+enter",
		"command+enter":  "cmd+enter",
		"  cmd+return  ": "cmd+enter",
		"unknown":        "",
	}
	for in, want := range cases {
		if got := normalizeSendKey(in); got != want {
			t.Errorf("normalizeSendKey(%q) = %q, want %q", in, got, want)
		}
	}
}
