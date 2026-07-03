# ura-talk-room

ura-talk の「ルーム」機能のための WebSocket リレーサーバ(Cloudflare Workers + Durable Objects)。

参加者が送った E2E 暗号化済みメッセージを、同じルームの全参加者(送信者自身を含む)にそのままブロードキャストする。サーバは本文を復号できず、何も永続化しない。

## API

| エンドポイント | 説明 |
|---|---|
| `POST /rooms` | ルームトークン(base64url 22 文字)を発行して `{"token":"..."}` を返す |
| `GET /r/<token>/ws` | WebSocket にアップグレードし、トークンに対応するルームへ接続 |

制約:

- テキストフレームのみ中継(バイナリは無視)
- メッセージサイズ上限 16KB(超過は黙って破棄)
- 接続ごとに直近 10 秒間 30 メッセージまで(超過は黙って破棄)

## 開発

```sh
npm install
npm test          # vitest (@cloudflare/vitest-pool-workers)
npx wrangler dev  # ローカル起動
```

## デプロイ

```sh
npx wrangler login   # 初回のみ
npx wrangler deploy
```

WebSocket Hibernation API + SQLite-backed Durable Object 構成のため、無料プランでデプロイできる。
