# Room 管理者向け(ホスト)

ルームを作って配る人向け。参加者のセットアップ([README](../README.md))に加えて、中継サーバのデプロイが要る。

## 中継サーバをデプロイする

```sh
(cd server && npx wrangler login)   # 初回のみ
make deploy                         # テスト → デプロイ。出力される URL を控える
```

出力された URL を config の **`room.server`** に設定する(ルームを作る人だけ。参加者には不要)。テキストのみ + WebSocket Hibernation 構成なので、**チーム会議の用途なら Cloudflare 無料枠で収まる**(目安は [development.md](development.md#アーキテクチャと無料枠のコスト))。API 仕様は [server/README.md](../server/README.md)。

**作成認証(推奨)**: サーバ URL は招待 URL 経由で全参加者に見えるため、既定では URL を知る誰でもルームを作れる。`(cd server && npx wrangler secret put CREATE_SECRET)` で認証を掛け、ルームを作る人の config の **`room.create_secret`** に同じ値を書く。

## ルームの作成と寿命

- メニューの「**新規ルームを作成 — 匿名 / 記名**」→ 共有 URL がクリップボードに入るのでメンバーへ配る(後から呼ぶときは「このルームの URL をコピー」)
- **URL の `#k=…` が復号鍵**。パスワード同様に扱う(公開チャンネルより DM 推奨)。コメントの履歴はどこにも残らない
- ルームは**7 日間未使用で自動失効**する(使い続ける限り残る。日数は [server/wrangler.jsonc](../server/wrangler.jsonc) の `ROOM_IDLE_TTL_DAYS`)
- 作成者はメニューの「**このルームを無効化…**」でいつでも閉鎖できる(全員が即切断・復活不可)。管理シークレットは作成者のマシンにだけ保存され、URL を知っているだけでは無効化できない
- メンバーを外したいときは、無効化して新しいルームを配り直す(鍵は回収できないが中継が止まるので、以後のコメントは一切届かない)

## Slack に記録する(任意)

E2E のためサーバでは記録できず、**記録役 1 人のクライアント**が復号済みコメントを転送する。記録役だけが設定すればよい:

1. Slack アプリを作成 → Bot Token Scopes に `chat:write` → ワークスペースにインストールして xoxb トークンを取得 → 投稿先チャンネルに bot を招待
2. config の `room.slack_bot_token` と `room.slack_channel` を設定
3. ルーム作成時に記録先チャンネルを聞かれる。指定するとチャンネルが URL に紐づき、**全参加者に「🔴Slack記録対象」と表示される**(透明性)。停止/再開はメニューの「Slack に記録」

投稿は bot 名義でスレッドに溜まる。**記録役が複数いると二重投稿になる**ので 1 人だけにすること。

## 撤去(やめるとき)

```sh
(cd server && npx wrangler delete)   # Worker と全ルームのメタデータを削除。全 URL が即座に無効になる
```

メンバーへ事前に知らせること。アプリ本体は [README のアンインストール](../README.md#アンインストール)を参照。Slack アプリを作っていたら削除(またはトークンを revoke)しておく。
