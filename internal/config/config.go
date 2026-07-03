// Package config はアプリ設定の読み込みを担う。
// モデルパスなどは環境変数でも上書きできる。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config はアプリ全体の設定。発話・入力はニコニコ風オーバーレイに流れ、
// ルーム(中継サーバ)を介してメンバーと共有できる。
type Config struct {
	// room(オーバーレイ共有)の設定。
	Room RoomConfig `json:"room"`

	// VoiceInput は音声入力(マイク→文字起こし)の使用可否。
	// "auto"(既定): 出力がスピーカー等のときは自動でオフ、イヤホン等のときはオン。
	// "on": 常に有効 / "off": 常に無効(文字入力バーのみ・マイク/whisper 不要)。
	// 旧来の true/false もそれぞれ on/off として受け付ける。
	VoiceInput VoiceMode `json:"voice_input"`

	// whisper-cli の実行パス(PATH 上にあれば "whisper-cli" のままで良い)。
	WhisperBin string `json:"whisper_bin"`
	// ggml モデルファイルのパス。
	WhisperModel string `json:"whisper_model"`
	// 録音に使う入力デバイス名(部分一致)。空ならシステム既定。
	// Bluetooth イヤホン以外(内蔵マイク等)を指定すると、イヤホンが通話モードに
	// 切り替わって再生音が途切れるのを防げる。`ura-talk devices` で候補を確認できる。
	InputDevice string `json:"input_device"`
	// 文字起こし言語。日本語なら "ja"、自動判定なら "auto"。
	Language string `json:"language"`
	// 録音音声の自動ゲイン(正規化)。小声・ボソボソの認識改善用。
	Gain GainConfig `json:"gain"`
	// whisper の初期プロンプト(口語・語彙のヒント。誤認識低減用)。
	WhisperPrompt string `json:"whisper_prompt"`
	// whisper のビーム幅(0 で既定 5)。上げると精度↑・速度↓。
	WhisperBeamSize int `json:"whisper_beam_size"`
	// whisper の no-speech 閾値(0 で既定 0.6)。下げると小声を拾いやすいが幻聴増。
	WhisperNoSpeechThold float64 `json:"whisper_no_speech_thold"`
	// 文字起こし結果のローカル LLM(Ollama)整形。日本語の読みやすさ向上用。
	Enhance EnhanceConfig `json:"enhance"`
	Emoji   EmojiConfig   `json:"emoji"`
	// 入力方式。"ptt"(押している間だけ録音)または "vad"(キーでリッスンを
	// トグルし、無音で自動区切りして発話ごとに投稿)。
	ListenMode string `json:"listen_mode"`
	// VAD 区切りのパラメータ(listen_mode が "vad" のとき有効)。
	VAD VADConfig `json:"vad"`
	// ホットキー。ptt では押下中に録音、vad ではリッスンの開始/停止トグル。
	// 単体修飾キー(rightcmd 等)も指定可(mods は空にする)。
	Hotkey Hotkey `json:"hotkey"`
	// 有効化/無効化時に鳴らす効果音。
	Sound SoundConfig `json:"sound"`
	// この長さ未満の録音は無視する(ptt の誤爆防止)。
	MinDurationMs int `json:"min_duration_ms"`
}

// VoiceMode は音声入力の使用可否("auto" / "on" / "off")。
type VoiceMode string

const (
	VoiceAuto VoiceMode = "auto"
	VoiceOn   VoiceMode = "on"
	VoiceOff  VoiceMode = "off"
)

