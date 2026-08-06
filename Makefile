.PHONY: help build clean setup setup-voice app app-open dist release model verify-model whisper-model enhance-model restart logs deploy icons uninstall

# 素の `make` はヘルプを表示する(誤って setup を走らせないため)。
.DEFAULT_GOAL := help

# 各ターゲット末尾の `## 説明` を集めて一覧表示する。
help: ## このヘルプを表示する
	@echo "gaya-talk make targets:"; \
	grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sort | \
	awk 'BEGIN{FS=":.*## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# バージョン情報をバイナリに埋め込む(メニュー・ログ・`gaya-talk version` で表示)。
# BUILD_VERSION: ローカルは git describe(例 v0.2.4-3-gc42e34d-dirty)。CI のリリース
# ビルドはタグを明示的に渡す(shallow checkout では describe できないため)。
# BUILD_KIND: "local"(既定)/ "release"(release.yml が渡す。自己アップデートの有効化条件)。
BUILD_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)
BUILD_KIND ?= local

# -extldflags は cgo の -lobjc 重複によるリンカ警告(無害)の抑止。
LDFLAGS := -ldflags "-X main.version=$(BUILD_VERSION) -X main.buildKind=$(BUILD_KIND) -extldflags=-Wl,-no_warn_duplicate_libraries"

# 配布バイナリが動く最小 macOS(Info.plist の LSMinimumSystemVersion と揃える)。
# 明示しないとリンカがビルドマシンの OS バージョンを最小要件としてバイナリに埋め込み、
# それより古い macOS では「アップデートが必要」と起動を拒否される(CI の macos-latest が
# macOS 26 になったことで実際に発生した)。
# -Werror=unguarded-availability-new は MACOS_MIN より新しい API を @available ガード
# なしで使うとビルドエラーにする(古い OS での実行時クラッシュをコンパイル時に検出する)。
# -g -O2 は cgo の既定値(環境変数で上書きすると既定が消えるため書き戻す)。
MACOS_MIN := 13.0
export CGO_CFLAGS := -g -O2 -mmacosx-version-min=$(MACOS_MIN) -Werror=unguarded-availability-new
export CGO_LDFLAGS := -mmacosx-version-min=$(MACOS_MIN)

APP := build/gaya-talk.app

# whisper モデルの取得先・置き場。config の whisper.model もここを指す。
MODEL_DIR := $(HOME)/.config/gaya-talk/models
MODEL := ggml-large-v3-turbo.bin
MODEL_URL := https://huggingface.co/ggerganov/whisper.cpp/resolve/main/$(MODEL)

# 既知モデルの SHA-256(Hugging Face の LFS メタデータより)。ダウンロード後に検証し、
# 配布元の侵害・すり替え・破損を検出する(モデルはネイティブコードに食わせるデータなので)。
# 一覧にないモデル名は検証をスキップする(その旨を表示)。
model_sha256 = $(strip \
  $(if $(filter ggml-large-v3-turbo.bin,$(1)),1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69,\
  $(if $(filter ggml-large-v3.bin,$(1)),64d182b440b98d5203c4f9bd541544d84c605196c4f7b845dfa11fb23594d1e2,\
  $(if $(filter ggml-large-v3-turbo-q5_0.bin,$(1)),394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2,))))

# verify_model: $(1)=ファイルパス $(2)=期待ハッシュ(空ならスキップ)。不一致ならファイルを消して失敗。
define verify_model
	if [ -n "$(2)" ]; then \
	  echo "$(2)  $(1)" | shasum -a 256 -c - >/dev/null 2>&1 \
	    || { echo "⚠️ チェックサム不一致のため削除しました(配布元の変更か破損): $(1)"; rm -f "$(1)"; exit 1; }; \
	  echo "✅ チェックサム OK: $(notdir $(1))"; \
	else \
	  echo "ℹ️ 既知のハッシュが無いため検証をスキップ: $(notdir $(1))"; \
	fi
