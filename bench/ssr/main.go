// SSR bench host: sleep + fixed Array + React renderToString via goqjs.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"goqjs/pool"
	"goqjs/runtime"
	"goqjs/stdlib"
)

//go:embed handler.js
var handlerJS string

func main() {
	workers := flag.Int("c", 1, "runtime pool size")
	addr := flag.String("addr", ":19200", "listen address")
	ssrPath := flag.String("ssr", "", "path to dist/server/ssr.js (default: alongside binary or ./dist/server/ssr.js)")
	flag.Parse()

	if *workers < 1 {
		fmt.Fprintf(os.Stderr, "bench-ssr: -c must be >= 1\n")
		os.Exit(2)
	}

	jsPath := *ssrPath
	if jsPath == "" {
		jsPath = findSSRJS()
	}
	ssrJS, err := os.ReadFile(jsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench-ssr: read %s: %v\n(run: npm run build in bench/ssr)\n", jsPath, err)
		os.Exit(1)
	}

	userRun := strings.TrimSpace(handlerJS)
	run := wrapHTTPRun(userRun)

	sessions := &sessionStore{}

	setup := func(r *runtime.Runtime) error {
		if err := stdlib.Install(r, stdlib.Options{Console: true, Fetch: false}); err != nil {
			return err
		}
		r.InjectHost("httpWrite", sessions.hostWrite)
		if err := r.Eval(`globalThis.sleep = function(ms) {
  return new Promise(function(resolve) { setTimeout(resolve, ms); });
};`); err != nil {
			return err
		}
		if err := r.Eval(qjsPolyfills); err != nil {
			return fmt.Errorf("polyfills: %w", err)
		}
		if err := r.Eval(string(ssrJS)); err != nil {
			return fmt.Errorf("eval ssr.js: %w", err)
		}
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p, err := pool.New(ctx, run, *workers, setup)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench-ssr: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		cancel()
		<-p.Done()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" && req.URL.Path != "/ssr" {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		id := sessions.alloc(w)
		defer sessions.release(id)

		meta, err := json.Marshal(reqMeta{
			Method:  req.Method,
			URL:     req.URL.String(),
			Path:    req.URL.Path,
			Query:   req.URL.RawQuery,
			Headers: flattenHeaders(req.Header),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := p.Run(id, string(meta)); err != nil {
			if !sessions.wrote(id) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			fmt.Fprintf(os.Stderr, "bench-ssr: run: %v\n", err)
		}
	})

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	fmt.Fprintf(os.Stderr, "bench-ssr goqjs: listening on %s (pool=%d ssr=%s)\n", *addr, *workers, jsPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "bench-ssr: %v\n", err)
		os.Exit(1)
	}
}

func findSSRJS() string {
	candidates := []string{
		"dist/server/ssr.js",
		filepath.Join("bench", "ssr", "dist", "server", "ssr.js"),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append([]string{
			filepath.Join(filepath.Dir(exe), "dist", "server", "ssr.js"),
			filepath.Join(filepath.Dir(exe), "ssr.js"),
		}, candidates...)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return "dist/server/ssr.js"
}

type reqMeta struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Path    string            `json:"path"`
	Query   string            `json:"query"`
	Headers map[string]string `json:"headers"`
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		if len(vals) > 0 {
			out[k] = vals[0]
		}
	}
	return out
}

func wrapHTTPRun(userRun string) string {
	return `async function(reqId, metaJSON) {
  var meta = JSON.parse(metaJSON);
  var req = {
    method: meta.method,
    url: meta.url,
    path: meta.path,
    query: meta.query,
    headers: meta.headers || {}
  };
  var res = {
    statusCode: 200,
    write: async function(chunk) {
      __goqjs_host("httpWrite", JSON.stringify({
        id: reqId,
        status: this.statusCode|0,
        body: chunk === undefined || chunk === null ? "" : String(chunk),
        end: false
      }));
    },
    end: async function(chunk) {
      __goqjs_host("httpWrite", JSON.stringify({
        id: reqId,
        status: this.statusCode|0,
        body: chunk === undefined || chunk === null ? "" : String(chunk),
        end: true
      }));
    }
  };
  return (` + userRun + `)(req, res);
}`
}

type sessionStore struct {
	next atomic.Int64
	mu   sync.Mutex
	m    map[int64]*connState
}

