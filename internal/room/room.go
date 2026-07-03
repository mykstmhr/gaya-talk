// Package room は「ルーム」= 中継サーバ(Cloudflare Workers)経由のコメント共有を担う。
//
// 共有 URL は https://<server>/r/<token>#k=<鍵>[&n=1] の形。鍵は URL フラグメントに
// 載せるためサーバには送信されず、本文は AES-GCM で E2E 暗号化される
// (中継サーバ = Cloudflare には暗号文しか見えない)。
package room

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Room は参加先ルームの情報(共有 URL から得られるもの)。
type Room struct {
	Server string // 例 "https://ura-talk-room.example.workers.dev"
	Token  string // ルームトークン(base64url 22文字)
	Key    []byte // AES-256-GCM 鍵(32 バイト)
	Named  bool   // 記名モードか(#...&n=1)
	// SlackChannel は Slack 記録対象チャンネル(#...&slack=)。空なら記録対象でない。
	// URL に持たせることで「このルームは記録対象」を全参加者が知れる(透明性)。
	// bot token は URL には入れず、記録するのは token を持つ人だけ。
	SlackChannel string
	// AdminSecret はルーム無効化用の管理シークレット(作成者だけが持つ)。
	// 共有 URL には決して載せない。空なら無効化できない(=作成者でない)。
	AdminSecret string
}

// tokenRe はサーバが発行するトークンの形(128bit base64url、パディングなし)。
var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// GenerateKey は AES-256 鍵をローカルで生成する(ルーム作成時に呼ぶ)。
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// URL はメンバーに共有する URL を組み立てる。
func (r *Room) URL() string {
	frag := "k=" + base64.RawURLEncoding.EncodeToString(r.Key)
	if r.Named {
		frag += "&n=1"
	}
	if r.SlackChannel != "" {
		frag += "&slack=" + url.QueryEscape(r.SlackChannel)
	}
	return fmt.Sprintf("%s/r/%s#%s", strings.TrimSuffix(r.Server, "/"), r.Token, frag)
}

// WSURL は WebSocket 接続先(wss://...)を返す。鍵を含むフラグメントは付けない。
func (r *Room) WSURL() string {
	base := strings.TrimSuffix(r.Server, "/")
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1) // ローカル開発(wrangler dev)用
	return fmt.Sprintf("%s/r/%s/ws", base, r.Token)
}

// Parse は共有 URL を解釈する。鍵が無い・形式が違う URL はエラー。
func Parse(raw string) (*Room, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("URL を解釈できません: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("https の URL を指定してください")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "r" || !tokenRe.MatchString(parts[1]) {
		return nil, fmt.Errorf("ルーム URL ではありません(/r/<token> の形式)")
	}
	frag, err := url.ParseQuery(u.Fragment)
	if err != nil {
		return nil, fmt.Errorf("URL のフラグメントを解釈できません: %w", err)
	}
	key, err := base64.RawURLEncoding.DecodeString(frag.Get("k"))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("URL に有効な鍵(#k=...)がありません")
	}
	return &Room{
		Server:       u.Scheme + "://" + u.Host,
		Token:        parts[1],
		Key:          key,
		Named:        frag.Get("n") == "1",
		SlackChannel: frag.Get("slack"),
	}, nil
}

// Payload は復号後のコメント 1 件。
type Payload struct {
	ID    string `json:"id,omitempty"` // 表示の重複排除用(送信側で採番)
	Text  string `json:"text"`
	Color string `json:"color"`          // "#rrggbb"
	Name  string `json:"name,omitempty"` // 記名モード用(v1 では常に空)
}

// NewID は Payload.ID 用のランダム ID を返す。
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// envelope は WebSocket に流す暗号化メッセージ(サーバにはこれしか見えない)。
type envelope struct {
	V  int    `json:"v"`
	IV string `json:"iv"` // AES-GCM nonce (base64)
	CT string `json:"ct"` // 暗号文+タグ (base64)
}

// Encrypt は Payload を AES-256-GCM で暗号化し、WS に流す JSON を返す。
func Encrypt(key []byte, p Payload) ([]byte, error) {
	plain, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, iv, plain, nil)
	return json.Marshal(envelope{
		V:  1,
		IV: base64.StdEncoding.EncodeToString(iv),
		CT: base64.StdEncoding.EncodeToString(ct),
	})
}

// Decrypt は WS から受け取った JSON を復号する。鍵違い・改竄・形式不正はエラー
// (呼び出し側は黙って捨てる)。
func Decrypt(key []byte, data []byte) (Payload, error) {
	var e envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Payload{}, err
	}
	if e.V != 1 {
		return Payload{}, fmt.Errorf("未知のバージョン: %d", e.V)
	}
	iv, err := base64.StdEncoding.DecodeString(e.IV)
	if err != nil {
		return Payload{}, err
	}
	ct, err := base64.StdEncoding.DecodeString(e.CT)
	if err != nil {
		return Payload{}, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return Payload{}, err
	}
	if len(iv) != gcm.NonceSize() {
		return Payload{}, fmt.Errorf("nonce 長が不正")
	}
	plain, err := gcm.Open(nil, iv, ct, nil)
	if err != nil {
		return Payload{}, fmt.Errorf("復号失敗(鍵違いの可能性): %w", err)
	}
	var p Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		return Payload{}, err
	}
	return p, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("鍵長が不正(32 バイト必要)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
