# ura-talk

声やキー入力をその場で文字にして、**ニコニコ動画風の「流れるコメント」を画面全体のオーバーレイに出す** macOS 常駐ツール。同じルームに参加したメンバーの画面にも同じコメントが流れる。文字起こしは **ローカルの whisper.cpp** で行い、音声は外部に送らない。

イメージは「**副音声 / 裏トーク**」。Gather / Google Meet / Zoom などの会議で、本線を邪魔せず相槌・短いコメントを声やタイプでそっと流す用途。ビデオツール非依存で、どのアプリの上にも流れる(クリックは下のアプリへ素通し)。**会議の画面共有・収録には映らない**ので、裏トークが相手に見えることはない。

- **入口はオーバーレイだけ**: `右⌘` で文字入力バー / `右Shift+右⌘` で音声リッスン(どちらも config で変更可)
- **共有**: ランダムな URL のルームを作り、メンバーに渡すだけ。本文は **E2E 暗号化**され、中継サーバには暗号文しか渡らない
- 音声・文字起こし・整形はすべてローカル完結。メニューバー常駐

読む人ごとに 3 つに分かれています:

- [**Room 参加者向け**](#room-参加者向け) — 招待された人。最低限で参加してコメントを流す
- [**Room 管理者向け(ホスト)**](#room-管理者向けホスト) — ルームを作って配る人。中継サーバや Slack 記録の設定
- [**開発者向け**](#開発者向け) — ビルド・配布・構成・設定リファレンス

共通で **macOS (Apple Silicon)** が必要です。

---

# Room 参加者向け

招待 URL を受け取って参加する人向け。**参加に必要なサーバ情報と復号鍵は招待 URL の中に入っている**ので、中継サーバの URL などを自分で設定する必要はありません。

## 文字だけで参加する(最軽量)

会議音声をスピーカーで流している人や、音声入力が要らない人はこれで十分です。**whisper.cpp もモデルもマイクも Ollama も不要**です。

```sh
brew install go            # ビルドに必要(未導入なら)
make setup                 # config を配置し voice_input を off に(モデルは取得しない)
make app-open              # .app をビルドして起動
```

または、ホストから **`ura-talk.app` の zip を受け取った場合**は、ビルド不要で解凍して起動できます(初回だけ Gatekeeper のため右クリック →「開く」。詳細は[配布](#配布用ビルド))。

起動したら:

1. 初回のみ **アクセシビリティ権限**を許可(`右⌘` の入力バー検知に使う。システム設定 → プライバシーとセキュリティ → アクセシビリティ)
2. メニューバーのアイコン →「**ルームに URL で参加…**」に招待 URL を貼る(コピー済みなら自動で入る。貼り付けは `Cmd+V` 可)
3. **`右⌘`** で画面下部に入力バーが出る。打って **Enter で流す**(バーは開いたままなので連投できる)。閉じるのは **Esc か再度 `右⌘`**

## 声でも参加する

イヤホン/ヘッドホンで会議音声を聞くなら、声もそのまま流せます(スピーカー出力だと相手の声をマイクが拾ってしまうため、後述のとおり自動でオフになります)。

```sh
brew install whisper-cpp   # 文字起こしエンジン
make setup-voice           # config 配置 + whisper モデルを番号で選んで取得
make app-open
```

- 初回に **マイク権限**も許可する
- **`右Shift+右⌘`** で音声リッスンの開始/停止(VAD)。話すと無音の切れ目で自動区切りして流れる。もう一度キーで停止
- リッスン中は**全モニターの画面下部に小さな状態バー**(音量メーター付き)が出て、メニューバーのアイコンがオレンジになる。発話を拾うと赤ドット、文字起こしが並行しているときはロボのバッジ(2件以上は ×N)、リッスンを止めた後も流れ終わるまで「文字起こし中…」が残る。スピーカー出力中でリッスンが始まらないときは「音声オフ」と一瞬表示される。バーは画面共有には映らず、クリックも素通しする(消したいなら config の `voice_bar` を `false`)
- 文字入力バーと音声リッスンは**排他**: リッスン中に文字入力バー(`右⌘`)を開くとリッスンは自動停止し、逆にリッスンを始めると入力途中のバーは閉じる
- `voice_input` は既定 `"auto"`: **イヤホン出力ならオン、スピーカー出力なら自動オフ**(相手の声を拾わないため)。イヤホンを抜き差しすると自動で切り替わる。常に使うなら `"on"`
- (任意)日本語をきれいに整形したいなら Ollama → [ローカル LLM 整形](#ローカル-llm-整形任意)

## 使うときのコツ

- コメントは**接続中の全モニター**に流れる。会議ウィンドウがどの画面にあっても見える
- **画面共有には映らない**ので、Meet で画面共有していても裏トークは相手に見えない
- 匿名ルームでは名前は付かない(起動ごとのランダムな色で同一人物を追える)。記名ルームでは各コメントに `[表示名]` が付く。記名ルームに初めて入るときは表示名を聞かれる(あとで「表示名を変更…」から変えられる)
- **`🔴Slack記録対象`** と出るルームは、ホストが Slack への記録を設定している(→ コメントが Slack チャンネルにも残る)
- `room.server` を設定していない参加専用の状態では、メニューに「新規ルームを作成」は出ない(ルーム作成には中継サーバが要るため)。自分でルームを作りたくなったら [Room 管理者向け](#room-管理者向けホスト) を参照

## 困ったとき

**声が小さい・認識が悪い**(効く順):
1. **自動ゲイン**(既定 ON)。まだ小さいなら `gain.max_gain` を `12 → 20`
2. **VAD で拾われない**: `URATALK_DEBUG=1 ./bin/ura-talk dryrun` で喋ったときの `rms=` を見て `vad.threshold` をその少し下に
3. **初期プロンプト** `whisper_prompt` に想定する口調・語彙(相槌など)を入れる

**マイクに切り替えたのに音声が入らない**: 出力がスピーカーだと `voice_input: "auto"` は自動オフになる(メニューに「音声オフ(スピーカー出力中)」)。イヤホンにするか `voice_input: "on"`。

**「⛔ このルームは無効化または期限切れです」と出る**: そのルームはホストが閉鎖したか、7 日間誰も使わず自動失効した。古い URL では入り直せないので、ホストに新しいルームの URL をもらう。

**モニターを抜き差ししたらコメントが出ない/位置がおかしい**: 起動時の画面構成でオーバーレイを作るため、構成を変えたらアプリを再起動する(メニュー →「終了」→ 再 open、または `make restart`)。

**Bluetooth イヤホンで再生音が途切れる**: 録音開始時にイヤホンが通話プロファイル(HFP)へ切り替わるため(macOS の仕様)。`./bin/ura-talk devices` で内蔵マイク名を調べ、`input_device` に指定して録音だけ内蔵マイクに固定すると回避できる。

## ショートカットキーの変更

`hotkey`(音声リッスン)と `room.input_hotkey`(文字入力バー)で変更(使えるキー名は `./bin/ura-talk keys`)。変更後は `make restart`。

- **単体修飾キー**(`mods` 空): `rightcmd` / `leftcmd` / `rightoption` / `leftoption` / `rightshift` / `leftshift` / `fn`
- **修飾キー2つのコード**: `mods` に押しっぱなしにする側を1つ(例 `{"mods":["rightshift"],"key":"rightcmd"}` = 右⇧+右⌘)。JIS 配列に右⌥ が無い場合に便利
- `hotkey` と `input_hotkey` は別のキーにすること。CGEventTap で検出するため要アクセシビリティ権限(監視のみ)

## ローカル LLM 整形(任意)

whisper の生出力(句読点なし・かな漢字揺れ・フィラー混じり)を **ローカル LLM(Ollama)で整形**する。音声・テキスト処理ともにローカル完結。

```sh
brew install ollama
make enhance-model          # 候補から番号で選ぶと pull + config 更新
make restart
```

- `ollama serve` の手動起動は不要(enhance 有効時、起動時に稼働確認し無ければ自動起動)
- 翻訳・加筆を禁止するプロンプトで整形。**失敗時は生テキストをそのまま流す**ので壊れない
- **`endpoint` には発話本文が送られる**。既定(`enhance.allow_remote: false`)では `localhost` 以外の `endpoint` を拒否し、発話が外部に流出しないようにする

---

# Room 管理者向け(ホスト)

ルームを作ってメンバーに配る人向け。参加者向けのセットアップに加えて、**中継サーバのデプロイ**が要ります(ルーム作成に使う)。

## 中継サーバをデプロイする

`server/` を自分の Cloudflare アカウントにデプロイする(Durable Objects + WebSocket Hibernation。無料枠で収まる想定。サーバは本文を復号できず、保存もしない)。

```sh
(cd server && npx wrangler login)   # ブラウザで Cloudflare に認可(初回のみ)
make deploy                         # テスト実行 → デプロイ。出力される https://ura-talk-room.<account>.workers.dev を控える
```

デプロイした URL を config の **`room.server`** に設定する(ルーム作成に使う。参加するだけの人には不要)。詳細は [server/README.md](server/README.md)、設計は [docs/room-overlay-design.md](docs/room-overlay-design.md)。

**費用**: テキストだけ・超低帯域なので、社内チームの会議で使う限り **Cloudflare 無料枠で収まります**。理由と目安は[アーキテクチャと無料枠のコスト](#アーキテクチャと無料枠のコスト)を参照。

## ルームを作って配る

メニューバーのアイコンから:

- **「新規ルームを作成 — 匿名」** … 名前の出ないルーム
- **「新規ルームを作成 — 記名」** … 各コメントに `[表示名]` が付くルーム。作成/参加時に自分の表示名を確定する(`room.display_name`、未設定なら入力を促す)

作成すると共有 URL がクリップボードに入るので、メンバーに渡す。**URL の `#k=…` に復号鍵が入る**ので、パスワード同様に扱う(公開チャンネルより DM 推奨)。後から人を呼ぶときは「**このルームの URL をコピー**」。コメントの履歴はどこにも残らない(サーバは中継するだけで保存しない)。

## ルームの無効化と自動失効

ルームの URL は放っておくと永久に使い回せてしまうため、2 つの仕組みで寿命を管理している。

**自動失効(7 日間未アクティブ)**

- ルームは**使い続けている限り残る**。誰かが接続するたび・発言するたびに延命される
- 最後のアクティビティから **7 日間**誰も使わなかったルームは自動で失効し、以後その URL では誰も参加できない(常設ルームでも、毎週使っていれば失効しない)
- 日数はサーバ側の設定([server/wrangler.jsonc](server/wrangler.jsonc) の `ROOM_IDLE_TTL_DAYS`)で変更できる

**手動の無効化(作成者のみ)**

- ルームに参加中、メニューの **「このルームを無効化…」** でいつでも閉鎖できる。参加中の全員が即座に切断され、以後その URL では誰も参加できない。**元に戻せない**
- 無効化できるのは作成者だけ。作成時に発行される管理シークレットが作成者のマシンにだけ保存される(`~/Library/Application Support/ura-talk/admin_secrets.json`。共有 URL には含まれない)ため、URL を知っているだけの参加者には閉鎖できない
- いったん退出していても、同じマシンで URL から入り直せばシークレットが復元されて無効化メニューが使える
- メンバーから外したい人がいる場合は、無効化して**新しいルームを作って配り直す**。復号鍵は URL に入っているため回収できないが、無効化すれば中継自体が止まるので、鍵を持っていても以後のコメントは一切届かない

失効・無効化したルームに参加しようとすると、オーバーレイに「⛔ このルームは無効化または期限切れです」と表示される。ルームが生きているかは curl でも確認できる:

```sh
curl -si --http1.1 \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  https://<server>/r/<token>/ws | head -1
# 101 = 生きている / 410 = 無効化・失効済み / 404 = 存在しない
# (--http1.1 必須。HTTP/2 だと Upgrade ヘッダが落ちて常に 426 になる)
```

## Slack に記録する(任意)

ルームのコメントを Slack チャンネルに残したいときは、**あなた(ミラー役)だけ**が Slack を設定すればよい(他のメンバーは何も要らない)。復号済みコメントはあなたのクライアントが持っているので、そこから転送する(中継サーバは本文を復号できない)。

1. Slack アプリを作成 → **Bot Token Scopes** に `chat:write` → ワークスペースにインストールして **Bot Token(`xoxb-…`)** を取得 → 投稿先チャンネルに bot を招待
2. config の `room.slack_bot_token`(または環境変数 `URATALK_SLACK_BOT_TOKEN`)を設定。`room.slack_channel` は作成時に尋ねられるチャンネルの既定値
3. **「新規ルームを作成」時に記録先チャンネルを尋ねられる**(空欄で記録なし)。指定するとそのチャンネルが**ルームの URL に紐づき**、作成者は自動で記録を開始する。ルームごとに別チャンネルにできる
4. **記録対象のルームは全参加者に見える**: 入ると「🔴 このルームは Slack に記録されます」がオーバーレイに出て、メニュー状態にも `🔴Slack記録対象` と表示される(透明性)
5. 記録はメニューの **「Slack に記録」** で止める/再開できる(退出・切断で自動停止)

投稿は bot(アプリ)名義なので、匿名ルームなら Slack 上でも匿名のまま。記名ルームなら `[表示名]` 付き。親メッセージ 1 本の下にスレッドで溜まる。**ミラー役が複数いると二重投稿になる**ので、記録するのは 1 人だけにすること。

---

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
internal/audioout/               既定の音声出力(イヤホン/スピーカー)を判定・監視(voice_input:auto 用)
internal/voicegate/              音声入力の可否(auto の出力追従・リッスン中の自動停止)を集約
internal/namestore/              記名ルームの表示名を config とは別の内部ファイルに永続化
internal/adminstore/             自分が作成したルームの管理シークレット(無効化用)を内部ファイルに永続化
internal/mirror/                 Slack ミラーの状態機械(親メッセージ→スレッド転送)
internal/slack/slack.go          Slack 投稿(bot token で chat.postMessage・スレッド投稿)
internal/modkey/modkey_darwin.go 単体修飾キー・修飾キー2つのコードを CGEventTap で検出(複数キー監視可)
internal/trayicon/trayicon.go    メニューバーのアイコン(待機/聞き取り=オレンジ/録音/文字起こし)
server/                          ルームの中継サーバ(Cloudflare Workers + Durable Objects)
```

## 設定リファレンス (config.json)

**コメントを書ける**(JSONC)。`//` 行コメント・`/* */` ブロックコメント・末尾カンマを許可する(`make setup` が配置する `config.example.json` 自体が各項目コメント付き)。環境変数 `URATALK_WHISPER_MODEL` / `URATALK_SLACK_BOT_TOKEN` / `URATALK_CONFIG` で上書き可能。

<details>
<summary>主な設定キー(クリックで展開)</summary>

| キー | 説明 | 既定 |
|---|---|---|
| `voice_input` | 音声入力の可否。`auto`(イヤホンでオン/スピーカーで自動オフ) / `on` / `off`(文字のみ・マイク/whisper 不要) | `auto` |
| `room.server` | 中継サーバ URL(`server/` のデプロイ先)。**作成時のみ必要**(参加は URL 内の情報を使う) | `""` |
| `room.input_hotkey` | 文字入力バーを出すキー(`hotkey` と同形式。コードも可) | `rightcmd` |
| `room.display_name` | 記名ルームで名乗る表示名。空なら作成/参加時に入力を促す(内部保存) | `""` |
| `room.slack_bot_token` | Slack ミラーの bot token(`xoxb-…`)。env `URATALK_SLACK_BOT_TOKEN` でも可 | `""` |
| `room.slack_channel` | Slack ミラーの投稿先チャンネル(作成時プロンプトの既定値) | `""` |
| `listen_mode` | 入力方式。`ptt`(押下中録音) / `vad`(トグルして自動区切り) | `ptt` |
| `hotkey` | 音声リッスンのホットキー(単体修飾キー / mods+key / コード) | `rightshift+rightcmd` |
| `whisper_bin` | whisper-cli の実行パス(`voice_input:off` なら不要) | `whisper-cli` |
| `whisper_model` | ggml モデルのパス(`voice_input:off` なら不要) | (音声時に必須) |
| `input_device` | 録音に使うマイク名(部分一致)。空でシステム既定 | (既定) |
| `language` | 文字起こし言語(`auto` で自動判定) | `ja` |
| `gain.*` | 録音音声の自動ゲイン(`enabled`/`target_peak`/`max_gain`) | `true`/`0.95`/`12` |
| `whisper_prompt` / `whisper_beam_size` / `whisper_no_speech_thold` | whisper のヒント/精度/無音閾値 | 本文参照 |
| `enhance.*` | ローカル LLM(Ollama)整形。`enabled`/`model`/`endpoint`/`allow_remote` | `enabled:true` |
| `emoji.mode` | 末尾に絵文字。`off`/`light`/`cheerful` | `off` |
| `vad.*` | 無音区切りのパラメータ(`threshold`/`silence_ms` 等) | 本文参照 |
| `sound.*` | 開始/停止の効果音 | `Submarine`/`Bottle` |
| `min_duration_ms` | これ未満の録音は無視(誤爆防止) | `300` |

</details>

## ロードマップ

設計と経緯は [docs/room-overlay-design.md](docs/room-overlay-design.md) を参照。

- ~~**v1**: 匿名・音声 + 入力バー・E2E・Workers デプロイ~~ ✅
- ~~**v1.1**: 記名モード(ルーム作成時に匿名/記名を選択)~~ ✅
- ~~**v1.2**: Slack ミラー(ルームのコメントを Slack チャンネルへ転送)~~ ✅
- 以降の候補: コメント密度に応じた自動フォント縮小、モニター構成変更への自動追従、記録中のリアルタイム告知、配布物の署名/notarize、Windows クライアント(表示側のみ先行)など