endef

# 設定ファイルの置き場(.app はここを読む)。setup が配置し、enhance-model が model を書き換える。
CONFIG := $(HOME)/.config/gaya-talk/config.json

# 整形用 Ollama の gaya-talk 専用ホスト:ポート(config の enhance.endpoint と揃える)。
# アプリが自動起動し、アプリ終了時に一緒に停止する。他アプリの Ollama(11434)とは干渉しない。
OLLAMA_HOST_DEDICATED := 127.0.0.1:11477

# 署名ID。未指定ならキーチェーンにある gaya-talk-dist(配布ビルドと同一の身元)→
# gaya-talk-dev の順で自動選択し、どちらも無ければアドホック(-)にフォールバックする。
# 配布と同じ身元で署名しておくと、ローカルビルドと配布版を行き来しても
# アクセシビリティ等の権限がバッティングしない(TCC はバンドルID+署名で許可を持つ)。
SIGN_IDENTITY ?=

# 既定のセットアップ = 文字だけで参加する人向け(参加者が最も多いため)。
# config を配置し voice.input を off にする。whisper モデルは取得しない
# (マイク・whisper・Ollama すべて不要)。声も使うなら setup-voice。
setup: ## 初回セットアップ(文字だけで参加。config 配置・voice.input off・モデル不要)
	@mkdir -p $(dir $(CONFIG))
	@if [ -f "$(CONFIG)" ]; then \
	  echo "既にあります(上書きしません): $(CONFIG)"; \
	  echo "文字だけで使うなら voice.input を \"off\" にしてください。"; \
	else \
	  cp config.example.json "$(CONFIG)"; \
	  sed -i '' '/"voice": *{/,/}/ s/"input": *"[^"]*"/"input": "off"/' "$(CONFIG)"; \
	  echo "配置しました(voice.input=off): $(CONFIG)"; \
	fi
	@echo "次: make app-open → アクセシビリティを許可 → メニューバーの「ルームに URL で参加…」に招待 URL を貼る"

# 音声入力も使う人向けのフルセットアップ。app 版(zip 配布)だけ使っていた人が
# 声も使いたくなったら、リポジトリを clone してこれを叩けば全部揃う。
setup-voice: ## 音声のフルセットアップ(whisper/ollama 導入 + モデル取得 + config 反映 + 再起動)
	@command -v whisper-cli >/dev/null 2>&1 || { echo "→ whisper-cpp をインストールします"; brew install whisper-cpp; }
	@command -v ollama >/dev/null 2>&1 || [ -d /Applications/Ollama.app ] || { echo "→ ollama をインストールします"; brew install ollama; }
	@mkdir -p $(dir $(CONFIG))
	@if [ -f "$(CONFIG)" ]; then \
	  echo "既にあります(上書きしません): $(CONFIG)"; \
	else \
	  cp config.example.json "$(CONFIG)"; \
	  echo "配置しました: $(CONFIG)(必要に応じ編集)"; \
	fi
	@# 旧デフォルトの endpoint(共有 11434)のままなら専用ポートへ移行する(カスタム値は触らない)
	@sed -i '' 's|"endpoint": *"http://localhost:11434"|"endpoint": "http://$(OLLAMA_HOST_DEDICATED)"|' "$(CONFIG)" 2>/dev/null || true
	@$(MAKE) whisper-model
	@$(MAKE) enhance-model
	@if pgrep -x gaya-talk >/dev/null 2>&1; then \
	  pkill -x gaya-talk; sleep 1; \
	  if [ -d /Applications/gaya-talk.app ]; then open /Applications/gaya-talk.app; \
	  elif [ -d $(APP) ]; then open $(APP); fi; \
	  echo "✅ 音声セットアップ完了。アプリを再起動しました。"; \
	elif [ -d /Applications/gaya-talk.app ]; then \
	  open /Applications/gaya-talk.app; echo "✅ 音声セットアップ完了。アプリを起動しました。"; \
	else \
	  echo "✅ 音声セットアップ完了。次: make app-open"; \
	fi

