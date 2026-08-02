package main

import (
	"fmt"
	"os"
	"strconv"

	"goqjs/runtime"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: goqjs <c> [c...]\n")
		fmt.Fprintf(os.Stderr, "  each <c> starts one concurrent async job that writes 0..c-1 with 1s sleeps\n")
		os.Exit(2)
	}

	counts := make([]int, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		n, err := strconv.Atoi(a)
		if err != nil || n < 0 {
			fmt.Fprintf(os.Stderr, "invalid count %q\n", a)
			os.Exit(2)
		}
		counts = append(counts, n)
	}

	if err := runtime.Run(counts); err != nil {
		fmt.Fprintf(os.Stderr, "goqjs: %v\n", err)
		os.Exit(1)
	}
}
