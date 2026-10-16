.PHONY: dev css css-build migrate-up migrate-down seed build deploy test e2e db-up tools

# Tailwind v4 standalone CLI (no Node.js in the CSS build). The Dockerfile
# pins the same version.
TAILWIND_VERSION := 4.3.3
TAILWIND := bin/tailwindcss

# The app's published port for `make dev` (the plan's 8080 unless taken).
APP_PORT ?= 8080
# The port `make e2e` serves on, separate from `make dev`.
E2E_PORT ?= 8090

UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)
ifeq ($(UNAME_S),Darwin)
  TW_OS := macos
else
  TW_OS := linux
endif
ifneq (,$(filter $(UNAME_M),arm64 aarch64))
  TW_ARCH := arm64
else
  TW_ARCH := x64
endif
ifeq ($(TW_OS),linux)
  TW_ASSET := tailwindcss-linux-$(TW_ARCH)-musl
else
  TW_ASSET := tailwindcss-$(TW_OS)-$(TW_ARCH)
endif
TW_URL := https://github.com/tailwindlabs/tailwindcss/releases/download/v$(TAILWIND_VERSION)

# Download the pinned Tailwind binary for this machine once, checking it
# against the release's published checksums.
$(TAILWIND):
	@mkdir -p bin
	curl -fsSL -o $(TAILWIND).download $(TW_URL)/$(TW_ASSET)
	curl -fsSL $(TW_URL)/sha256sums.txt | grep " ./$(TW_ASSET)$$" | awk '{print $$1 "  $(TAILWIND).download"}' | shasum -a 256 -c -
	chmod +x $(TAILWIND).download && mv $(TAILWIND).download $(TAILWIND)
	@./$(TAILWIND) --help | head -1 | grep -q "v$(TAILWIND_VERSION)" || { echo "wrong Tailwind version in $(TAILWIND)"; exit 1; }

tools: $(TAILWIND)

# Build static/css/output.css once.
css-build: $(TAILWIND)
	./$(TAILWIND) -i input.css -o static/css/output.css

# Rebuild static/css/output.css whenever a template changes.
css: $(TAILWIND)
	./$(TAILWIND) -i input.css -o static/css/output.css --watch

db-up:
	docker compose up -d --wait db

# Local stack: migrate and seed (hikers.localhost:$(APP_PORT)), then serve
# at localhost:$(APP_PORT). Templates and static files are bind-mounted.
dev: css-build db-up
	APP_PORT=$(APP_PORT) docker compose run --rm --build app ./server migrate up
	APP_PORT=$(APP_PORT) docker compose run --rm app ./server seed-dev
	APP_PORT=$(APP_PORT) docker compose up --build app

# Migrations are embedded in the binary, so these rebuild the image first
# and always apply the files in the working tree.
migrate-up: db-up
	docker compose run --rm --build app ./server migrate up

migrate-down: db-up
	docker compose run --rm --build app ./server migrate down

seed: db-up
	docker compose run --rm --build app ./server seed-dev

# go vet, then every test with the race detector against the compose
# Postgres. -count=1: database-backed results must never come from the
# test cache.
test: css-build db-up
	go vet ./...
	go test -race -count=1 ./...

# Playwright, on the platform and on a custom domain, against a dedicated
# e2e database (never the dev database).
e2e: css-build db-up
	E2E_PORT=$(E2E_PORT) ./e2e/run.sh

build:
	docker build -t meetspace:latest .

deploy: build
	docker compose -f docker-compose.prod.yml run --rm app ./server migrate up
	docker compose -f docker-compose.prod.yml up -d
