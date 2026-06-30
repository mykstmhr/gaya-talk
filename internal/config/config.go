// Package config はアプリ設定の読み込みを担う。
// 機密値(client_secret)やモデルパスは環境変数でも上書きできる。
// user token(xoxp)は config には置かず、Keychain に保存する(tokenstore 参照)。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config はアプリ全体の設定。
type Config struct {
	// Slack アプリの Client ID / Client Secret(OAuth 用)。
	SlackClientID     string `json:"slack_client_id"`
	SlackClientSecret string `json:"slack_client_secret"`
	// 出力先。"slack"(chat.postMessage で自分名義投稿)または
	// "keystroke"(フォーカス中のフィールドへ合成入力)。
	Output string `json:"output"`
	// keystroke 出力の設定。
	Keystroke KeystrokeConfig `json:"keystroke"`

	// 投稿先チャンネル(チャンネル ID "C0123..." 推奨。"#general" でも可)。output が "slack" のとき必須。
	SlackChannel string `json:"slack_channel"`
	// OAuth コールバックを受けるローカル HTTPS サーバのポート。
	OAuthRedirectPort int `json:"oauth_redirect_port"`

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
	// Slack 投稿時に本文の先頭へ付ける接頭辞(例 "🗣 ")。
	MessagePrefix string `json:"message_prefix"`
}

// KeystrokeConfig は合成入力(keystroke)出力の設定。
type KeystrokeConfig struct {
	// AutoEnter は後方互換用。send_key が未指定のときの既定として解釈する(true=enter, false=none)。
	AutoEnter bool `json:"auto_enter"`
	// SendKey は貼り付け後に送る「送信キー」の既定: none|enter|shift+enter|cmd+enter。
	// 空なら AutoEnter にフォールバックする。
	SendKey   string `json:"send_key"`
	PinTarget bool   `json:"pin_target"` // リッスン開始時に最前面だったアプリを固定し、そのアプリが前面のときだけ貼り付けるか
	// Overrides はアプリ別の送信キー上書き。貼り付け先アプリが app に一致すれば
	// その send_key を使う(Slack は enter で送信、別アプリは cmd+enter、ドキュメントは enter で改行、等)。
	Overrides []KeystrokeOverride `json:"overrides"`
}

// KeystrokeOverride はアプリ別の送信キー上書き 1 件。
type KeystrokeOverride struct {
	App     string `json:"app"`      // アプリ名(メニューバーの 🎯 表示名)または bundle id。大文字小文字は無視。
	SendKey string `json:"send_key"` // このアプリでの送信キー: none|enter|shift+enter|cmd+enter
}

// SendKeyFor は貼り付け先アプリ(表示名 name / bundle id bundleID)に対する送信キーを
// 正規化して返す(none|enter|shift+enter|cmd+enter)。
// 優先順: 一致する override の send_key → 既定 send_key → auto_enter(true=enter, false=none)。
func (k KeystrokeConfig) SendKeyFor(name, bundleID string) string {
	for _, o := range k.Overrides {
		if matchApp(o.App, name, bundleID) {
			if s := normalizeSendKey(o.SendKey); s != "" {
				return s
			}
			break // app は一致したが send_key 未指定 → 既定へフォールバック
		}
	}
	if s := normalizeSendKey(k.SendKey); s != "" {
		return s
	}
	if k.AutoEnter {
		return "enter"
	}
	return "none"
}

// normalizeSendKey は send_key の表記ゆれを正規トークンへ。未指定/不明は ""(=既定にフォールバック)。
func normalizeSendKey(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ""
	case "none", "off", "no", "false":
		return "none"
	case "enter", "return", "cr", "↵":
		return "enter"
	case "shift+enter", "shift+return", "shift-enter", "⇧↵":
		return "shift+enter"
	case "cmd+enter", "cmd+return", "command+enter", "meta+enter", "super+enter", "⌘↵":
		return "cmd+enter"
	default:
		return "" // 不明な指定は既定にフォールバック(タイプミスで送信が壊れないように)
	}
}

// matchApp は override の app 指定が貼り付け先(表示名 or bundle id)に一致するかを返す(大文字小文字無視の完全一致)。
func matchApp(pat, name, bundleID string) bool {
	pat = strings.ToLower(strings.TrimSpace(pat))
	if pat == "" {
		return false
	}
	return pat == strings.ToLower(name) || pat == strings.ToLower(bundleID)
}

// EnhanceConfig はローカル LLM(Ollama)による文字起こし整形の設定。
type EnhanceConfig struct {
	Enabled  bool   `json:"enabled"`  // 整形を有効にするか
	Backend  string `json:"backend"`  // 現状 "ollama"
	Endpoint string `json:"endpoint"` // Ollama エンドポイント
	Model    string `json:"model"`    // 使うモデル名(例 qwen2.5:7b)
	Prompt   string `json:"prompt"`   // 整形プロンプト(空で既定)
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

// RedirectURI は OAuth のコールバック URL を組み立てる。Slack の仕様上 HTTPS 必須。
func (c *Config) RedirectURI() string {
	return fmt.Sprintf("https://localhost:%d/oauth/callback", c.OAuthRedirectPort)
}

// Load は設定を読み込む。検索順は環境変数 URATALK_CONFIG → ./config.json →
// ~/.config/ura-talk/config.json。見つからない項目はデフォルトを使う。
// 必須項目の検証はコマンドごとに ValidateForPost / ValidateForLogin で行う。
func Load() (*Config, error) {
	cfg := &Config{
		OAuthRedirectPort: 53682,
		Output:            "slack",
		WhisperBin:        "whisper-cli",
		Language:          "ja",
		MinDurationMs:     300,
		MessagePrefix:     "🗣 ",
		ListenMode:        "ptt",
		WhisperBeamSize:   5,
		Gain:              GainConfig{Enabled: true, TargetPeak: 0.95, MaxGain: 12},
		Enhance:           EnhanceConfig{Enabled: false, Backend: "ollama", Endpoint: "http://localhost:11434", Model: "qwen2.5:7b"},
		Hotkey:            Hotkey{Mods: nil, Key: "rightcmd"},
		Sound:             SoundConfig{Enabled: true, On: "Submarine", Off: "Bottle"},
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
			if err := json.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("設定ファイル %s のパースに失敗: %w", path, err)
			}
		}
	}

	if v := os.Getenv("URATALK_SLACK_CLIENT_SECRET"); v != "" {
		cfg.SlackClientSecret = v
	}
	if v := os.Getenv("URATALK_WHISPER_MODEL"); v != "" {
		cfg.WhisperModel = v
	}
	cfg.WhisperModel = expandHome(cfg.WhisperModel)
	return cfg, nil
}

// ValidateForLogin は `login` サブコマンドに必要な項目を検証する。
func (c *Config) ValidateForLogin() error {
	if c.SlackClientID == "" {
		return fmt.Errorf("slack_client_id が未設定です")
	}
	if c.SlackClientSecret == "" {
		return fmt.Errorf("slack_client_secret が未設定です(config.json か URATALK_SLACK_CLIENT_SECRET)")
	}
	return nil
}

// ValidateForPost は通常起動(録音→投稿)に必要な項目を検証する。
func (c *Config) ValidateForPost() error {
	if c.SlackChannel == "" {
		return fmt.Errorf("slack_channel が未設定です")
	}
	if c.WhisperModel == "" {
		return fmt.Errorf("whisper_model が未設定です(ggml モデルのパス)")
	}
	return nil
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
