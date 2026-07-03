package config

import (
	"encoding/json"
	"os"
	"testing"
)

// TestShippedExampleParses は出荷する config.example.json(コメント付き)が
// 常に JSONC として読めることを保証する(将来 example を編集して壊すのを防ぐ)。
func TestShippedExampleParses(t *testing.T) {
	data, err := os.ReadFile("../../config.example.json")
	if err != nil {
		t.Fatalf("config.example.json を読めない: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(stripJSONC(data), &cfg); err != nil {
		t.Fatalf("config.example.json が JSONC として壊れている: %v", err)
	}
}

func TestStripJSONC(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"行コメント", "{\n  \"a\": 1 // メモ\n}", "{\n  \"a\": 1 \n}"},
		{"ブロックコメント", "{/* x */\"a\":1}", "{\"a\":1}"},
		{"末尾カンマ(オブジェクト)", "{\"a\":1,}", "{\"a\":1}"},
		{"末尾カンマ(配列)", "[1,2,]", "[1,2]"},
		{"末尾カンマ+改行", "{\n  \"a\":1,\n}", "{\n  \"a\":1\n}"},
		// 文字列内の // や , や /* は壊さない。
		{"文字列内のURL", "{\"u\":\"http://x/y\"}", "{\"u\":\"http://x/y\"}"},
		{"文字列内のコメント風", "{\"s\":\"a // b /* c */ d\"}", "{\"s\":\"a // b /* c */ d\"}"},
		{"文字列内のカンマ前の閉じ括弧", "{\"s\":\"x,}\"}", "{\"s\":\"x,}\"}"},
		{"エスケープされた引用符", "{\"s\":\"he said \\\"hi\\\" // no\"}", "{\"s\":\"he said \\\"hi\\\" // no\"}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(stripJSONC([]byte(c.in))); got != c.want {
				t.Errorf("stripJSONC(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// TestStripJSONCParses はコメント付き設定が実際に Unmarshal できることを確認する。
func TestStripJSONCParses(t *testing.T) {
	in := []byte(`{
  // 中継サーバ
  "room": {
    "server": "https://example.workers.dev", // ルーム中継先
    "display_name": "",  /* 匿名 */
  },
  "language": "ja", // 文字起こし言語
}`)
	var cfg Config
	if err := json.Unmarshal(stripJSONC(in), &cfg); err != nil {
		t.Fatalf("コメント付き設定の Unmarshal に失敗: %v", err)
	}
	if cfg.Room.Server != "https://example.workers.dev" {
		t.Errorf("room.server = %q", cfg.Room.Server)
	}
	if cfg.Language != "ja" {
		t.Errorf("language = %q, want ja", cfg.Language)
	}
}
