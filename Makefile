BINARY      := x-dl
PKG         := ./cmd/x-dl
DIST        := dist
INSTALL_DIR := $(HOME)/.local/bin

.PHONY: build test vet tidy clean install cross all help deps deps-system

all: vet test build

deps:
	go mod download

deps-system:
	@command -v ffmpeg >/dev/null 2>&1 \
		&& echo "✓ ffmpeg present ($$(ffmpeg -version | head -n1))" \
		|| { \
			echo "Installing ffmpeg..."; \
			if [ "$$(uname)" = "Darwin" ]; then \
				command -v brew >/dev/null 2>&1 \
					&& brew install ffmpeg \
					|| { echo "❌ Homebrew not found. Install from https://brew.sh, then re-run."; exit 1; }; \
			elif [ "$$(uname)" = "Linux" ]; then \
				if command -v apt-get >/dev/null 2>&1; then sudo apt-get update && sudo apt-get install -y ffmpeg; \
				elif command -v dnf >/dev/null 2>&1; then sudo dnf install -y ffmpeg; \
				elif command -v yum >/dev/null 2>&1; then sudo yum install -y ffmpeg; \
				elif command -v pacman >/dev/null 2>&1; then sudo pacman -S --noconfirm ffmpeg; \
				else echo "❌ No supported package manager found"; exit 1; fi; \
			else echo "❌ Unsupported platform"; exit 1; fi; \
		}
	@if [ "$$(uname)" = "Darwin" ] && [ ! -d "/Applications/Google Chrome.app" ]; then \
		echo "⚠️  Google Chrome not found at /Applications/Google Chrome.app — install it from https://www.google.com/chrome/"; \
	elif [ "$$(uname)" = "Linux" ] && ! command -v google-chrome >/dev/null 2>&1 && ! command -v chromium >/dev/null 2>&1; then \
		echo "⚠️  Chrome/Chromium not found — install one (e.g. sudo apt-get install chromium-browser)"; \
	else \
		echo "✓ Chrome/Chromium present"; \
	fi

build:
	@mkdir -p $(DIST)
	go build -o $(DIST)/$(BINARY) $(PKG)
	@ls -lh $(DIST)/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(DIST)

install: build
	@mkdir -p $(INSTALL_DIR)
	cp $(DIST)/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "Installed to $(INSTALL_DIR)/$(BINARY)"

cross:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=arm64 go build -o $(DIST)/$(BINARY)-macos-arm64 $(PKG)
	GOOS=darwin GOARCH=amd64 go build -o $(DIST)/$(BINARY)-macos-intel $(PKG)
	GOOS=linux  GOARCH=amd64 go build -o $(DIST)/$(BINARY)-linux-x64   $(PKG)
	@ls -lh $(DIST)/

help:
	@echo "Targets:"
	@echo "  build        Build $(BINARY) for the current platform into $(DIST)/"
	@echo "  test         Run all Go tests"
	@echo "  vet          Run go vet on all packages"
	@echo "  tidy         Run go mod tidy"
	@echo "  clean        Remove the $(DIST)/ directory"
	@echo "  install      Build and copy the binary to $(INSTALL_DIR)/"
	@echo "  cross        Cross-compile for macOS arm64, macOS intel, and Linux x86_64"
	@echo "  deps         Download Go module dependencies"
	@echo "  deps-system  Install ffmpeg (via brew/apt/etc) and check for Chrome"
	@echo "  all          vet + test + build (default if no target given)"
