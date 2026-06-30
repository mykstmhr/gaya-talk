// Package recorder はマイクから 16kHz/mono/16bit PCM を取り込む。
// バッファ録音(push-to-talk)とストリーム録音(VAD 用)の 2 通りを提供する。
// 出力は whisper-cli がそのまま読める WAV 形式に揃えられる。
package recorder

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/gen2brain/malgo"
)

// SampleRate は録音サンプリングレート(Hz)。VAD など外部の計算でも使う。
const (
	SampleRate    = 16000
	channels      = 1
	bitsPerSample = 16
)

// Recorder はマイク入力を保持する。New で生成し、Start/Stop か StartStream/StopStream を使う。
type Recorder struct {
	ctx        *malgo.AllocatedContext
	deviceName string // 入力デバイス名の部分一致指定。空ならシステム既定。

	mu        sync.Mutex
	device    *malgo.Device
	buf       bytes.Buffer
	recording bool
}

// New は録音コンテキストを初期化する。deviceName を指定すると、その名前を含む
// 入力デバイスから録音する(空ならシステム既定)。終了時に Close を呼ぶこと。
func New(deviceName string) (*Recorder, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("オーディオコンテキスト初期化失敗: %w", err)
	}
	return &Recorder{ctx: ctx, deviceName: deviceName}, nil
}

// InputDevices は利用可能な入力デバイス名の一覧を返す(既定には " (default)" を付ける)。
func (r *Recorder) InputDevices() ([]string, error) {
	infos, err := r.ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("入力デバイスの列挙失敗: %w", err)
	}
	names := make([]string, 0, len(infos))
	for i := range infos {
		name := infos[i].Name()
		if infos[i].IsDefault != 0 {
			name += " (default)"
		}
		names = append(names, name)
	}
	return names, nil
}

// buildConfig は capture 用の DeviceConfig を組み立てる。deviceName が指定されていれば
// その名前を含むデバイスを選ぶ。返す infos は InitDevice 呼び出しまで生存させること。
func (r *Recorder) buildConfig() (malgo.DeviceConfig, []malgo.DeviceInfo, error) {
	cfg := malgo.DefaultDeviceConfig(malgo.Capture)
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = channels
	cfg.SampleRate = SampleRate
	cfg.Alsa.NoMMap = 1

	if r.deviceName == "" {
		return cfg, nil, nil
	}
	// Bluetooth イヤホン以外(内蔵マイク等)を選べるようにして、録音開始時に
	// イヤホンが通話プロファイル(HFP)へ切り替わり再生音が途切れるのを防ぐ。
	infos, err := r.ctx.Devices(malgo.Capture)
	if err != nil {
		return cfg, nil, fmt.Errorf("入力デバイスの列挙失敗: %w", err)
	}
	idx := -1
	for i := range infos {
		if strings.Contains(strings.ToLower(infos[i].Name()), strings.ToLower(r.deviceName)) {
			idx = i
			break
		}
	}
	if idx < 0 {
		var names []string
		for i := range infos {
			names = append(names, infos[i].Name())
		}
		return cfg, nil, fmt.Errorf("入力デバイス %q が見つかりません。候補: %s", r.deviceName, strings.Join(names, ", "))
	}
	cfg.Capture.DeviceID = infos[idx].ID.Pointer()
	return cfg, infos, nil
}

// Start はバッファ録音(push-to-talk)を開始する。既に録音中なら何もしない。
func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recording {
		return nil
	}
	r.buf.Reset()
	// Data コールバックは malgo のオーディオスレッドから呼ばれる。bytes.Buffer.Write は
	// 値をコピーするので、書き込み手がこの 1 スレッドだけなら安全。
	return r.startLocked(func(_, in []byte, _ uint32) {
		r.buf.Write(in)
	})
}

// StartStream はストリーム録音を開始する。チャンク(PCM)ごとに onFrame を呼ぶ。
// onFrame に渡すバイト列はコールバックごとにコピーした独立スライス。
func (r *Recorder) StartStream(onFrame func(pcm []byte)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recording {
		return nil
	}
	return r.startLocked(func(_, in []byte, _ uint32) {
		// malgo は in のバッファを次のコールバックで再利用するためコピーする。
		b := make([]byte, len(in))
		copy(b, in)
		onFrame(b)
	})
}

