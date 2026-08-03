package hnapi

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"crawshaw.io/sqlite"
	"crawshaw.io/sqlite/sqlitex"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS items (
  id INTEGER PRIMARY KEY,
  type TEXT NOT NULL,
  by TEXT,
  time INTEGER,
  title TEXT,
  url TEXT,
  text TEXT,
  score INTEGER,
  descendants INTEGER,
  parent_id INTEGER REFERENCES items(id),
  child_pos INTEGER,
  dead INTEGER NOT NULL DEFAULT 0,
  deleted INTEGER NOT NULL DEFAULT 0,
  synced_depth INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS items_parent ON items(parent_id);

CREATE TABLE IF NOT EXISTS story_lists (
  list TEXT NOT NULL,
  pos INTEGER NOT NULL,
  item_id INTEGER NOT NULL REFERENCES items(id),
  PRIMARY KEY (list, pos)
);

CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

const metaTopSyncedAt = "top_synced_at"

// Store is a SQLite-backed HN read model.
type Store struct {
	pool *sqlitex.Pool
	cfg  Config
}

// Open opens the database pool and migrates schema.
func Open(cfg Config) (*Store, error) {
	cfg = cfg.withDefaults()
	pool, err := sqlitex.Open(cfg.DB, 0, cfg.PoolSize)
	if err != nil {
		return nil, fmt.Errorf("hnapi: open db: %w", err)
	}
	s := &Store{pool: pool, cfg: cfg}
	conn := pool.Get(context.Background())
	if conn == nil {
		_ = pool.Close()
		return nil, fmt.Errorf("hnapi: get conn for migrate")
	}
	defer pool.Put(conn)
	if err := sqlitex.ExecScript(conn, schemaSQL); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("hnapi: migrate: %w", err)
	}
	return s, nil
}

// Close closes the connection pool.
func (s *Store) Close() error {
	if s == nil || s.pool == nil {
		return nil
	}
	return s.pool.Close()
}

// Config returns a copy of the store config.
func (s *Store) Config() Config { return s.cfg }

func (s *Store) withConn(ctx context.Context, fn func(*sqlite.Conn) error) error {
	conn := s.pool.Get(ctx)
	if conn == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("hnapi: no db connection")
	}
	defer s.pool.Put(conn)
	return fn(conn)
}

// UpsertItem writes or replaces an item row.
// parentID/childPos may be nil; syncedDepth < 0 leaves synced_depth unchanged on update
// (new rows get 0).
func (s *Store) UpsertItem(ctx context.Context, it *firebaseItem, parentID *int64, childPos *int, syncedDepth int) error {
	return s.withConn(ctx, func(conn *sqlite.Conn) error {
		return upsertItemConn(conn, it, parentID, childPos, syncedDepth)
	})
}

func upsertItemConn(conn *sqlite.Conn, it *firebaseItem, parentID *int64, childPos *int, syncedDepth int) error {
	if it == nil || it.ID == 0 {
		return nil
	}
	typ := it.Type
	if typ == "" {
		typ = "story"
	}
	dead, deleted := 0, 0
	if it.Dead {
		dead = 1
	}
	if it.Deleted {
		deleted = 1
	}

	var parentArg interface{}
	if parentID != nil {
		parentArg = *parentID
	} else if it.Parent != 0 {
		parentArg = it.Parent
	}

	var posArg interface{}
	if childPos != nil {
		posArg = *childPos
	}

	sd := syncedDepth
	if sd < 0 {
		sd = 0
	}

	err := sqlitex.Exec(conn, `
INSERT INTO items (id, type, by, time, title, url, text, score, descendants, parent_id, child_pos, dead, deleted, synced_depth)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  type=excluded.type,
  by=excluded.by,
  time=excluded.time,
  title=excluded.title,
  url=excluded.url,
  text=excluded.text,
  score=excluded.score,
  descendants=excluded.descendants,
  parent_id=COALESCE(excluded.parent_id, items.parent_id),
  child_pos=COALESCE(excluded.child_pos, items.child_pos),
  dead=excluded.dead,
  deleted=excluded.deleted,
  synced_depth=CASE
    WHEN ? < 0 THEN items.synced_depth
    WHEN excluded.synced_depth > items.synced_depth THEN excluded.synced_depth
    ELSE items.synced_depth
  END
`, nil,
		it.ID, typ, it.By, it.Time, it.Title, it.URL, it.Text, it.Score, it.Descendants,
		parentArg, posArg, dead, deleted, sd,
		syncedDepth,
	)
	if err != nil {
		return fmt.Errorf("upsert item %d: %w", it.ID, err)
	}
	return nil
}

