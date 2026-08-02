package stdlib

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"goqjs/runtime"
)

// Options selects which standard host APIs to install.
type Options struct {
	Console bool
	Fetch   bool

	// Log is the sink for console.log (default os.Stdout).
	Log io.Writer
	// Client is used by fetch (default http.DefaultClient).
	Client *http.Client
}

// Install injects selected stdlib APIs. Must be called before the first Run.
func Install(r *runtime.Runtime, opt Options) error {
	if opt.Console {
		if err := installConsole(r, opt.Log); err != nil {
			return err
		}
	}
	if opt.Fetch {
		if err := installFetch(r, opt.Client); err != nil {
			return err
		}
	}
	return nil
}

func installConsole(r *runtime.Runtime, w io.Writer) error {
	if w == nil {
		w = os.Stdout
	}
	r.InjectHost("consoleLog", func(payload string) (string, error) {
		var args []any
		if err := json.Unmarshal([]byte(payload), &args); err != nil {
			var s string
			if err2 := json.Unmarshal([]byte(payload), &s); err2 != nil {
				return "", err
			}
			args = []any{s}
		}
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = fmt.Sprint(a)
		}
		line := strings.Join(parts, " ")
		if _, err := fmt.Fprintln(w, line); err != nil {
			return "", err
		}
		if f, ok := w.(*os.File); ok {
			_ = f.Sync()
		}
		return "", nil
	})
	return r.Eval(`
globalThis.console = globalThis.console || {};
globalThis.console.log = function() {
  var args = Array.prototype.slice.call(arguments);
  __goqjs_host("consoleLog", JSON.stringify(args));
};
`)
}

func installFetch(r *runtime.Runtime, client *http.Client) error {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	r.InjectAsyncHost("fetch", func(payload string, complete func(ok bool, result string)) {
		var req struct {
			URL    string `json:"url"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal([]byte(payload), &req); err != nil {
			complete(false, err.Error())
			return
		}
		if req.URL == "" {
			complete(false, "fetch: missing url")
			return
		}
		if req.Method == "" {
			req.Method = http.MethodGet
		}
		go func() {
			httpReq, err := http.NewRequest(req.Method, req.URL, nil)
			if err != nil {
				complete(false, err.Error())
				return
			}
			resp, err := client.Do(httpReq)
			if err != nil {
				complete(false, err.Error())
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			if err != nil {
				complete(false, err.Error())
				return
			}
			out, err := json.Marshal(map[string]any{
				"status":     resp.StatusCode,
				"statusText": resp.Status,
				"ok":         resp.StatusCode >= 200 && resp.StatusCode < 300,
				"url":        resp.Request.URL.String(),
				"body":       string(body),
			})
			if err != nil {
				complete(false, err.Error())
				return
			}
			complete(true, string(out))
		}()
	})
	return r.Eval(`
globalThis.fetch = function(input, init) {
  var url = input;
  var method = "GET";
  if (input && typeof input === "object" && input.url) url = input.url;
  if (init && init.method) method = String(init.method);
  return __goqjs_async("fetch", { url: String(url), method: method }).then(function(res) {
    return {
      ok: !!res.ok,
      status: res.status|0,
      statusText: String(res.statusText || ""),
      url: String(res.url || ""),
      text: function() { return Promise.resolve(String(res.body || "")); },
      json: function() { return Promise.resolve(JSON.parse(String(res.body || "null"))); }
    };
  });
};
`)
}
