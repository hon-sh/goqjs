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
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

//export goqjs_host_write
func goqjs_host_write(callID C.int, s *C.char) {
	fmt.Fprintf(os.Stdout, "[%d]%s\n", int(callID), C.GoString(s))
	_ = os.Stdout.Sync()
}

//export goqjs_host_done
func goqjs_host_done(id C.int, ok C.int, errMsg *C.char) {
	activeMu.Lock()
	r := active
	activeMu.Unlock()
	if r == nil {
		return
	}
	r.finish(int(id), ok != 0, C.GoString(errMsg))
}

//export goqjs_host_wake_process
func goqjs_host_wake_process() {
	activeMu.Lock()
	r := active
	activeMu.Unlock()
	if r == nil {
		return
	}
	r.drainRequests()
}

var (
	activeMu sync.Mutex
	active   *Runtime
)

type request struct {
	id   int
	args string
}

// Runtime is a single QuickJS instance with an event loop on one OS thread.
type Runtime struct {
	ctx      context.Context
	handleMu sync.RWMutex
	handle   *C.goqjs_rt

	reqCh    chan request
	pending  sync.Map // id -> chan error
	nextID   atomic.Int64
	loopDone chan struct{}
	startErr error
	started  chan struct{}
}

// New creates a QuickJS runtime, installs sleep/resp helpers, binds run as the
// function invoked by Run, and starts js_std_loop on a dedicated OS thread.
//
// run is a JS function expression (not a full script), e.g.
//
//	async function(c) { await resp.write(c, "hi"); }
//	async (a, b) => { ... }
//
// The loop exits after ctx is cancelled and idle.
func New(ctx context.Context, run string) (*Runtime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	run = strings.TrimSpace(run)
	if run == "" {
		return nil, fmt.Errorf("empty run")
	}

	r := &Runtime{
		ctx:      ctx,
		reqCh:    make(chan request, 64),
		loopDone: make(chan struct{}),
		started:  make(chan struct{}),
	}

	activeMu.Lock()
	if active != nil {
		activeMu.Unlock()
		return nil, fmt.Errorf("only one goqjs runtime is supported")
	}
	active = r
	activeMu.Unlock()

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
	defer func() {
		activeMu.Lock()
		if active == r {
			active = nil
		}
		activeMu.Unlock()
	}()

	h := C.goqjs_create()
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

	// Bind the caller's function expression; storage name is an internal detail.
	bind := "globalThis.__goqjs_run = (" + run + ");"
	cs := C.CString(bind)
	cname := C.CString("<run>")
	ret := C.goqjs_eval(h, cs, cname, 0)
	C.free(unsafe.Pointer(cs))
	C.free(unsafe.Pointer(cname))
	if ret != 0 {
		r.startErr = fmt.Errorf("eval run failed")
		close(r.started)
		return
	}

	if C.goqjs_install_wake(h) != 0 {
		r.startErr = fmt.Errorf("install wake handler failed")
		close(r.started)
		return
	}

	close(r.started)

	// Cancel → stop loop (clears wake handler; exits when idle).
	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		<-r.ctx.Done()
		C.goqjs_request_stop(h)
	}()

	C.goqjs_loop(h)
	<-stopDone

	// Fail any still-waiting Run callers.
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
// and waits until the returned value settles (awaits Promises).
func (r *Runtime) Run(args ...any) error {
	if err := checkArgs(args); err != nil {
		return err
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}

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
