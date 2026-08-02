package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"goqjs/pool"
	"goqjs/runtime"
	"goqjs/stdlib"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: goqjs [-c N] (-e code | -f file) [arg...]\n")
		fmt.Fprintf(os.Stderr, "  -c N      runtime pool size (default 1)\n")
		fmt.Fprintf(os.Stderr, "  -e code   JS function expression for run\n")
		fmt.Fprintf(os.Stderr, "  -f file   read run function expression from file\n")
		fmt.Fprintf(os.Stderr, "  each arg starts one concurrent pool.Run(arg)\n")
		fmt.Fprintf(os.Stderr, "\nexamples:\n")
		fmt.Fprintf(os.Stderr, "  goqjs -f examples/sleep.js 3 5 6\n")
		fmt.Fprintf(os.Stderr, "  goqjs -c 2 -f examples/fib.js 32 33 34 35\n")
		fmt.Fprintf(os.Stderr, "  goqjs -f examples/fact.js 20000 20000\n")
	}

	workers := flag.Int("c", 1, "runtime pool size")
	code := flag.String("e", "", "JS function expression for run")
	file := flag.String("f", "", "file containing JS function expression for run")
	flag.Parse()

	if *workers < 1 {
		fmt.Fprintf(os.Stderr, "goqjs: -c must be >= 1\n")
		os.Exit(2)
	}

	hasE := *code != ""
	hasF := *file != ""
	if hasE == hasF {
		if !hasE && !hasF {
			fmt.Fprintf(os.Stderr, "goqjs: require -e or -f\n")
		} else {
			fmt.Fprintf(os.Stderr, "goqjs: -e and -f are mutually exclusive\n")
		}
		flag.Usage()
		os.Exit(2)
	}

	var run string
	if hasE {
		run = *code
	} else {
		b, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "goqjs: read %s: %v\n", *file, err)
			os.Exit(1)
		}
		run = string(b)
	}
	run = strings.TrimSpace(run)
	if run == "" {
		fmt.Fprintf(os.Stderr, "goqjs: empty run\n")
		os.Exit(2)
	}

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "goqjs: require at least one arg for run\n")
		flag.Usage()
		os.Exit(2)
	}

	runArgs := make([]any, len(args))
	for i, a := range args {
		if n, err := strconv.Atoi(a); err == nil {
			runArgs[i] = n
		} else {
			runArgs[i] = a
		}
	}

	setup := func(r *runtime.Runtime) error {
		if err := stdlib.Install(r, stdlib.Options{Console: true}); err != nil {
			return err
		}
		// Convenience sugar for examples; not part of runtime core.
		return r.Eval(`globalThis.sleep = function(ms) {
  return new Promise(function(resolve) { setTimeout(resolve, ms); });
};`)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p, err := pool.New(ctx, run, *workers, setup)
	if err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "goqjs: %v\n", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup
	for _, arg := range runArgs {
		wg.Go(func() {
			if err := p.Run(arg); err != nil {
				fmt.Fprintf(os.Stderr, "goqjs: run(%v): %v\n", arg, err)
			}
		})
	}
	wg.Wait()
	cancel()
	<-p.Done()
}
