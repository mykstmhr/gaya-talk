package enhance

import (
	"context"
	"strings"
)

// 絵文字モードごとのシステムプロンプト。本文は変えず、末尾に付ける絵文字1つだけを選ばせる。
// 会話・説明はさせず、絵文字1文字(または無し)だけを出力させる。
const (
	emojiPromptLight = `あなたは日本語の短い発話に合う絵文字を1つだけ選ぶエンジンです。
ルール:
1. 出力は絵文字1文字だけ。説明・文章・記号・引用符は一切出力しない
2. 控えめに。明確に合うときだけ選び、微妙なら何も出力しない(空)
3. 本文は繰り返さない。絵文字以外は書かない`

	emojiPromptCheerful = `あなたは日本語の短い発話を絵文字で華やかに彩るエンジンです。
ルール:
1. 出力は絵文字だけ(1〜3個)。説明・文章・記号・引用符は一切出力しない
2. 明るく前向きに。テンションが上がる絵文字を積極的に付ける
3. 本文は繰り返さない。絵文字以外は書かない。合うものが無ければ空`
)

// モード別の最大付与数。
const (
	maxEmojiLight    = 1
	maxEmojiCheerful = 3
)

// normalizeEmojiMode は表記ゆれを off|light|cheerful に正規化する(不明は off)。
func normalizeEmojiMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "light":
		return "light"
	case "cheerful":
		return "cheerful"
	default:
		return "off"
	}
}

// Emoji は text に合う絵文字を1つ返す(付けないときは空)。本文は一切変えない。
// mode は off|light|cheerful。off/空・モデル未設定・失敗時は空を返す(err は呼び出し側でログ可)。
func (e *Enhancer) Emoji(ctx context.Context, text string) (string, error) {
	if e == nil || e.cfg.Model == "" || strings.TrimSpace(text) == "" {
		return "", nil
	}
	var system string
	var max int
	switch normalizeEmojiMode(e.cfg.EmojiMode) {
	case "light":
		system, max = emojiPromptLight, maxEmojiLight
	case "cheerful":
		system, max = emojiPromptCheerful, maxEmojiCheerful
	default: // off
		return "", nil
	}
	out, err := e.postChat(ctx, []map[string]string{
		{"role": "system", "content": system},
		{"role": "user", "content": text},
	})
	if err != nil {
		return "", err
	}
	return extractEmojis(out, max), nil // LLM が本文を返しても絵文字だけ抜き出す(安全網)
}

// AppendEmoji は本文末尾に絵文字を自然に付ける。文末の句点(。．.)は絵文字で置き換え、
// 感嘆符・疑問符・三点リーダ等はそのまま残して絵文字を続ける。余計な空白は入れない。
// emoji が空なら本文をそのまま返す。
func AppendEmoji(text, emoji string) string {
	if emoji == "" {
		return text
	}
	t := strings.TrimRight(text, " \t　\n\r") // 末尾の空白類を除去
	t = strings.TrimRight(t, "。．.")          // 文末の句点は絵文字に置き換える
	return t + emoji
}

// firstEmoji は文字列から最初の絵文字クラスタ1つを取り出す(extractEmojis の max=1)。
func firstEmoji(s string) string { return extractEmojis(s, 1) }

// extractEmojis は文字列から最大 max 個の絵文字クラスタを順に取り出して連結する。
// 異体字セレクタ・肌色修飾・ZWJ 連結(例: 家族絵文字)は1クラスタとして扱う。
// クラスタ間の非絵文字(空白・本文)は読み飛ばす。LLM が絵文字以外を混ぜても本文が漏れない。
func extractEmojis(s string, max int) string {
	runes := []rune(s)
	var out []rune
	for i, count := 0, 0; i < len(runes) && count < max; {
		if !isEmojiBase(runes[i]) {
			i++
			continue
		}
		end := emojiClusterEnd(runes, i)
		out = append(out, runes[i:end]...)
		count++
		i = end
	}
	return string(out)
}

// emojiClusterEnd は runes[start](絵文字基底)から始まる1クラスタの終端 index を返す。
func emojiClusterEnd(runes []rune, start int) int {
	end := start + 1
	for end < len(runes) {
		r := runes[end]
		switch {
		case r == 0xFE0F || (r >= 0x1F3FB && r <= 0x1F3FF): // 異体字セレクタ / 肌色修飾
			end++
		case r == 0x200D && end+1 < len(runes) && isEmojiBase(runes[end+1]): // ZWJ + 次の絵文字
			end += 2
		default:
			return end
		}
	}
	return end
}

// isEmojiBase は絵文字の基底になり得るコードポイントか(主要ブロックを緩めにカバー)。
func isEmojiBase(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF: // 記号・絵文字・補助記号など主要ブロック
		return true
	case r >= 0x2600 && r <= 0x27BF: // Misc Symbols + Dingbats(☀〜➿)
		return true
	case r >= 0x2B00 && r <= 0x2BFF: // ⭐ など
		return true
	case r == 0x2764, r == 0x2122, r == 0x2139: // ❤ ™ ℹ
		return true
	}
	return false
}