// startLocked は共通の録音開始処理。呼び出し側で mu をロックしておくこと。
func (r *Recorder) startLocked(data func(out, in []byte, frames uint32)) error {
	cfg, infos, err := r.buildConfig()
	if err != nil {
		return err
	}
	device, err := malgo.InitDevice(r.ctx.Context, cfg, malgo.DeviceCallbacks{Data: data})
	runtime.KeepAlive(infos) // cfg が指す infos を InitDevice まで生存させる
	if err != nil {
		return fmt.Errorf("録音デバイス初期化失敗: %w", err)
	}
	if err := device.Start(); err != nil {
		device.Uninit()
		return fmt.Errorf("録音開始失敗: %w", err)
	}
	r.device = device
	r.recording = true
	return nil
}

// stopLocked はデバイスを停止・解放する。呼び出し側で mu をロックしておくこと。
func (r *Recorder) stopLocked() {
	if r.device != nil {
		// Uninit はコールバックを止めてから返る。
		r.device.Uninit()
		r.device = nil
	}
	r.recording = false
}

// Stop はバッファ録音を止め、PCM バイト列と録音時間(ミリ秒)を返す。
// 録音していなければ nil, 0 を返す。
func (r *Recorder) Stop() ([]byte, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		return nil, 0, nil
	}
	r.stopLocked()
	pcm := append([]byte(nil), r.buf.Bytes()...)
	return pcm, DurationMs(len(pcm)), nil
}

// StopStream はストリーム録音を止める。
func (r *Recorder) StopStream() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
}

// Close はオーディオコンテキストを解放する。
func (r *Recorder) Close() {
	if r.ctx != nil {
		_ = r.ctx.Uninit()
		r.ctx.Free()
	}
}

// DurationMs は PCM バイト数から録音時間(ミリ秒)を求める。
func DurationMs(pcmLen int) int {
	frames := pcmLen / (channels * bitsPerSample / 8)
	return frames * 1000 / SampleRate
}

// NormalizePCM は S16LE PCM をピーク正規化して音量を持ち上げる。
// ボソボソ・小声を Whisper に渡す前に大きくして認識精度を上げる用途。
// ピーク基準のため原理上クリップしない。gain が 1 以下(既に十分大きい)なら素通し。
// maxGain で無音・雑音を過剰増幅しないよう上限をかける。
func NormalizePCM(pcm []byte, targetPeak, maxGain float64) []byte {
	if len(pcm) < 2 {
		return pcm
	}
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
	if peak == 0 {
		return pcm
	}
	gain := targetPeak * 32767 / float64(peak)
	if gain > maxGain {
		gain = maxGain
	}
	if gain <= 1.0 {
		return pcm
	}
	out := make([]byte, len(pcm))
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i:i+2]))) * gain
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		binary.LittleEndian.PutUint16(out[i:i+2], uint16(int16(v)))
	}
	return out
}

// WAVFromPCM は PCM データの先頭に 44 バイトの WAV ヘッダを付ける。
func WAVFromPCM(pcm []byte) []byte {
	var b bytes.Buffer
	dataLen := uint32(len(pcm))
	byteRate := uint32(SampleRate * channels * bitsPerSample / 8)
	blockAlign := uint16(channels * bitsPerSample / 8)

	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36)+dataLen)
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16)) // fmt チャンクサイズ
	binary.Write(&b, binary.LittleEndian, uint16(1))  // PCM
	binary.Write(&b, binary.LittleEndian, uint16(channels))
	binary.Write(&b, binary.LittleEndian, uint32(SampleRate))
	binary.Write(&b, binary.LittleEndian, byteRate)
	binary.Write(&b, binary.LittleEndian, blockAlign)
	binary.Write(&b, binary.LittleEndian, uint16(bitsPerSample))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, dataLen)
	b.Write(pcm)
	return b.Bytes()
}
