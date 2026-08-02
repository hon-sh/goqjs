.PHONY: test bench

test:
	go test ./runtime/... ./pool/... ./stdlib/... -count=1

bench:
	./bench/run.sh
