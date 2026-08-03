package hnapi

import (
	"context"
	"testing"
)

func TestGetItemTreeDepthAndNesting(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DB = "file:test_tree?mode=memory&cache=shared"
	cfg.PoolSize = 2
	store, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	// story 1
	//   comment 2
	//     comment 3
	//       comment 4  (depth 3 — beyond maxDepth=2)
	//   comment 5
	//   comment 6 (deleted — skipped in tree)
	fixtures := []struct {
		it       firebaseItem
		parent   *int64
		pos      *int
		syncDepth int
	}{
		{firebaseItem{ID: 1, Type: "story", By: "a", Title: "Root", Score: 10, Descendants: 4}, nil, nil, 2},
		{firebaseItem{ID: 2, Type: "comment", By: "b", Text: "c2", Parent: 1}, int64Ptr(1), intPtr(0), -1},
		{firebaseItem{ID: 3, Type: "comment", By: "c", Text: "c3", Parent: 2}, int64Ptr(2), intPtr(0), -1},
		{firebaseItem{ID: 4, Type: "comment", By: "d", Text: "c4", Parent: 3}, int64Ptr(3), intPtr(0), -1},
		{firebaseItem{ID: 5, Type: "comment", By: "e", Text: "c5", Parent: 1}, int64Ptr(1), intPtr(1), -1},
		{firebaseItem{ID: 6, Type: "comment", By: "f", Text: "gone", Parent: 1, Deleted: true}, int64Ptr(1), intPtr(2), -1},
	}
	for _, f := range fixtures {
		f := f
		if err := store.UpsertItem(ctx, &f.it, f.parent, f.pos, f.syncDepth); err != nil {
			t.Fatalf("upsert %d: %v", f.it.ID, err)
		}
	}

	root, err := store.GetItemTree(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if root == nil {
		t.Fatal("expected root")
	}
	if root.Title != "Root" {
		t.Fatalf("title=%q", root.Title)
	}
	if len(root.Comments) != 2 {
		t.Fatalf("top comments=%d want 2 (deleted filtered)", len(root.Comments))
	}
	if root.Comments[0].ID != 2 || root.Comments[1].ID != 5 {
		t.Fatalf("order: %#v %#v", root.Comments[0].ID, root.Comments[1].ID)
	}
	if len(root.Comments[0].Comments) != 1 || root.Comments[0].Comments[0].ID != 3 {
		t.Fatalf("level-2 under 2: %+v", root.Comments[0].Comments)
	}
	// depth=2 includes comment 3 (level 2) but not comment 4 (level 3)
	if len(root.Comments[0].Comments[0].Comments) != 0 {
		t.Fatalf("level-3 should be empty at depth=2, got %d", len(root.Comments[0].Comments[0].Comments))
	}

	shallow, err := store.GetItemTree(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if shallow == nil || len(shallow.Comments) != 0 {
		t.Fatalf("depth=0 should have no comments, got %+v", shallow)
	}

	missing, err := store.GetItemTree(ctx, 999, 2)
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatal("expected nil for missing id")
	}
}

func TestGetTopStories(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DB = "file:test_top?mode=memory&cache=shared"
	cfg.PoolSize = 2
	store, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	for _, id := range []int64{10, 20, 30} {
		it := &firebaseItem{ID: id, Type: "story", Title: "S", Score: int(id)}
		if err := store.UpsertItem(ctx, it, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetTopList(ctx, []int64{30, 10, 20}); err != nil {
		t.Fatal(err)
	}

	stories, err := store.GetTopStories(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(stories) != 2 || stories[0].ID != 30 || stories[1].ID != 10 {
		t.Fatalf("got %+v", stories)
	}

	stale, err := store.TopListStale(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stale {
		t.Fatal("fresh list should not be stale")
	}
	st, err := store.TopListStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != TopListFresh {
		t.Fatalf("status=%v want Fresh", st)
	}
}

func int64Ptr(v int64) *int64 { return &v }
func intPtr(v int) *int       { return &v }
