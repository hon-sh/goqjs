package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"goqjs/examples/hn-ssr/hnapi"
)

// topSyncGate coalesces work: at most one in flight; waiters share its result.
type topSyncGate struct {
	mu      sync.Mutex
	running bool
	waiters []chan error
}

// do runs syncFn if idle. If wait, blocks until the (current or new) sync finishes.
// If !wait and a sync is already running, returns immediately.
func (g *topSyncGate) do(wait bool, label string, syncFn func() error) error {
	g.mu.Lock()
	if g.running {
		if !wait {
			g.mu.Unlock()
			return nil
		}
		ch := make(chan error, 1)
		g.waiters = append(g.waiters, ch)
		g.mu.Unlock()
		return <-ch
	}
	g.running = true
	g.mu.Unlock()

	finish := func(err error) {
		g.mu.Lock()
		waiters := g.waiters
		g.waiters = nil
		g.running = false
		g.mu.Unlock()
		for _, ch := range waiters {
			ch <- err
		}
	}

	if wait {
		err := syncFn()
		finish(err)
		return err
	}
	go func() {
		err := syncFn()
		if err != nil {
			fmt.Fprintf(os.Stderr, "hn-ssr: background %s: %v\n", label, err)
		}
		finish(err)
	}()
	return nil
}

// registerHNAPI mounts business read endpoints backed by Store + Syncer.
func registerHNAPI(mux *http.ServeMux, store *hnapi.Store, syncer *hnapi.Syncer) {
	cfg := store.Config()
	var listGate, warmGate topSyncGate

	mux.HandleFunc("GET /api/topstories", func(w http.ResponseWriter, r *http.Request) {
		limit := cfg.DefaultLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				http.Error(w, "bad limit", http.StatusBadRequest)
				return
			}
			if n > 500 {
				n = 500
			}
			limit = n
		}

		ctx := r.Context()
		status, err := store.TopListStatus(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		bg := context.Background()
		// List phase only: waiters (empty) unblock after SetTopList.
		// Comment-tree warmup is always scheduled after a successful list sync.
		listThenWarm := func() error {
			ids, err := syncer.SyncTopList(bg, limit)
			if err != nil {
				return err
			}
			idsCopy := append([]int64(nil), ids...)
			_ = warmGate.do(false, "WarmTopStories", func() error {
				return syncer.WarmTopStories(bg, idsCopy, cfg.DefaultDepth)
			})
			return nil
		}

		switch status {
		case hnapi.TopListEmpty:
			if err := listGate.do(true, "SyncTopList", listThenWarm); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		case hnapi.TopListExpired:
			_ = listGate.do(false, "SyncTopList", listThenWarm)
		}

		stories, err := store.GetTopStories(ctx, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, stories)
	})

	mux.HandleFunc("GET /api/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		depth := cfg.DefaultDepth
		if v := r.URL.Query().Get("depth"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				http.Error(w, "bad depth", http.StatusBadRequest)
				return
			}
			depth = n
		}

		ctx := r.Context()
		synced, err := store.SyncedDepth(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if synced < depth {
			if err := syncer.SyncItemTree(ctx, id, depth); err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
		}
		item, err := store.GetItemTree(ctx, id, depth)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if item == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, item)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// listenLoopbackBase builds an absolute origin for SSR fetch back into this process.
func listenLoopbackBase(addr string) string {
	host := addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	} else if strings.HasPrefix(host, "0.0.0.0:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "0.0.0.0:")
	} else if strings.HasPrefix(host, "[::]:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "[::]:")
	}
	return "http://" + host
}
