// Package trayicon はメニューバー用のモノクロ/カラーアイコン(PNG)を埋め込んで提供する。
// idle/listen/transcribe/pin はテンプレート(macOS が明暗に自動着色)、
// rec/recRing は録音を目立たせるためのカラー(赤)。
package trayicon

import _ "embed"

//go:embed icons/idle.png
var Idle []byte // 待機(一時停止)

//go:embed icons/listen.png
var Listen []byte // リッスン中(マイク)

//go:embed icons/rec.png
var Rec []byte // 録音中(赤丸・点滅の点灯フレーム)

//go:embed icons/rec_ring.png
var RecRing []byte // 録音中(赤リング・点滅の消灯フレーム)

//go:embed icons/transcribe.png
var Transcribe []byte // 文字起こし中(吹き出し)

//go:embed icons/pin.png
var Pin []byte // 固定先マーカー(ドロップダウン用のピン)
