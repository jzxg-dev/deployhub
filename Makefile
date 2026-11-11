# 常用命令。Node 和 pnpm 版本由 mise.toml 固定，所以 pnpm 通过 mise exec 调用。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X deployhub/internal/buildinfo.Version=$(VERSION)
GO ?= go
PNPM ?= mise exec -- pnpm

.PHONY: build web web-deps run dev-api dev-web test clean

## build：构建前端，再编译 Go 程序到 bin/deployhub（前端会嵌入可执行文件）
build: web
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/deployhub ./cmd/deployhub

web: web-deps
	cd web && $(PNPM) build

web-deps:
	cd web && $(PNPM) install --frozen-lockfile

## run：构建并启动，自动打开浏览器
run: build
	./bin/deployhub

## dev-api + dev-web：开发时分别在两个终端运行，浏览器打开终端里打印的地址（指向 5173）
dev-api:
	$(GO) run ./cmd/deployhub -dev-ui http://127.0.0.1:5173 -no-keychain -data .deployhub-dev

dev-web:
	cd web && $(PNPM) dev

## test：Go 的 vet 和测试，前端类型检查
test:
	$(GO) vet ./...
	$(GO) test ./...
	cd web && $(PNPM) typecheck

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