// UnmarshalJSON は "auto"/"on"/"off" に加え、旧来の bool(true/false)も受け付ける。
func (m *VoiceMode) UnmarshalJSON(b []byte) error {
	switch strings.TrimSpace(string(b)) {
	case "true":
		*m = VoiceOn
		return nil
	case "false":
		*m = VoiceOff
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch v := VoiceMode(strings.ToLower(strings.TrimSpace(s))); v {
	case VoiceAuto, VoiceOn, VoiceOff:
		*m = v
	case "":
		*m = VoiceAuto
	default:
		return fmt.Errorf("voice_input は auto/on/off のいずれかです(現在: %q)", s)
	}
	return nil
}

// RoomConfig は room(ニコニコ風オーバーレイ共有)の設定。
type RoomConfig struct {
	// Server は中継サーバ(Cloudflare Workers)の URL。例 "https://ura-talk-room.<name>.workers.dev"。
	// 空でもオーバーレイ自体は動く(ルーム未参加=自分の画面にだけ流れるソロモード)。
	Server string `json:"server"`
	// InputHotkey は文字入力バーを出すキー(音声トリガの hotkey とは別)。
	InputHotkey Hotkey `json:"input_hotkey"`
	// DisplayName は記名モードのルームで名乗る表示名(空なら記名ルームでも匿名)。
	DisplayName string `json:"display_name"`
	// SlackBotToken は Slack ミラー(コメントをチャンネルへ転送)に使う bot token(xoxb)。
	// 設定した人だけがミラー役になれる。空ならミラー機能は出ない。
	// 環境変数 URATALK_SLACK_BOT_TOKEN でも渡せる(config に平文で書きたくない場合)。
	SlackBotToken string `json:"slack_bot_token"`
	// SlackChannel は Slack ミラーの投稿先チャンネル(ID "C0123..." 推奨、"#general" も可)。
	SlackChannel string `json:"slack_channel"`
}

// EnhanceConfig はローカル LLM(Ollama)による文字起こし整形の設定。
type EnhanceConfig struct {
	Enabled  bool   `json:"enabled"`  // 整形を有効にするか
	Backend  string `json:"backend"`  // 現状 "ollama"
	Endpoint string `json:"endpoint"` // Ollama エンドポイント
	Model    string `json:"model"`    // 使うモデル名(例 qwen2.5:7b)
	Prompt   string `json:"prompt"`   // 整形プロンプト(空で既定)
	// AllowRemote は endpoint に非ローカル(localhost 以外)のホストを許すか。
	// 既定 false では発話本文が外部へ出るのを防ぐため非ローカル endpoint を拒否する。
	// 意図的にリモート Ollama を使う場合のみ true にする。
	AllowRemote bool `json:"allow_remote"`
}

// EmojiConfig は発話内容に応じて末尾に絵文字を付ける設定(本文は変えない)。
// 絵文字の選定は enhance と同じ Ollama を使う。
type EmojiConfig struct {
	// Mode は付与モード: off(付けない=ビジネスライク)/ light(控えめ)/ cheerful(積極的に明るく)。
	Mode string `json:"mode"`
}

// GainConfig は録音音声のピーク正規化(自動ゲイン)設定。
type GainConfig struct {
	Enabled    bool    `json:"enabled"`     // 自動ゲインを有効にするか
	TargetPeak float64 `json:"target_peak"` // 正規化後のピーク目標(0..1)
	MaxGain    float64 `json:"max_gain"`    // 増幅倍率の上限(無音の過剰増幅防止)
}

// VADConfig は無音区切りのパラメータ。
type VADConfig struct {
	Threshold    float64 `json:"threshold"`      // 発話とみなす RMS の下限(0..1)
	MinSpeechMs  int     `json:"min_speech_ms"`  // これ未満の発話は捨てる
	SilenceMs    int     `json:"silence_ms"`     // この無音長で 1 発話を区切る
	MaxSegmentMs int     `json:"max_segment_ms"` // 1 発話の最大長
	PrerollMs    int     `json:"preroll_ms"`     // 発話開始の手前を含める量
}

// SoundConfig は効果音の設定。On/Off は /System/Library/Sounds/ の名前(拡張子なし)。
type SoundConfig struct {
	Enabled bool   `json:"enabled"`
	On      string `json:"on"`  // 有効化(リッスン開始/録音開始)時
	Off     string `json:"off"` // 無効化(停止)時
}

// Hotkey は修飾キーとメインキーの組。
type Hotkey struct {
	Mods []string `json:"mods"` // 例: ["ctrl","shift"]
	Key  string   `json:"key"`  // 例: "space"
}

// String は "ctrl+shift+space" のような表示用文字列を返す。
func (h Hotkey) String() string {
	return strings.Join(append(append([]string{}, h.Mods...), h.Key), "+")
}

// Load は設定を読み込む。検索順は環境変数 URATALK_CONFIG → ./config.json →
// ~/.config/ura-talk/config.json。見つからない項目はデフォルトを使う。
func Load() (*Config, error) {
	cfg := &Config{
		VoiceInput:      VoiceAuto,
		WhisperBin:      "whisper-cli",
		Language:        "ja",
		MinDurationMs:   300,
		ListenMode:      "ptt",
		WhisperBeamSize: 5,
		Gain:            GainConfig{Enabled: true, TargetPeak: 0.95, MaxGain: 12},
		Enhance:         EnhanceConfig{Enabled: true, Backend: "ollama", Endpoint: "http://localhost:11434", Model: "qwen2.5:7b"},
		Emoji:           EmojiConfig{Mode: "off"},
		Hotkey:          Hotkey{Mods: nil, Key: "rightcmd"},
		Room:            RoomConfig{InputHotkey: Hotkey{Mods: []string{"rightshift"}, Key: "rightcmd"}},
		Sound:           SoundConfig{Enabled: true, On: "Submarine", Off: "Bottle"},
		VAD: VADConfig{
			Threshold:    0.01,
			MinSpeechMs:  300,
			SilenceMs:    700,
			MaxSegmentMs: 15000,
			PrerollMs:    300,
		},
	}

	path := os.Getenv("URATALK_CONFIG")
	if path == "" {
		if _, err := os.Stat("config.json"); err == nil {
			path = "config.json"
		} else if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".config", "ura-talk", "config.json")
		}
	}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			// JSONC(コメント・末尾カンマ)を許可するため、素の JSON へ落としてから読む。
			if err := json.Unmarshal(stripJSONC(data), cfg); err != nil {
				return nil, fmt.Errorf("設定ファイル %s のパースに失敗: %w", path, err)
			}
		}
	}

	if v := os.Getenv("URATALK_WHISPER_MODEL"); v != "" {
		cfg.WhisperModel = v
	}
	if v := os.Getenv("URATALK_SLACK_BOT_TOKEN"); v != "" {
		cfg.Room.SlackBotToken = v
	}
	cfg.WhisperModel = expandHome(cfg.WhisperModel)
	return cfg, nil
}

// expandHome は先頭の "~" をホームディレクトリに展開する。
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
