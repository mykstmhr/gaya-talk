package recorder

import (
	"encoding/binary"
	"testing"
)

func pcmOf(samples ...int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
	}
	return b
}

func peakOf(pcm []byte) int {
	peak := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

func TestNormalizePCM_AmplifiesQuiet(t *testing.T) {
	// ピーク 1000(小声)。target 0.95、max 100 → ピークが 0.95*32767 付近まで上がる。
	in := pcmOf(1000, -800, 500)
	target := 0.95
	out := NormalizePCM(in, target, 100)
	got := peakOf(out)
	want := int(target * 32767)
	if d := got - want; d < -200 || d > 200 {
		t.Errorf("正規化後ピーク: got %d, want ≈ %d", got, want)
	}
}

func TestNormalizePCM_RespectsMaxGain(t *testing.T) {
	// ピーク 100。max_gain 8 で頭打ち → ピークは約 800。
	in := pcmOf(100, -100)
	out := NormalizePCM(in, 0.95, 8)
	if got := peakOf(out); got < 760 || got > 840 {
		t.Errorf("max_gain 上限後ピーク: got %d, want ≈ 800", got)
	}
}

func TestNormalizePCM_LeavesLoudAlone(t *testing.T) {
	// 既に大きい(ゲイン<=1)→ そのまま。
	in := pcmOf(32000, -31000)
	out := NormalizePCM(in, 0.95, 12)
	if peakOf(out) != 32000 {
		t.Errorf("十分大きい信号は変更しないはず: got peak %d", peakOf(out))
	}
}

func TestNormalizePCM_Silence(t *testing.T) {
	in := pcmOf(0, 0, 0)
	out := NormalizePCM(in, 0.95, 12)
	if peakOf(out) != 0 {
		t.Errorf("無音は無音のまま: got peak %d", peakOf(out))
	}
}
