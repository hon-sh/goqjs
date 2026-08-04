package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/hon-go/hon/pool"
	"github.com/hon-go/hon/runtime"
	"github.com/hon-go/hon/stdlib"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: gohon-serve [-c N] [-addr host:port] (-e code | -f file)\n")
		fmt.Fprintf(os.Stderr, "  -c N       runtime pool size (default 1)\n")
		fmt.Fprintf(os.Stderr, "  -addr      listen address (default :8080)\n")
		fmt.Fprintf(os.Stderr, "  -e/-f      JS run as async function(req, res) { ... }\n")
		fmt.Fprintf(os.Stderr, "\nexamples:\n")
		fmt.Fprintf(os.Stderr, "  gohon-serve -f examples/serve-hello.js\n")
		fmt.Fprintf(os.Stderr, "  gohon-serve -c 2 -f examples/serve-sleep.js -addr :8080\n")
	}

	workers := flag.Int("c", 1, "runtime pool size")
	addr := flag.String("addr", ":8080", "listen address")
	code := flag.String("e", "", "JS function expression for run(req, res)")
	file := flag.String("f", "", "file containing JS function expression for run(req, res)")
	flag.Parse()

	if *workers < 1 {
		fmt.Fprintf(os.Stderr, "gohon-serve: -c must be >= 1\n")
		os.Exit(2)
	}

	hasE := *code != ""
	hasF := *file != ""
	if hasE == hasF {
		if !hasE && !hasF {
			fmt.Fprintf(os.Stderr, "gohon-serve: require -e or -f\n")
		} else {
			fmt.Fprintf(os.Stderr, "gohon-serve: -e and -f are mutually exclusive\n")
		}
		flag.Usage()
		os.Exit(2)
	}

	var userRun string
	if hasE {
		userRun = *code
	} else {
		b, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gohon-serve: read %s: %v\n", *file, err)
			os.Exit(1)
		}
		userRun = string(b)
	}
	userRun = strings.TrimSpace(userRun)
	if userRun == "" {
		fmt.Fprintf(os.Stderr, "gohon-serve: empty run\n")
		os.Exit(2)
	}

	// Wrap user run(req,res) so Go only passes req_id + meta JSON.
	run := wrapHTTPRun(userRun)

	sessions := &sessionStore{}

	setup := func(r *runtime.Runtime) error {
		if err := stdlib.Install(r, stdlib.Options{Console: true, Fetch: true}); err != nil {
			return err
		}
		r.InjectHost("httpWrite", sessions.hostWrite)
		return r.Eval(`globalThis.sleep = function(ms) {
  return new Promise(function(resolve) { setTimeout(resolve, ms); });
};`)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p, err := pool.New(ctx, run, *workers, setup)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gohon-serve: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		cancel()
		<-p.Done()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
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
			fmt.Fprintf(os.Stderr, "gohon-serve: run: %v\n", err)
		}
	})

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	fmt.Fprintf(os.Stderr, "gohon-serve: listening on %s (pool=%d)\n", *addr, *workers)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "gohon-serve: %v\n", err)
		os.Exit(1)
	}
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
	// reqId (number) + metaJSON (string) → semantic req/res for user run.
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
      __hon_host("httpWrite", JSON.stringify({
        id: reqId,
        status: this.statusCode|0,
        body: chunk === undefined || chunk === null ? "" : String(chunk),
        end: false
      }));
    },
    end: async function(chunk) {
      __hon_host("httpWrite", JSON.stringify({
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
