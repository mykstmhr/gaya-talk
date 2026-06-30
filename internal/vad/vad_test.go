package vad

import (
	"encoding/binary"
	"testing"
)

const testRate = 16000

// makeChunk は ms ミリ秒ぶんの、振幅 amp(絶対値)の定数 PCM(S16LE mono)を作る。
func makeChunk(ms int, amp int16) []byte {
	samples := testRate * ms / 1000
	b := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(amp))
	}
	return b
}

func testConfig() Config {
	return Config{
		SampleRate:   testRate,
		Threshold:    0.02,
		MinSpeechMs:  300,
		SilenceMs:    700,
		MaxSegmentMs: 15000,
		PrerollMs:    300,
	}
}

func TestSegmenter_SingleUtterance(t *testing.T) {
	var segments [][]byte
	s := New(testConfig(), func(pcm []byte, _ int) {
		segments = append(segments, pcm)
	})

	feed := func(total, chunk int, amp int16) {
		for n := 0; n < total; n += chunk {
			s.Feed(makeChunk(chunk, amp))
		}
	}
	feed(500, 20, 0)    // 無音
	feed(800, 20, 3000) // 発話(rms ≈ 0.09 > 0.02)
	feed(900, 20, 0)    // 無音 700ms 超 → 区切り

	if len(segments) != 1 {
		t.Fatalf("発話セグメント数: got %d, want 1", len(segments))
	}
	// 先読み(300ms)+ 発話(800ms)+ 区切り判定までの無音 程度の長さがあるはず。
	durMs := DurationMs(len(segments[0]))
	if durMs < 800 {
		t.Errorf("セグメント長が短すぎ: got %d ms, want >= 800", durMs)
	}
}

func TestSegmenter_TwoUtterances(t *testing.T) {
	count := 0
	s := New(testConfig(), func(pcm []byte, _ int) { count++ })

	feed := func(total, chunk int, amp int16) {
		for n := 0; n < total; n += chunk {
			s.Feed(makeChunk(chunk, amp))
		}
	}
	feed(800, 20, 3000) // 1 つめ
	feed(900, 20, 0)    // 区切り
	feed(800, 20, 3000) // 2 つめ
	feed(900, 20, 0)    // 区切り

	if count != 2 {
		t.Fatalf("発話セグメント数: got %d, want 2", count)
	}
}

func TestSegmenter_ShortNoiseDropped(t *testing.T) {
	count := 0
	s := New(testConfig(), func(pcm []byte, _ int) { count++ })

	// 100ms だけの音(MinSpeechMs=300 未満)→ 捨てられる。
	for n := 0; n < 100; n += 20 {
		s.Feed(makeChunk(20, 3000))
	}
	for n := 0; n < 900; n += 20 {
		s.Feed(makeChunk(20, 0))
	}

	if count != 0 {
		t.Fatalf("短い雑音は捨てるはず: got %d segments, want 0", count)
	}
}

func TestSegmenter_FlushEmitsInProgress(t *testing.T) {
	count := 0
	s := New(testConfig(), func(pcm []byte, _ int) { count++ })

	for n := 0; n < 800; n += 20 { // 無音区切り前に止める
		s.Feed(makeChunk(20, 3000))
	}
	s.Flush()

	if count != 1 {
		t.Fatalf("Flush で進行中の発話を出すはず: got %d, want 1", count)
	}
}

// DurationMs はテスト内で長さ確認に使う簡易版(recorder と同じ式)。
func DurationMs(pcmLen int) int {
	frames := pcmLen / 2 // mono S16
	return frames * 1000 / testRate
}
