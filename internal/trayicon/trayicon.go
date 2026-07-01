// Package trayicon はメニューバー用のモノクロ/カラーアイコン(PNG)を埋め込んで提供する。
// idle/listen/transcribe/pin はテンプレート(macOS が明暗に自動着色)、
// rec/recRing は録音を目立たせるためのカラー(赤)。
package trayicon

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
)

//go:embed icons/idle.png
var Idle []byte // 待機(一時停止)

//go:embed icons/listen.png
var Listen []byte // リッスン中(マイク・テンプレート)

// ListenOn はリッスン中を目立たせるオレンジ版マイク(カラー=非テンプレート)。
// リッスン中だけ SetIcon で使い、待機時はテンプレートの Listen/Idle に戻す。
// 赤は録音中の点滅で使うため、リッスンはオレンジにして区別する。
var ListenOn = tint(Listen, color.RGBA{R: 0xFF, G: 0x95, B: 0x00, A: 0xFF})

// tint は src(PNG)の不透明度(アルファ)を保ったまま RGB を c に塗り替えた PNG を返す。
// アンチエイリアスのエッジを残すため形状はアルファで表現する。失敗時は src をそのまま返す。
func tint(src []byte, c color.RGBA) []byte {
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return src
	}
	b := img.Bounds()
	dst := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			dst.SetNRGBA(x, y, color.NRGBA{R: c.R, G: c.G, B: c.B, A: uint8(a >> 8)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return src
	}
	return buf.Bytes()
}

//go:embed icons/rec.png
var Rec []byte // 録音中(赤丸・点滅の点灯フレーム)

//go:embed icons/rec_ring.png
var RecRing []byte // 録音中(赤リング・点滅の消灯フレーム)

//go:embed icons/transcribe.png
var Transcribe []byte // 文字起こし中(吹き出し)

//go:embed icons/pin.png
var Pin []byte // 固定先マーカー(ドロップダウン用のピン)
