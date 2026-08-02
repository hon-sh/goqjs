.PHONY: test

test:
	go test ./runtime/... ./pool/... ./stdlib/... -count=1
