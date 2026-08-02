package pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"goqjs/runtime"
)

// Pool is N independent Runtimes (each with its own JS heap and loop thread).
// Run dispatches round-robin across workers.
type Pool struct {
	workers []*runtime.Runtime
	next    atomic.Uint64
	done    chan struct{}
}

// SetupFunc configures a Runtime after New and before any Run (e.g. stdlib.Install).
type SetupFunc func(*runtime.Runtime) error

// New starts n Runtimes sharing ctx and the same run function expression.
// If setup is non-nil, it is called for each worker before the pool is returned.
func New(ctx context.Context, run string, n int, setup SetupFunc) (*Pool, error) {
	if n < 1 {
		return nil, fmt.Errorf("pool size must be >= 1")
	}
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}

	ctx, stop := context.WithCancel(ctx)

	workers := make([]*runtime.Runtime, 0, n)
	for i := 0; i < n; i++ {
		r, err := runtime.New(ctx, run)
		if err != nil {
			stop()
			for _, w := range workers {
				<-w.Done()
			}
			return nil, err
		}
		if setup != nil {
			if err := setup(r); err != nil {
				stop()
				<-r.Done()
				for _, w := range workers {
					<-w.Done()
				}
				return nil, err
			}
		}
		workers = append(workers, r)
	}

	p := &Pool{
		workers: workers,
		done:    make(chan struct{}),
	}
	go func() {
		defer stop()
		var wg sync.WaitGroup
		for _, w := range workers {
			wg.Add(1)
			go func(w *runtime.Runtime) {
				defer wg.Done()
				<-w.Done()
			}(w)
		}
		wg.Wait()
		close(p.done)
	}()
	return p, nil
}

// Run invokes run on the next worker (round-robin).
func (p *Pool) Run(args ...any) error {
	if len(p.workers) == 0 {
		return fmt.Errorf("empty pool")
	}
	i := p.next.Add(1) - 1
	return p.workers[i%uint64(len(p.workers))].Run(args...)
}

// Done is closed after every worker Runtime has exited.
func (p *Pool) Done() <-chan struct{} {
	return p.done
}

// Size returns the number of worker Runtimes.
func (p *Pool) Size() int {
	return len(p.workers)
}

// ForEach runs fn for each worker. Only safe before any Run.
func (p *Pool) ForEach(fn func(*runtime.Runtime)) {
	for _, w := range p.workers {
		fn(w)
	}
}
