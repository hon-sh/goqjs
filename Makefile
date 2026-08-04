.PHONY: test prod prod-goqjs prod-goqjs-serve

GO_LDFLAGS := -s -w
GO_BUILD := go build -trimpath -ldflags "$(GO_LDFLAGS)"

test:
	go test ./runtime/... ./pool/... ./stdlib/... -count=1

prod: prod-goqjs prod-goqjs-serve

prod-goqjs:
	$(GO_BUILD) -o goqjs ./cmd/goqjs

prod-goqjs-serve:
	$(GO_BUILD) -o goqjs-serve ./cmd/goqjs-serve