build: ## バイナリをビルド(bin/gaya-talk)
	go build $(LDFLAGS) -o bin/gaya-talk .

# アイコン(メニューバー PNG と AppIcon.icns)をコードから再生成する。
# デザインを変えるときは build/genicons.swift を編集してこれを叩く。
icons: ## アイコン一式を再生成(build/genicons.swift)
	swift build/genicons.swift

# .app バンドルを生成し、アドホック署名する。
# 権限(マイク/アクセシビリティ)はこの .app の身元に紐づくようになる。
app: build ## .app を生成して署名する
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp bin/gaya-talk $(APP)/Contents/MacOS/gaya-talk
	cp build/Info.plist $(APP)/Contents/Info.plist
	cp build/AppIcon.icns $(APP)/Contents/Resources/AppIcon.icns
	@id="$(SIGN_IDENTITY)"; \
	if [ -z "$$id" ]; then \
	  if security find-identity -p codesigning 2>/dev/null | grep -q "gaya-talk-dist"; then id="gaya-talk-dist"; \
	  elif security find-identity -p codesigning 2>/dev/null | grep -q "gaya-talk-dev"; then id="gaya-talk-dev"; \
	  else echo "署名証明書が無いためアドホック署名にフォールバックします"; id="-"; fi; \
	elif [ "$$id" != "-" ] && ! security find-identity -p codesigning 2>/dev/null | grep -q "$$id"; then \
	  echo "署名ID '$$id' がキーチェーンに無いためアドホック署名にフォールバックします"; id="-"; \
	fi; \
	codesign --force --sign "$$id" --identifier com.mykstmhr.gayatalk $(APP); \
	echo "built $(APP) (signed with: $$id)"

# 生成した .app を起動する(ログは ~/Library/Logs/gaya-talk.log)。
app-open: app ## .app をビルドして起動する
	open $(APP)

# 配布用に .app を zip 化する(チームへ配って各自ビルド不要にする)。
# 注意: 自己署名/アドホック署名のため、受け取った人は Gatekeeper で初回だけ
# 「右クリック→開く」か quarantine 属性の削除が必要(下に案内を出す)。
dist: app ## 配布用に .app を zip 化(dist/gaya-talk.app.zip)
	@mkdir -p dist
	@rm -f dist/gaya-talk.app.zip
	@ditto -c -k --sequesterRsrc --keepParent $(APP) dist/gaya-talk.app.zip
	@echo "作成: dist/gaya-talk.app.zip"
	@echo "配布先での初回起動: アプリを右クリック→「開く」(またはターミナルで xattr -dr com.apple.quarantine /path/to/gaya-talk.app)"
	@echo "起動後にアクセシビリティ権限を許可。音声も使うなら別途 whisper-cpp とモデルが必要。"