// SetTopList replaces the 'top' story list with the given ordered item IDs.
func (s *Store) SetTopList(ctx context.Context, ids []int64) error {
	return s.withConn(ctx, func(conn *sqlite.Conn) error {
		if err := sqlitex.Exec(conn, `DELETE FROM story_lists WHERE list = 'top'`, nil); err != nil {
			return err
		}
		for i, id := range ids {
			if err := sqlitex.Exec(conn,
				`INSERT INTO story_lists (list, pos, item_id) VALUES ('top', ?, ?)`,
				nil, i, id); err != nil {
				return err
			}
		}
		return setMetaConn(conn, metaTopSyncedAt, strconv.FormatInt(time.Now().Unix(), 10))
	})
}

func setMetaConn(conn *sqlite.Conn, key, value string) error {
	return sqlitex.Exec(conn,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		nil, key, value)
}

// TopListFreshness is the cache state of the top story list.
type TopListFreshness int

const (
	// TopListFresh: non-empty and within TopTTL.
	TopListFresh TopListFreshness = iota
	// TopListEmpty: no rows in story_lists (cold start).
	TopListEmpty
	// TopListExpired: has rows but past TopTTL.
	TopListExpired
)

// TopListStatus reports whether the top list is empty, expired, or fresh.
func (s *Store) TopListStatus(ctx context.Context) (TopListFreshness, error) {
	var status TopListFreshness = TopListFresh
	err := s.withConn(ctx, func(conn *sqlite.Conn) error {
		var n int64
		err := sqlitex.Exec(conn, `SELECT COUNT(*) FROM story_lists WHERE list = 'top'`,
			func(stmt *sqlite.Stmt) error {
				n = stmt.ColumnInt64(0)
				return nil
			})
		if err != nil {
			return err
		}
		if n == 0 {
			status = TopListEmpty
			return nil
		}
		var raw string
		err = sqlitex.Exec(conn, `SELECT value FROM meta WHERE key = ?`,
			func(stmt *sqlite.Stmt) error {
				raw = stmt.ColumnText(0)
				return nil
			}, metaTopSyncedAt)
		if err != nil {
			return err
		}
		if raw == "" {
			status = TopListExpired
			return nil
		}
		ts, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			status = TopListExpired
			return nil
		}
		if time.Since(time.Unix(ts, 0)) > s.cfg.TopTTL {
			status = TopListExpired
		}
		return nil
	})
	return status, err
}

// TopListStale is true when the list is empty or past TopTTL.
func (s *Store) TopListStale(ctx context.Context) (bool, error) {
	st, err := s.TopListStatus(ctx)
	return st != TopListFresh, err
}

// SyncedDepth returns the stored synced_depth for id, or -1 if missing.
func (s *Store) SyncedDepth(ctx context.Context, id int64) (int, error) {
	depth := -1
	err := s.withConn(ctx, func(conn *sqlite.Conn) error {
		found := false
		err := sqlitex.Exec(conn, `SELECT synced_depth FROM items WHERE id = ?`,
			func(stmt *sqlite.Stmt) error {
				depth = int(stmt.ColumnInt64(0))
				found = true
				return nil
			}, id)
		if err != nil {
			return err
		}
		if !found {
			depth = -1
		}
		return nil
	})
	return depth, err
}

// SetSyncedDepth sets synced_depth = max(existing, depth) for id.
func (s *Store) SetSyncedDepth(ctx context.Context, id int64, depth int) error {
	return s.withConn(ctx, func(conn *sqlite.Conn) error {
		return sqlitex.Exec(conn, `
UPDATE items SET synced_depth = CASE
  WHEN synced_depth > ? THEN synced_depth ELSE ? END
WHERE id = ?`, nil, depth, depth, id)
	})
}

