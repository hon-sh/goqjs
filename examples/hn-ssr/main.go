// Minimal Hacker News SSR host: embeds Vite dist/ and renders via goqjs.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"regexp"
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

const hnAPIPrefix = "https://hacker-news.firebaseio.com/v0"

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	workers := flag.Int("c", 2, "goqjs runtime pool size")
	enableCache := flag.Bool("cache", false, "cache HN Firebase GET responses (FIFO, max 100 URLs)")
	clientJS := flag.String("client-js", "on", "include client JS for hydrate: on|off")
	flag.Parse()

	if *workers < 1 {
		fmt.Fprintf(os.Stderr, "hn-ssr: -c must be >= 1\n")
		os.Exit(2)
	}
	clientJSOn, err := parseOnOff("client-js", *clientJS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hn-ssr: %v\n", err)
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
	tpl := string(template)
	if !clientJSOn {
		tpl = stripClientScripts(tpl)
	}

	clientFS, err := fs.Sub(distFS, "dist/client")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hn-ssr: %v\n", err)
		os.Exit(1)
	}

	fetchClient := &http.Client{Timeout: 30 * time.Second}
	var hnCache *hnFetchCache
	if *enableCache {
		hnCache = newHNFetchCache(100)
		fetchClient.Transport = &hnCacheTransport{
			base:  http.DefaultTransport,
			cache: hnCache,
		}
	}

	results := &resultStore{}
	run := `async function(reqId, url) {
  if (typeof globalThis.__hn_render !== "function") {
    throw new Error("__hn_render missing; rebuild dist (make pdist prod)");
  }
  try {
    var out = await globalThis.__hn_render(String(url));
    if (!out || typeof out.html !== "string") {
      throw new Error("bad render result: " + typeof out);
    }
    __goqjs_host("ssrResult", JSON.stringify({
      id: reqId,
      html: out.html,
      data: out.data,
      load_ms: out.load_ms|0,
      render_ms: out.render_ms|0
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
		if err := stdlib.Install(r, stdlib.Options{
			Console: true,
			Fetch:   true,
			Client:  fetchClient,
		}); err != nil {
			return err
		}
		r.InjectHost("ssrResult", results.host)
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
	mux.Handle("/assets/", withHashedAssetCache(http.FileServer(http.FS(clientFS))))
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.HasPrefix(req.URL.Path, "/assets/") {
			http.NotFound(w, req)
			return
		}

		start := time.Now()
		id := results.nextID()
		url := req.URL.RequestURI()

		var hits0, misses0 int64
		if hnCache != nil {
			hits0, misses0 = hnCache.stats()
		}

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
		page := tpl
		page = strings.Replace(page, "<!--app-html-->", out.HTML, 1)
		if clientJSOn {
			page = strings.Replace(
				page,
				"<!--app-data-->",
				`<script>window.__INITIAL_DATA__=`+serializeJSON(out.Data)+`</script>`,
				1,
			)
		} else {
			page = strings.Replace(page, "<!--app-data-->", "", 1)
		}
		asmMs := time.Since(tAsm).Milliseconds()

		tWrite := time.Now()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", pageCacheControl(req.URL.Path))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page))
		writeMs := time.Since(tWrite).Milliseconds()

		cachePart := ""
		if hnCache != nil {
			hits1, misses1 := hnCache.stats()
			cachePart = fmt.Sprintf(" fetch_cache=%d/%d",
				hits1-hits0, (hits1-hits0)+(misses1-misses0))
		}
		fmt.Fprintf(os.Stderr,
			"hn-ssr: %s %s total=%dms run=%dms (load=%dms render=%dms%s) assemble=%dms write=%dms\n",
			req.Method, url,
			time.Since(start).Milliseconds(),
			runMs, out.LoadMs, out.RenderMs, cachePart,
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
		cacheNote = "on (HN GET FIFO max 100)"
	}
	jsNote := "on"
	if !clientJSOn {
		jsNote = "off"
	}
	fmt.Fprintf(os.Stderr, "hn-ssr: listening on %s (pool=%d cache=%s client-js=%s)\n",
		*addr, *workers, cacheNote, jsNote)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "hn-ssr: %v\n", err)
		os.Exit(1)
	}
}

func parseOnOff(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "1", "true":
		return true, nil
	case "off", "0", "false":
		return false, nil
	default:
		return false, fmt.Errorf("-%s must be on or off (got %q)", name, v)
	}
}

var clientScriptRE = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>\s*`)

func stripClientScripts(html string) string {
	return clientScriptRE.ReplaceAllString(html, "")
}

func withHashedAssetCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".js") {
			// Vite content-hashed filenames under /assets/.
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		next.ServeHTTP(w, r)
	})
}

func pageCacheControl(path string) string {
	if strings.HasPrefix(path, "/item/") {
		return "public, max-age=180"
	}
	// home and other HTML pages
	return "public, max-age=60"
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

type hnCacheEntry struct {
	status int
	body   []byte
}

// hnFetchCache is a FIFO map of full request URL → response body (demo; no TTL).
type hnFetchCache struct {
	mu     sync.Mutex
	max    int
	items  map[string]hnCacheEntry
	order  []string
	hits   atomic.Int64
	misses atomic.Int64
}

func newHNFetchCache(max int) *hnFetchCache {
	if max < 1 {
		max = 100
	}
	return &hnFetchCache{
		max:   max,
		items: make(map[string]hnCacheEntry),
	}
}

func (c *hnFetchCache) stats() (hits, misses int64) {
	return c.hits.Load(), c.misses.Load()
}

func (c *hnFetchCache) get(url string) (hnCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[url]
	return e, ok
}

func (c *hnFetchCache) put(url string, e hnCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[url]; ok {
		c.items[url] = e
		return
	}
	for len(c.order) >= c.max {
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.items, old)
	}
	c.items[url] = e
	c.order = append(c.order, url)
}

// hnCacheTransport caches GET responses under hnAPIPrefix.
type hnCacheTransport struct {
	base  http.RoundTripper
	cache *hnFetchCache
}

func (t *hnCacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	url := req.URL.String()
	cacheable := req.Method == http.MethodGet && strings.HasPrefix(url, hnAPIPrefix)

	if cacheable {
		if e, ok := t.cache.get(url); ok {
			t.cache.hits.Add(1)
			return cachedResponse(req, e), nil
		}
		t.cache.misses.Add(1)
	}

	resp, err := base.RoundTrip(req)
	if err != nil || !cacheable || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	t.cache.put(url, hnCacheEntry{status: resp.StatusCode, body: body})
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

func cachedResponse(req *http.Request, e hnCacheEntry) *http.Response {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", e.status, http.StatusText(e.status)),
		StatusCode:    e.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(e.body)),
		ContentLength: int64(len(e.body)),
		Request:       req,
	}
}
