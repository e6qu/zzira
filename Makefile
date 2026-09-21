SERVER_PORT ?= 8080
DATABASE_URL ?= postgres://zzira:zzira@localhost:5433/zzira?sslmode=disable
WORKSPACE_SLUG ?= zzira
DATABASE_NAME ?= zzira
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS = -s -w -X github.com/e6qu/zzira/internal/build.Version=$(VERSION)

.PHONY: all assets server client-wasm test build migrate dev down reset seed demo conformance e2e clean

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

# reset puts the development database back to where CI starts: empty,
# migrated and seeded. A drop that fails because something is still
# connected would otherwise leave yesterday's data behind, and a browser run
# reads whatever it finds as fact, so the connections are closed first and
# psql stops on the first error rather than reporting it and carrying on.
reset:
	docker compose up -d --wait postgres
	docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U zzira -d postgres \
		-c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='$(DATABASE_NAME)' AND pid <> pg_backend_pid()"
	docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U zzira -d postgres -c 'DROP DATABASE IF EXISTS $(DATABASE_NAME)'
	docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U zzira -d postgres -c 'CREATE DATABASE $(DATABASE_NAME)'
	$(MAKE) migrate
	$(MAKE) seed

seed:
	go run ./cmd/server -mode=seed

# demo builds the declarative demo company: three months of history across
# Jira, Jira Software, Jira Service Management and Confluence. It builds into
# the workspace `make dev` serves, or the company would be in a site the
# server never shows.
demo:
	WORKSPACE_SLUG='$(WORKSPACE_SLUG)' go run ./cmd/server -mode=demo -scenario=demo/company.json

conformance: build
	python3 -m unittest api/conformance/test_inventory.py api/conformance/test_coverage.py
	python3 api/conformance/inventory.py --check
	python3 api/conformance/coverage.py --check
	go test ./internal/api3 ./internal/confluence -v

loadtest: build
	./bin/loadtest

e2e: build dev
	cd e2e && npx playwright test

clean:
	rm -rf bin
