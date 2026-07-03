// Package vad は音声ストリームをエネルギー(RMS)ベースで発話単位に区切る。
//
// 連続して流れてくる PCM(S16LE / mono)を Feed に与えると、無音で区切られた
// 1 発話ぶんがたまるたびに onSegment が呼ばれる。閾値ベースの簡易 VAD のため、
// 環境音で誤検出することがある。閾値や無音長は Config で調整する。
package vad

import (
	"encoding/binary"
	"log"
	"math"
)

// Config は区切りのパラメータ。
type Config struct {
	SampleRate   int     // サンプリングレート(Hz)
	Threshold    float64 // 発話とみなす RMS の下限(0..1 に正規化)
	MinSpeechMs  int     // これ未満の発話は雑音として捨てる
	SilenceMs    int     // この長さの無音が続いたら 1 発話の終わりとみなす
	MaxSegmentMs int     // 1 発話の最大長(超えたら強制的に区切る)
	PrerollMs    int     // 発話開始の少し手前から含めるための先読み量
	Debug        bool    // true で音量(RMS)や状態遷移をログ出力する
}

// Segmenter は発話区切りの状態機械。Feed はオーディオスレッドから、
// Flush は停止時にメインスレッドから呼ぶ想定(両者は同時に呼ばないこと)。
type Segmenter struct {
	cfg        Config
	bytesPerMs int
	onSegment  func(pcm []byte, durMs int)

	inSpeech      bool
	speech        []byte
	preroll       []byte
	speechMs      int
	silenceMs     int
	lastVoicedLen int // 直近で発話と判定した時点の speech の長さ(末尾無音トリム用)

	// OnActivity は発話の検出が始まった/終わったときに呼ばれる(任意)。
	// UI(メニューバーのアイコン)で「いま音声を検出中か」を表示する用途。
	OnActivity func(active bool)

	// デバッグ用の音量メーター(直近ウィンドウの最大 RMS)。
	dbgAccumMs int
	dbgMaxRMS  float64
}

// New は Segmenter を生成する。onSegment は 1 発話ぶんの PCM とその長さ(ms)で呼ばれる。
func New(cfg Config, onSegment func(pcm []byte, durMs int)) *Segmenter {
	bpms := cfg.SampleRate * 2 / 1000 // S16 mono の 1ms あたりバイト数
	if bpms < 1 {
		bpms = 1
	}
	return &Segmenter{cfg: cfg, bytesPerMs: bpms, onSegment: onSegment}
}

// Feed は連続 PCM チャンクを 1 つ与える。
func (s *Segmenter) Feed(pcm []byte) {
	chunkMs := len(pcm) / s.bytesPerMs
	if chunkMs == 0 {
		chunkMs = 1
	}
	rms := rmsNorm(pcm)
	// ヒステリシス: 発話開始は Threshold、いったん始まったら継続は低め(half)で判定する。
	// 発話中の音量のゆらぎで途切れる/破棄されるのを防ぐ。
	onset := rms >= s.cfg.Threshold
	sustain := rms >= s.cfg.Threshold*0.5

	if s.cfg.Debug {
		if rms > s.dbgMaxRMS {
			s.dbgMaxRMS = rms
		}
		if s.dbgAccumMs += chunkMs; s.dbgAccumMs >= 500 {
			log.Printf("[vad] 直近0.5秒の最大音量 rms=%.4f (閾値=%.4f, %s)",
				s.dbgMaxRMS, s.cfg.Threshold, map[bool]string{true: "発話中", false: "無音待ち"}[s.inSpeech])
			s.dbgAccumMs = 0
			s.dbgMaxRMS = 0
		}
	}

	if !s.inSpeech {
		// 先読みバッファを更新(発話開始の手前を取りこぼさないため)。
		s.preroll = append(s.preroll, pcm...)
		if max := s.cfg.PrerollMs * s.bytesPerMs; len(s.preroll) > max {
			s.preroll = s.preroll[len(s.preroll)-max:]
		}
		if onset {
			s.inSpeech = true
			s.speech = append(s.speech[:0], s.preroll...)
			s.speech = append(s.speech, pcm...)
			s.speechMs = chunkMs
			s.silenceMs = 0
			s.lastVoicedLen = len(s.speech)
			s.preroll = s.preroll[:0]
			if s.OnActivity != nil {
				s.OnActivity(true)
			}
			if s.cfg.Debug {
				log.Printf("[vad] 発話開始 (rms=%.4f)", rms)
			}
		}
		return
	}

	// 発話中。継続判定は低めの閾値(sustain)で行う。
	s.speech = append(s.speech, pcm...)
	if sustain {
		s.speechMs += chunkMs
		s.silenceMs = 0
		s.lastVoicedLen = len(s.speech)
	} else {
		s.silenceMs += chunkMs
	}

	segMs := len(s.speech) / s.bytesPerMs
	if s.silenceMs >= s.cfg.SilenceMs || segMs >= s.cfg.MaxSegmentMs {
		s.emit()
		s.reset()
	}
}

// Flush は進行中の発話があれば確定して出す(停止時に呼ぶ)。
func (s *Segmenter) Flush() {
	if s.inSpeech {
		s.emit()
	}
	s.reset()
}

// emit は発話を確定して出す。末尾の無音は切り詰めてから渡す(Whisper の幻聴対策)。
func (s *Segmenter) emit() {
	if s.speechMs < s.cfg.MinSpeechMs {
		if s.cfg.Debug {
			log.Printf("[vad] 発話が短すぎて破棄 (発話 %d ms < min %d ms)", s.speechMs, s.cfg.MinSpeechMs)
		}
		return // 雑音や咳払い程度は捨てる
	}
	// 末尾無音をトリム。最後に発話と判定した位置 + 余韻 200ms まで残す。
	end := s.lastVoicedLen + 200*s.bytesPerMs
	if end > len(s.speech) {
		end = len(s.speech)
	}
	seg := make([]byte, end)
	copy(seg, s.speech[:end])
	s.onSegment(seg, end/s.bytesPerMs)
}

func (s *Segmenter) reset() {
	if s.inSpeech && s.OnActivity != nil {
		s.OnActivity(false)
	}
	s.inSpeech = false
	s.speech = s.speech[:0]
	s.speechMs = 0
	s.silenceMs = 0
	s.lastVoicedLen = 0
}

// RMS は PCM(S16LE)の音量を 0..1 で返す。UI のレベル表示など外部の用途向け。
func RMS(pcm []byte) float64 {
	return rmsNorm(pcm)
}

// rmsNorm は PCM(S16LE)の RMS を 0..1 に正規化して返す。
func rmsNorm(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		f := v / 32768.0
		sum += f * f
	}
	return math.Sqrt(sum / float64(n))
}
