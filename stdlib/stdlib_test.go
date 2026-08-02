package stdlib_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goqjs/runtime"
	"goqjs/stdlib"
)

func TestFetchGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt, err := runtime.New(ctx, `async function(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error("not ok");
  const j = await res.json();
  if (j.hello !== "world") throw new Error("bad json");
}`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); <-rt.Done() }()

	if err := stdlib.Install(rt, stdlib.Options{Fetch: true, Client: srv.Client()}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Run(srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestConsoleMultiArg(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var b strings.Builder
	rt, err := runtime.New(ctx, `async function() { console.log("a", 1, true); }`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); <-rt.Done() }()
	if err := stdlib.Install(rt, stdlib.Options{Console: true, Log: &b}); err != nil {
		t.Fatal(err)
	}
	if err := rt.Run(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "a 1 true" {
		t.Fatalf("got %q", got)
	}
}
