package config

// stripJSONC は JSONC(`//` 行コメント・`/* */` ブロックコメント・末尾カンマ)を
// 素の JSON へ変換する。文字列リテラル内の `//` や `,`(例: "http://..." やメッセージ)
// はそのまま残す。Go 標準の encoding/json はコメントを解さないため、Unmarshal の前段に挟む。
func stripJSONC(src []byte) []byte {
	out := make([]byte, 0, len(src))
	inStr := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(src) { // エスケープ: 次の1文字はそのまま退避
				out = append(out, src[i+1])
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			// 行コメント: 改行の手前まで読み飛ばす(改行自体は次の反復で残す)。
			i += 2
			for i < len(src) && src[i] != '\n' {
				i++
			}
			i-- // ループの i++ と相殺して改行を残す
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			// ブロックコメント: `*/` まで読み飛ばす。
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i++ // `*/` の `/` まで進める(ループの i++ で次へ)
		default:
			out = append(out, c)
		}
	}
	return removeTrailingCommas(out)
}

// removeTrailingCommas は `}` や `]` の直前に置かれた末尾カンマを除去する(文字列内は対象外)。
// コメント除去後に呼ばれる前提なのでコメントは考慮しない。
func removeTrailingCommas(src []byte) []byte {
	out := make([]byte, 0, len(src))
	inStr := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(src) {
				out = append(out, src[i+1])
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r') {
				j++
			}
			if j < len(src) && (src[j] == '}' || src[j] == ']') {
				continue // 末尾カンマ → 出力しない
			}
		}
		out = append(out, c)
	}
	return out
}
