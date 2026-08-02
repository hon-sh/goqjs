package pool_test

import (
	"context"
	goruntime "runtime"
	"sync"
	"testing"
	"time"

	"goqjs/pool"
	"goqjs/runtime"
)

func TestRoundRobin(t *testing.T) {
	const run = `async function(n) { console.log("x"); }`
	var mu sync.Mutex
	var count int
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := pool.New(ctx, run, 2, func(r *runtime.Runtime) error {
		r.InjectHost("consoleLog", func(payload string) (string, error) {
			mu.Lock()
			count++
			mu.Unlock()
			return "", nil
		})
		return r.Eval(`globalThis.console = { log: function() {
  __goqjs_host("consoleLog", "[]");
}};`)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		<-p.Done()
	})
	if p.Size() != 2 {
		t.Fatalf("size=%d", p.Size())
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		n := i
		wg.Go(func() {
			if err := p.Run(n); err != nil {
				t.Errorf("Run(%d): %v", n, err)
			}
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if count != 4 {
		t.Fatalf("writes=%d", count)
	}
}

func TestCPUParallelism(t *testing.T) {
	if goruntime.NumCPU() < 2 {
		t.Skip("need at least 2 CPUs")
	}
	const run = `async function(iters) {
  let x = 0;
  for (let i = 0; i < iters; i++) x = (x + i) | 0;
}`
	const iters = 25_000_000

	timePool := func(nWorkers int) time.Duration {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p, err := pool.New(ctx, run, nWorkers, nil)
		if err != nil {
			t.Fatalf("New(%d): %v", nWorkers, err)
		}
		start := time.Now()
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Go(func() {
				if err := p.Run(iters); err != nil {
					t.Errorf("Run: %v", err)
				}
			})
		}
		wg.Wait()
		d := time.Since(start)
		cancel()
		<-p.Done()
		return d
	}

	d1 := timePool(1)
	d2 := timePool(2)
	t.Logf("c=1 %v; c=2 %v", d1, d2)
	if d2 >= d1*85/100 {
		t.Fatalf("expected c=2 faster than c=1: c1=%v c2=%v", d1, d2)
	}
}

func TestInvalidSize(t *testing.T) {
	_, err := pool.New(context.Background(), `async function() {}`, 0, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
