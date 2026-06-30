# ura-talk

声をその場で文字起こしして、**フォーカス中の任意のフィールドに入力**するか、**Slack に自分名義で投稿**する macOS 常駐ツール。文字起こしは **ローカルの whisper.cpp** で行い、音声は外部に送らない。

イメージは「**副音声**」。Gather / Google Meet などの会議でミュート中に、本線を邪魔せず相槌・短いコメントを声でそっと流す用途を想定。ビデオツール非依存でどのアプリでも使える。

- **出力先**: `keystroke`(フォーカス先に入力・既定) / `slack`(自分名義で投稿)
- **入力方式**: `vad`(無音で自動区切り・既定) / `ptt`(押している間だけ録音)
- 音声処理はローカル完結。メニューバー常駐(`🎙`)。

## 必要なもの

- macOS (Apple Silicon) / Go 1.26+
- whisper.cpp(`brew install whisper-cpp`)

(Slack に投稿したい場合のみ、別途 Slack アプリが必要 → [Slack 出力(任意)](#slack-出力任意))

## セットアップ

```sh
brew install whisper-cpp                                # whisper-cli を入れる
make model                                              # モデルを ~/.config/ura-talk/models/ に取得(約1.5GB)
mkdir -p ~/.config/ura-talk
cp config.example.json ~/.config/ura-talk/config.json   # 設定を配置(必要に応じ編集)
make app                                                # .app をビルド(ura-talk-dev 署名)
open build/ura-talk.app                                 # 起動。初回はマイク/アクセシビリティを許可
```

- 既定は **keystroke 出力 / VAD** なので **Slack 設定は不要**。チャット欄にフォーカスして `右コマンドキー(右⌘)` でリッスン開始 → 話す → もう一度キーで停止。
- 設定は **`~/.config/ura-talk/config.json`**(`.app` はここを読む)。
- モデルは `~/.config/ura-talk/models/` に置く。`make model` が取得する。
  - より軽く/速くしたいなら量子化版 `ggml-large-v3-turbo-q5_0.bin`(精度ほぼ同等・サイズ約半分以下)も可。`MODEL` を差し替えて `make model`。
- whisper-cli のパスは config の `whisper_bin` で絶対指定(既定 `/opt/homebrew/bin/whisper-cli`。Intel Mac は `/usr/local/bin/whisper-cli`)。`.app` 起動時に PATH が無くても確実に見つかる。

### 出力先: Slack と keystroke

`output` でテキストの投げ先を切り替える。

- **`slack`(既定)**: `chat.postMessage` で **自分名義** で投稿。フォーカス不要(裏で送れる)、
  スレッド/チャンネル指定が効く。`login` で取得した user token を使う。
- **`keystroke`**: いま **フォーカスしているフィールドへテキストを入力**(クリップボード経由で
  Cmd+V 貼り付け)。Slack 以外の Discord・ブラウザ・任意のチャットUIでも使える。
  Slack アプリも `login` も不要。
  - `keystroke.auto_enter: true` にすると貼り付け後に **Enter を送って送信** する。
    Enter=送信 のUI向け。Cmd+Enter 等が必要なUIでは `false`(自分で送信)が無難。
  - 必要権限はアクセシビリティ/入力監視(グローバルホットキーと同じ)。
  - 仕組み上、貼り付け中だけ一時的にクリップボードを使う(直後に元の内容へ復元する)。

> **keystroke なのにターミナルに入ってしまう場合**
> 貼り付けは「**発話が終わった時点で最前面のアプリ**」に入る。ターミナルを前面にしたまま
> 喋るとターミナルに入る。**入力したいチャットの入力欄にフォーカスを当ててから**喋ること
> (グローバルホットキーなので、前面がチャットでも開始/停止できる)。
> それでも何も貼り付かない場合はアクセシビリティ権限が無い(Cmd+V が無反応になる)。
> 起動時に権限が無ければ警告を出すので、システム設定で ura-talk の起動元アプリを許可する。

戻したくなったら `output` を切り替えるだけでよい(Slack 設定はそのまま残せる)。

### 入力方式: PTT と VAD

`listen_mode` で 2 通りから選べる。

- **`ptt`(既定)**: `右コマンドキー(右⌘)` を **押している間だけ** 録音し、離すと 1 回投稿。
- **`vad`**: `右コマンドキー(右⌘)` で **リッスンの開始/停止をトグル**。リッスン中は喋りの
  切れ目(無音)を検出して **発話ごとに自動で区切って投稿** する。一語ごとにキーを
  離す必要がない。話し終わったらもう一度キーを押して停止。

VAD は無音区切りなので、リッスン中に喋ったこと(本ツール宛でない独り言も)は
投稿され得る点に注意。区切りが過敏/鈍いときは設定表の `vad.*` を調整する。
`listen_mode: "vad"` のまま `dryrun` で、Slack に送らず区切り具合だけ確認できる。

> **Whisper の幻聴について**: 無音・雑音区間に対して Whisper は「ご視聴ありがとう
> ございました」等の定型句を勝手に出すことがある。本ツールは末尾無音をトリムしたうえで、
> 既知の幻聴フレーズ(`internal/transcribe/whisper.go` の `hallucinations`)を除外する。
> 他の幻聴句が出たらこのリストに追加する。

### 声が小さい・ボソボソで認識が悪いとき

入力音量が小さいと Whisper の精度が落ちる。効く順に:

1. **自動ゲイン(既定 ON)**: `gain.enabled` を `true`。Whisper に渡す前に音量をピーク
   正規化で持ち上げる。まだ小さいと感じるなら `gain.max_gain` を `12 → 20` などへ。
2. **VAD で拾われるように**: 小声だと `vad.threshold` に届かず無視される。`devices` ＋
   `URATALK_DEBUG=1` のドライランで喋ったときの `rms=` を見て、その少し下に設定。
3. **初期プロンプト**: `whisper_prompt` に想定する口調・語彙(相槌など)を入れると、
   不明瞭な発話を“それらしい”語へ寄せやすい。
4. **no-speech 閾値を下げる**: `whisper_no_speech_thold` を `0.6 → 0.3` 程度に。小声を
   「無音」と切り捨てにくくなる(ただし幻聴が増える→幻聴フィルタで吸収)。
5. **ビーム幅**: `whisper_beam_size` を `5 → 8` で精度がやや上がる(処理は遅くなる)。

### 動作確認(ドライラン)

ホットキー → 録音 → 文字起こし までを、どこにも出力せず確認できる(結果はログに出るだけ)。

```sh
./bin/ura-talk dryrun
```

既定(VAD)では **右⌘ でリッスン開始 → 喋る → もう一度右⌘ で停止**。文字起こし結果がログに出る。
（この場合でもマイク・アクセシビリティ権限の許可は必要)

### Bluetooth イヤホンで再生音が途切れる場合

既定の入力マイクが Bluetooth イヤホン(AirPods / Shokz 等)だと、録音開始時に
イヤホンが通話プロファイル(HFP)へ切り替わり、**聞いている音声が途切れる**。
これは macOS の仕様。録音だけを内蔵マイクなど別デバイスに固定すれば回避できる。

```sh
# 使えるマイクの一覧を確認
./bin/ura-talk devices
#   - MacBook Airのマイク
#   - OpenFit by Shokz (default)

# config.json で録音を内蔵マイクに固定(部分一致でOK)
#   "input_device": "MacBook"
```

こうするとイヤホンは再生専用(A2DP)のままになり、耳の音は途切れない。

初回の常駐起動時、macOS が以下の権限を要求する。両方許可が必要:

- **マイク** … 録音のため
- **アクセシビリティ / 入力監視** … グローバルホットキー検知のため
  （システム設定 → プライバシーとセキュリティ → 入力監視 / アクセシビリティ）

> ターミナルから起動した場合、許可対象は「ターミナル.app」になる。常用するなら `.app` バンドル化を検討。

## .app バンドルにする(常用向け)

`.app` 化すると、**マイク/アクセシビリティ権限が「ターミナル」ではなく「ura-talk.app」に紐づく**ため、毎回ターミナルを許可し直す必要がなくなり、Finder からの起動・常駐がしやすくなる。

`.app` は実体としては「決まった構成のフォルダ + `Info.plist`」にすぎない:

```
ura-talk.app/Contents/
  Info.plist          # バンドルID・マイク使用理由(NSMicrophoneUsageDescription)・LSUIElement 等
  MacOS/ura-talk    # 実行バイナリ
```

生成:

```sh
make app        # build/ura-talk.app を生成し、アドホック署名する
make app-open   # 生成して起動(open)する
```

使うときの注意:

- **設定の場所**: Finder 起動だと作業ディレクトリが `/` になるため、`config.json` は
  `~/.config/ura-talk/config.json` に置く(環境変数 `URATALK_*` も Finder 起動では効かない)。
- **ログ**: Finder 起動はコンソールが無いので、ログは `~/Library/Logs/ura-talk.log` に出る
  (端末から起動したときは従来どおり標準エラーへ)。
- **権限**: 初回起動時に **マイク** と **アクセシビリティ/入力監視** を ura-talk.app に許可する。
- **メニューバー**: 起動すると **メニューバーにアイコン**が出る。アイコンで動作状態が分かる:
  - `🎙` 待機(停止中) / `👂` リッスン中(無音待ち) / `🔴` **いま音声を検出中** / `💬` 文字起こし中
  - メニューを開くと状態テキストも表示。**「終了」** で停止。
- **多重起動防止**: ファイルロックで二重起動を防ぐ。既に動いていれば 2 つ目は起動しない
  (「古いのが残ってるかも」を解消)。
- **終了**: メニューバーの **「終了」**。コマンドからは `pkill -x ura-talk` も可(再起動は `make restart`)。
- **アドホック署名だと権限が毎回失効する**: 既定のアドホック署名(`-`)は `make app` の
  たびに署名が変わり、付与済みの **アクセシビリティ等の許可が無効化**される(=貼り付けが
  急に効かなくなる)。これを防ぐには**安定した自己署名証明書**で署名する:

  1. **Keychain Access** を開く → メニュー **証明書アシスタント → 証明書を作成…**
  2. 名前 `ura-talk-dev` / 固有名のタイプ **自己署名ルート** / 証明書のタイプ **コード署名** で作成
  3. その ID で署名してビルド: `make app SIGN_IDENTITY="ura-talk-dev"`
  4. アクセシビリティ/入力監視/マイクを **一度だけ** 許可すれば、以後は再ビルドしても許可が残る

  > keystroke 出力で「文字起こしはログに出る(`→ keystroke: …`)のに貼り付かない」場合は、
  > ほぼアクセシビリティ権限が無い状態。上記で恒久化するか、暫定的にシステム設定で
  > ura-talk.app を許可 → 再起動する。

## 日本語をきれいにする(ローカル LLM 整形)

whisper の生出力は「句読点なし・漢字/かな揺れ・フィラー混じり」になりがち。これを
**ローカル LLM(Ollama)に通して整形**すると、読みやすい日本語になる(VoiceInk 等の
"AI Enhancement" 相当)。**音声は出ず、整形のテキスト処理もローカル完結**。

```sh
# 1. Ollama を用意してモデルを取得(日本語に強い qwen2.5 推奨)
brew install ollama          # 未導入なら
ollama pull qwen2.5:7b       # 軽くしたいなら qwen2.5:3b

# 2. config の enhance を有効化
#   "enhance": { "enabled": true, "model": "qwen2.5:7b" }
```

> **`ollama serve` の手動起動は不要**。enhance 有効時、ura-talk が起動時に Ollama の稼働を確認し、
> 動いていなければ自動起動する(Ollama.app があればそれを、無ければ `ollama serve` を**出力を捨てて**
> バックグラウンド起動)。だからターミナルに Ollama のログが流れることもない。

**モデルの選択・変更は `make enhance-model` が簡単**:候補から番号で選ぶと、`ollama pull`
してから config の `enhance.model` を自動で書き換える。

```sh
make enhance-model
#   1) qwen2.5:7b    高品質・遅め
#   2) qwen2.5:3b    バランス(おすすめ)
#   3) qwen2.5:1.5b  最速・軽量
#   4) gemma2:2b     代替候補
# → 選ぶと pull + config 更新。あとは make restart で反映。
```

- 整形は **翻訳・加筆を禁止**するプロンプトで行う(`internal/enhance/enhance.go`)。
- **失敗時(Ollama 未起動・モデル無し等)は生テキストをそのまま出力**するので壊れない。
- 整形中はメニューバーが `💬`。
- **速度**: 起動時にモデルを**ウォームアップ**＋`keep_alive`で常駐させるので、初回の長い待ち
  (コールドスタート)は出ない。それでも 7B は 1 発話あたり数秒かかるので、**速さ重視なら
  `qwen2.5:3b`**(`ollama pull qwen2.5:3b` → `enhance.model` を変更)。整形が不要なら `enabled:false`。
- 起動ログで使用可否が分かる(`✅ 整形(Ollama)有効…` / `⚠️ …使えません: 理由`)。
  発話ごとに `整形 ✏️ "前" → "後"` が出る。
- プロンプトを変えたいときは `enhance.prompt` に上書きを書く。

## ショートカットキーの変更

ホットキーは config の `hotkey` で変更できる。使えるキー名は **`./bin/ura-talk keys`** で一覧表示。

**既定は右コマンドキー単体(右⌘)**:
```jsonc
"hotkey": { "mods": [], "key": "rightcmd" }
```

組み合わせキーにもできる:
```jsonc
"hotkey": { "mods": ["ctrl", "shift"], "key": "space" }
```

- **単体修飾キー**(`mods` を空にして指定): `rightcmd` / `leftcmd` / `rightoption` / `leftoption` / `rightshift` / `fn`
  - これは CGEventTap で検出するため **アクセシビリティ権限が必要**(keystroke 出力と同じ権限)
  - 監視のみ(キー本来の動作は奪わない)
- **組み合わせ**: `mods` = `ctrl`/`shift`/`option`(`alt`)/`cmd`、`key` = `a`〜`z` / `0`〜`9` / `f1`〜`f20` / `space` / `return` / `tab` / `escape` / `delete` / 矢印 など
- 変更後は **`make restart`** で反映。他アプリと衝突して登録に失敗する場合は別のキーにする。

## Slack 出力(任意)

`output: "slack"` にすると、文字起こしを **Slack に自分名義で投稿**できる(フォーカス不要・スレッド指定可)。OAuth で取得した user token(xoxp)を使い、token は **macOS Keychain** に保存する。

### Slack アプリの作成

1. <https://api.slack.com/apps> → **Create New App** → From scratch。ワークスペースを選ぶ。
2. **OAuth & Permissions**:
   - **Redirect URLs** に `https://localhost:53682/oauth/callback` を追加(`oauth_redirect_port` を変えたら合わせる)
   - **User Token Scopes**(Bot ではなく **User**)に `chat:write` を追加
3. **Basic Information → App Credentials** の **Client ID / Client Secret** を控える
4. **Install to Workspace**

> Redirect URL は HTTPS 必須。本ツールは認可時に自己署名証明書付きのローカル HTTPS サーバを
> 立てるため、ブラウザで一度だけ証明書警告が出る(「詳細 → このまま続行」)。

### 設定とログイン

```sh
# config に output:"slack" と slack_client_id / slack_client_secret / slack_channel を記入
./bin/ura-talk login    # ブラウザ認可 → token を Keychain 保存
./bin/ura-talk logout   # token を削除
```

(client_secret は config に書かず `URATALK_SLACK_CLIENT_SECRET` 環境変数でも渡せる)

## 設定 (config.json)

| キー | 説明 | 既定 |
|---|---|---|
| `output` | 出力先。`slack`(自分名義で投稿) / `keystroke`(フォーカス中のUIへ入力) | `slack` |
| `keystroke.auto_enter` | keystroke 出力時、貼り付け後に Enter を送って送信するか | `false` |
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
| `sound.on` | 有効化(リッスン開始)時の音(`/System/Library/Sounds/` の名前) | `Submarine` |
| | 候補: Basso, Blow, Bottle, Frog, Funk, Glass, Hero, Morse, Ping, Pop, Purr, Sosumi, Submarine, Tink | |
| `sound.off` | 無効化(停止)時の音 | `Bottle` |
| `min_duration_ms` | これ未満の録音は無視(誤爆防止) | `300` |
| `message_prefix` | 投稿本文の先頭に付ける文字列。**`output: slack` のときだけ適用**(keystroke は素のテキスト) | `🗣 ` |

環境変数 `URATALK_SLACK_CLIENT_SECRET` / `URATALK_WHISPER_MODEL` / `URATALK_CONFIG` で上書き可能。

## 構成

```
main.go                        サブコマンド(login/logout/run)・push-to-talk ループ
internal/config/config.go      設定の読み込み・検証
internal/oauth/oauth.go        OAuth v2 フロー(自己署名 HTTPS コールバック → user token)
internal/tokenstore/store.go   user token の Keychain 保存・読み出し
internal/recorder/recorder.go  マイク録音 (malgo)。バッファ録音とストリーム録音
internal/vad/vad.go            音声ストリームを無音で発話単位に区切る(VAD)
internal/transcribe/whisper.go whisper-cli 呼び出し(ローカル STT)・ノイズ除去
internal/enhance/enhance.go    文字起こしをローカル LLM(Ollama)で整形(任意)
internal/slack/slack.go        chat.postMessage 投稿(本人名義)
internal/keystroke/keystroke.go フォーカス中のUIへ合成入力(Cmd+V 貼り付け / 任意で Enter)
internal/modkey/modkey_darwin.go 単体修飾キー(右⌘ 等)の押下/解放を CGEventTap で検出
```

## 今後のアイデア

- スレッド指定投稿(`thread_ts` で会議用スレッドにぶら下げ)
- メニューバー常駐 UI(Tauri 等)・`.app` バンドル化(権限が素直に)
- チーム配布(各メンバーが `login` で自分のトークンを取得)
- VAD の精度向上(Silero VAD モデルや whisper の `--vad` 連携)
