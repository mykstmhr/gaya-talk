package config

import "testing"

// TestSendKeyForMasterOff は auto_enter=false が最上位で、send_key/overrides を無視して
// 常に none になることを確認する。
func TestSendKeyForMasterOff(t *testing.T) {
	k := KeystrokeConfig{
		AutoEnter: false,
		SendKey:   "cmd+enter", // 設定されていても…
		Overrides: []KeystrokeOverride{{App: "Slack", SendKey: "enter"}},
	}
	if got := k.SendKeyFor("Slack", ""); got != "none" {
		t.Errorf("auto_enter=false は send_key/override を無視して none: got %q", got)
	}
	if got := k.SendKeyFor("X", ""); got != "none" {
		t.Errorf("auto_enter=false は常に none: got %q", got)
	}
}

// TestSendKeyForMasterOn は auto_enter=true のとき 既定 enter → send_key → override の順で
// 上書きされることを確認する。
func TestSendKeyForMasterOn(t *testing.T) {
	k := KeystrokeConfig{
		AutoEnter: true,
		SendKey:   "", // 既定 enter
		Overrides: []KeystrokeOverride{
			{App: "Slack", SendKey: "cmd+enter"},       // 名前一致
			{App: "com.apple.Notes", SendKey: "none"},  // bundle id 一致 + 無送信(このアプリだけ off)
			{App: " Cosense ", SendKey: "shift+enter"}, // 前後空白
			{App: "Listed", SendKey: ""},               // app 一致だが未指定 → 既定へ
		},
	}
	cases := []struct {
		name, appName, bundleID, want string
	}{
		{"一致なしは既定 enter", "TextEdit", "com.apple.TextEdit", "enter"},
		{"override enter→cmd+enter", "Slack", "com.tinyspeck.slackmacgap", "cmd+enter"},
		{"名前は大小無視", "slack", "", "cmd+enter"},
		{"bundle id 一致で none(このアプリだけ無送信)", "Notes", "com.apple.Notes", "none"},
		{"空白入り shift+enter", "Cosense", "", "shift+enter"},
		{"override 未指定は全体既定 enter", "Listed", "", "enter"},
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
	// auto_enter=true + 全体 send_key。
	if got := (KeystrokeConfig{AutoEnter: true, SendKey: "cmd+enter"}).SendKeyFor("X", ""); got != "cmd+enter" {
		t.Errorf("全体 send_key を使うべき: got %q", got)
	}
	// auto_enter=true, send_key 未指定 → enter。
	if got := (KeystrokeConfig{AutoEnter: true}).SendKeyFor("X", ""); got != "enter" {
		t.Errorf("既定は enter: got %q", got)
	}
	// auto_enter=false → none。
	if got := (KeystrokeConfig{AutoEnter: false, SendKey: "enter"}).SendKeyFor("X", ""); got != "none" {
		t.Errorf("auto_enter=false は none: got %q", got)
	}
	// 不明トークンは無視 → 既定 enter(auto_enter=true)。
	if got := (KeystrokeConfig{SendKey: "banana", AutoEnter: true}).SendKeyFor("X", ""); got != "enter" {
		t.Errorf("不明な send_key は既定 enter にフォールバックすべき: got %q", got)
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
