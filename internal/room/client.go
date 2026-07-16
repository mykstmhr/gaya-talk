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

// Status はルームの現在の同時接続数を返す(作成者のみ)。接続数はサーバが中継の
// ために元々持っているメタデータで、これを見ても本文の秘匿性(E2E)は変わらない。
func Status(ctx context.Context, r *Room) (int, error) {
	if r.AdminSecret == "" {
		return 0, fmt.Errorf("管理シークレットがありません(接続数を見られるのはルームの作成者だけです)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/r/%s", trimSlash(r.Server), r.Token), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.AdminSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("接続数の取得に失敗(サーバ %s に届きません): %w", r.Server, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return 0, fmt.Errorf("接続数を取得できません(管理シークレットが一致しません)")
	case http.StatusNotFound:
		return 0, fmt.Errorf("ルームがサーバに存在しません")
	case http.StatusGone:
		return 0, fmt.Errorf("ルームは無効化または失効しています")
	default:
		return 0, fmt.Errorf("接続数の取得に失敗: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Connections int `json:"connections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("接続数レスポンスの解析に失敗: %w", err)
	}
	if out.Connections < 0 {
		return 0, fmt.Errorf("サーバが不正な接続数を返しました: %d", out.Connections)
	}
	return out.Connections, nil
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
	// gen は参加の「世代」。Join・Leave のたびに増え、run goroutine は自分の
	// 世代のときだけ状態を触れる(古い run が新しい参加を壊す競合の防止)。
	gen   uint64
	state State
}

// Join はルームへの接続を開始する(すぐ返る。接続・再接続は裏で行う)。
// 既に別ルームに参加中なら先に退出する。
func (c *Client) Join(r *Room) {
	c.Leave()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.room = r
	c.cancel = cancel
	c.gen++
	gen := c.gen
	c.mu.Unlock()
	go c.run(ctx, r, gen)
}

// Leave はルームから退出する。未参加なら何もしない。
func (c *Client) Leave() {
	c.mu.Lock()
	conn := c.teardownLocked()
	c.mu.Unlock()
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "leave")
	}
	c.setState(StateDisconnected)
}

// leaveIf は gen が現在の世代のときだけ退出する。run の恒久エラー経路用で、
// cancel 前の一瞬に別ルームへ Join されていた場合に、古い run が新しい参加を
// 退出させてしまわないようにする。
func (c *Client) leaveIf(gen uint64) {
	c.mu.Lock()
	if c.gen != gen {
		c.mu.Unlock()
		return
	}
	conn := c.teardownLocked()
	c.mu.Unlock()
	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "leave")
	}
	c.setState(StateDisconnected)
}

// teardownLocked は参加状態を解除する(mu 保持中に呼ぶ)。cancel をロック内で
// 呼ぶことで、run が「Leave の unlock 後・cancel 前」の隙間で新しい接続を保存
// してしまう競合を塞ぐ。閉じるべき conn を返す(Close はロック外で行う)。
func (c *Client) teardownLocked() *websocket.Conn {
	if c.cancel != nil {
		c.cancel()
	}
	conn := c.conn
	c.room = nil
	c.cancel = nil
	c.conn = nil
	c.gen++
	return conn
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
// gen は起動時点の世代(自分が最新の run かの判定に使う)。
func (c *Client) run(ctx context.Context, r *Room, gen uint64) {
	backoff := time.Second
	for {
		c.setStateIf(gen, StateConnecting)
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
				c.leaveIf(gen)
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
		// Leave・別ルームへの Join と競合した場合(世代交代済み)は保持しない。
		if ctx.Err() != nil || c.gen != gen {
			c.mu.Unlock()
			conn.Close(websocket.StatusNormalClosure, "leave")
			return
		}
		c.conn = conn
		c.mu.Unlock()
		c.setStateIf(gen, StateConnected)
		log.Println("✅ ルームに接続しました")
		backoff = time.Second

		hbCtx, hbCancel := context.WithCancel(ctx)
		go heartbeat(hbCtx, conn)
		c.readLoop(ctx, conn, r)
		hbCancel()

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

// heartbeatInterval は心拍の間隔。サーバは「3 拍(90 秒)途絶えたら sleep 中と
// みなして接続数から外す」ので、変えるならサーバの HEARTBEAT_STALE_MS と揃える
// (テストが差し替えられるよう var にしてある)。
var heartbeatInterval = 30 * time.Second

// heartbeat は接続が生きている間、"ping" テキストを定期送信する。サーバは
// エッジで "pong" を自動応答し(DO は起きない)、他の参加者へは中継されない。
// sleep で無言になった接続をサーバ側の人数計測から外すための生存信号。
// 送信失敗はここでは扱わない(切断は readLoop が検知して再接続する)。
func heartbeat(ctx context.Context, conn *websocket.Conn) {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(wctx, websocket.MessageText, []byte("ping"))
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// readLoop は受信→復号→通知を接続が切れるまで繰り返す。復号できない
// メッセージ(鍵違い等)は黙って捨てる。r は run に渡されたルーム
// (c.Room() を読み直すと、Join で差し替わった別ルームの鍵で復号しかねない)。
func (c *Client) readLoop(ctx context.Context, conn *websocket.Conn, r *Room) {
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
		// リプレイ対策: 送信時刻が古すぎる(または未来すぎる)暗号文は捨てる。
		// ID の重複排除は 5 分で忘れるため、それより後の再送はこちらで防ぐ。
		// SentAt=0(旧クライアント)は検査しない。窓は時計ずれを見込んで ±2 分。
		if p.SentAt != 0 {
			if d := time.Since(time.UnixMilli(p.SentAt)); d > 2*time.Minute || d < -2*time.Minute {
				continue
			}
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

// setStateIf は gen が現在の世代のときだけ状態を変える(古い run の遷移を無視し、
// 「退出済みなのに参加中表示」のような巻き戻りを防ぐ)。
func (c *Client) setStateIf(gen uint64, s State) {
	c.mu.Lock()
	if c.gen != gen {
		c.mu.Unlock()
		return
	}
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
