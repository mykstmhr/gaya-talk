// Package oauth は Slack の OAuth v2 フローを実行し、user token(xoxp)を取得する。
//
// Slack の redirect URL は HTTPS 必須のため、ローカルに自己署名証明書付きの
// HTTPS サーバを一時的に立ててコールバックを受ける。ブラウザでは認可後に
// 証明書警告が一度出るので、それを通過するとトークン交換へ進む。
package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// Result は OAuth 完了後に得られる情報。
type Result struct {
	UserToken string // xoxp- で始まる user token
	UserID    string // 認可したユーザの ID
	TeamName  string // ワークスペース名
}

// Login はブラウザ認可 → コード受領 → トークン交換までを行う。
func Login(ctx context.Context, clientID, clientSecret, redirectURI string, userScopes []string, port int) (*Result, error) {
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	// PKCE(RFC 7636): 認可コードが横取りされても、code_verifier を知らない
	// 第三者はコード交換を完遂できない。ローカルポート固定のコールバックを
	// 別プロセスに先取りされる攻撃(ポートスクワッティング)への防御になる。
	verifier, err := randomVerifier()
	if err != nil {
		return nil, err
	}
	challenge := codeChallengeS256(verifier)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			http.Error(w, "認可がキャンセルされました: "+e, http.StatusBadRequest)
			errCh <- fmt.Errorf("認可エラー: %s", e)
			return
		}
		if q.Get("state") != state {
			http.Error(w, "state が一致しません", http.StatusBadRequest)
			errCh <- fmt.Errorf("state 不一致(CSRF の疑い)")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "code がありません", http.StatusBadRequest)
			errCh <- fmt.Errorf("code が空")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, donePage)
		codeCh <- code
	})

	cert, err := selfSignedCert()
	if err != nil {
		return nil, fmt.Errorf("自己署名証明書の生成失敗: %w", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ポート %d をlistenできません: %w", port, err)
	}
	srv := &http.Server{
		Handler:   mux,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	go srv.ServeTLS(ln, "", "")
	defer srv.Close()

	authURL := authorizeURL(clientID, userScopes, redirectURI, state, challenge)
	fmt.Println("ブラウザで Slack の認可ページを開きます。")
	fmt.Println("もし開かない場合は次の URL を手動で開いてください:")
	fmt.Println("  " + authURL)
	fmt.Println()
	fmt.Println("※ コールバック先 https://localhost は自己署名証明書のため、")
	fmt.Println("  ブラウザの警告画面で「このまま開く / 詳細→続行」を選んでください。")
	_ = openBrowser(authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case code := <-codeCh:
		return exchange(ctx, clientID, clientSecret, code, redirectURI, verifier)
	}
}

// authorizeURL は認可ページの URL を組み立てる。user scope のみ要求する。
// challenge は PKCE の code_challenge(S256)。
func authorizeURL(clientID string, userScopes []string, redirectURI, state, challenge string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("user_scope", strings.Join(userScopes, ","))
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return "https://slack.com/oauth/v2/authorize?" + q.Encode()
}

// exchange は code を oauth.v2.access で user token に交換する。
// verifier は PKCE の code_verifier(認可時に送った challenge の元)。
func exchange(ctx context.Context, clientID, clientSecret, code, redirectURI, verifier string) (*Result, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/oauth.v2.access", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out struct {
		OK         bool   `json:"ok"`
		Error      string `json:"error"`
		AuthedUser struct {
			ID          string `json:"id"`
			AccessToken string `json:"access_token"`
			Scope       string `json:"scope"`
		} `json:"authed_user"`
		Team struct {
			Name string `json:"name"`
		} `json:"team"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("レスポンス解析失敗: %w", err)
	}
	if !out.OK {
		return nil, fmt.Errorf("oauth.v2.access エラー: %s", out.Error)
	}
	if out.AuthedUser.AccessToken == "" {
		return nil, fmt.Errorf("user token が取得できませんでした(user_scope に chat:write があるか確認)")
	}
	return &Result{
		UserToken: out.AuthedUser.AccessToken,
		UserID:    out.AuthedUser.ID,
		TeamName:  out.Team.Name,
	}, nil
}

// randomState は CSRF 対策の state 値を生成する。
func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomVerifier は PKCE の code_verifier を生成する。
// RFC 7636 は 43〜128 文字の [A-Za-z0-9-._~] を求める。32 バイトの乱数を
// base64url(パディング無し)にすると 43 文字になり要件を満たす。
func randomVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// codeChallengeS256 は verifier から code_challenge を作る:
// BASE64URL(SHA256(verifier))(パディング無し)。RFC 7636 の S256 方式。
func codeChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// selfSignedCert は localhost 用の自己署名証明書をメモリ上で生成する。
func selfSignedCert() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

// openBrowser は macOS の open コマンドで URL を開く。
func openBrowser(u string) error {
	return exec.Command("open", u).Start()
}

const donePage = `<!doctype html><html lang="ja"><head><meta charset="utf-8">
<title>ura-talk</title></head>
<body style="font-family:sans-serif;text-align:center;margin-top:4rem">
<h2>✅ 認証が完了しました</h2>
<p>このタブを閉じてターミナルに戻ってください。</p>
</body></html>`
