package transcribe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupStaleTempFiles(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		return p
	}

	stale := write("ura-talk-old.wav", time.Hour)     // 古い → 消える
	fresh := write("ura-talk-new.wav", time.Second)   // 新しい(処理中かも)→ 残す
	other := write("important.wav", time.Hour)          // 対象外 → 残す

	n, err := CleanupStaleTempFiles(dir, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("削除件数: got %d, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("古い一時ファイルは削除されるべき: %s", stale)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("新しい一時ファイルは残すべき: %s", fresh)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("対象外ファイルは残すべき: %s", other)
	}
}

func TestClean(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"こんにちは", "こんにちは"},
		{"  なるほど \n", "なるほど"},
		{"いいですね\nそうしましょう", "いいですね そうしましょう"},
		{"[BLANK_AUDIO]", ""},
		{"(拍手)", ""},
		{"（音楽）", ""},
		{"[Music]", ""},
		{"♪♪♪", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := clean(c.in); got != c.want {
			t.Errorf("clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsNoise(t *testing.T) {
	noisy := []string{"[BLANK_AUDIO]", "(拍手)", "（笑い声）", "[Applause]", "♪"}
	for _, s := range noisy {
		if !isNoise(s) {
			t.Errorf("isNoise(%q) = false, want true", s)
		}
	}
	ok := []string{"こんにちは", "それ賛成です", "(笑) と言いつつ続ける文"}
	for _, s := range ok {
		if isNoise(s) {
			t.Errorf("isNoise(%q) = true, want false", s)
		}
	}
}

func TestIsHallucination(t *testing.T) {
	junk := []string{
		"ご視聴ありがとうございました",
		"ご視聴ありがとうございました。",
		"ご視聴 ありがとうございました",
		"チャンネル登録お願いします",
	}
	for _, s := range junk {
		if !isHallucination(s) {
			t.Errorf("isHallucination(%q) = false, want true", s)
		}
	}
	// 正当な発話は弾かない(部分一致で誤爆しないこと)。
	ok := []string{"ありがとうございました", "ご視聴ありがとうございましたと彼は言った", "なるほど"}
	for _, s := range ok {
		if isHallucination(s) {
			t.Errorf("isHallucination(%q) = true, want false", s)
		}
	}
}
