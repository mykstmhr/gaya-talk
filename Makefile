.PHONY: build run tidy clean app app-open model enhance-model restart

# cgo の -lobjc 重複によるリンカ警告(無害)を抑止する。
LDFLAGS := -ldflags=-extldflags=-Wl,-no_warn_duplicate_libraries

APP := build/ura-talk.app

# whisper モデルの取得先・置き場。config の whisper_model もここを指す。
MODEL_DIR := $(HOME)/.config/ura-talk/models
MODEL := ggml-large-v3-turbo.bin
MODEL_URL := https://huggingface.co/ggerganov/whisper.cpp/resolve/main/$(MODEL)

# 整形(enhance)用の設定ファイル。enhance-model がここの model を書き換える。
ENHANCE_CONFIG := $(HOME)/.config/ura-talk/config.json

# 署名ID。既定は安定した自己署名証明書 ura-talk-dev。
# これで署名すると再ビルドしてもアクセシビリティ等の権限が失効しない。
# 証明書がキーチェーンに無ければ自動でアドホック(-)にフォールバックする。
SIGN_IDENTITY ?= ura-talk-dev

build:
	go build $(LDFLAGS) -o bin/ura-talk .

run:
	go run $(LDFLAGS) .

# .app バンドルを生成し、アドホック署名する。
# 権限(マイク/アクセシビリティ)はこの .app の身元に紐づくようになる。
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp bin/ura-talk $(APP)/Contents/MacOS/ura-talk
	cp build/Info.plist $(APP)/Contents/Info.plist
	@id="$(SIGN_IDENTITY)"; \
	if ! security find-identity -p codesigning 2>/dev/null | grep -q "$$id"; then \
	  echo "署名ID '$$id' がキーチェーンに無いためアドホック署名にフォールバックします"; id="-"; \
	fi; \
	codesign --force --sign "$$id" --identifier com.mykstmhr.uratalk $(APP); \
	echo "built $(APP) (signed with: $$id)"

# 生成した .app を起動する(ログは ~/Library/Logs/ura-talk.log)。
app-open: app
	open $(APP)

# 起動中の ura-talk を停止して開き直す(config 変更の反映など)。
# -x はプロセス名で完全一致するので、pkill 自身のシェル行を誤爆しない。
restart:
	@pkill -x ura-talk 2>/dev/null || true
	@sleep 1
	@open $(APP)
	@echo "再起動しました(ログ: ~/Library/Logs/ura-talk.log)"

# whisper モデルを ~/.config/ura-talk/models/ にダウンロードする(約1.5GB、既にあれば skip)。
model:
	@mkdir -p $(MODEL_DIR)
	@if [ -f "$(MODEL_DIR)/$(MODEL)" ]; then \
	  echo "既にあります: $(MODEL_DIR)/$(MODEL)"; \
	else \
	  echo "ダウンロード中: $(MODEL_URL)"; \
	  curl -L --fail -o "$(MODEL_DIR)/$(MODEL)" "$(MODEL_URL)"; \
	  echo "保存: $(MODEL_DIR)/$(MODEL)"; \
	fi

# 整形用の LLM(Ollama)モデルを選んで pull し、config の enhance.model に反映する。
enhance-model:
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
	if [ -f "$(ENHANCE_CONFIG)" ]; then \
	  sed -i '' "s/\"model\": *\"[^\"]*\"/\"model\": \"$$m\"/" "$(ENHANCE_CONFIG)"; \
	  echo "✅ enhance.model を $$m に更新: $(ENHANCE_CONFIG)"; \
	else \
	  echo "⚠️ $(ENHANCE_CONFIG) が無いので enhance.model は手動設定してください(値: $$m)"; \
	fi; \
	echo "反映するには: make restart"

tidy:
	go mod tidy

clean:
	rm -rf bin $(APP)
