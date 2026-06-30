// Package enhance は whisper の生の文字起こしをローカル LLM(Ollama)で整形する。
// 句読点・漢字・フィラー除去などを整え、日本語の読みやすさを上げる。
// ネットワーク/モデルのエラー時は必ず元テキストを返す(整形で壊さない)。
package enhance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// defaultPrompt は「整形のみ・会話/返答禁止」を厳守させるシステムプロンプト。
const defaultPrompt = `あなたは日本語の音声文字起こしを整える「テキスト整形エンジン」です。会話や応答は一切しません。
入力された文字列を、次のルールで整形し、整形後の本文だけを出力してください。
1. 内容を変えない・要約しない・翻訳しない・新しい情報や返答を足さない
2. 質問文であっても絶対に答えない。そのまま質問文として整形するだけ
3. 句読点を自然に補い、読みやすくする
4. 文脈から明らかな誤変換・同音異義語の誤りを直す
5. 「えーっと」「あのー」等のフィラーや言い淀みを取り除く
6. 日本語のまま。前置き・説明・引用符・コードブロックは付けず、整形結果の本文のみを返す`

// fewShot は「入力をそのまま整形するだけ(返答しない)」を示す例。
// 特に質問文を答えずに整形する例を入れて、会話化を防ぐ。
var fewShot = []struct{ in, out string }{
	{"あー、えーっと これうまく動くのかな", "これ、うまく動くのかな?"},
	{"りょうかいです あとでみときます", "了解です。あとで見ておきます。"},
	{"うまく使えるかな。", "うまく使えるかな?"},
}

// Config は整形の設定。
type Config struct {
	Enabled   bool   // 整形を有効にするか
	Endpoint  string // Ollama のエンドポイント(既定 http://localhost:11434)
	Model     string // 使うモデル名(例 qwen2.5:7b)
	Prompt    string // システムプロンプト(空なら defaultPrompt)
	KeepAlive string // モデルをメモリに保持する時間(空なら "30m")。コールドスタート対策。
}

// Enhancer は整形クライアント。
type Enhancer struct {
	cfg  Config
	http *http.Client
}

// New は Enhancer を生成する。
func New(cfg Config) *Enhancer {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://localhost:11434"
	}
	if strings.TrimSpace(cfg.Prompt) == "" {
		cfg.Prompt = defaultPrompt
	}
	if cfg.KeepAlive == "" {
		cfg.KeepAlive = "30m" // モデルを温存してコールドスタートを防ぐ
	}
	return &Enhancer{cfg: cfg, http: &http.Client{Timeout: 60 * time.Second}}
}

// reachable は Ollama サーバが応答するかを確認する。
func (e *Enhancer) reachable(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.cfg.Endpoint+"/api/version", nil)
	if err != nil {
		return false
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// EnsureServer は Ollama が起動していなければ自動起動し、応答するまで待つ。
// started は今回自分で起動したかどうか。無効時や既に起動済みなら何もしない。
func (e *Enhancer) EnsureServer(ctx context.Context) (started bool, err error) {
	if e == nil || !e.cfg.Enabled {
		return false, nil
	}
	if e.reachable(ctx) {
		return false, nil // 既に起動している
	}
	if err := startOllama(); err != nil {
		return false, err
	}
	// 起動して応答するまで待つ(最大 ~15 秒)。
	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		if e.reachable(ctx) {
			return true, nil
		}
	}
	return true, fmt.Errorf("Ollama を起動しましたが応答しません")
}

// startOllama は Ollama サーバを起動する。Ollama.app があればそれを(ログはファイルへ)、
// 無ければ `ollama serve` を出力破棄で起動する(ターミナルを汚さない)。
func startOllama() error {
	if _, err := os.Stat("/Applications/Ollama.app"); err == nil {
		if err := exec.Command("open", "-a", "Ollama").Run(); err == nil {
			return nil
		}
	}
	bin := ollamaBin()
	if bin == "" {
		return fmt.Errorf("ollama が見つかりません(brew install ollama)")
	}
	cmd := exec.Command(bin, "serve")
	// Stdout/Stderr を nil のままにすると /dev/null に接続される(ログを捨てる)。
	// Setpgid で ura-talk のプロセスグループから切り離し、ura-talk 終了後も残す。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd.Start()
}

