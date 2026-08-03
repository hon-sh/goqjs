.PHONY: prod prod-linux-x64 pdist clean

GO_LDFLAGS := -s -w
GO_BUILD := go build -trimpath -ldflags "$(GO_LDFLAGS)"

ZIG ?= zig
ZIG_CC_LINUX_X64 := $(ZIG) cc -target x86_64-linux-musl

# Directory targets: missing → install/build. Stale src/ will not rebuild until pdist.
node_modules: package.json package-lock.json
	npm install
	@touch node_modules

dist: node_modules
	npm run build
	@touch dist

prod: dist
	$(GO_BUILD) -o hn-ssr .

# Cross-compile with zig cc + musl (cgo + QuickJS). Requires Zig on PATH.
prod-linux-x64: dist
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC="$(ZIG_CC_LINUX_X64)" \
		$(GO_BUILD) -o hn-ssr-linux-x64 .

pdist:
	rm -rf dist

clean: pdist
	rm -rf node_modules hn-ssr hn-ssr-linux-x64
