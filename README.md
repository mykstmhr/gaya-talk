# ura-talk

声やキー入力をその場で文字にして、**ニコニコ動画風の「流れるコメント」を画面全体のオーバーレイに出す** macOS 常駐ツール。同じルームに参加したメンバーの画面にも同じコメントが流れる。文字起こしは **ローカルの whisper.cpp** で行い、音声は外部に送らない。

イメージは「**副音声 / 裏トーク**」。Gather / Google Meet / Zoom などの会議で、本線を邪魔せず相槌・短いコメントを声やタイプでそっと流す用途。ビデオツール非依存で、どのアプリの上にも流れる(クリックは下のアプリへ素通し)。**会議の画面共有・収録には映らない**ので、裏トークが相手に見えることはない。

- **入口**: `右Shift+右⌘` で音声リッスン / `右⌘` で文字入力バー(どちらも config で変更可)
- **共有**: ランダムな URL のルームを作り、メンバーに渡すだけ。本文は **E2E 暗号化**され、中継サーバには暗号文しか渡らない
- **入力方式**: `vad`(無音で自動区切り・既定) / `ptt`(押している間だけ録音)
- 音声・文字起こし・整形はすべてローカル完結。メニューバー常駐(マイクのアイコン。聞き取り中はオレンジ)

---

# 利用者向け

## 必要なもの