// ollamaBin は ollama 実行ファイルのパスを解決する(.app 起動時は PATH が乏しいため)。
func ollamaBin() string {
	if p, err := exec.LookPath("ollama"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/ollama", "/usr/local/bin/ollama"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Warmup はモデルをメモリに先読みする(初回発話の待ちを無くす)。起動時に呼ぶ。
func (e *Enhancer) Warmup(ctx context.Context) error {
	if e == nil || !e.cfg.Enabled || e.cfg.Model == "" {
		return nil
	}
	_, err := e.callOllama(ctx, "")
	return err
}

// Enhance は raw を整形して返す。整形後テキストと、失敗理由(あれば)を返す。
// 無効・空・失敗時は raw をそのまま返す(err 非 nil なら呼び出し側でログ可)。
func (e *Enhancer) Enhance(ctx context.Context, raw string) (string, error) {
	if e == nil || !e.cfg.Enabled || strings.TrimSpace(raw) == "" || e.cfg.Model == "" {
		return raw, nil
	}
	out, err := e.callOllama(ctx, raw)
	if err != nil {
		return raw, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return raw, fmt.Errorf("整形結果が空でした")
	}
	// 安全網: 整形結果が極端に長い場合は「返答してしまった(会話化)」疑いが高いので生テキストを使う。
	if tooLong(raw, out) {
		return raw, fmt.Errorf("整形結果が不自然に長い(会話化の疑い)ため破棄")
	}
	return out, nil
}

// tooLong は out が raw に対して不自然に長い(=返答・会話化の疑い)かを判定する。
func tooLong(raw, out string) bool {
	return utf8.RuneCountInString(out) > utf8.RuneCountInString(raw)*2+20
}

// Check は Ollama に接続でき、設定モデルが存在するかを確認する。
// 無効なら nil。起動時に呼んで使用可否をログ表示する用途。
func (e *Enhancer) Check(ctx context.Context) error {
	if e == nil || !e.cfg.Enabled {
		return nil
	}
	if e.cfg.Model == "" {
		return fmt.Errorf("model が未設定")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.cfg.Endpoint+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("Ollama に接続できません(%s)。`ollama serve` を起動してください: %w", e.cfg.Endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Ollama status %d", resp.StatusCode)
	}
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	for _, m := range out.Models {
		// 完全一致、またはタグ省略指定(例 "qwen2.5" → "qwen2.5:7b")を許容する。
		// 素の HasPrefix だと "qwen2.5" が "qwen2.5-coder:7b" のような別モデルにも誤って一致するため避ける。
		if m.Name == e.cfg.Model || strings.HasPrefix(m.Name, e.cfg.Model+":") {
			return nil
		}
	}
	return fmt.Errorf("モデル %q が見つかりません(`ollama pull %s` が必要)", e.cfg.Model, e.cfg.Model)
}

// callOllama は Ollama の /api/chat を非ストリームで呼ぶ。
func (e *Enhancer) callOllama(ctx context.Context, raw string) (string, error) {
	// system → few-shot(整形例)→ 実入力、の順でメッセージを組む。
	// few-shot により「質問に答えず、入力をそのまま整形する」挙動を固定する。
	messages := []map[string]string{{"role": "system", "content": e.cfg.Prompt}}
	for _, ex := range fewShot {
		messages = append(messages,
			map[string]string{"role": "user", "content": ex.in},
			map[string]string{"role": "assistant", "content": ex.out},
		)
	}
	messages = append(messages, map[string]string{"role": "user", "content": raw})

	payload, err := json.Marshal(map[string]any{
		"model":      e.cfg.Model,
		"stream":     false,
		"keep_alive": e.cfg.KeepAlive, // モデルを温存(コールドスタート対策)
		"messages":   messages,
		"options":    map[string]any{"temperature": 0},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.cfg.Endpoint+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Message.Content, nil
}