# GitHub Release を作る。タグを push すると CI(.github/workflows/release.yml)が
# テスト → make dist → zip を Release に添付する。ローカルではビルドしない。
# VERSION には vX.Y.Z の直接指定のほか patch / minor / major を渡せる
# (直近のタグから該当の桁を 1 つ上げる。自動計算のときは確認プロンプトを出す)。
release: ## GitHub Release を作る(make release VERSION=v1.2.3 | patch | minor | major)
	@v="$(VERSION)"; \
	if [ -z "$$v" ]; then \
	  echo "使い方: make release VERSION=v1.2.3(または patch / minor / major)"; \
	  last=$$(git describe --tags --abbrev=0 2>/dev/null); \
	  [ -n "$$last" ] && echo "直近のタグ: $$last" || echo "タグはまだありません(v0.1.0 から始めるのがおすすめ)"; \
	  exit 1; \
	fi; \
	case "$$v" in \
	  major|minor|patch) \
	    last=$$(git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0); \
	    case "$$last" in \
	      v[0-9]*.[0-9]*.[0-9]*) ;; \
	      *) echo "⚠️ 直近のタグ $$last が vX.Y.Z 形式でないため自動インクリメントできません。VERSION=vX.Y.Z で指定してください。"; exit 1 ;; \
	    esac; \
	    base=$${last#v}; base=$${base%%-*}; base=$${base%%+*}; \
	    X=$${base%%.*}; rest=$${base#*.}; Y=$${rest%%.*}; Z=$${rest#*.}; \
	    case "$$v" in \
	      major) X=$$((X+1)); Y=0; Z=0 ;; \
	      minor) Y=$$((Y+1)); Z=0 ;; \
	      patch) Z=$$((Z+1)) ;; \
	    esac; \
	    v="v$$X.$$Y.$$Z"; \
	    printf "直近のタグ %s → %s としてリリースします。よろしいですか? [y/N]: " "$$last" "$$v"; \
	    read ans; case "$$ans" in y|Y|yes) ;; *) echo "中止しました。"; exit 1 ;; esac; \
	    ;; \
	  v[0-9]*) ;; \
	  *) echo "⚠️ VERSION は vX.Y.Z か patch / minor / major を指定してください"; exit 1 ;; \
	esac; \
	[ "$$(git branch --show-current)" = "main" ] || { echo "⚠️ main 以外のブランチです(push でローカルの古い main が飛ぶため中止)。"; exit 1; }; \
	[ -z "$$(git status --porcelain)" ] || { echo "⚠️ 未コミットの変更があります。コミットしてから release してください。"; exit 1; }; \
	go test ./internal/... > /dev/null || { echo "⚠️ テストが失敗しました。"; exit 1; }; \
	git tag -a "$$v" -m "$$v"; \
	git push origin main "$$v"; \
	echo "✅ タグ $$v を push しました。CI がテスト → zip ビルド → Release 添付まで行います。"; \
	echo "   進捗: https://github.com/mykstmhr/gaya-talk/actions"

# 起動中の gaya-talk を停止して開き直す(config 変更の反映など)。
# -x はプロセス名で完全一致するので、pkill 自身のシェル行を誤爆しない。
# 開き直す .app は /Applications 優先(gh release で入れた人が大半)、無ければローカルビルド。
restart: ## 起動中の .app を停止して開き直す(config 変更の反映。再ビルドはしない)
	@pkill -x gaya-talk 2>/dev/null || true
	@sleep 1
	@if [ -d /Applications/gaya-talk.app ]; then open /Applications/gaya-talk.app; \
	elif [ -d $(APP) ]; then open $(APP); \
	else echo "⚠️ gaya-talk.app が見つかりません(/Applications にも $(APP) にも無い)。README のインストールか make app-open を先に。"; exit 1; fi
	@echo "再起動しました(ログ: ~/Library/Logs/gaya-talk.log)"

# .app 起動時のログ(~/Library/Logs/gaya-talk.log)を追尾する。
# ログの実体は macOS の作法どおり ~/Library/Logs/ に置く(リポジトリ内には置かない:
# 発話由来の内容を含みうるため誤コミットを避け、.app の CWD にも依存しない)。
logs: ## .app のログ(~/Library/Logs/gaya-talk.log)を tail -f で追う
	@touch ~/Library/Logs/gaya-talk.log
	@tail -f ~/Library/Logs/gaya-talk.log

# whisper モデルを ~/.config/gaya-talk/models/ にダウンロードする(既定 turbo、既にあれば skip)。
# 特定モデルを直接指定するとき: make model MODEL=ggml-large-v3.bin
# verify-model は MODEL のチェックサムを検証する内部ターゲット(whisper-model から呼ぶ)。
verify-model:
	@$(call verify_model,$(MODEL_DIR)/$(MODEL),$(call model_sha256,$(MODEL)))

