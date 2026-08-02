package runtime

/*
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// HostFunc is a synchronous host callback. payload is JSON text; return value is
// returned to JS as a string (often JSON).
type HostFunc func(payload string) (string, error)

// AsyncHostFunc starts work and must call complete exactly once.
// When ok, result must be JSON text passed to Promise resolve.
// When !ok, result is an error message string (will be JSON-stringified for reject).
type AsyncHostFunc func(payload string, complete func(ok bool, result string))

type ctrlMsg struct {
	script string
	module bool
	done   chan error
}

type asyncSettle struct {
	id      int
	ok      bool
	payload string
}

func (r *Runtime) freezeLocked() {
	r.frozen = true
}

func (r *Runtime) mustNotFrozenLocked() {
	if r.frozen {
		panic("goqjs: host injection after first Run")
	}
}

// Eval evaluates a global script on the loop thread. Panics if called after the
// first Run.
func (r *Runtime) Eval(script string) error {
	r.mu.Lock()
	if r.frozen {
		r.mu.Unlock()
		panic("goqjs: host injection after first Run")
	}
	r.mu.Unlock()

	done := make(chan error, 1)
	select {
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-r.loopDone:
		return fmt.Errorf("runtime stopped")
	case r.ctrlCh <- ctrlMsg{script: script, module: false, done: done}:
		r.wake()
	}
	return <-done
}

// InjectHost registers a synchronous host function visible via
// __goqjs_host(name, payload). Panics after first Run.
func (r *Runtime) InjectHost(name string, fn HostFunc) {
	if name == "" || fn == nil {
		panic("goqjs: InjectHost requires name and fn")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mustNotFrozenLocked()
	if r.hostSync == nil {
		r.hostSync = make(map[string]HostFunc)
	}
	r.hostSync[name] = fn
}

// InjectAsyncHost registers an async host used via __goqjs_async(name, obj).
// Panics after first Run.
func (r *Runtime) InjectAsyncHost(name string, fn AsyncHostFunc) {
	if name == "" || fn == nil {
		panic("goqjs: InjectAsyncHost requires name and fn")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mustNotFrozenLocked()
	if r.hostAsync == nil {
		r.hostAsync = make(map[string]AsyncHostFunc)
	}
	r.hostAsync[name] = fn
}

func (r *Runtime) drainCtrl() {
	for {
		select {
		case msg := <-r.ctrlCh:
			filename := "<eval>"
			mod := 0
			if msg.module {
				mod = 1
				filename = "<module>"
			}
			cs := C.CString(msg.script)
			cname := C.CString(filename)
			ret := C.goqjs_eval(r.handle, cs, cname, C.int(mod))
			C.free(unsafe.Pointer(cs))
			C.free(unsafe.Pointer(cname))
			var err error
			if ret != 0 {
				err = fmt.Errorf("eval failed")
			}
			msg.done <- err
		default:
			return
		}
	}
}

func (r *Runtime) drainAsyncSettles() {
	for {
		select {
		case s := <-r.asyncCh:
			var payload string
			if s.ok {
				payload = s.payload
				if payload == "" {
					payload = "null"
				}
			} else {
				b, _ := json.Marshal(s.payload)
				payload = string(b)
			}
			cp := C.CString(payload)
			ret := C.goqjs_async_settle(r.handle, C.int(s.id), boolToCInt(s.ok), cp)
			C.free(unsafe.Pointer(cp))
			if ret != 0 {
				// Best-effort: reject waiter if settle eval failed.
				_ = ret
			}
		default:
			return
		}
	}
}

func boolToCInt(ok bool) C.int {
	if ok {
		return 1
	}
	return 0
}

//export goqjs_host_call
func goqjs_host_call(goID C.int, name *C.char, payload *C.char, errOut **C.char) *C.char {
	r := lookupRuntime(int32(goID))
	if r == nil {
		*errOut = C.CString("runtime not found")
		return nil
	}
	n := C.GoString(name)
	p := C.GoString(payload)
	r.mu.Lock()
	fn := r.hostSync[n]
	r.mu.Unlock()
	if fn == nil {
		*errOut = C.CString("unknown host function: " + n)
		return nil
	}
	out, err := fn(p)
	if err != nil {
		*errOut = C.CString(err.Error())
		return nil
	}
	return C.CString(out)
}

//export goqjs_host_async_start
func goqjs_host_async_start(goID C.int, name *C.char, payload *C.char, errOut **C.char) C.int {
	r := lookupRuntime(int32(goID))
	if r == nil {
		*errOut = C.CString("runtime not found")
		return 0
	}
	n := C.GoString(name)
	p := C.GoString(payload)
	r.mu.Lock()
	fn := r.hostAsync[n]
	r.mu.Unlock()
	if fn == nil {
		*errOut = C.CString("unknown async host: " + n)
		return 0
	}
	id := int(r.asyncID.Add(1))
	fn(p, func(ok bool, result string) {
		select {
		case r.asyncCh <- asyncSettle{id: id, ok: ok, payload: result}:
			r.wake()
		case <-r.loopDone:
		}
	})
	return C.int(id)
}
