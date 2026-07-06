package room

import (
	"strings"
	"testing"
)

func TestURLRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	r := &Room{
		Server:       "https://gaya-talk-room.example.workers.dev",
		Token:        "abcdefghijKLMNOPQRST12",
		Key:          key,
		Named:        true,
		SlackChannel: "C0123ABCD",
	}
	got, err := Parse(r.URL())
	if err != nil {
		t.Fatalf("Parse(%q): %v", r.URL(), err)
	}
	if got.Server != r.Server || got.Token != r.Token || !got.Named || got.SlackChannel != r.SlackChannel {
		t.Errorf("Parse 結果が一致しない: %+v", got)
	}
	if string(got.Key) != string(r.Key) {
		t.Error("鍵が一致しない")
	}
	if !strings.HasPrefix(got.WSURL(), "wss://") || !strings.HasSuffix(got.WSURL(), "/ws") {
		t.Errorf("WSURL が不正: %s", got.WSURL())
	}
	if strings.Contains(got.WSURL(), "k=") {
		t.Error("WSURL に鍵が漏れている")
	}
}

func TestParseRejectsBadURLs(t *testing.T) {
	key, _ := GenerateKey()
	k := (&Room{Server: "https://example.com", Token: "abcdefghijKLMNOPQRST12", Key: key}).URL()
	k = k[strings.Index(k, "#k=")+3:] // 有効な鍵の base64url 表現
	for _, raw := range []string{
		"",
		"not a url",
		"https://example.com/r/short#k=x", // token 不正
		"https://example.com/r/abcdefghijKLMNOPQRST12",               // 鍵なし
		"https://example.com/r/abcdefghijKLMNOPQRST12#k=dG9vc2hvcnQ", // 鍵が短い
		"ftp://example.com/r/abcdefghijKLMNOPQRST12#k=x",
		// http は localhost 限定(平文 WS でトークンが盗聴できてしまうため)
		"http://example.com/r/abcdefghijKLMNOPQRST12#k=" + k,
		// 細工された Slack チャンネル指定は弾く
		"https://example.com/r/abcdefghijKLMNOPQRST12#k=" + k + "&slack=" + "C0123%20evil",
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) がエラーにならない", raw)
		}
	}
}

func TestParseAllowsLocalhostHTTP(t *testing.T) {
	// wrangler dev 用に localhost の http だけは許す。
	key, _ := GenerateKey()
	u := (&Room{Server: "http://localhost:8787", Token: "abcdefghijKLMNOPQRST12", Key: key}).URL()
	r, err := Parse(u)
	if err != nil {
		t.Fatalf("Parse(%q): %v", u, err)
	}
	if !strings.HasPrefix(r.WSURL(), "ws://localhost:8787/") {
		t.Errorf("WSURL = %s", r.WSURL())
	}
}

func TestValidSlackChannel(t *testing.T) {
	for s, want := range map[string]bool{
		"C0123ABCD": true,
		"#general":  true,
		"":          false,
		"C0123 x":   false,
		"<script>":  false,
	} {
		if got := ValidSlackChannel(s); got != want {
			t.Errorf("ValidSlackChannel(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	p := Payload{Text: "それな", Color: "#ffcc00"}
	data, err := Encrypt(key, p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "それな") {
		t.Error("暗号文に平文が含まれている")
	}
	got, err := Decrypt(key, data)
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Errorf("復号結果が一致しない: %+v", got)
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	key1, _ := GenerateKey()
	key2, _ := GenerateKey()
	data, err := Encrypt(key1, Payload{Text: "secret", Color: "#ffffff"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(key2, data); err == nil {
		t.Error("鍵違いで復号できてしまった")
	}
}