model: ## whisper モデルを直接指定して取得(make model MODEL=<file>)
	@mkdir -p $(MODEL_DIR)
	@if [ -f "$(MODEL_DIR)/$(MODEL)" ]; then \
	  echo "既にあります: $(MODEL_DIR)/$(MODEL)"; \
	else \
	  echo "ダウンロード中: $(MODEL_URL)"; \
	  curl -L --fail -o "$(MODEL_DIR)/$(MODEL)" "$(MODEL_URL)"; \
	  $(call verify_model,$(MODEL_DIR)/$(MODEL),$(call model_sha256,$(MODEL))); \
	  echo "保存: $(MODEL_DIR)/$(MODEL)"; \
	fi

# whisper モデルを候補から選んで取得し、config の whisper.model に反映する(setup-voice から呼ばれる)。
whisper-model: ## whisper モデルを番号で選び直す(config も更新)
	@mkdir -p $(MODEL_DIR)
	@echo "whisper モデルを選んでください(数字を入力):"; \
	echo "  1) ggml-large-v3-turbo.bin       速い・軽い(おすすめ既定, 約1.5GB)"; \
	echo "  2) ggml-large-v3.bin             高精度・重い(約3GB)"; \
	echo "  3) ggml-large-v3-turbo-q5_0.bin  量子化 turbo・最軽量(約1GB)"; \
	printf "番号 [1]: "; read n; \
	case "$$n" in \
	  2) m=ggml-large-v3.bin ;; \
	  3) m=ggml-large-v3-turbo-q5_0.bin ;; \
	  *) m=ggml-large-v3-turbo.bin ;; \
	esac; \
	if [ -f "$(MODEL_DIR)/$$m" ]; then \
	  echo "既にあります: $(MODEL_DIR)/$$m"; \
	else \
	  echo "→ $$m をダウンロードします"; \
	  curl -L --fail -o "$(MODEL_DIR)/$$m" "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/$$m" || { echo "ダウンロード失敗"; exit 1; }; \
	  $(MAKE) --no-print-directory verify-model MODEL="$$m" || exit 1; \
	  echo "保存: $(MODEL_DIR)/$$m"; \
	fi; \
	if [ -f "$(CONFIG)" ]; then \
	  sed -i '' -e "/\"whisper\": *{/,/}/ s|\"model\": *\"[^\"]*\"|\"model\": \"~/.config/gaya-talk/models/$$m\"|" \
	            -e "s|\"whisper_model\": *\"[^\"]*\"|\"whisper_model\": \"~/.config/gaya-talk/models/$$m\"|" "$(CONFIG)"; \
	  echo "✅ whisper.model を $$m に更新: $(CONFIG)"; \
	else \
	  echo "⚠️ $(CONFIG) が無いので whisper.model は手動設定してください(値: ~/.config/gaya-talk/models/$$m)"; \
	fi

