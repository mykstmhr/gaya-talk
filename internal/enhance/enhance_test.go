package enhance

import (
	"context"
	"testing"
)

func TestEnhance_DisabledReturnsRaw(t *testing.T) {
	e := New(Config{Enabled: false, Model: "x"})
	got, err := e.Enhance(context.Background(), "なるほど")
	if got != "なるほど" || err != nil {
		t.Errorf("無効時は素通し・err無し: got %q err=%v", got, err)
	}
}

func TestEnhance_NoModelReturnsRaw(t *testing.T) {
	e := New(Config{Enabled: true, Model: ""})
	got, err := e.Enhance(context.Background(), "テスト")
	if got != "テスト" || err != nil {
		t.Errorf("モデル未指定は素通し: got %q err=%v", got, err)
	}
}

func TestEnhance_UnreachableFallsBack(t *testing.T) {
	// 到達不能なエンドポイント → 元テキストを返し、err を返す(壊さない)。
	e := New(Config{Enabled: true, Model: "qwen2.5:7b", Endpoint: "http://127.0.0.1:1"})
	raw := "これはフォールバックの確認。"
	got, err := e.Enhance(context.Background(), raw)
	if got != raw {
		t.Errorf("到達不能時は元テキストを返すべき: got %q", got)
	}
	if err == nil {
		t.Error("到達不能時は err を返すべき")
	}
}

func TestTooLong(t *testing.T) {
	// 会話化の安全網: 入力の2倍+20文字超は「長すぎ」と判定する。
	raw := "うまく使えるかな。"
	answer := "はい、問題なく使えます。具体的な内容を教えていただけますか？さらに詳しく説明します。"
	if !tooLong(raw, answer) {
		t.Errorf("会話化した長い出力は破棄対象のはず")
	}
	cleaned := "うまく使えるかな?"
	if tooLong(raw, cleaned) {
		t.Errorf("通常の整形は破棄しないはず: %q", cleaned)
	}
}

func TestEnhance_NilSafe(t *testing.T) {
	var e *Enhancer
	got, err := e.Enhance(context.Background(), "x")
	if got != "x" || err != nil {
		t.Errorf("nil でも素通し: got %q err=%v", got, err)
	}
}
