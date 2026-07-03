.PHONY: help build clean setup setup-voice app app-open dist model whisper-model enhance-model restart logs deploy icons

# 素の `make` はヘルプを表示する(誤って setup を走らせないため)。
.DEFAULT_GOAL := help

# 各ターゲット末尾の `## 説明` を集めて一覧表示する。
help: ## このヘルプを表示する
	@echo "ura-talk make targets:"; \
	grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | sort | \
	awk 'BEGIN{FS=":.*## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# cgo の -lobjc 重複によるリンカ警告(無害)を抑止する。
LDFLAGS := -ldflags=-extldflags=-Wl,-no_warn_duplicate_libraries

APP := build/ura-talk.app

# whisper モデルの取得先・置き場。config の whisper_model もここを指す。
MODEL_DIR := $(HOME)/.config/ura-talk/models
MODEL := ggml-large-v3-turbo.bin
MODEL_URL := https://huggingface.co/ggerganov/whisper.cpp/resolve/main/$(MODEL)

# 設定ファイルの置き場(.app はここを読む)。setup が配置し、enhance-model が model を書き換える。
CONFIG := $(HOME)/.config/ura-talk/config.json

# 署名ID。既定は安定した自己署名証明書 ura-talk-dev。
# これで署名すると再ビルドしてもアクセシビリティ等の権限が失効しない。
# 証明書がキーチェーンに無ければ自動でアドホック(-)にフォールバックする。
SIGN_IDENTITY ?= ura-talk-dev

# 既定のセットアップ = 文字だけで参加する人向け(参加者が最も多いため)。
# config を配置し voice_input を off にする。whisper モデルは取得しない
# (マイク・whisper・Ollama すべて不要)。声も使うなら setup-voice。
setup: ## 初回セットアップ(文字だけで参加。config 配置・voice_input off・モデル不要)
	@mkdir -p $(dir $(CONFIG))
	@if [ -f "$(CONFIG)" ]; then \
	  echo "既にあります(上書きしません): $(CONFIG)"; \
	  echo "文字だけで使うなら voice_input を \"off\" にしてください。"; \
	else \
	  cp config.example.json "$(CONFIG)"; \
	  sed -i '' 's/"voice_input": *"[^"]*"/"voice_input": "off"/' "$(CONFIG)"; \
	  echo "配置しました(voice_input=off): $(CONFIG)"; \
	fi
	@echo "次: make app-open → アクセシビリティを許可 → メニューバーの「ルームに URL で参加…」に招待 URL を貼る"

# 音声入力も使う人向け: config を配置し、whisper モデルを選んで取得する。
setup-voice: ## 音声も使うセットアップ(config 配置 + whisper モデルを選んで取得)
	@mkdir -p $(dir $(CONFIG))
	@if [ -f "$(CONFIG)" ]; then \
	  echo "既にあります(上書きしません): $(CONFIG)"; \
	else \
	  cp config.example.json "$(CONFIG)"; \
	  echo "配置しました: $(CONFIG)(必要に応じ編集)"; \
	fi
	@$(MAKE) whisper-model

build: ## バイナリをビルド(bin/ura-talk)
	go build $(LDFLAGS) -o bin/ura-talk .

# アイコン(メニューバー PNG と AppIcon.icns)をコードから再生成する。
# デザインを変えるときは build/genicons.swift を編集してこれを叩く。
icons: ## アイコン一式を再生成(build/genicons.swift)
	swift build/genicons.swift

# .app バンドルを生成し、アドホック署名する。
# 権限(マイク/アクセシビリティ)はこの .app の身元に紐づくようになる。
app: build ## .app を生成して署名する
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp bin/ura-talk $(APP)/Contents/MacOS/ura-talk
	cp build/Info.plist $(APP)/Contents/Info.plist
	cp build/AppIcon.icns $(APP)/Contents/Resources/AppIcon.icns
	@id="$(SIGN_IDENTITY)"; \
	if ! security find-identity -p codesigning 2>/dev/null | grep -q "$$id"; then \
	  echo "署名ID '$$id' がキーチェーンに無いためアドホック署名にフォールバックします"; id="-"; \
	fi; \
	codesign --force --sign "$$id" --identifier com.mykstmhr.uratalk $(APP); \
	echo "built $(APP) (signed with: $$id)"

# 生成した .app を起動する(ログは ~/Library/Logs/ura-talk.log)。
app-open: app ## .app をビルドして起動する
	open $(APP)

