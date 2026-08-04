package runtime

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func startRuntime(t *testing.T, run string) (context.CancelFunc, *Runtime) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := New(ctx, run)
	if err != nil {
		cancel()
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		<-r.Done()
	})
	return cancel, r
}

func installTestLog(r *Runtime, w *bytes.Buffer) error {
	r.InjectHost("consoleLog", func(payload string) (string, error) {
		s := strings.TrimSpace(payload)
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			inner := strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
			fmt.Fprintln(w, strings.ReplaceAll(inner, `"`, ""))
			return "", nil
		}
		fmt.Fprintln(w, payload)
		return "", nil
	})
	return r.Eval(`
globalThis.console = { log: function() {
  __hon_host("consoleLog", JSON.stringify(Array.prototype.slice.call(arguments)));
}};
`)
}

func TestRuntimeConsoleLog(t *testing.T) {
	const run = `async function(c) { console.log("[" + c + "]ok"); }`
	_, r := startRuntime(t, run)
	var buf bytes.Buffer
	if err := installTestLog(r, &buf); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(7); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[7]ok" {
		t.Fatalf("log=%q", got)
	}
}

func TestRuntimeRunReject(t *testing.T) {
	const run = `async function() { throw new Error("boom"); }`
	_, r := startRuntime(t, run)
	err := r.Run()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRuntimeSleepMultiplex(t *testing.T) {
	const run = `async function(ms) {
  await new Promise(function(resolve) { setTimeout(resolve, ms); });
}`
	_, r := startRuntime(t, run)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Go(func() {
			if err := r.Run(150); err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Fatalf("expected multiplexed sleeps, took %v", elapsed)
	}
}

func TestInjectAfterRunPanics(t *testing.T) {
	const run = `async function() {}`
	_, r := startRuntime(t, run)
	if err := r.Run(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = r.Eval(`globalThis.x = 1;`)
}

func TestMultipleRuntimesConsole(t *testing.T) {
	const run = `async function(n) { console.log(String(n)); }`
	var buf1, buf2 bytes.Buffer
	_, r1 := startRuntime(t, run)
	_, r2 := startRuntime(t, run)
	if err := installTestLog(r1, &buf1); err != nil {
		t.Fatal(err)
	}
	if err := installTestLog(r2, &buf2); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := r1.Run(1); err != nil {
			t.Errorf("r1: %v", err)
		}
	})
	wg.Go(func() {
		if err := r2.Run(2); err != nil {
			t.Errorf("r2: %v", err)
		}
	})
	wg.Wait()
	if strings.TrimSpace(buf1.String()) != "1" || strings.TrimSpace(buf2.String()) != "2" {
		t.Fatalf("buf1=%q buf2=%q", buf1.String(), buf2.String())
	}
}
