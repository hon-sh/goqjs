package runtime

/*
#cgo CFLAGS: -I${SRCDIR} -I${SRCDIR}/../third_party/quickjs -D_GNU_SOURCE
#cgo LDFLAGS: -lm
#cgo linux LDFLAGS: -ldl -lpthread
#cgo darwin LDFLAGS: -ldl -lpthread

#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	registry sync.Map // int32 -> *Runtime
	nextGoID atomic.Int32
)

func lookupRuntime(id int32) *Runtime {
	v, ok := registry.Load(id)
	if !ok {
		return nil
	}
	return v.(*Runtime)
}

//export goqjs_host_done
func goqjs_host_done(goID C.int, id C.int, ok C.int, errMsg *C.char) {
	r := lookupRuntime(int32(goID))
	if r == nil {
		return
	}
	r.finish(int(id), ok != 0, C.GoString(errMsg))
}

//export goqjs_host_wake_process
func goqjs_host_wake_process(goID C.int) {
	r := lookupRuntime(int32(goID))
	if r == nil {
		return
	}
	r.drainCtrl()
	r.drainAsyncSettles()
	r.drainRequests()
}

type request struct {
	id   int
	args string
}

// Runtime is a single QuickJS instance with an event loop on one OS thread.
type Runtime struct {
	id       int32
	ctx      context.Context
	handleMu sync.RWMutex
	handle   *C.goqjs_rt

	mu     sync.Mutex
	frozen bool

	hostSync  map[string]HostFunc
	hostAsync map[string]AsyncHostFunc

	reqCh    chan request
	ctrlCh   chan ctrlMsg
	asyncCh  chan asyncSettle
	asyncID  atomic.Int64
	pending  sync.Map // id -> chan error
	nextID   atomic.Int64
	loopDone chan struct{}
	startErr error
	started  chan struct{}
}

// New creates a QuickJS runtime, evaluates the core boot (std/os, timers,
// invoke), binds run, and starts js_std_loop. Host APIs (console/fetch) are not
// installed — use goqjs/stdlib.Install or Inject* before the first Run.
//
// run is a JS function expression, e.g. `async function(c) { ... }`.
func New(ctx context.Context, run string) (*Runtime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	run = strings.TrimSpace(run)
	if run == "" {
		return nil, fmt.Errorf("empty run")
	}

	id := nextGoID.Add(1)
	r := &Runtime{
		id:       id,
		ctx:      ctx,
		reqCh:    make(chan request, 64),
		ctrlCh:   make(chan ctrlMsg, 16),
		asyncCh:  make(chan asyncSettle, 64),
		loopDone: make(chan struct{}),
		started:  make(chan struct{}),
	}
	registry.Store(id, r)

	go r.loop(run)

	select {
	case <-r.started:
		if r.startErr != nil {
			<-r.loopDone
			return nil, r.startErr
		}
		return r, nil
	case <-ctx.Done():
		<-r.loopDone
		return nil, ctx.Err()
	}
}

func (r *Runtime) loop(run string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(r.loopDone)
	defer registry.Delete(r.id)

	h := C.goqjs_create(C.int32_t(r.id))
	if h == nil {
		r.startErr = fmt.Errorf("goqjs_create failed")
		close(r.started)
		return
	}
	r.handleMu.Lock()
	r.handle = h
	r.handleMu.Unlock()
	defer func() {
		r.handleMu.Lock()
		r.handle = nil
		r.handleMu.Unlock()
		C.goqjs_destroy(h)
	}()

	if err := r.evalOnLoop(h, coreBoot, "<boot>", 1); err != nil {
		r.startErr = err
		close(r.started)
		return
	}
	if err := r.evalOnLoop(h, asyncHelperJS, "<async-helper>", 0); err != nil {
		r.startErr = err
		close(r.started)
		return
	}

	bind := "globalThis.__goqjs_run = (" + run + ");"
	if err := r.evalOnLoop(h, bind, "<run>", 0); err != nil {
		r.startErr = fmt.Errorf("eval run: %w", err)
		close(r.started)
		return
	}

	if C.goqjs_install_wake(h) != 0 {
		r.startErr = fmt.Errorf("install wake handler failed")
		close(r.started)
		return
	}

	close(r.started)

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		<-r.ctx.Done()
		C.goqjs_request_stop(h)
	}()

	C.goqjs_loop(h)
	<-stopDone

	r.pending.Range(func(key, value any) bool {
		ch := value.(chan error)
		select {
		case ch <- context.Canceled:
		default:
		}
		r.pending.Delete(key)
		return true
	})
}

func (r *Runtime) evalOnLoop(h *C.goqjs_rt, script, filename string, module int) error {
	cs := C.CString(script)
	cname := C.CString(filename)
	ret := C.goqjs_eval(h, cs, cname, C.int(module))
	C.free(unsafe.Pointer(cs))
	C.free(unsafe.Pointer(cname))
	if ret != 0 {
		return fmt.Errorf("eval %s failed", filename)
	}
	return nil
}

func (r *Runtime) wake() {
	r.handleMu.RLock()
	h := r.handle
	r.handleMu.RUnlock()
	if h != nil {
		C.goqjs_wake(h)
	}
}

func (r *Runtime) drainRequests() {
	for {
		select {
		case req := <-r.reqCh:
			cs := C.CString(req.args)
			ret := C.goqjs_invoke(r.handle, C.int(req.id), cs)
			C.free(unsafe.Pointer(cs))
			if ret != 0 {
				r.finish(req.id, false, "invoke run failed")
			}
		default:
			return
		}
	}
}

func (r *Runtime) finish(id int, ok bool, errMsg string) {
	v, loaded := r.pending.LoadAndDelete(id)
	if !loaded {
		return
	}
	ch := v.(chan error)
	var err error
	if !ok {
		if errMsg == "" {
			errMsg = "js run rejected"
		}
		err = fmt.Errorf("%s", errMsg)
	}
	select {
	case ch <- err:
	default:
	}
}

// Run invokes the function expression passed to New with args (int/string/bool)
// and waits until the returned value settles. The first Run freezes host injection.
func (r *Runtime) Run(args ...any) error {
	if err := checkArgs(args); err != nil {
		return err
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.freezeLocked()
	r.mu.Unlock()

	id := int(r.nextID.Add(1))
	done := make(chan error, 1)
	r.pending.Store(id, done)

	select {
	case <-r.ctx.Done():
		r.pending.Delete(id)
		return r.ctx.Err()
	case <-r.loopDone:
		r.pending.Delete(id)
		return fmt.Errorf("runtime stopped")
	case r.reqCh <- request{id: id, args: string(payload)}:
		r.wake()
	}

	select {
	case err := <-done:
		return err
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-r.loopDone:
		return fmt.Errorf("runtime stopped")
	}
}

// Done is closed after the JS loop has exited and the runtime is destroyed.
func (r *Runtime) Done() <-chan struct{} {
	return r.loopDone
}

func checkArgs(args []any) error {
	for i, a := range args {
		switch a.(type) {
		case nil, bool, string, int, int32, int64, float64:
		default:
			return fmt.Errorf("unsupported Run arg[%d] type %T (want int/string/bool)", i, a)
		}
	}
	return nil
}
