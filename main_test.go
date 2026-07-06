package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mykstmhr/ura-talk/internal/config"
)

func TestTruncRunes(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"abcdef", 10, "abcdef"},
		{"abcdef", 6, "abcdef"},
		{"abcdef", 3, "abc…"},
		{"あいうえお", 3, "あいう…"}, // バイトでなくルーンで数える
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := truncRunes(c.in, c.max); got != c.want {
			t.Errorf("truncRunes(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestBodyForLog(t *testing.T) {
	// 既定(URATALK_DEBUG なし)では発話本文をログに出さず文字数だけにする。
	t.Setenv("URATALK_DEBUG", "")
	os.Unsetenv("URATALK_DEBUG") // t.Setenv で復元は担保しつつ、空でなく未設定にする
	body := "会議の機微な発言"
	got := bodyForLog(body)
	if strings.Contains(got, body) {
		t.Errorf("既定では本文を出さないはず: %q", got)
	}
	if !strings.Contains(got, "8") {
		t.Errorf("文字数(8)を含むはず: %q", got)
	}

	t.Setenv("URATALK_DEBUG", "1")
	if got := bodyForLog(body); !strings.Contains(got, body) {
		t.Errorf("デバッグ時は本文を出すはず: %q", got)
	}
}

func TestPrettyHotkey(t *testing.T) {
	cases := []struct {
		h    config.Hotkey
		want string
	}{
		{config.Hotkey{Key: "rightcmd"}, "右⌘"},
		{config.Hotkey{Mods: []string{"rightshift"}, Key: "rightcmd"}, "右⇧+右⌘"},
		{config.Hotkey{Mods: []string{"ctrl", "shift"}, Key: "space"}, "⌃+⇧+space"}, // 未知キーはそのまま
	}
	for _, c := range cases {
		if got := prettyHotkey(c.h); got != c.want {
			t.Errorf("prettyHotkey(%v) = %q, want %q", c.h, got, c.want)
		}
	}
}

func TestVoiceUnavailable(t *testing.T) {
	model := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(model, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	if got := voiceUnavailable(cfg); !strings.Contains(got, "whisper.model") {
		t.Errorf("model 未設定の理由を返すはず: %q", got)
	}

	cfg.Whisper.Model = filepath.Join(t.TempDir(), "missing.bin")
	if got := voiceUnavailable(cfg); !strings.Contains(got, "見つかりません") {
		t.Errorf("model 不在の理由を返すはず: %q", got)
	}

	cfg.Whisper.Model = model
	cfg.Whisper.Bin = "no-such-whisper-cli-binary"
	if got := voiceUnavailable(cfg); !strings.Contains(got, "whisper-cli") {
		t.Errorf("whisper-cli 不在の理由を返すはず: %q", got)
	}

	// すべて揃っていれば空(= 音声を使える)。bin は確実に存在する /bin/ls で代用。
	cfg.Whisper.Bin = "/bin/ls"
	if got := voiceUnavailable(cfg); got != "" {
		t.Errorf("揃っていれば空のはず: %q", got)
	}
}
