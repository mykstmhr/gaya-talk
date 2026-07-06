package room

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Create は中継サーバにルームを作成し、鍵をローカルで生成して Room を返す。
// 鍵はサーバに送らない(URL フラグメント経由でメンバーにだけ渡る)。
// slackChannel は Slack 記録対象チャンネル(空なら記録対象でない)。
// createSecret はサーバが作成認証(CREATE_SECRET)を有効にしている場合の
// シークレット(空なら送らない)。
func Create(ctx context.Context, server string, named bool, slackChannel, createSecret string) (*Room, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/rooms", trimSlash(server)), nil)
	if err != nil {
		return nil, err
	}
	if createSecret != "" {
		req.Header.Set("Authorization", "Bearer "+createSecret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ルーム作成に失敗(サーバ %s に届きません): %w", server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("ルーム作成に失敗: サーバが作成シークレットを要求しています(config の room.create_secret を設定してください)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ルーム作成に失敗: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Token       string `json:"token"`
		AdminSecret string `json:"adminSecret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ルーム作成レスポンスの解析に失敗: %w", err)
	}
	if !tokenRe.MatchString(out.Token) {
		return nil, fmt.Errorf("サーバが不正なトークンを返しました")
	}
	key, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	return &Room{
		Server:       trimSlash(server),
		Token:        out.Token,
		Key:          key,
		Named:        named,
		SlackChannel: slackChannel,
		AdminSecret:  out.AdminSecret, // 旧サーバは返さない(空のまま=無効化不可)
	}, nil
}

// Revoke はルームを無効化する(作成者のみ)。成功すると全参加者が切断され、
// 以後そのルームには誰も接続できなくなる。元に戻せない。
func Revoke(ctx context.Context, r *Room) error {
	if r.AdminSecret == "" {
		return fmt.Errorf("管理シークレットがありません(無効化できるのはルームの作成者だけです)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		fmt.Sprintf("%s/r/%s", trimSlash(r.Server), r.Token), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.AdminSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("無効化に失敗(サーバ %s に届きません): %w", r.Server, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("無効化できません(管理シークレットが一致しません)")
	case http.StatusNotFound:
		return fmt.Errorf("ルームがサーバに存在しません")
	default:
		return fmt.Errorf("無効化に失敗: HTTP %d", resp.StatusCode)
	}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// State は接続状態(メニューバー表示用)。
type State int

const (
	StateDisconnected State = iota // 未参加・退出済み
	StateConnecting                // 接続中(再接続待ち含む)
	StateConnected                 // 接続済み
)

// Client は 1 つのルームへの接続を保持し、切断されても自動で繋ぎ直す。
// Join〜Leave の間、受信コメントは OnMessage、状態変化は OnState で通知する
// (どちらも接続 goroutine から呼ばれるので重い処理は呼び出し側で逃がすこと)。
type Client struct {
	OnMessage func(Payload)
	OnState   func(State)
	// OnFatal はルームが無効化・失効していて再接続が無意味なとき、退出直前に
	// 理由とともに一度だけ呼ばれる(接続 goroutine から)。
	OnFatal func(reason string)

	mu     sync.Mutex
	room   *Room
	conn   *websocket.Conn
	cancel context.CancelFunc
	state  State
}

// Join はルームへの接続を開始する(すぐ返る。接続・再接続は裏で行う)。
// 既に別ルームに参加中なら先に退出する。
func (c *Client) Join(r *Room) {
	c.Leave()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.room = r
	c.cancel = cancel
	c.mu.Unlock()
	go c.run(ctx, r)
}

// Leave はルームから退出する。未参加なら何もしない。
func (c *Client) Leave() {
	c.mu.Lock()
	cancel := c.cancel
	conn := c.conn
	c.room = nil
	c.cancel = nil
	c.conn = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "leave")
	}
	c.setState(StateDisconnected)
}

// Room は参加中のルームを返す(未参加なら nil)。
func (c *Client) Room() *Room {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.room
}

// Connected は現在サーバと繋がっているかを返す。
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == StateConnected
}

// Send はコメントを暗号化してルームへ送る。未接続ならエラー
// (呼び出し側でローカル表示にフォールバックする)。
func (c *Client) Send(p Payload) error {
	c.mu.Lock()
	conn := c.conn
	r := c.room
	c.mu.Unlock()
	if conn == nil || r == nil {
		return fmt.Errorf("ルームに接続していません")
	}
	data, err := Encrypt(r.Key, p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, data)
}

// run は接続→受信ループ→切断時の再接続、を Leave されるまで繰り返す。
func (c *Client) run(ctx context.Context, r *Room) {
	backoff := time.Second
	for {
		c.setState(StateConnecting)
		conn, resp, err := websocket.Dial(ctx, r.WSURL(), nil)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if reason := permanentReason(resp); reason != "" {
				log.Printf("⛔ %s。再接続しません。", reason)
				if c.OnFatal != nil {
					c.OnFatal(reason)
				}
				c.Leave()
				return
			}
			log.Printf("ルーム接続失敗(%.0f 秒後に再試行): %v", backoff.Seconds(), err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter(backoff)):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		conn.SetReadLimit(64 << 10)

		c.mu.Lock()
		// Leave と競合した場合(cancel 済み)は保持しない。
		if ctx.Err() != nil {
			c.mu.Unlock()
			conn.Close(websocket.StatusNormalClosure, "leave")
			return
		}
		c.conn = conn
		c.mu.Unlock()
		c.setState(StateConnected)
		log.Println("✅ ルームに接続しました")
		backoff = time.Second

		c.readLoop(ctx, conn)

		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		log.Println("⚠️ ルームから切断されました。再接続します…")
	}
}

// readLoop は受信→復号→通知を接続が切れるまで繰り返す。復号できない
// メッセージ(鍵違い等)は黙って捨てる。
func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn) {
	r := c.Room()
	if r == nil {
		return
	}
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		p, err := Decrypt(r.Key, data)
		if err != nil || p.Text == "" {
			continue
		}
		if c.OnMessage != nil {
			c.OnMessage(p)
		}
	}
}

func (c *Client) setState(s State) {
	c.mu.Lock()
	changed := c.state != s
	c.state = s
	cb := c.OnState
	c.mu.Unlock()
	if changed && cb != nil {
		cb(s)
	}
}

// permanentReason は WebSocket ハンドシェイクの HTTP レスポンスから、再接続しても
// 無駄な失敗(ルーム消滅など)を判定する。恒久エラーでなければ空を返す(=再試行)。
func permanentReason(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	switch resp.StatusCode {
	case http.StatusGone: // 無効化 or 未アクティブ失効
		return "このルームは無効化または期限切れです"
	case http.StatusNotFound: // POST /rooms を経ていない token(旧サーバ時代の URL 等)
		return "このルームはサーバに存在しません(URL が古い可能性)"
	}
	return ""
}

// jitter は再接続の同時多発を避けるため待ち時間を ±25% 揺らす。
func jitter(d time.Duration) time.Duration {
	f := 0.75 + rand.Float64()*0.5
	return time.Duration(float64(d) * f)
}
