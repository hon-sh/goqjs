.PHONY: prod

GO_LDFLAGS := -s -w
GO_BUILD := go build -trimpath -ldflags "$(GO_LDFLAGS)"

prod:
	npm run build
	$(GO_BUILD) -o hn-ssr .