# 配布用に .app を zip 化する(チームへ配って各自ビルド不要にする)。
# 注意: 自己署名/アドホック署名のため、受け取った人は Gatekeeper で初回だけ
# 「右クリック→開く」か quarantine 属性の削除が必要(下に案内を出す)。
dist: app ## 配布用に .app を zip 化(dist/ura-talk.app.zip)
	@mkdir -p dist
	@rm -f dist/ura-talk.app.zip
	@ditto -c -k --sequesterRsrc --keepParent $(APP) dist/ura-talk.app.zip
	@echo "作成: dist/ura-talk.app.zip"
	@echo "配布先での初回起動: アプリを右クリック→「開く」(またはターミナルで xattr -dr com.apple.quarantine /path/to/ura-talk.app)"
	@echo "起動後にアクセシビリティ権限を許可。音声も使うなら別途 whisper-cpp とモデルが必要。"

# 起動中の ura-talk を停止して開き直す(config 変更の反映など)。
# -x はプロセス名で完全一致するので、pkill 自身のシェル行を誤爆しない。
restart: ## 起動中の .app を停止して開き直す(config 変更の反映。再ビルドはしない)
	@pkill -x ura-talk 2>/dev/null || true
	@sleep 1
	@open $(APP)
	@echo "再起動しました(ログ: ~/Library/Logs/ura-talk.log)"

# .app 起動時のログ(~/Library/Logs/ura-talk.log)を追尾する。
# ログの実体は macOS の作法どおり ~/Library/Logs/ に置く(リポジトリ内には置かない:
# 発話由来の内容を含みうるため誤コミットを避け、.app の CWD にも依存しない)。
logs: ## .app のログ(~/Library/Logs/ura-talk.log)を tail -f で追う
	@touch ~/Library/Logs/ura-talk.log
	@tail -f ~/Library/Logs/ura-talk.log

# whisper モデルを ~/.config/ura-talk/models/ にダウンロードする(既定 turbo、既にあれば skip)。
# 特定モデルを直接指定するとき: make model MODEL=ggml-large-v3.bin
model: ## whisper モデルを直接指定して取得(make model MODEL=<file>)
	@mkdir -p $(MODEL_DIR)
	@if [ -f "$(MODEL_DIR)/$(MODEL)" ]; then \
	  echo "既にあります: $(MODEL_DIR)/$(MODEL)"; \
	else \
	  echo "ダウンロード中: $(MODEL_URL)"; \
	  curl -L --fail -o "$(MODEL_DIR)/$(MODEL)" "$(MODEL_URL)"; \
	  echo "保存: $(MODEL_DIR)/$(MODEL)"; \
	fi

# whisper モデルを候補から選んで取得し、config の whisper_model に反映する(setup-voice から呼ばれる)。
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
	  echo "保存: $(MODEL_DIR)/$$m"; \
	fi; \
	if [ -f "$(CONFIG)" ]; then \
	  sed -i '' "s|\"whisper_model\": *\"[^\"]*\"|\"whisper_model\": \"~/.config/ura-talk/models/$$m\"|" "$(CONFIG)"; \
	  echo "✅ whisper_model を $$m に更新: $(CONFIG)"; \
	else \
	  echo "⚠️ $(CONFIG) が無いので whisper_model は手動設定してください(値: ~/.config/ura-talk/models/$$m)"; \
	fi

# 整形用の LLM(Ollama)モデルを選んで pull し、config の enhance.model に反映する。
enhance-model: ## 整形用の Ollama モデルを番号で選んで pull(config も更新)
	@command -v ollama >/dev/null 2>&1 || { echo "ollama が必要です: brew install ollama"; exit 1; }
	@echo "整形に使う Ollama モデルを選んでください(数字を入力):"; \
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
	ollama pull "$$m" || { echo "pull に失敗。Ollama が起動しているか確認してください(ollama serve / Ollama.app)"; exit 1; }; \
	if [ -f "$(CONFIG)" ]; then \
	  sed -i '' "s/\"model\": *\"[^\"]*\"/\"model\": \"$$m\"/" "$(CONFIG)"; \
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
	@cd server && { [ -d node_modules ] || npm install; }
	@cd server && npm test
	@cd server && npx wrangler deploy
	@echo "✅ デプロイしました。config の room.server が上の URL と一致しているか確認してください。"

clean: ## ビルド成果物を削除する
	rm -rf bin $(APP)
