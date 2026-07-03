# 開発者向け

## ビルドと実行

```sh
make            # ターゲット一覧(help)
make build      # bin/ura-talk を生成
make app        # build/ura-talk.app を生成して署名
make app-open   # ビルドして .app を起動
make restart    # 起動中の .app を停止して開き直す(再ビルドはしない)
make dist       # 配布用に .app を zip 化(dist/ura-talk.app.zip)
make clean      # bin / .app を削除
make logs       # .app のログを追尾(tail -f)
```

> 端末から直接動作確認するなら `go run . dryrun` / `go run . devices` / `go run . overlay-demo`。ただしメニューバー常駐(ホットキー)の権限は署名済み `.app` に紐づくので、実挙動の確認は `make app-open` を使う。

Go 1.26.4+ が必要。Finder/`.app` 起動では作業ディレクトリが `/` になるため、設定は `~/.config/ura-talk/config.json` を読む。ログは `.app` 起動時 `~/Library/Logs/ura-talk.log`(パーミッション `0600`)、端末起動時は標準エラー。発話本文は既定でログに残さず(文字数のみ)、`URATALK_DEBUG=1` のときだけ本文を出す。多重起動はファイルロックで防止。

### 配布用ビルド

`make dist` が `dist/ura-talk.app.zip` を作る。ビルド環境の無いメンバーに配れる。ただし **自己署名/アドホック署名**なので、受け取った人は初回のみ Gatekeeper を通す必要がある:

- Finder でアプリを**右クリック →「開く」**(以後は普通に起動できる)
- またはターミナルで `xattr -dr com.apple.quarantine /path/to/ura-talk.app`

起動後にアクセシビリティ権限を許可する。音声も使う人は別途 `brew install whisper-cpp` とモデルが必要。

> 不特定多数に配るなら Apple Developer ID による署名 + notarization が必要(この構成では未対応)。社内・小チームでの共有を想定。

### 署名(権限を失効させない)

`make app` は安定した自己署名証明書 `ura-talk-dev` で署名する。この証明書がキーチェーンに無いとアドホック署名にフォールバックし、再ビルドのたびに身元が変わって**付与済みの権限が失効**する。一度だけ証明書を作れば解消する: Keychain Access → 証明書アシスタント → 証明書を作成 → 名前 `ura-talk-dev` / 自己署名ルート / コード署名。別名なら `make app SIGN_IDENTITY="名前"`。

## テスト

```sh
go test ./internal/...                                   # 単体テスト
URATALK_E2E_SERVER=https://<your>.workers.dev go test ./internal/room -run E2E   # 実サーバ相手の疎通
cd server && npm test                                    # 中継サーバ(vitest)
```

## アーキテクチャと無料枠のコスト

### データの流れ

```
[各メンバーの Mac(クライアント)]                 [Cloudflare Workers]
 mic → VAD → whisper → LLM整形 ┐
 右⌘ の文字入力バー ───────────┼→ AES-GCM 暗号化 → WebSocket ──→ Room (Durable Object)
                               │                                    │ 同じルームの全接続へ
 透過オーバーレイ ←── 復号 ←────┴──────────────────────────────────┘ ブロードキャスト(送信者含む)
 (記録役だけ) → Slack スレッドへ転送
```

- **すべてクライアントで完結**。文字起こし(whisper)・整形(Ollama)はローカル。コメント本文は各クライアントで **AES-GCM 暗号化**してから送る
- **鍵は共有 URL のフラグメント(`#k=…`)にだけ載る**。フラグメントは HTTP リクエストに含まれないため、**中継サーバ(Cloudflare)には暗号文しか渡らない**(E2E)
- **サーバは本文を保存しない**。ルーム = Durable Object 1 インスタンスで、storage に置くのは管理メタデータ(管理シークレットのハッシュ・作成/最終アクティブ時刻)だけ。全員が切断してアイドルになると DO はメモリから退避され、7 日未アクティブで失効する(判定は接続時の遅延評価。定期クリーンアップは不要)
- 自分のコメントも**サーバのエコー経由**で表示するので、全員の画面で同じ順序で流れる。表示は ID で重複排除
- Slack 記録は E2E のためサーバではできない。**記録役 1 人のクライアント**が復号済みコメントを Slack へ転送する

### なぜ Cloudflare Workers + Durable Objects か

- テキストのみで帯域がほぼゼロ。**WebSocket Hibernation** を使うと、接続していても**発言していない間は計算リソースを消費しない**(アイドル中は DO がメモリから退避される)。会議中ずっと繋ぎっぱなしでも、実際に課金対象になるのは「誰かが喋った/打った瞬間」だけ
- C2C(P2P)も検討したが、WebRTC はシグナリング + NAT 越えの TURN(=結局サーバ)が要り、企業ネットワークで安定しない。テキスト中継なら軽量な WebSocket リレーの方が確実