// GetTopStories returns hydrated story items for the top list (no nested comments).
func (s *Store) GetTopStories(ctx context.Context, limit int) ([]Item, error) {
	if limit < 1 {
		limit = s.cfg.DefaultLimit
	}
	var out []Item
	err := s.withConn(ctx, func(conn *sqlite.Conn) error {
		return sqlitex.Exec(conn, `
SELECT i.id, i.type, i.by, i.time, i.title, i.url, i.text, i.score, i.descendants,
       i.parent_id, i.dead, i.deleted
FROM story_lists s
JOIN items i ON i.id = s.item_id
WHERE s.list = 'top'
ORDER BY s.pos
LIMIT ?`,
			func(stmt *sqlite.Stmt) error {
				it := scanItemCols(stmt)
				it.Comments = []*Item{}
				out = append(out, it)
				return nil
			}, limit)
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []Item{}
	}
	return out, nil
}

type treeRow struct {
	Item
	ParentID  int64
	HasParent bool
	Depth     int
}

// GetItemTree loads root id and nested comments up to depth (comment levels).
// depth=2 means two levels of comments. Returns nil, nil if root missing.
func (s *Store) GetItemTree(ctx context.Context, id int64, depth int) (*Item, error) {
	if depth < 0 {
		depth = 0
	}
	var rows []treeRow
	err := s.withConn(ctx, func(conn *sqlite.Conn) error {
		return sqlitex.Exec(conn, `
WITH RECURSIVE tree AS (
  SELECT id, type, by, time, title, url, text, score, descendants,
         parent_id, child_pos, dead, deleted, 0 AS depth
  FROM items
  WHERE id = ?

  UNION ALL

  SELECT i.id, i.type, i.by, i.time, i.title, i.url, i.text, i.score, i.descendants,
         i.parent_id, i.child_pos, i.dead, i.deleted, t.depth + 1
  FROM items i
  JOIN tree t ON i.parent_id = t.id
  WHERE t.depth < ?
    AND i.deleted = 0 AND i.dead = 0
)
SELECT id, type, by, time, title, url, text, score, descendants,
       parent_id, dead, deleted, depth, child_pos
FROM tree
ORDER BY depth, child_pos, id
`,
			func(stmt *sqlite.Stmt) error {
				it := scanItemCols(stmt)
				r := treeRow{
					Item:  it,
					Depth: int(stmt.ColumnInt64(12)),
				}
				if stmt.ColumnType(9) != sqlite.SQLITE_NULL {
					r.ParentID = stmt.ColumnInt64(9)
					r.HasParent = true
				}
				rows = append(rows, r)
				return nil
			}, id, depth)
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}

	byID := make(map[int64]*Item, len(rows))
	for i := range rows {
		rows[i].Comments = []*Item{}
		byID[rows[i].ID] = &rows[i].Item
	}
	var root *Item
	for i := range rows {
		r := &rows[i]
		node := byID[r.ID]
		if r.Depth == 0 {
			root = node
			continue
		}
		if !r.HasParent {
			continue
		}
		parent := byID[r.ParentID]
		if parent == nil {
			continue
		}
		parent.Comments = append(parent.Comments, node)
	}
	if root != nil && root.Comments == nil {
		root.Comments = []*Item{}
	}
	return root, nil
}

// Columns: id, type, by, time, title, url, text, score, descendants, parent_id, dead, deleted
func scanItemCols(stmt *sqlite.Stmt) Item {
	it := Item{
		ID:          stmt.ColumnInt64(0),
		Type:        stmt.ColumnText(1),
		By:          stmt.ColumnText(2),
		Time:        stmt.ColumnInt64(3),
		Title:       stmt.ColumnText(4),
		URL:         stmt.ColumnText(5),
		Text:        stmt.ColumnText(6),
		Score:       int(stmt.ColumnInt64(7)),
		Descendants: int(stmt.ColumnInt64(8)),
		Dead:        stmt.ColumnInt64(10) != 0,
		Deleted:     stmt.ColumnInt64(11) != 0,
		Comments:    []*Item{},
	}
	if stmt.ColumnType(9) != sqlite.SQLITE_NULL {
		p := stmt.ColumnInt64(9)
		if p != 0 {
			it.Parent = &p
		}
	}
	return it
}
