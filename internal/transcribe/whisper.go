// Package transcribe はローカルの whisper-cli を呼んで WAV を文字起こしする。
// 音声はローカルで処理され外部には出ない。
package transcribe

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// tempPattern は本ツールが作る一時 WAV のグロブパターン。
const tempPattern = "gaya-talk-*.wav"

// CleanupStaleTempFiles は dir 内に残った古い一時 WAV を削除する。
// 通常は処理後に削除されるが、クラッシュ時の消し残りに備えて起動時に呼ぶ。
// 処理中のファイルを巻き込まないよう、olderThan より古いものだけ消す。
func CleanupStaleTempFiles(dir string, olderThan time.Duration) (int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, tempPattern))
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, p := range matches {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.ModTime().Before(cutoff) {
			if os.Remove(p) == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// Whisper は whisper-cli の呼び出し設定。
type Whisper struct {
	Bin           string  // 実行パス(例 "whisper-cli")
	Model         string  // ggml モデルのパス
	Lang          string  // 言語コード(例 "ja")
	BeamSize      int     // ビーム幅(0 で whisper 既定)。上げると精度↑・速度↓
	Prompt        string  // 初期プロンプト(口語・語彙のヒント)
	NoSpeechThold float64 // no-speech 閾値(0 で whisper 既定 0.6)。下げると小声を拾いやすい
}

// Transcribe は WAV バイト列を一時ファイルに書き出し、whisper-cli で文字起こしする。
func (w Whisper) Transcribe(wav []byte) (string, error) {
	tmp, err := os.CreateTemp("", tempPattern)
	if err != nil {
		return "", fmt.Errorf("一時ファイル作成失敗: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(wav); err != nil {
		tmp.Close()
		return "", fmt.Errorf("一時ファイル書き込み失敗: %w", err)
	}
	tmp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// -nt: タイムスタンプ無し / -np: 結果以外を出力しない → stdout に本文だけ出る。
	args := []string{
		"-m", w.Model,
		"-f", tmp.Name(),
		"-l", w.Lang,
		"-nt",
		"-np",
	}
	if w.BeamSize > 0 {
		args = append(args, "-bs", strconv.Itoa(w.BeamSize))
	}
	if w.NoSpeechThold > 0 {
		args = append(args, "-nth", strconv.FormatFloat(w.NoSpeechThold, 'f', -1, 64))
	}
	if w.Prompt != "" {
		args = append(args, "--prompt", w.Prompt)
	}
	cmd := exec.CommandContext(ctx, w.Bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("whisper-cli 実行失敗: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	text := clean(out.String())
	if isHallucination(text) {
		return "", nil // 無音・雑音に対する典型的な幻聴は捨てる
	}
	return text, nil
}

// hallucinations は Whisper が無音・雑音に対して出しがちな定型句。
// 会議の発話としてはまず出てこない YouTube 由来のフレーズに絞ってある。
var hallucinations = map[string]bool{
	"ご視聴ありがとうございました":         true,
	"ご視聴ありがとうございます":          true,
	"最後までご視聴いただきありがとうございました": true,
	"ご清聴ありがとうございました":         true,
	"チャンネル登録お願いします":          true,
	"チャンネル登録をお願いします":         true,
	"高評価とチャンネル登録をお願いします":     true,
	"次の動画でお会いしましょう":          true,
	"おやすみなさい":                true,
}

// isHallucination は全体が既知の幻聴フレーズと一致するかを判定する。
// 句読点・空白・記号を除いて比較するため "ご視聴ありがとうございました。" なども弾ける。
func isHallucination(text string) bool {
	norm := strings.NewReplacer(
		" ", "", "　", "", "。", "", "、", "", "！", "", "？", "", ".", "", ",", "", "!", "", "?", "",
	).Replace(strings.TrimSpace(text))
	return hallucinations[norm]
}

// clean は複数行・余分な空白を 1 行のテキストにまとめる。
// whisper が無音・雑音に対して出すノイズ([BLANK_AUDIO] や括弧書きの効果音)は捨てる。
func clean(s string) string {
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isNoise(line) {
			continue
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " ")
}

// isNoise は whisper が無音・雑音に出す典型的なノイズ行かどうかを判定する。
func isNoise(line string) bool {
	upper := strings.ToUpper(line)
	if strings.Contains(upper, "[BLANK_AUDIO]") || strings.Contains(upper, "[ SILENCE ]") {
		return true
	}
	// 全体が括弧で囲まれた効果音表記(例 "(拍手)" "（音楽）" "[Music]")。
	pairs := [][2]string{{"(", ")"}, {"（", "）"}, {"[", "]"}, {"［", "］"}}
	for _, p := range pairs {
		if line != p[0]+p[1] && strings.HasPrefix(line, p[0]) && strings.HasSuffix(line, p[1]) {
			return true
		}
	}
	// 音符記号や空白だけの行。
	return strings.Trim(line, "♪♬〜~ ") == ""
}
