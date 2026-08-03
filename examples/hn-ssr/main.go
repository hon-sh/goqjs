// Minimal Hacker News SSR host: embeds Vite dist/ and renders via goqjs.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"goqjs/pool"
	"goqjs/runtime"
	"goqjs/stdlib"
)

//go:embed all:dist
var distFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	workers := flag.Int("c", 2, "goqjs runtime pool size")
	enableCache := flag.Bool("cache", false, "cache loadData JSON by URL (FIFO, max 100)")
	flag.Parse()

	if *workers < 1 {
		fmt.Fprintf(os.Stderr, "hn-ssr: -c must be >= 1\n")
		os.Exit(2)
	}

	ssrJS, err := fs.ReadFile(distFS, "dist/server/ssr.js")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hn-ssr: missing dist/server/ssr.js (run: npm run build)\n%v\n", err)
		os.Exit(1)
	}
	template, err := fs.ReadFile(distFS, "dist/client/index.html")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hn-ssr: missing dist/client/index.html (run: npm run build)\n%v\n", err)
		os.Exit(1)
	}

	clientFS, err := fs.Sub(distFS, "dist/client")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hn-ssr: %v\n", err)
		os.Exit(1)
	}

	results := &resultStore{}
	cache := newLoadCache(100)
	run := `async function(reqId, url) {
  if (typeof globalThis.__hn_load !== "function" || typeof globalThis.__hn_render_data !== "function") {
    throw new Error("__hn_load / __hn_render_data missing; rebuild dist (make pdist prod)");
  }
  try {
    url = String(url);
    var data;
    var load_ms = 0;
    var cache_hit = false;
    var cached = __goqjs_host("cacheGet", url);
    if (cached) {
      data = JSON.parse(cached);
      cache_hit = true;
    } else {
      var t0 = Date.now();
      data = await globalThis.__hn_load(url);
      load_ms = Date.now() - t0;
      __goqjs_host("cachePut", JSON.stringify({ url: url, data: data }));
    }
    var t1 = Date.now();
    var html = globalThis.__hn_render_data(data);
    var render_ms = Date.now() - t1;
    if (!html || typeof html !== "string") {
      throw new Error("bad render result: " + typeof html);
    }
    __goqjs_host("ssrResult", JSON.stringify({
      id: reqId,
      html: html,
      data: data,
      load_ms: load_ms|0,
      render_ms: render_ms|0,
      cache_hit: cache_hit
    }));
  } catch (e) {
    var msg = "ssr failed";
    try { if (e && e.message) msg = String(e.message); else msg = String(e); } catch (_) {}
    try { if (e && e.stack) msg += " | " + String(e.stack); } catch (_) {}
    if (globalThis.console && console.log) console.log("HN_SSR_ERROR", msg);
    throw new Error(msg);
  }
}`

	setup := func(r *runtime.Runtime) error {
		if err := stdlib.Install(r, stdlib.Options{Console: true, Fetch: true}); err != nil {
			return err
		}
		r.InjectHost("ssrResult", results.host)
		if *enableCache {
			r.InjectHost("cacheGet", cache.Get)
			r.InjectHost("cachePut", cache.Put)
		} else {
			r.InjectHost("cacheGet", func(string) (string, error) { return "", nil })
			r.InjectHost("cachePut", func(string) (string, error) { return "", nil })
		}
		if err := r.Eval(qjsPolyfills); err != nil {
			return fmt.Errorf("eval polyfills: %w", err)
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
		fmt.Fprintf(os.Stderr, "hn-ssr: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		cancel()
		<-p.Done()
	}()

	mux := http.NewServeMux()
	mux.Handle("/assets/", http.FileServer(http.FS(clientFS)))
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Let FileServer-style paths fall through only for assets (handled above).
		if strings.HasPrefix(req.URL.Path, "/assets/") {
			http.NotFound(w, req)
			return
		}

		start := time.Now()
		id := results.nextID()
		url := req.URL.RequestURI()

		tRun := time.Now()
		err := p.Run(id, url)
		runMs := time.Since(tRun).Milliseconds()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			fmt.Fprintf(os.Stderr, "hn-ssr: %s %s total=%dms run=%dms err=%v\n",
				req.Method, url, time.Since(start).Milliseconds(), runMs, err)
			return
		}
		out, ok := results.take(id)
		if !ok {
			http.Error(w, "ssr produced no result", http.StatusInternalServerError)
			fmt.Fprintf(os.Stderr, "hn-ssr: %s %s total=%dms run=%dms err=no result\n",
				req.Method, url, time.Since(start).Milliseconds(), runMs)
			return
		}

		tAsm := time.Now()
		page := string(template)
		page = strings.Replace(page, "<!--app-html-->", out.HTML, 1)
		page = strings.Replace(
			page,
			"<!--app-data-->",
			`<script>window.__INITIAL_DATA__=`+serializeJSON(out.Data)+`</script>`,
			1,
		)
		asmMs := time.Since(tAsm).Milliseconds()

		tWrite := time.Now()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page))
		writeMs := time.Since(tWrite).Milliseconds()

		cacheTag := "miss"
		if out.CacheHit {
			cacheTag = "hit"
		}
		fmt.Fprintf(os.Stderr,
			"hn-ssr: %s %s total=%dms run=%dms (load=%dms cache=%s render=%dms) assemble=%dms write=%dms\n",
			req.Method, url,
			time.Since(start).Milliseconds(),
			runMs, out.LoadMs, cacheTag, out.RenderMs,
			asmMs, writeMs,
		)
	})

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	cacheNote := "off"
	if *enableCache {
		cacheNote = "on (FIFO max 100)"
	}
	fmt.Fprintf(os.Stderr, "hn-ssr: listening on %s (pool=%d cache=%s)\n", *addr, *workers, cacheNote)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "hn-ssr: %v\n", err)
		os.Exit(1)
	}
}

func serializeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return strings.ReplaceAll(string(raw), "<", "\\u003c")
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
      } else if (c >= 0xd800 && c <= 0xdbff && i + 1 < str.length) {
        var c2 = str.charCodeAt(++i);
        var cp = 0x10000 + ((c & 0x3ff) << 10) + (c2 & 0x3ff);
        out.push(
          0xf0 | (cp >> 18),
          0x80 | ((cp >> 12) & 0x3f),
          0x80 | ((cp >> 6) & 0x3f),
          0x80 | (cp & 0x3f)
        );
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
      else if (c < 0xe0) {
        out += String.fromCharCode(((c & 0x1f) << 6) | (u8[i++] & 0x3f));
      } else if (c < 0xf0) {
        out += String.fromCharCode(((c & 0x0f) << 12) | ((u8[i++] & 0x3f) << 6) | (u8[i++] & 0x3f));
      } else {
        var cp = ((c & 0x07) << 18) | ((u8[i++] & 0x3f) << 12) | ((u8[i++] & 0x3f) << 6) | (u8[i++] & 0x3f);
        cp -= 0x10000;
        out += String.fromCharCode(0xd800 + (cp >> 10), 0xdc00 + (cp & 0x3ff));
      }
    }
    return out;
  };
}
if (typeof URL === "undefined") {
  globalThis.URL = function URL(input, base) {
    var href = String(input || "");
    if (base && href && href.charAt(0) === "/") {
      var b = String(base);
      var slash = b.indexOf("/", b.indexOf("//") >= 0 ? b.indexOf("//") + 2 : 0);
      href = (slash >= 0 ? b.slice(0, slash) : b.replace(/\/$/, "")) + href;
    }
    this.href = href;
    var m = href.match(/^(https?:)\/\/([^\/?#]+)([^?#]*)(\?[^#]*)?(#.*)?$/i);
    if (m) {
      this.protocol = m[1].toLowerCase();
      this.host = m[2];
      this.hostname = m[2].split(":")[0];
      this.pathname = m[3] || "/";
      this.search = m[4] || "";
      this.hash = m[5] || "";
    } else {
      this.protocol = "";
      this.host = "";
      this.hostname = "";
      this.pathname = href.split("?")[0].split("#")[0] || "/";
      this.search = href.indexOf("?") >= 0 ? "?" + href.split("?")[1].split("#")[0] : "";
      this.hash = href.indexOf("#") >= 0 ? "#" + href.split("#")[1] : "";
    }
  };
}
`

type ssrOut struct {
	HTML     string
	Data     json.RawMessage
	LoadMs   int64
	RenderMs int64
	CacheHit bool
}

type resultStore struct {
	next atomic.Int64
	mu   sync.Mutex
	m    map[int64]ssrOut
}

func (s *resultStore) nextID() int64 {
	return s.next.Add(1)
}

func (s *resultStore) host(payload string) (string, error) {
	var msg struct {
		ID       int64           `json:"id"`
		HTML     string          `json:"html"`
		Data     json.RawMessage `json:"data"`
		LoadMs   int64           `json:"load_ms"`
		RenderMs int64           `json:"render_ms"`
		CacheHit bool            `json:"cache_hit"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.m == nil {
		s.m = make(map[int64]ssrOut)
	}
	s.m[msg.ID] = ssrOut{
		HTML:     msg.HTML,
		Data:     msg.Data,
		LoadMs:   msg.LoadMs,
		RenderMs: msg.RenderMs,
		CacheHit: msg.CacheHit,
	}
	s.mu.Unlock()
	return "", nil
}

func (s *resultStore) take(id int64) (ssrOut, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, ok := s.m[id]
	if ok {
		delete(s.m, id)
	}
	return out, ok
}

// loadCache is a tiny FIFO map of URL → loadData JSON (demo; no TTL).
type loadCache struct {
	mu    sync.Mutex
	max   int
	items map[string]json.RawMessage
	order []string
}

func newLoadCache(max int) *loadCache {
	if max < 1 {
		max = 100
	}
	return &loadCache{
		max:   max,
		items: make(map[string]json.RawMessage),
	}
}

func (c *loadCache) Get(url string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[url]
	if !ok {
		return "", nil
	}
	return string(v), nil
}

func (c *loadCache) Put(payload string) (string, error) {
	var msg struct {
		URL  string          `json:"url"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(payload), &msg); err != nil {
		return "", err
	}
	if msg.URL == "" || len(msg.Data) == 0 {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[msg.URL]; ok {
		c.items[msg.URL] = msg.Data
		return "", nil
	}
	for len(c.order) >= c.max {
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.items, old)
	}
	c.items[msg.URL] = msg.Data
	c.order = append(c.order, msg.URL)
	return "", nil
}