### 無料枠でどれくらい流せるか

いちばん効くのは **Durable Objects のリクエスト数**で、ざっくり **「コメント数 × 参加人数」** で消費する(1 コメントを人数ぶん配信するため)。

| 無料枠の項目 | 目安の上限/日 | このアプリの消費 |
|---|---|---|
| Workers リクエスト | 10万/日 | ルーム作成 + WebSocket 接続の張り直し程度。会議数十回でも数百 |
| Durable Objects リクエスト | 10万/日 | コメント数 × 参加人数(実質のカウント対象) |
| DO 計算時間 | 13,000 GB秒/日 | Hibernation で発言していない間はほぼ 0 |

例: **5 人ルームで 1 時間に 200 コメント**流しても約 1,000 リクエスト。1 日に何十セッションやっても 10万/日 には届かない。上限に当たるのは「数十人規模で全員が高頻度に流し続ける」ような極端なケースだけで、通常のチーム会議の裏トーク用途では無料枠で困らない。使用量は Cloudflare ダッシュボード → Workers & Pages で確認できる。

## 構成

```
main.go                          サブコマンド(run/dryrun/devices/keys/overlay-demo)・メニューバー常駐・PTT/VAD ループ
room_ui.go                       ルームのメニュー配線・送受信(作成/参加/退出/無効化・URLコピー・表示名・Slack記録の配線)
internal/config/config.go        設定の読み込み・検証(JSONC)
internal/recorder/recorder.go    マイク録音 (malgo)。バッファ録音とストリーム録音
internal/vad/vad.go              音声ストリームを無音で発話単位に区切る(VAD)
internal/transcribe/whisper.go   whisper-cli 呼び出し(ローカル STT)・ノイズ除去
internal/enhance/enhance.go      文字起こしをローカル LLM(Ollama)で整形・絵文字付与(任意)
internal/room/                   ルーム: 共有 URL・E2E 暗号化(AES-GCM)・自動再接続つき WS クライアント
internal/overlay/                ニコニコ風オーバーレイ(透過・クリック貫通・全モニター・画面共有に映らない)
internal/inputbar/               Spotlight 風の文字入力バー(非アクティブ化パネル)
internal/voicebar/               リッスン/録音中の状態バー(音量メーター。画面共有に映らない)
internal/trayicon/               メニューバーの状態アイコン(build/genicons.swift で生成)
internal/dialog/                 モーダル入力ダイアログ(URL 参加・表示名入力用)
internal/audioout/               既定の音声出力(イヤホン/スピーカー)を判定・監視(voice.input:auto 用)
internal/voicegate/              音声入力の可否(auto の出力追従・リッスン中の自動停止)を集約
internal/namestore/              記名ルームの表示名を config とは別の内部ファイルに永続化
internal/adminstore/             自分が作成したルームの管理シークレット(無効化用)を内部ファイルに永続化
internal/mirror/                 Slack ミラーの状態機械(親メッセージ→スレッド転送)
internal/slack/slack.go          Slack 投稿(bot token で chat.postMessage・スレッド投稿)
internal/modkey/modkey_darwin.go 単体修飾キー・修飾キー2つのコードを CGEventTap で検出(複数キー監視可)
server/                          ルームの中継サーバ(Cloudflare Workers + Durable Objects)
```

## 設定リファレンス (config.json)

各キーの説明と既定値は **[config.example.json](../config.example.json) のコメントが正**(`make setup` が配置するファイルそのもの)。ここでは全体像だけ:

- **コメントを書ける**(JSONC)。`//` 行コメント・`/* */` ブロックコメント・末尾カンマを許可する
- トップレベルは主従を反映したブロック構成: `room`(オーバーレイ共有)+ `input_hotkey`(文字入力バー)が主、`voice`(音声入力)と `whisper`(文字起こし)がサブ。ほかに `sound`(効果音)・`enhance`(Ollama 整形)・`emoji`
- 旧スキーマ(`voice_input` / `whisper_model` などトップレベルのフラットなキー、`room.input_hotkey`)の config もそのまま読める(新キーが優先)
- 環境変数 `URATALK_ROOM_SERVER` / `URATALK_WHISPER_MODEL` / `URATALK_SLACK_BOT_TOKEN` / `URATALK_CONFIG` で上書き可能

## ロードマップ

設計と経緯は [room-overlay-design.md](room-overlay-design.md) を参照。

- ~~**v1**: 匿名・音声 + 入力バー・E2E・Workers デプロイ~~ ✅
- ~~**v1.1**: 記名モード(ルーム作成時に匿名/記名を選択)~~ ✅
- ~~**v1.2**: Slack ミラー(ルームのコメントを Slack チャンネルへ転送)~~ ✅
- 以降の候補: コメント密度に応じた自動フォント縮小、記録中のリアルタイム告知、配布物の署名/notarize、Windows クライアント(表示側のみ先行)など
