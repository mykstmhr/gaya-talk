# gaya-room

gaya の「ルーム」機能のための WebSocket リレーサーバ(Cloudflare Workers + Durable Objects)。

参加者が送った E2E 暗号化済みメッセージを、同じルームの全参加者(送信者自身を含む)にそのままブロードキャストする。サーバは本文を復号できず、保存もしない(保存するのはルームごとの管理メタデータ = 管理シークレットのハッシュとタイムスタンプだけ)。

## API

| エンドポイント | 説明 |
|---|---|
| `POST /rooms` | ルームを作成し `{"token":"...","adminSecret":"..."}` を返す(どちらも base64url 22 文字)。`adminSecret` は無効化用で、作成者だけが保持する(共有 URL には載せない)。サーバに `CREATE_SECRET` が設定されている場合は `Authorization: Bearer <CREATE_SECRET>` が必要(欠落・不一致は 401) |
| `GET /r/<token>/ws` | WebSocket にアップグレードし、トークンに対応するルームへ接続 |
| `DELETE /r/<token>` | ルームを無効化する(`Authorization: Bearer <adminSecret>`)。全参加者を切断し、以後の接続を拒否する。元に戻せない |

制約:

- テキストフレームのみ中継(バイナリは無視)
- メッセージサイズ上限 16KB(超過は黙って破棄)
- 接続ごとに直近 10 秒間 30 メッセージまで(超過は黙って破棄)
- 1 ルームの同時接続は 32 まで(超過は 503)

## ルームのライフサイクル

- ルームは `POST /rooms` で作られたものだけが有効。適当なトークンで接続しても 404(リレーのタダ乗り防止)
- **未アクティブ失効**: 最後の接続・発言から `ROOM_IDLE_TTL_DAYS`(wrangler.jsonc の vars、既定 7 日)を超えたルームは失効し、以後の接続は 410。使い続けている限り(接続 or 発言があれば)失効しない
- **無効化**: 作成者は `DELETE /r/<token>` でいつでも閉鎖できる。以後の接続は 410
- 失効・無効化を検知した時点で、接続中のソケットもすべて閉じる(繋ぎっぱなしの接続が中継し続けないように)
- 失効・無効化したルームは復活しない(数十バイトの墓標レコードを Durable Object に残し、同じ URL での再生成を防ぐ)。判定はすべて接続・発言時の遅延評価なので、定期クリーンアップのジョブは不要

## 作成認証(任意)

サーバ URL は招待 URL の一部として全参加者に見えるため、既定ではその URL を知る誰でも `POST /rooms` でルームを作れる。第三者のタダ乗りや大量作成を防ぎたい場合は、作成認証を有効にする:

```sh
npx wrangler secret put CREATE_SECRET   # 任意の文字列を設定
```

設定後は、ルームを作成する人だけがクライアント側の config(`room.create_secret` または環境変数 `GAYA_ROOM_CREATE_SECRET`)に同じ値を設定する。参加だけのメンバーには不要。

## 開発

```sh
npm install
npm test          # vitest (@cloudflare/vitest-pool-workers)
npx wrangler dev  # ローカル起動
```

## デプロイ

```sh
npx wrangler login   # 初回のみ
npx wrangler deploy  # またはリポジトリルートで make deploy(テストを通してからデプロイする)
```

WebSocket Hibernation API + SQLite-backed Durable Object 構成のため、無料プランでデプロイできる。
