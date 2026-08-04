.PHONY: test prod prod-gohon prod-gohon-serve

GO_LDFLAGS := -s -w
GO_BUILD := go build -trimpath -ldflags "$(GO_LDFLAGS)"

test:
	go test ./runtime/... ./pool/... ./stdlib/... -count=1

prod: prod-gohon prod-gohon-serve

prod-gohon:
	$(GO_BUILD) -o gohon ./cmd/gohon

prod-gohon-serve:
	$(GO_BUILD) -o gohon-serve ./cmd/gohon-serve