# 整形用の LLM(Ollama)モデルを選んで pull し、config の enhance.model に反映する。
# CLI が PATH に無くても Ollama.app 同梱の CLI で動かす(setup-voice の判定と揃える)。
enhance-model: ## 整形用の Ollama モデルを番号で選んで pull(config も更新)
	@ollama="$$(command -v ollama || true)"; \
	[ -n "$$ollama" ] || { [ -x /Applications/Ollama.app/Contents/Resources/ollama ] && ollama=/Applications/Ollama.app/Contents/Resources/ollama; }; \
	[ -n "$$ollama" ] || { echo "ollama が必要です: brew install ollama"; exit 1; }; \
	echo "整形に使う Ollama モデルを選んでください(数字を入力):"; \
	echo "  1) qwen2.5:7b    高品質・遅め(ロード~15s)"; \
	echo "  2) qwen2.5:3b    バランス(おすすめ)"; \
	echo "  3) qwen2.5:1.5b  最速・軽量"; \
	echo "  4) gemma2:2b     代替候補"; \
	printf "番号 [2]: "; read n; \
	case "$$n" in \
	  1) m=qwen2.5:7b ;; \
	  3) m=qwen2.5:1.5b ;; \
	  4) m=gemma2:2b ;; \
	  *) m=qwen2.5:3b ;; \
	esac; \
	echo "→ $$m をダウンロードします"; \
	tmp=""; \
	if ! curl -sf "http://$(OLLAMA_HOST_DEDICATED)/api/version" >/dev/null 2>&1; then \
	  OLLAMA_HOST=$(OLLAMA_HOST_DEDICATED) "$$ollama" serve >/dev/null 2>&1 & tmp=$$!; sleep 2; \
	fi; \
	OLLAMA_HOST=$(OLLAMA_HOST_DEDICATED) "$$ollama" pull "$$m"; rc=$$?; \
	[ -n "$$tmp" ] && kill $$tmp 2>/dev/null; \
	[ $$rc -eq 0 ] || { echo "pull に失敗しました"; exit 1; }; \
	if [ -f "$(CONFIG)" ]; then \
	  sed -i '' "/\"enhance\": *{/,/}/ s/\"model\": *\"[^\"]*\"/\"model\": \"$$m\"/" "$(CONFIG)"; \
	  echo "✅ enhance.model を $$m に更新: $(CONFIG)"; \
	else \
	  echo "⚠️ $(CONFIG) が無いので enhance.model は手動設定してください(値: $$m)"; \
	fi; \
	echo "反映するには: make restart"

# 中継サーバ(server/)を Cloudflare にデプロイする。壊れたサーバを上げないよう
# テストを通してから deploy する。初回は `cd server && npx wrangler login` が必要。
# 注意: ルームのライフサイクル変更を含むデプロイでは、既存ルームの URL が無効になる
# ことがある(デプロイ後に作り直して配り直す)。
deploy: ## 中継サーバを Cloudflare にデプロイ(テスト → wrangler deploy)
	@cd server && { [ -d node_modules ] || npm ci; }
	@cd server && npm test
	@cd server && npx wrangler deploy
	@echo "✅ デプロイしました。config の room.server が上の URL と一致しているか確認してください。"

clean: ## ビルド成果物を削除する
	rm -rf bin $(APP)

# アプリと関連データを削除する(README「アンインストール」の 1〜5 と同じ)。
# brew パッケージ(whisper-cpp / ollama / gh)と ollama のモデルは他アプリと共有の
# 可能性があるため触らない(必要なら README の任意手順を参照)。
uninstall: ## アプリ・設定・モデル・内部データ・ログ・権限登録を削除する
	@echo "以下を削除します:"
	@echo "  /Applications/gaya-talk.app と $(APP)"
	@echo "  ~/.config/gaya-talk(config と whisper モデル)"
	@echo "  ~/Library/Application Support/gaya-talk(表示名・ルーム管理シークレット)"
	@echo "  ~/Library/Logs/gaya-talk.log と 権限登録(アクセシビリティ/マイク)"
	@echo "⚠️ 自分が作成したルームは管理シークレットが消えると二度と無効化できません。"
	@printf "よろしいですか? [y/N]: "; read ans; case "$$ans" in y|Y|yes) ;; *) echo "中止しました。"; exit 1 ;; esac
	@pkill -x gaya-talk 2>/dev/null || true; sleep 1
	@rm -rf /Applications/gaya-talk.app $(APP) bin
	@rm -rf "$(HOME)/.config/gaya-talk"
	@rm -rf "$(HOME)/Library/Application Support/gaya-talk"
	@rm -f "$(HOME)/Library/Logs/gaya-talk.log"
	@tccutil reset Accessibility com.mykstmhr.gayatalk >/dev/null 2>&1 || true
	@tccutil reset Microphone com.mykstmhr.gayatalk >/dev/null 2>&1 || true
	@echo "✅ アンインストールしました。ollama のモデルや brew パッケージも消す場合は README の「アンインストール」を参照。"
