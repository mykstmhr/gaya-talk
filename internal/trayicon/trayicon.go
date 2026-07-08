// Package trayicon はメニューバー用のアイコン(PNG)を埋め込んで提供する。
// メニューバーは「マイクの生死」だけを表す方針: 待機は Idle(黒テンプレート=
// macOS が明暗に自動着色)、リッスン/録音中は ListenOn(オレンジのカラー)。
// 詳細な状態(録音・文字起こし)は画面下部のバー(internal/voicebar)が担う。
// PNG は build/genicons.swift で生成する(make icons)。
package trayicon

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
)

//go:embed icons/idle.png
var Idle []byte // 待機(流れるコメントの吹き出し。アプリの顔)

//go:embed icons/listen.png
var Listen []byte // リッスン中(吹き出し+音波・テンプレート)

// ListenOn はリッスン中を目立たせるオレンジ版(カラー=非テンプレート)。
// リッスン中だけ SetIcon で使い、待機時はテンプレートの Listen/Idle に戻す。
// 赤は録音中の点滅で使うため、リッスンはオレンジにして区別する。
var ListenOn = tint(Listen, color.RGBA{R: 0xFF, G: 0x95, B: 0x00, A: 0xFF})

// IdleShared / ListenShared は「画面共有にコメントを映している」間の赤版。
// 外に見えている状態を常時視覚で示す(オンのまま忘れる事故の防止)。
var (
	IdleShared   = tint(Idle, color.RGBA{R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF})
	ListenShared = tint(Listen, color.RGBA{R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF})
)

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
