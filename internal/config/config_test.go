package config

import (
	"encoding/json"
	"testing"
)

// 旧スキーマ(フラットな音声キー・room.input_hotkey)の config が
// 新スキーマへ正しく写ることを確認する。
func TestMigrateLegacy(t *testing.T) {
	legacy := []byte(`{
		"room": {
			"server": "https://example.workers.dev",
			"input_hotkey": {"mods": [], "key": "rightcmd"}
		},
		"voice_input": "off",
		"listen_mode": "vad",
		"hotkey": {"mods": ["rightshift"], "key": "rightcmd"},
		"input_device": "MacBook",
		"voice_bar": false,
		"sound": {"enabled": false, "on": "Ping", "off": "Bottle"},
		"min_duration_ms": 500,
		"vad": {"threshold": 0.02},
		"whisper_bin": "/opt/homebrew/bin/whisper-cli",
		"whisper_model": "~/models/ggml.bin",
		"language": "auto",
		"whisper_prompt": "hint",
		"whisper_beam_size": 3,
		"whisper_no_speech_thold": 0.7
	}`)
	plain, err := migrateLegacy(legacy)
	if err != nil {
		t.Fatalf("migrateLegacy: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(plain, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Room.Server != "https://example.workers.dev" {
		t.Errorf("room.server = %q", cfg.Room.Server)
	}
	if cfg.InputHotkey.Key != "rightcmd" {
		t.Errorf("input_hotkey.key = %q(room.input_hotkey から写るはず)", cfg.InputHotkey.Key)
	}
	if cfg.Voice.Input != VoiceOff {
		t.Errorf("voice.input = %q", cfg.Voice.Input)
	}
	if cfg.Voice.ListenMode != "vad" {
		t.Errorf("voice.listen_mode = %q", cfg.Voice.ListenMode)
	}
	if cfg.Voice.Hotkey.String() != "rightshift+rightcmd" {
		t.Errorf("voice.hotkey = %q", cfg.Voice.Hotkey)
	}
	if cfg.Voice.Device != "MacBook" {
		t.Errorf("voice.device = %q", cfg.Voice.Device)
	}
	if cfg.Voice.Bar {
		t.Error("voice.bar = true(false のはず)")
	}
	if cfg.Voice.MinDurationMs != 500 {
		t.Errorf("voice.min_duration_ms = %d", cfg.Voice.MinDurationMs)
	}
	if cfg.Voice.VAD.Threshold != 0.02 {
		t.Errorf("voice.vad.threshold = %v", cfg.Voice.VAD.Threshold)
	}
	// 旧フラットスキーマの sound はトップレベルのまま(現行と同じ場所)。
	if cfg.Sound.Enabled || cfg.Sound.On != "Ping" {
		t.Errorf("sound = %+v", cfg.Sound)
	}
	if cfg.Whisper.Bin != "/opt/homebrew/bin/whisper-cli" {
		t.Errorf("whisper.bin = %q", cfg.Whisper.Bin)
	}
	if cfg.Whisper.Model != "~/models/ggml.bin" {
		t.Errorf("whisper.model = %q", cfg.Whisper.Model)
	}
	if cfg.Whisper.Language != "auto" {
		t.Errorf("whisper.language = %q", cfg.Whisper.Language)
	}
	if cfg.Whisper.Prompt != "hint" {
		t.Errorf("whisper.prompt = %q", cfg.Whisper.Prompt)
	}
	if cfg.Whisper.BeamSize != 3 {
		t.Errorf("whisper.beam_size = %d", cfg.Whisper.BeamSize)
	}
	if cfg.Whisper.NoSpeechThold != 0.7 {
		t.Errorf("whisper.no_speech_thold = %v", cfg.Whisper.NoSpeechThold)
	}
}

// 新旧キーが混在した場合は新キーが勝つ。
func TestMigrateLegacyNewKeyWins(t *testing.T) {
	mixed := []byte(`{
		"voice_input": "on",
		"voice": {"input": "off", "sound": {"enabled": true, "on": "Frog"}},
		"whisper_model": "old.bin",
		"whisper": {"model": "new.bin"}
	}`)
	plain, err := migrateLegacy(mixed)
	if err != nil {
		t.Fatalf("migrateLegacy: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(plain, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Voice.Input != VoiceOff {
		t.Errorf("voice.input = %q(新キーが勝つはず)", cfg.Voice.Input)
	}
	if cfg.Whisper.Model != "new.bin" {
		t.Errorf("whisper.model = %q(新キーが勝つはず)", cfg.Whisper.Model)
	}
	// 効果音が音声専用だった時期の voice.sound はトップレベルへ写る。
	if !cfg.Sound.Enabled || cfg.Sound.On != "Frog" {
		t.Errorf("sound = %+v(voice.sound から写るはず)", cfg.Sound)
	}
}

// 新スキーマの config はそのまま素通りする。
func TestMigrateLegacyPassthrough(t *testing.T) {
	fresh := []byte(`{
		"input_hotkey": {"mods": [], "key": "rightcmd"},
		"voice": {"input": "auto", "hotkey": {"mods": ["rightshift"], "key": "rightcmd"}},
		"whisper": {"model": "m.bin", "language": "ja"}
	}`)
	plain, err := migrateLegacy(fresh)
	if err != nil {
		t.Fatalf("migrateLegacy: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(plain, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.InputHotkey.Key != "rightcmd" || cfg.Voice.Input != VoiceAuto || cfg.Whisper.Model != "m.bin" {
		t.Errorf("新スキーマが素通りしていない: %+v", cfg)
	}
}

// config.example.json が新スキーマとして正しくパースできることを確認する
// (example を更新したときの壊れ検知)。
func TestExampleConfig(t *testing.T) {
	t.Setenv("GAYATALK_CONFIG", "../../config.example.json")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.InputHotkey.String() != "rightcmd" {
		t.Errorf("input_hotkey = %q", cfg.InputHotkey)
	}
	if cfg.Voice.Input != VoiceAuto || cfg.Voice.Hotkey.String() != "rightshift+rightcmd" || cfg.Voice.ListenMode != "vad" || !cfg.Voice.Bar {
		t.Errorf("voice = %+v", cfg.Voice)
	}
	if cfg.Whisper.Bin == "" || cfg.Whisper.Model == "" || cfg.Whisper.Language != "ja" {
		t.Errorf("whisper = %+v", cfg.Whisper)
	}
	if cfg.Enhance.Model != "qwen2.5:3b" || cfg.Enhance.Backend != "ollama" {
		t.Errorf("enhance = %+v", cfg.Enhance)
	}
	if !cfg.Sound.Enabled || cfg.Sound.InputOpen != "Pop" || cfg.Sound.InputClose != "Bottle" {
		t.Errorf("sound = %+v", cfg.Sound)
	}
}

// 環境変数 GAYATALK_ROOM_SERVER が config の room.server を上書きする。
func TestEnvRoomServer(t *testing.T) {
	t.Setenv("GAYATALK_CONFIG", "../../config.example.json")
	t.Setenv("GAYATALK_ROOM_SERVER", "https://env.example.workers.dev")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Room.Server != "https://env.example.workers.dev" {
		t.Errorf("room.server = %q(環境変数が優先のはず)", cfg.Room.Server)
	}
}
