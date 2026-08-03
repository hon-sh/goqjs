package hnapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// Syncer pulls HN Firebase data into a Store.
type Syncer struct {
	store  *Store
	client *http.Client
	base   string
	cfg    Config
}

// NewSyncer builds a Syncer bound to store. cfg.HTTPClient is used when set.
func NewSyncer(store *Store, cfg Config) *Syncer {
	cfg = cfg.withDefaults()
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: cfg.HTTPTimeout}
	}
	return &Syncer{
		store:  store,
		client: client,
		base:   cfg.FirebaseBase,
		cfg:    cfg,
	}
}

// SyncTopList fetches topstories IDs + story items and refreshes story_lists.
// It does not pull comment trees. Returns the ordered ids written to the list.
func (sy *Syncer) SyncTopList(ctx context.Context, limit int) ([]int64, error) {
	if limit < 1 {
		limit = sy.cfg.DefaultLimit
	}

	ids, err := sy.fetchTopIDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}

	items, err := sy.fetchItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it == nil {
			continue
		}
		if err := sy.store.UpsertItem(ctx, it, nil, nil, -1); err != nil {
			return nil, err
		}
	}
	if err := sy.store.SetTopList(ctx, ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// WarmTopStories runs SyncItemTree for each id (bounded concurrency).
func (sy *Syncer) WarmTopStories(ctx context.Context, ids []int64, depth int) error {
	if depth <= 0 || len(ids) == 0 {
		return nil
	}

	sem := make(chan struct{}, sy.cfg.FetchConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			if err := sy.SyncItemTree(ctx, id, depth); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// SyncTop fetches the top list then optionally warms each story's comment tree.
func (sy *Syncer) SyncTop(ctx context.Context, limit, depth int) error {
	if depth < 0 {
		depth = sy.cfg.DefaultDepth
	}
	ids, err := sy.SyncTopList(ctx, limit)
	if err != nil {
		return err
	}
	return sy.WarmTopStories(ctx, ids, depth)
}

// SyncItemTree fetches id and descendants up to depth comment levels.
func (sy *Syncer) SyncItemTree(ctx context.Context, id int64, depth int) error {
	if depth < 0 {
		depth = 0
	}
	root, err := sy.fetchItem(ctx, id)
	if err != nil {
		return err
	}
	if root == nil {
		return nil
	}
	if err := sy.store.UpsertItem(ctx, root, nil, nil, depth); err != nil {
		return err
	}

	type node struct {
		item  *firebaseItem
		level int // comment level under root; kids of root are level 1
	}
	queue := []node{{item: root, level: 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.level >= depth {
			continue
		}
		kids := cur.item.Kids
		if len(kids) > sy.cfg.PerLevel {
			kids = kids[:sy.cfg.PerLevel]
		}
		if len(kids) == 0 {
			continue
		}
		fetched, err := sy.fetchItems(ctx, kids)
		if err != nil {
			return err
		}
		parentID := cur.item.ID
		for i, kid := range fetched {
			if kid == nil || kid.Deleted || kid.Dead {
				continue
			}
			pos := i
			pid := parentID
			if err := sy.store.UpsertItem(ctx, kid, &pid, &pos, -1); err != nil {
				return err
			}
			queue = append(queue, node{item: kid, level: cur.level + 1})
		}
	}
	return sy.store.SetSyncedDepth(ctx, id, depth)
}

func (sy *Syncer) fetchTopIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	if err := sy.getJSON(ctx, "/topstories.json", &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (sy *Syncer) fetchItem(ctx context.Context, id int64) (*firebaseItem, error) {
	var it *firebaseItem
	if err := sy.getJSON(ctx, fmt.Sprintf("/item/%d.json", id), &it); err != nil {
		return nil, err
	}
	return it, nil
}

func (sy *Syncer) fetchItems(ctx context.Context, ids []int64) ([]*firebaseItem, error) {
	out := make([]*firebaseItem, len(ids))
	sem := make(chan struct{}, sy.cfg.FetchConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, id := range ids {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			it, err := sy.fetchItem(ctx, id)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			out[i] = it
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (sy *Syncer) getJSON(ctx context.Context, path string, dest interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sy.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := sy.client.Do(req)
	if err != nil {
		return fmt.Errorf("hnapi sync %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hnapi sync %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("hnapi sync %s: decode: %w", path, err)
	}
	return nil
}
