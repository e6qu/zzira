SERVER_PORT ?= 8080
DATABASE_URL ?= postgres://zzira:zzira@localhost:5433/zzira?sslmode=disable
WORKSPACE_SLUG ?= zzira
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS = -s -w -X github.com/e6qu/zzira/internal/build.Version=$(VERSION)

.PHONY: all assets server client-wasm test build migrate dev down seed conformance e2e clean

all: assets build test

assets:
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/static/wasm/wasm_exec.js
	mkdir -p bin
	cp bin/zzira-worker.wasm web/static/zzira-worker.wasm

server:
	go build -ldflags '$(LDFLAGS)' -o bin/zzira-server ./cmd/server

client-wasm:
	GOOS=js GOARCH=wasm go build -ldflags '$(LDFLAGS)' -o bin/zzira-worker.wasm ./cmd/client

build: server client-wasm

test:
	go test ./...
	GOOS=js GOARCH=wasm go build ./... 

migrate:
	go run ./cmd/server -mode=migrate

dev:
	docker compose up -d --wait postgres
	$(MAKE) migrate
	$(MAKE) seed
	DATABASE_URL='$(DATABASE_URL)' SERVER_PORT=$(SERVER_PORT) WORKSPACE_SLUG='$(WORKSPACE_SLUG)' go run ./cmd/server

down:
	docker compose down

seed:
	go run ./cmd/server -mode=seed

conformance: build
	python3 api/conformance/inventory.py --check
	go test ./internal/api3 ./internal/confluence -v

loadtest: build
	./bin/loadtest

e2e: build dev
	cd e2e && npx playwright test

clean:
	rm -rf bin
