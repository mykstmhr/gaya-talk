# ura-talk

声をその場で文字起こしして、**フォーカス中の任意のフィールドに入力**するか、**Slack に自分名義で投稿**する macOS 常駐ツール。文字起こしは **ローカルの whisper.cpp** で行い、音声は外部に送らない。

イメージは「**副音声**」。Gather / Google Meet などの会議でミュート中に、本線を邪魔せず相槌・短いコメントを声でそっと流す用途。ビデオツール非依存でどのアプリでも使える。

- **出力先**: `keystroke`(フォーカス先に入力・既定) / `slack`(自分名義で投稿)
- **入力方式**: `vad`(無音で自動区切り・既定) / `ptt`(押している間だけ録音)
- 音声処理はローカル完結。メニューバー常駐(`🎙`)。

---

# 利用者向け

## 必要なもの

- macOS (Apple Silicon)
- whisper.cpp(`brew install whisper-cpp`)
- (任意) Slack 投稿には別途 Slack アプリ → [Slack 出力](#slack-出力任意)

## セットアップ

```sh
brew install whisper-cpp   # 文字起こしエンジン(初回のみ)
make setup                 # config を配置 + whisper モデルを取得(約1.5GB)
make app-open              # .app をビルドして起動。初回はマイク/アクセシビリティを許可
```

これだけで動く。既定は **keystroke 出力 / VAD** なので **Slack 設定は不要**。チャット欄にフォーカスして `右⌘` でリッスン開始 → 話す → もう一度キーで停止。

- 設定は **`~/.config/ura-talk/config.json`**(`make setup` が配置。`.app` はここを読む)。全項目は[設定](#設定-configjson)を参照。
- config を編集したら **`make restart`** で反映。
- モデルは `~/.config/ura-talk/models/`。軽量化したいなら量子化版 `ggml-large-v3-turbo-q5_0.bin` を `make model MODEL=ggml-large-v3-turbo-q5_0.bin` で取得し、`whisper_model` を差し替え。

> **任意**: 日本語をもっと読みやすくしたいなら、ローカル LLM 整形(`make enhance-model`)も使える → [ローカル LLM 整形](#ローカル-llm-整形任意)。

### 権限

初回起動時に **マイク**(録音)と **アクセシビリティ/入力監視**(グローバルホットキー検知)の両方を許可する(システム設定 → プライバシーとセキュリティ)。`.app` で起動しているので、権限は「ura-talk.app」に紐づく。

<details>
<summary><strong>再ビルドで権限が失効する場合(安定した自己署名で署名する)</strong></summary>

`make app` は安定した自己署名証明書 `ura-talk-dev` で署名する。この証明書がキーチェーンに無いとアドホック署名にフォールバックし、再ビルドのたびに身元が変わって**付与済みのアクセシビリティ等の許可が失効**する(貼り付けが急に効かなくなる)。一度だけ証明書を作っておけば解消する:

1. **Keychain Access** → メニュー **証明書アシスタント → 証明書を作成…**
2. 名前 `ura-talk-dev` / 固有名のタイプ **自己署名ルート** / 証明書のタイプ **コード署名**
3. 以後 `make app` はこの証明書で署名する(別名なら `make app SIGN_IDENTITY="名前"`)
4. アクセシビリティ/入力監視/マイクを一度だけ許可すれば、再ビルドしても許可が残る
</details>

## 使い方

メニューバーのアイコンで状態が分かる: `🎙` 待機 / `👂` リッスン中 / `🔴` 音声検出中 / `💬` 文字起こし中。**「終了」**で停止。

### 出力先: keystroke と slack

`output` で投げ先を切り替える。戻すときは値を変えるだけ(Slack 設定は残せる)。

- **`keystroke`**: いまフォーカス中のフィールドへクリップボード経由(Cmd+V)で入力。Slack 以外の Discord・ブラウザ・任意のチャット UI でも使え、`login` 不要。`keystroke.auto_enter: true` で貼り付け後に Enter 送信(アプリ別に上書き可、後述)。要アクセシビリティ権限。
- **`slack`**: `chat.postMessage` で自分名義投稿。フォーカス不要(裏で送れる)。`login` で取得した user token を使う。

> **keystroke で意図しない所に入る**: 既定では貼り付けは「発話終了時点で最前面のアプリ」に入る。入力したいチャット欄にフォーカスを当ててから喋ること。何も貼り付かない場合はアクセシビリティ権限が無い(`→ keystroke: …` はログに出るのに貼り付かない、が典型)。

> **ターゲットを固定して誤爆を防ぐ(`keystroke.pin_target: true`)**: リッスン/録音を**開始した時点で最前面だったアプリ**を「ターゲット」として覚え、**そのアプリが前面のときだけ**貼り付ける。別アプリを前面にしている間は貼り付けを**スキップ**(ログに `⏸ 固定先「…」が前面にない…` が出る)。会議中に席を外して別アプリを触っても、意図しない場所に貼られない安全弁。メニューバーに `🎯アプリ名` が出るのでターゲット中だと分かる。
>
> 既定(`false`)は従来どおり「発話終了時点で最前面のアプリ」へ貼り付ける(アプリ問わずアクティブな入力欄に入る)。ターゲットを変えるには一度リッスンを停止し、入れたい欄にフォーカスしてから再開する。
>
> ※ 「裏に回ったアプリへ無理やり入力する」ことは macOS の制約(バックグラウンドアプリは前面でないと合成貼り付けを受け付けない)上できないため、固定モードは "ターゲット以外には入れない安全弁" として動く。

> **送信キーをアプリ別に変える(`keystroke.send_key` / `overrides`)**: 貼り付け後に送る「送信キー」はアプリによって作法が違う(チャットは `Enter` 送信、別アプリは `Cmd+Enter`、ドキュメントは `Enter` で改行、等)。`send_key` で指定する。値は `none`(貼るだけ) / `enter` / `shift+enter` / `cmd+enter`。
> ```json
> "keystroke": {
>   "send_key": "none",
>   "overrides": [
>     { "app": "Slack", "send_key": "enter" },               // Enter で送信
>     { "app": "com.google.Chrome", "send_key": "cmd+enter" },// このアプリは Cmd+Enter で送信(bundle id 指定)
>     { "app": "Notion", "send_key": "enter" }                // ドキュメントは Enter=改行
>   ]
> }
> ```
> 解決の優先順は **一致する override の `send_key` → 全体の `send_key` → `auto_enter`(後方互換: `true`=`enter` / `false`=`none`)**。`app` は**メニューバーに出る 🎯 の表示名**(例 `Slack`)か **bundle id**(例 `com.google.Chrome`)で、大文字小文字は無視・完全一致。bundle id は `osascript -e 'id of app "Slack"'` で調べられる。固定モードでなくても(前面アプリ判定で)効く。`auto_enter` だけ書けば従来どおり(`true`=毎回 Enter)。

### 入力方式: PTT と VAD

`listen_mode` で選ぶ。どちらも `右⌘` がトリガ。

- **`ptt`**: `右⌘` を押している間だけ録音し、離すと 1 回出力。
- **`vad`**: `右⌘` でリッスンの開始/停止をトグル。リッスン中は無音の切れ目で発話ごとに自動区切りして出力。話し終わったらもう一度キーで停止。

> VAD はリッスン中の発話(本ツール宛でない独り言含む)も出力され得る。区切りが過敏/鈍いときは `vad.*` を調整。`./bin/ura-talk dryrun` で出力せず区切り具合だけ確認できる。

> **Whisper の幻聴**: 無音・雑音区間に「ご視聴ありがとうございました」等の定型句が出ることがある。末尾無音をトリムしたうえで既知フレーズ(`internal/transcribe/whisper.go` の `hallucinations`)を除外する。他の幻聴句が出たらこのリストに追加する。

## 困ったとき

**声が小さい・認識が悪い**(効く順):
1. **自動ゲイン**(既定 ON)。まだ小さいなら `gain.max_gain` を `12 → 20`。
2. **VAD で拾われない**: `URATALK_DEBUG=1 ./bin/ura-talk dryrun` で喋ったときの `rms=` を見て、`vad.threshold` をその少し下に。
3. **初期プロンプト** `whisper_prompt` に想定する口調・語彙(相槌など)を入れる。
4. **no-speech 閾値** `whisper_no_speech_thold` を `0.6 → 0.3`(小声を拾うが幻聴増→フィルタで吸収)。
5. **ビーム幅** `whisper_beam_size` を `5 → 8`(精度↑・速度↓)。

**Bluetooth イヤホンで再生音が途切れる**: 録音開始時にイヤホンが通話プロファイル(HFP)へ切り替わるため(macOS の仕様)。録音だけを内蔵マイクに固定すれば回避できる。

```sh
./bin/ura-talk devices                  # 使えるマイク一覧
#   "input_device": "MacBook"           # config.json で内蔵マイクに固定(部分一致でOK)
```

## ローカル LLM 整形(任意)

whisper の生出力(句読点なし・かな漢字揺れ・フィラー混じり)を **ローカル LLM(Ollama)で整形**する。音声・テキスト処理ともにローカル完結。

```sh
brew install ollama    # 未導入なら
make enhance-model      # 候補から番号で選ぶと pull + config 更新
make restart            # 反映
```

```
make enhance-model
#   1) qwen2.5:7b    高品質・遅め
#   2) qwen2.5:3b    バランス(おすすめ)
#   3) qwen2.5:1.5b  最速・軽量
#   4) gemma2:2b     代替候補
```

- `ollama serve` の手動起動は不要(enhance 有効時、起動時に稼働確認し、無ければ自動起動)。
- 翻訳・加筆を禁止するプロンプトで整形。**失敗時は生テキストをそのまま出力**するので壊れない。整形中はメニューバーが `💬`。
- 起動時にモデルをウォームアップ + `keep_alive` で常駐させるのでコールドスタート待ちは出ない。それでも 7B は 1 発話あたり数秒かかる。速さ重視なら `qwen2.5:3b`、不要なら `enabled: false`。
- 起動ログで使用可否(`✅ 整形(Ollama)有効…` / `⚠️ …使えません`)、発話ごとに `整形 ✏️ "前" → "後"` が出る。

## ショートカットキーの変更

config の `hotkey` で変更(使えるキー名は `./bin/ura-talk keys`)。変更後は `make restart`。

```jsonc
"hotkey": { "mods": [], "key": "rightcmd" }              // 既定: 単体修飾キー(右⌘)
"hotkey": { "mods": ["ctrl", "shift"], "key": "space" }  // 組み合わせ
```

- **単体修飾キー**(`mods` 空): `rightcmd` / `leftcmd` / `rightoption` / `leftoption` / `rightshift` / `fn`。CGEventTap で検出するため要アクセシビリティ権限(監視のみ、本来の動作は奪わない)。
- **組み合わせ**: `mods` = `ctrl`/`shift`/`option`(`alt`)/`cmd`、`key` = `a`〜`z` / `0`〜`9` / `f1`〜`f20` / `space` / `return` / `tab` / `escape` / `delete` / 矢印 など。

## Slack 出力(任意)

`output: "slack"` で文字起こしを **Slack に自分名義で投稿**(フォーカス不要)。OAuth で取得した user token(xoxp)は **macOS Keychain** に保存する。

1. <https://api.slack.com/apps> → **Create New App** → From scratch → ワークスペースを選ぶ。
2. **OAuth & Permissions**:
   - **Redirect URLs** に `https://localhost:53682/oauth/callback`(`oauth_redirect_port` を変えたら合わせる)
   - **User Token Scopes**(Bot ではなく **User**)に `chat:write`
3. **Basic Information → App Credentials** の **Client ID / Client Secret** を控える
4. **Install to Workspace**

```sh
# config に output:"slack" と slack_client_id / slack_client_secret / slack_channel を記入
./bin/ura-talk login    # ブラウザ認可 → token を Keychain 保存
./bin/ura-talk logout   # token を削除
```

> Redirect URL は HTTPS 必須。認可時に自己署名証明書付きのローカル HTTPS サーバを立てるため、ブラウザで一度だけ証明書警告が出る(「詳細 → このまま続行」)。client_secret は config に書かず `URATALK_SLACK_CLIENT_SECRET` 環境変数でも渡せる。

## 設定 (config.json)

環境変数 `URATALK_SLACK_CLIENT_SECRET` / `URATALK_WHISPER_MODEL` / `URATALK_CONFIG` で上書き可能。

<details>
<summary>全設定キー(クリックで展開)</summary>

| キー | 説明 | 既定 |
|---|---|---|
| `output` | 出力先。`slack`(自分名義で投稿) / `keystroke`(フォーカス中のUIへ入力) | `slack` |
| `keystroke.auto_enter` | 貼り付け後に Enter を送るか(後方互換。`send_key` 未指定時の既定: `true`=`enter` / `false`=`none`) | `false` |
| `keystroke.send_key` | 貼り付け後に送る送信キーの既定。`none` / `enter` / `shift+enter` / `cmd+enter`(空なら `auto_enter` を使用) | `""` |
| `keystroke.pin_target` | リッスン開始時に最前面だったアプリを固定し、**そのアプリが前面のときだけ**貼り付ける(別アプリ前面時はスキップ=誤爆防止)。`false` は従来どおり最前面へ貼り付け | `false` |
| `keystroke.overrides` | アプリ別の送信キー上書き。`[{ "app": "Slack", "send_key": "enter" }]` の形。`app` は 🎯 表示名か bundle id(大文字小文字無視・完全一致) | `[]` |
| `slack_client_id` | Slack アプリの Client ID | (slack 出力の login に必須) |
| `slack_client_secret` | Slack アプリの Client Secret | (login に必須) |
| `slack_channel` | 投稿先チャンネル ID / 名前 | (投稿に必須) |
| `oauth_redirect_port` | OAuth コールバック用ローカルポート | `53682` |
| `whisper_bin` | whisper-cli の実行パス | `whisper-cli` |
| `whisper_model` | ggml モデルのパス(`~` 展開可) | (投稿に必須) |
| `input_device` | 録音に使うマイク名(部分一致)。空でシステム既定 | (既定) |
| `language` | 文字起こし言語(`auto` で自動判定) | `ja` |
| `gain.enabled` | 録音音声の自動ゲイン(正規化)。小声・ボソボソの改善に有効 | `true` |
| `gain.target_peak` | 正規化後のピーク目標(0〜1) | `0.95` |
| `gain.max_gain` | 増幅倍率の上限(無音の過剰増幅を防ぐ) | `12` |
| `whisper_prompt` | 初期プロンプト。口語・語彙のヒントを与え誤認識を減らす | (なし) |
| `whisper_beam_size` | ビーム幅。上げると精度↑・速度↓ | `5` |
| `whisper_no_speech_thold` | no-speech 閾値(0で既定0.6)。下げると小声を拾うが幻聴増 | `0`(=0.6) |
| `enhance.enabled` | 文字起こしをローカル LLM(Ollama)で整形するか | `false` |
| `enhance.model` | 整形に使う Ollama モデル(例 `qwen2.5:7b` / `qwen2.5:3b`) | `qwen2.5:7b` |
| `enhance.endpoint` | Ollama エンドポイント | `http://localhost:11434` |
| `enhance.prompt` | 整形プロンプト(空で既定の「整形のみ・翻訳/加筆禁止」) | (既定) |
| `listen_mode` | 入力方式。`ptt`(押下中録音) / `vad`(トグルして自動区切り) | `ptt` |
| `vad.threshold` | 発話開始とみなす音量(RMS, 0〜1)。継続は内部でこの半分の閾値で判定(ヒステリシス)。上げると鈍く、下げると過敏 | `0.01` |
| `vad.min_speech_ms` | これ未満の発話は雑音として捨てる | `300` |
| `vad.silence_ms` | この長さの無音で 1 発話を区切る | `700` |
| `vad.max_segment_ms` | 1 発話の最大長(超えたら強制区切り) | `15000` |
| `vad.preroll_ms` | 発話開始の手前を含める量(頭切れ防止) | `300` |
| `hotkey.mods` | 修飾キー (`ctrl`/`shift`/`option`/`cmd`)。単体修飾キー指定時は空 | `[]` |
| `hotkey.key` | メインキー、または単体修飾キー(`rightcmd` 等) | `rightcmd` |
| `sound.enabled` | 有効化/無効化時に効果音を鳴らす | `true` |
| `sound.on` | 有効化(リッスン開始)時の音(`/System/Library/Sounds/` の名前。候補: Basso, Blow, Bottle, Frog, Funk, Glass, Hero, Morse, Ping, Pop, Purr, Sosumi, Submarine, Tink) | `Submarine` |
| `sound.off` | 無効化(停止)時の音 | `Bottle` |
| `min_duration_ms` | これ未満の録音は無視(誤爆防止) | `300` |
| `message_prefix` | 投稿本文の先頭に付ける文字列。**`output: slack` のときだけ適用**(keystroke は素のテキスト) | `🗣 ` |

</details>

---

# 開発者向け

## ビルドと実行

```sh
make build     # bin/ura-talk を生成
make run       # ビルドせず go run で起動(端末ログを見ながら開発)
make app       # build/ura-talk.app を生成して署名
make restart   # 起動中の .app を停止して開き直す
make tidy      # go mod tidy
make clean     # bin / .app を削除
```

Go 1.26+ が必要。Finder/`.app` 起動では作業ディレクトリが `/` になり環境変数 `URATALK_*` も効かないため、設定は `~/.config/ura-talk/config.json` を読む。ログは `.app` 起動時 `~/Library/Logs/ura-talk.log`、端末起動時は標準エラー。多重起動はファイルロックで防止。

## 構成

```
main.go                          サブコマンド(login/logout/run)・push-to-talk ループ
internal/config/config.go        設定の読み込み・検証
internal/oauth/oauth.go          OAuth v2 フロー(自己署名 HTTPS コールバック → user token)
internal/tokenstore/store.go     user token の Keychain 保存・読み出し
internal/recorder/recorder.go    マイク録音 (malgo)。バッファ録音とストリーム録音
internal/vad/vad.go              音声ストリームを無音で発話単位に区切る(VAD)
internal/transcribe/whisper.go   whisper-cli 呼び出し(ローカル STT)・ノイズ除去
internal/enhance/enhance.go      文字起こしをローカル LLM(Ollama)で整形(任意)
internal/slack/slack.go          chat.postMessage 投稿(本人名義)
internal/keystroke/keystroke.go  フォーカス中のUIへ合成入力(Cmd+V 貼り付け / 任意で Enter)
internal/modkey/modkey_darwin.go 単体修飾キー(右⌘ 等)の押下/解放を CGEventTap で検出
```

## 今後のアイデア

- スレッド指定投稿(`thread_ts` で会議用スレッドにぶら下げ)
- チーム配布(各メンバーが `login` で自分のトークンを取得)
- VAD の精度向上(Silero VAD モデルや whisper の `--vad` 連携)