type connState struct {
	w             http.ResponseWriter
	headerWritten bool
	ended         bool
	wroteBody     bool
}

func (s *sessionStore) alloc(w http.ResponseWriter) int64 {
	id := s.next.Add(1)
	s.mu.Lock()
	if s.m == nil {
		s.m = make(map[int64]*connState)
	}
	s.m[id] = &connState{w: w}
	s.mu.Unlock()
	return id
}

func (s *sessionStore) release(id int64) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

func (s *sessionStore) wrote(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.m[id]
	return st != nil && (st.headerWritten || st.wroteBody || st.ended)
}

func (s *sessionStore) hostWrite(payload string) (string, error) {
	var msg struct {
		ID     int64  `json:"id"`
		Status int    `json:"status"`
		Body   string `json:"body"`
		End    bool   `json:"end"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		return "", err
	}

	s.mu.Lock()
	st := s.m[msg.ID]
	s.mu.Unlock()
	if st == nil {
		return "", fmt.Errorf("unknown req_id %d", msg.ID)
	}

	s.mu.Lock()
	if st.ended {
		s.mu.Unlock()
		return "", fmt.Errorf("response already ended")
	}
	if !st.headerWritten {
		if msg.Status == 0 {
			msg.Status = http.StatusOK
		}
		st.w.WriteHeader(msg.Status)
		st.headerWritten = true
	}
	s.mu.Unlock()

	if msg.Body != "" {
		if _, err := st.w.Write([]byte(msg.Body)); err != nil {
			return "", err
		}
		s.mu.Lock()
		st.wroteBody = true
		s.mu.Unlock()
		if f, ok := st.w.(http.Flusher); ok {
			f.Flush()
		}
	}
	if msg.End {
		s.mu.Lock()
		st.ended = true
		s.mu.Unlock()
	}
	return "", nil
}

// Injected before ssr.js — React touches MessageChannel at module init.
const qjsPolyfills = `
if (typeof queueMicrotask !== "function") {
  globalThis.queueMicrotask = function(fn) { Promise.resolve().then(fn); };
}
if (typeof MessageChannel === "undefined") {
  globalThis.MessageChannel = function MessageChannel() {
    var self = this;
    this.port1 = { onmessage: null };
    this.port2 = {
      postMessage: function(data) {
        queueMicrotask(function() {
          if (typeof self.port1.onmessage === "function") {
            self.port1.onmessage({ data: data });
          }
        });
      }
    };
  };
}
if (typeof performance === "undefined" || typeof performance.now !== "function") {
  globalThis.performance = { now: function() { return Date.now(); } };
}
if (typeof process === "undefined") {
  globalThis.process = { env: { NODE_ENV: "production" } };
}
if (typeof reportError !== "function") {
  globalThis.reportError = function(e) {
    if (globalThis.console && console.log) console.log(String(e));
  };
}
if (typeof TextEncoder === "undefined") {
  globalThis.TextEncoder = function TextEncoder() {};
  TextEncoder.prototype.encode = function(str) {
    str = String(str);
    var out = [];
    for (var i = 0; i < str.length; i++) {
      var c = str.charCodeAt(i);
      if (c < 0x80) out.push(c);
      else if (c < 0x800) {
        out.push(0xc0 | (c >> 6), 0x80 | (c & 0x3f));
      } else {
        out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
      }
    }
    var u8 = new Uint8Array(out.length);
    for (var i = 0; i < out.length; i++) u8[i] = out[i];
    return u8;
  };
}
if (typeof TextDecoder === "undefined") {
  globalThis.TextDecoder = function TextDecoder() {};
  TextDecoder.prototype.decode = function(buf) {
    if (!buf) return "";
    var u8 = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
    var out = "";
    for (var i = 0; i < u8.length; ) {
      var c = u8[i++];
      if (c < 0x80) out += String.fromCharCode(c);
      else if ((c & 0xe0) === 0xc0) {
        out += String.fromCharCode(((c & 0x1f) << 6) | (u8[i++] & 0x3f));
      } else if ((c & 0xf0) === 0xe0) {
        out += String.fromCharCode(((c & 0x0f) << 12) | ((u8[i++] & 0x3f) << 6) | (u8[i++] & 0x3f));
      } else {
        i += 2;
      }
    }
    return out;
  };
}
`
