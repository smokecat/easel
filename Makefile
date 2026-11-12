.DEFAULT_GOAL := build

BIN     ?= bin/easel
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --verify -q HEAD 2>/dev/null)
PKG     := github.com/smokecat/easel/internal/cli
LDFLAGS := -s -w -X $(PKG).version=$(VERSION) -X $(PKG).commit=$(COMMIT)

export CGO_ENABLED ?= 0

.PHONY: build web go deps dev test test-go test-web lint generate fmt clean

## build: 构建前端并编译单二进制到 $(BIN)
build: web go

## web: 构建管理面板到 web/dist（随后被 go:embed 嵌入）
web: web/node_modules
	cd web && bun run build
	@touch web/dist/.gitkeep

## go: 只编译 Go 二进制（使用当前 web/dist 的内容）
go:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/easel

web/node_modules: web/package.json web/bun.lock
	cd web && bun install --frozen-lockfile
	@touch $@

## deps: 安装前端依赖、下载 Go 依赖
deps: web/node_modules
	go mod download

## dev: 同时起后端（:7780）和前端 dev server（Vite，转发 /api 到后端）
dev: web/node_modules
	@trap 'trap - INT TERM EXIT; kill 0' INT TERM EXIT; \
	go run ./cmd/easel serve & \
	cd web && bun run dev

## test: go test 与前端测试
test: test-go test-web

test-go:
	go test ./...

test-web: web/node_modules
	cd web && bun run test

## lint: gofmt / go vet / oxlint / tsc
lint: web/node_modules
	@out=$$(gofmt -l $$(git ls-files --cached --others --exclude-standard '*.go')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...
	cd web && bun run lint && bun run typecheck

## generate: 改了 internal/store 的迁移或 queries.sql 后重新生成 sqlc 代码（sqlc 需要 cgo）
generate:
	go generate ./internal/store

## fmt: 格式化 Go 代码
fmt:
	gofmt -w $$(git ls-files --cached --others --exclude-standard '*.go')

## clean: 删除构建产物
clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
