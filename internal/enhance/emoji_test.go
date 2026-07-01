package enhance

import "testing"

func TestFirstEmoji(t *testing.T) {
	cases := map[string]string{
		"👍":       "👍",
		"そうですね👍":  "👍", // 本文が混ざっても絵文字だけ抜く
		"👍です":     "👍",
		" 😊 ":     "😊", // 前後の空白は無視
		"なるほど":    "",  // 絵文字なし
		"":        "",
		"🎉✨":      "🎉",   // ZWJ を挟まない隣接は1つ目だけ
		"❤️":      "❤️",  // ❤ + 異体字セレクタ
		"👍🏻":      "👍🏻",  // 肌色修飾を含める
		"👩‍👧":     "👩‍👧", // ZWJ 連結(家族)は1クラスタ
		"了解しました。": "",
	}
	for in, want := range cases {
		if got := firstEmoji(in); got != want {
			t.Errorf("firstEmoji(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractEmojis(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"🎉👍✨", 3, "🎉👍✨"},     // 隣接3個を全部
		{"🎉👍✨", 1, "🎉"},       // max=1 は先頭のみ
		{"やった🎉🎉🎉😄", 3, "🎉🎉🎉"}, // 本文混在でも絵文字だけ・上限まで
		{"すごい", 3, ""},        // 絵文字なし
		{"👍🏻🎉", 2, "👍🏻🎉"},     // 肌色修飾クラスタ + 次
	}
	for _, c := range cases {
		if got := extractEmojis(c.in, c.max); got != c.want {
			t.Errorf("extractEmojis(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

func TestAppendEmoji(t *testing.T) {
	cases := []struct{ text, emoji, want string }{
		{"ありがとうございます。", "🙏", "ありがとうございます🙏"}, // 文末の句点は絵文字に置換
		{"いいね！", "👍", "いいね！👍"},              // 感嘆符は残す
		{"本当ですか?", "🤔", "本当ですか?🤔"},          // 疑問符は残す
		{"なるほど", "🤔", "なるほど🤔"},              // 句読点なし
		{"そうかも…", "🤔", "そうかも…🤔"},            // 三点リーダは残す
		{"了解です. ", "👍", "了解です👍"},            // 末尾空白+ピリオド
		{"確かに", "", "確かに"},                  // 絵文字なしは素通し
	}
	for _, c := range cases {
		if got := AppendEmoji(c.text, c.emoji); got != c.want {
			t.Errorf("AppendEmoji(%q, %q) = %q, want %q", c.text, c.emoji, got, c.want)
		}
	}
}

func TestNormalizeEmojiMode(t *testing.T) {
	cases := map[string]string{
		"":         "off",
		"off":      "off",
		"Light":    "light",
		" light ":  "light",
		"CHEERFUL": "cheerful",
		"unknown":  "off",
	}
	for in, want := range cases {
		if got := normalizeEmojiMode(in); got != want {
			t.Errorf("normalizeEmojiMode(%q) = %q, want %q", in, got, want)
		}
	}
}
