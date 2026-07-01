package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// verifier は RFC 7636 の unreserved 文字([A-Za-z0-9-._~])43〜128 文字であること。
var verifierRe = regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)

func TestRandomVerifier(t *testing.T) {
	v1, err := randomVerifier()
	if err != nil {
		t.Fatalf("randomVerifier: %v", err)
	}
	if !verifierRe.MatchString(v1) {
		t.Fatalf("verifier が RFC 7636 の書式に合わない: %q (len=%d)", v1, len(v1))
	}
	// 毎回異なる(乱数)こと。
	v2, err := randomVerifier()
	if err != nil {
		t.Fatalf("randomVerifier: %v", err)
	}
	if v1 == v2 {
		t.Fatalf("verifier が固定値になっている: %q", v1)
	}
}

func TestCodeChallengeS256(t *testing.T) {
	// RFC 7636 Appendix B の公式テストベクタで検証する。
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const wantChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := codeChallengeS256(verifier); got != wantChallenge {
		t.Fatalf("codeChallengeS256 = %q, want %q", got, wantChallenge)
	}

	// 自前生成の verifier でも SHA256→base64url と一致すること。
	v, _ := randomVerifier()
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got := codeChallengeS256(v); got != want {
		t.Fatalf("codeChallengeS256(自前) = %q, want %q", got, want)
	}
}

func TestAuthorizeURLIncludesPKCE(t *testing.T) {
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	raw := authorizeURL("cid", []string{"chat:write"}, "https://localhost:53682/oauth/callback", "state123", challenge)

	if !strings.HasPrefix(raw, "https://slack.com/oauth/v2/authorize?") {
		t.Fatalf("認可 URL のベースが不正: %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("URL パース失敗: %v", err)
	}
	q := u.Query()
	if got := q.Get("code_challenge"); got != challenge {
		t.Errorf("code_challenge = %q, want %q", got, challenge)
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if got := q.Get("state"); got != "state123" {
		t.Errorf("state = %q, want state123", got)
	}
	if got := q.Get("user_scope"); got != "chat:write" {
		t.Errorf("user_scope = %q, want chat:write", got)
	}
}