- macOS (Apple Silicon)
- whisper.cpp(`brew install whisper-cpp`)
- ルームでメンバーと共有するなら、中継サーバ(`server/`)を自分の Cloudflare アカウントにデプロイ → [中継サーバ](#中継サーバルーム共有に必要)

## セットアップ

```sh
brew install whisper-cpp   # 文字起こしエンジン(初回のみ)
make setup                 # config を配置 + whisper モデルを選んで取得(番号選択)
make app-open              # .app をビルドして起動。初回はマイク/アクセシビリティを許可
```

- 設定は **`~/.config/ura-talk/config.json`**(`make setup` が配置。`.app` はここを読む)。全項目は[設定](#設定-configjson)を参照。
- config を編集したら **`make restart`** で反映。
- モデルは `~/.config/ura-talk/models/`。`make setup` 時に **番号で選択**(1=turbo 速い・軽い / 2=large-v3 高精度・重い / 3=turbo 量子化 最軽量)。あとから変えたいときは **`make whisper-model`** で選び直す。特定モデルを直接入れるなら `make model MODEL=<ファイル名>`。

> 見た目だけ先に試すなら `make build && ./bin/ura-talk overlay-demo`(サンプルコメントが流れる)。

### 権限

初回起動時に **マイク**(録音)と **アクセシビリティ/入力監視**(グローバルホットキー検知)の両方を許可する(システム設定 → プライバシーとセキュリティ)。`.app` で起動しているので、権限は「ura-talk.app」に紐づく。

<details>
<summary><strong>再ビルドで権限が失効する場合(安定した自己署名で署名する)</strong></summary>

`make app` は安定した自己署名証明書 `ura-talk-dev` で署名する。この証明書がキーチェーンに無いとアドホック署名にフォールバックし、再ビルドのたびに身元が変わって**付与済みのアクセシビリティ等の許可が失効**する。一度だけ証明書を作っておけば解消する:

1. **Keychain Access** → メニュー **証明書アシスタント → 証明書を作成…**
2. 名前 `ura-talk-dev` / 固有名のタイプ **自己署名ルート** / 証明書のタイプ **コード署名**
3. 以後 `make app` はこの証明書で署名する(別名なら `make app SIGN_IDENTITY="名前"`)
4. アクセシビリティ/入力監視/マイクを一度だけ許可すれば、再ビルドしても許可が残る
</details>

## 使い方

メニューバーのアイコンで状態が分かる: **マイク(黒)** 待機 / **マイク(オレンジ)** 聞き取り中 / **赤丸の点滅** 音声検出中 / **吹き出し** 文字起こし中。アイコンをクリックすると現在の状態・動作情報(`方式` / `キー`)とルーム操作メニューが開く。

### コメントを流す

- **音声**: `右Shift+右⌘` でリッスン開始(VAD)。話すと無音の切れ目で自動区切りして流れる。もう一度キーで停止。PTT のときは押している間だけ録音。
- **文字**: `右⌘` で画面下部に入力バーが出る。打って **Enter で流して閉じる**、**Esc でキャンセル**。入力バーは今カーソルがあるモニターに出る。

どちらも whisper→ローカル LLM 整形を通ってからオーバーレイに流れる(整形は任意)。

> **音声入力の自動オフ(`voice_input`)**: スピーカーで会議音声を流していると内蔵マイクが相手の声も拾ってしまう(macOS のエコーキャンセルは各アプリ個別で、ura-talk の録音には効かない)。そこで既定の **`"auto"`** では、**出力がスピーカー(内蔵・HDMI 等)のときは音声入力を自動でオフ**にし、**イヤホン/ヘッドホン(Bluetooth・USB・ヘッドホン端子)のときはオン**にする。イヤホンを抜き差しすると自動で切り替わる(再起動不要)。常に音声を使うなら `"on"`、文字入力バー(`右⌘`)だけで使う(マイク/whisper 不要)なら `"off"`。

### メンバーと共有する(ルーム)

1. ホストがメニューバー →「**新規ルームを作成して URL をコピー**」。共有 URL がクリップボードに入る
2. その URL を Slack の DM などでメンバーに渡す(URL の `#k=…` に**復号鍵**が入るので、パスワード同様に扱う)
3. メンバーはメニューバー →「**ルームに URL で参加…**」。コピー済みなら自動で入るのでそのまま参加
4. 以降、全員のコメントが全員の画面に流れる。後から人を呼ぶときは「**このルームの URL をコピー**」
5. 終わったら「**ルームから退出**」。全員が抜けてアイドルになればルームは中継サーバ上から自然消滅する(履歴はどこにも残らない)

**ソロモード**: ルーム未参加(または `room.server` 未設定)でも、自分のコメントは自分の画面に流れる。

**匿名**: コメントに名前は付かない。起動ごとにランダムな色が割り当てられ、同一人物の発言は色で追える。

## 困ったとき

**声が小さい・認識が悪い**(効く順):
1. **自動ゲイン**(既定 ON)。まだ小さいなら `gain.max_gain` を `12 → 20`。
2. **VAD で拾われない**: `URATALK_DEBUG=1 ./bin/ura-talk dryrun` で喋ったときの `rms=` を見て、`vad.threshold` をその少し下に。
3. **初期プロンプト** `whisper_prompt` に想定する口調・語彙(相槌など)を入れる。
4. **no-speech 閾値** `whisper_no_speech_thold` を `0.6 → 0.3`(小声を拾うが幻聴増→フィルタで吸収)。
5. **ビーム幅** `whisper_beam_size` を `5 → 8`(精度↑・速度↓)。

**マルチモニター**: コメントは接続中の全モニターに流れる。入力バーはカーソルのあるモニターに出る。モニターを抜き差ししたらアプリを再起動する(起動時の画面構成でオーバーレイを作るため)。

**Whisper の幻聴**: 無音・雑音区間に「ご視聴ありがとうございました」等の定型句が出ることがある。末尾無音をトリムしたうえで既知フレーズ(`internal/transcribe/whisper.go` の `hallucinations`)を除外する。他の幻聴句が出たらこのリストに追加する。

**Bluetooth イヤホンで再生音が途切れる**: 録音開始時にイヤホンが通話プロファイル(HFP)へ切り替わるため(macOS の仕様)。`./bin/ura-talk devices` で内蔵マイク名を調べ、`input_device` に指定して録音だけ内蔵マイクに固定すると回避できる。

## ローカル LLM 整形(任意)

whisper の生出力(句読点なし・かな漢字揺れ・フィラー混じり)を **ローカル LLM(Ollama)で整形**する。音声・テキスト処理ともにローカル完結。

```sh
brew install ollama    # 未導入なら
make enhance-model      # 候補から番号で選ぶと pull + config 更新
make restart            # 反映
```

- `ollama serve` の手動起動は不要(enhance 有効時、起動時に稼働確認し、無ければ自動起動)。
- 翻訳・加筆を禁止するプロンプトで整形。**失敗時は生テキストをそのまま流す**ので壊れない。
- **`endpoint` には発話本文がそのまま送られる**。既定(`enhance.allow_remote: false`)では `localhost` / `127.0.0.1` / `::1` 以外の `endpoint` を拒否し、発話が外部に流出しないようにする。意図的にリモート Ollama を使う場合のみ `enhance.allow_remote: true`。

## ショートカットキーの変更

`hotkey`(音声リッスン)と `room.input_hotkey`(文字入力バー)で変更(使えるキー名は `./bin/ura-talk keys`)。変更後は `make restart`。

```jsonc
"hotkey": { "mods": ["rightshift"], "key": "rightcmd" }  // 既定: 右⇧を押しながら右⌘
"room": { "input_hotkey": { "mods": [], "key": "rightcmd" } }  // 既定: 右⌘ 単体
```

- **単体修飾キー**(`mods` 空): `rightcmd` / `leftcmd` / `rightoption` / `leftoption` / `rightshift` / `leftshift` / `fn`。
- **修飾キー2つのコード**: `mods` に押しっぱなしにする側を1つ書く(例 `["rightshift"]` + `"rightcmd"` = 右⇧+右⌘)。JIS 配列に右⌥ が無い場合に便利。
- **組み合わせ**: `mods` = `ctrl`/`shift`/`option`(`alt`)/`cmd`、`key` = `a`〜`z` / `0`〜`9` / `f1`〜`f20` / `space` / `return` など。
- CGEventTap で検出するため要アクセシビリティ権限(監視のみ、本来の動作は奪わない)。`hotkey` と `input_hotkey` は別のキーにすること。

## 中継サーバ(ルーム共有に必要)

`server/` を自分の Cloudflare アカウントにデプロイして使う(Durable Objects + WebSocket Hibernation。無料枠で収まる想定)。

```sh
cd server
npm install
npx wrangler login    # ブラウザで Cloudflare に認可(初回のみ)
npx wrangler deploy   # 出力される https://ura-talk-room.<account>.workers.dev を控える
```

デプロイした URL を config の `room.server` に設定する。サーバは本文を復号できず、何も永続化しない。詳細は [server/README.md](server/README.md)、設計は [docs/room-overlay-design.md](docs/room-overlay-design.md)。

## 設定 (config.json)

**コメントを書ける**(JSONC)。`//` 行コメント・`/* */` ブロックコメント・末尾カンマを許可する(`make setup` が配置する `config.example.json` 自体が各項目コメント付き)。環境変数 `URATALK_WHISPER_MODEL` / `URATALK_CONFIG` で上書き可能。

<details>
<summary>主な設定キー(クリックで展開)</summary>

| キー | 説明 | 既定 |
|---|---|---|
| `room.server` | 中継サーバ URL(`server/` のデプロイ先)。空ならソロモード | `""` |
| `room.input_hotkey` | 文字入力バーを出すキー(`hotkey` と同形式。コードも可) | `rightcmd` |
| `room.display_name` | 記名モードのルームで名乗る表示名(空なら匿名) | `""` |
| `voice_input` | 音声入力の可否。`auto`(出力がイヤホンならオン/スピーカーなら自動オフ) / `on` / `off`(文字のみ・マイク不要)。旧 `true`/`false` も可 | `auto` |
| `listen_mode` | 入力方式。`ptt`(押下中録音) / `vad`(トグルして自動区切り) | `ptt` |
| `hotkey` | 音声リッスンのホットキー(単体修飾キー / mods+key / コード) | `rightshift+rightcmd` |
| `whisper_bin` | whisper-cli の実行パス | `whisper-cli` |
| `whisper_model` | ggml モデルのパス(`~` 展開可) | (必須) |
| `input_device` | 録音に使うマイク名(部分一致)。空でシステム既定 | (既定) |
| `language` | 文字起こし言語(`auto` で自動判定) | `ja` |
| `gain.enabled` / `gain.target_peak` / `gain.max_gain` | 録音音声の自動ゲイン | `true` / `0.95` / `12` |
| `whisper_prompt` | 初期プロンプト(口語・語彙のヒント) | (なし) |
| `whisper_beam_size` | ビーム幅(上げると精度↑・速度↓) | `5` |
| `whisper_no_speech_thold` | no-speech 閾値(0で既定0.6) | `0`(=0.6) |
| `enhance.*` | ローカル LLM(Ollama)整形。`enabled` / `model` / `endpoint` / `allow_remote` 等 | `enabled:true` |
| `emoji.mode` | 末尾に絵文字を付ける。`off` / `light` / `cheerful` | `off` |
| `vad.*` | 無音区切りのパラメータ(`threshold` / `silence_ms` 等) | 本文参照 |
| `sound.*` | 開始/停止の効果音 | `Submarine` / `Bottle` |
| `min_duration_ms` | これ未満の録音は無視(誤爆防止) | `300` |

</details>

---

# 開発者向け

## ビルドと実行

```sh
make           # ターゲット一覧(help)
make build     # bin/ura-talk を生成
make app       # build/ura-talk.app を生成して署名
make app-open  # ビルドして .app を起動
make restart   # 起動中の .app を停止して開き直す(再ビルドはしない)
make clean     # bin / .app を削除
make logs      # .app のログを追尾(tail -f)
```

> 端末から直接動作確認するなら `go run . dryrun` / `go run . devices` / `go run . overlay-demo`。ただしメニューバー常駐(ホットキー)の権限は署名済み `.app` に紐づくので、実挙動の確認は `make app-open` を使う。

Go 1.26.4+ が必要。Finder/`.app` 起動では作業ディレクトリが `/` になるため、設定は `~/.config/ura-talk/config.json` を読む。ログは `.app` 起動時 `~/Library/Logs/ura-talk.log`(パーミッション `0600`)、端末起動時は標準エラー。発話本文は既定でログに残さず(文字数のみ)、`URATALK_DEBUG=1` のときだけ本文を出す。多重起動はファイルロックで防止。

## 構成

```
main.go                          サブコマンド(run/dryrun/devices/keys/overlay-demo)・メニューバー常駐・PTT/VAD ループ
room_ui.go                       ルームのメニュー配線・送受信(作成/参加/退出・URLコピー・ソロモードのフォールバック)
internal/config/config.go        設定の読み込み・検証(JSONC)
internal/recorder/recorder.go    マイク録音 (malgo)。バッファ録音とストリーム録音
internal/vad/vad.go              音声ストリームを無音で発話単位に区切る(VAD)
internal/transcribe/whisper.go   whisper-cli 呼び出し(ローカル STT)・ノイズ除去
internal/enhance/enhance.go      文字起こしをローカル LLM(Ollama)で整形・絵文字付与(任意)
internal/room/                   ルーム: 共有 URL・E2E 暗号化(AES-GCM)・自動再接続つき WS クライアント
internal/overlay/                ニコニコ風オーバーレイ(透過・クリック貫通・全モニター・画面共有に映らない)
internal/inputbar/               Spotlight 風の文字入力バー(非アクティブ化パネル)
internal/dialog/                 モーダル入力ダイアログ(URL 参加用)
internal/audioout/                既定の音声出力(イヤホン/スピーカー)を判定・監視(voice_input:auto 用)
internal/modkey/modkey_darwin.go 単体修飾キー・修飾キー2つのコードを CGEventTap で検出(複数キー監視可)
internal/trayicon/trayicon.go    メニューバーのアイコン(待機/聞き取り=オレンジ/録音/文字起こし)
server/                          ルームの中継サーバ(Cloudflare Workers + Durable Objects)
```

## ロードマップ

設計と今後の予定は [docs/room-overlay-design.md](docs/room-overlay-design.md) を参照。

- **v1.1**: 記名モード(ルーム作成時に匿名/記名を選択)
- **v1.2**: Slack ミラー(ルームのコメントを Slack チャンネルへ転送。入口はあくまでオーバーレイ)
- 以降: コメント密度に応じた自動フォント縮小、モニター構成変更への自動追従、Windows クライアント(表示側のみ先行)など
