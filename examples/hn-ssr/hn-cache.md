# HN 本地数据：SQLite + recursive CTE

> 给后续 agent / 人类：在 `examples/hn-ssr` 之上，用 **SQLite 邻接表 + `WITH RECURSIVE`** 替代 Firebase 扇出拉取，作为本地 HN 读模型。  
> 本文是设计结论与落地边界；实现以后续 PR 为准。  
> 相关现状：`examples/hn-ssr/`（Vite React SSR + `main.go` goqjs 宿主）。

---

## 1. 背景与动机

### 1.1 当前 hn-ssr 读路径

```text
HTTP → Go → goqjs Run
         → JS loadData(url)
              → 多次 fetch(https://hacker-news.firebaseio.com/v0/…)
         → renderToString
         → 拼 HTML（可选 hydrate）
```

| 页面 | 聚合 | 约略请求数 |
|------|------|-----------|
| `/` | `topstories` + 每条 `item/:id` | ~1 + 30 |
| `/item/:id` | 根 item + `kids` 递归（depth ≤ 2，每层最多 40） | 可达几十次 |

公网 [HN Firebase API](https://github.com/HackerNews/API) 里 **item 是泛类型**（story / comment / job / poll / …）。父子靠 `kids[]`（子 id）与 `parent`；**不内嵌评论体**，一级评论也要再 `GET item/:id`。

### 1.2 已有 Go HTTP cache 的局限

`main.go -cache`：对 `GET https://hacker-news.firebaseio.com/v0…` 做 FIFO（≤100 URL）的 `RoundTripper` 缓存。

- **省的是**：出网与 Firebase 延迟。  
- **省不掉的是**：每一次 JS `fetch` 仍跨 **QJS ↔ cgo ↔ Go** 异步桥（次数 ≈ 扇出次数）。  
- 二次访问首页 load 会降，但仍可能是 ~30 次边界穿越。

### 1.3 目标

把「按页需要的数据」变成：

```text
Go（或 SQLite actor）一次查询 → 整页 JSON
→ 注入 JS / 或仅 Go 拼 props → renderToString
```

理想：**每 SSR 请求 O(1) 次数据获取**（相对边界），树展开在 SQLite 内完成。

---

## 2. 数据模型（HN item → 关系表）

### 2.1 概念

- **item**：统一实体；用 `type` 区分。  
- **边**：评论树 / 列表关系用邻接表表达（`parent_id`），不必存 JSON `kids` 数组作权威来源（同步时可由 `kids` 展开成行，或只存 parent）。  
- **列表**：`topstories` 等是有序 id 列表，单独表或物化。

### 2.2 建议 schema（草案）

```sql
-- 权威 item 行（字段按需裁剪，对齐 Firebase item JSON）
CREATE TABLE items (
  id          INTEGER PRIMARY KEY,
  type        TEXT NOT NULL,          -- story|comment|job|poll|pollopt
  by          TEXT,
  time        INTEGER,
  title       TEXT,
  url         TEXT,
  text        TEXT,
  score       INTEGER,
  descendants INTEGER,
  parent_id   INTEGER REFERENCES items(id),  -- comment 等
  dead        INTEGER NOT NULL DEFAULT 0,
  deleted     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX items_parent ON items(parent_id);
CREATE INDEX items_type_time ON items(type, time DESC);

-- 榜单（首页）
CREATE TABLE story_lists (
  list    TEXT NOT NULL,              -- 'top' | 'new' | …
  pos     INTEGER NOT NULL,
  item_id INTEGER NOT NULL REFERENCES items(id),
  PRIMARY KEY (list, pos)
);
```

说明：

- 用 **`parent_id` 邻接表** 表达树；读路径不依赖运行时递归 HTTP。  
- Firebase 的 `kids` 顺序若要保留，可加 `child_pos` 或同步时按 kids 下标写入辅助表；demo 可先按 `id`/`time` 排。  
- 不必上 GraphQL。

### 2.3 与公网 API / HN 官网的关系

| | HN 官网 | Firebase 公网 API | 本方案 SQLite |
|--|--|--|--|
| 角色 | 生产读模型 | 第三方只读镜像 | **本地读模型 / 缓存库** |
| 取树 | 服务端一次聚合 | 客户端扇出 `item` | **CTE 一次取出** |

本地库的数据来源可以是：定期从 Firebase 同步、导入样本集（README 曾提 ClickHouse HN sample 等）、或手工 fixture。同步策略另开任务；本文聚焦 **存储与查询**。

---

## 3. 查询：recursive CTE

### 3.1 详情页：根 item + 深度受限子树

目标对齐当前 JS：`getItemWithComments(id, maxDepth=2)`，但 **一条 SQL**。

示意（深度用 CTE 累加；过滤 deleted/dead；限制每层扇出可在应用层再切，或 SQL 窗口函数——demo 可先不做每层 40 封顶）：

```sql
WITH RECURSIVE tree AS (
  SELECT id, type, by, time, title, url, text, score, descendants,
         parent_id, dead, deleted, 0 AS depth
  FROM items
  WHERE id = :root_id

  UNION ALL

  SELECT i.id, i.type, i.by, i.time, i.title, i.url, i.text, i.score, i.descendants,
         i.parent_id, i.dead, i.deleted, t.depth + 1
  FROM items i
  JOIN tree t ON i.parent_id = t.id
  WHERE t.depth < :max_depth
    AND i.deleted = 0 AND i.dead = 0
)
SELECT * FROM tree ORDER BY depth, id;
```

Go 侧把平坦行集 **装配成** 与现前端兼容的嵌套结构（`item.comments[]` 递归），供 `App` / `loadData` 形状使用，减少 UI 改动。

### 3.2 首页：top N stories

```sql
SELECT i.*
FROM story_lists s
JOIN items i ON i.id = s.item_id
WHERE s.list = 'top'
ORDER BY s.pos
LIMIT :n;
```

无需 CTE。

### 3.3 为何优于「KV / 应用层递归 Get」

| | SQLite CTE | KV + Go/JS 递归 |
|--|--|--|
| 往返 | 1 次查询 | 每节点 ≥1 次 Get / fetch |
| 解析 | 行扫描 | 反复反序列化 |
| 深度/剪枝 | SQL 内 | 手写易拉爆 |
| 与索引 | `parent_id` B-Tree | 难用地道树查询 |

朴素 KV 递归很难追上 CTE；要对齐需 batch/`MGET`、物化路径或闭包表，复杂度接近自建小型查询引擎。  
**结论：树遍历交给 CTE；KV 只适合整页/单 item 缓存，不当树引擎。**

---

## 4. Go 进程内架构

### 4.1 与 goqjs 的类比（可选 actor）

主流 Go SQLite 绑定是细粒度 API（`Query`/`Exec`），但 **写连接宜串行**。可做成与 goqjs 同构的 **req-resp 小服务**（同进程即可，不必独立 OS 进程）：

```text
业务 / SSR ──req{首页|详情 id, depth}──► SQLite actor（独占写 Conn 或串行队列）
业务 / SSR ◄─resp{JSON 页数据}──────────┘
```

- 读多可 WAL + 只读连接；demo 单连接串行也够。  
- 绑定选型（实现阶段再定）：`modernc.org/sqlite`（纯 Go）或 `mattn/go-sqlite3`（cgo）等——**无强制**与 goqjs 一样上「独立 loop」，channel actor 足够。

### 4.2 与 hn-ssr / goqjs 的衔接（推荐方向）

优先减少 **JS↔Go 边界次数**，而不是继续在 QJS 里 `fetch` 扇出：

**推荐 A（改动面小）**

```text
Go HTTP handler
  → SQLite 查页 data（CTE）
  → pool.Run 只做 render：传入 data，或 Eval 前注入
  → 或：Run(url) 但 JS 的 loadData 改为调 host「getPage(url)」一次
```

**推荐 B（更干净）**

```text
Go 查 data → 仅把 data 塞进模板所需的 render 路径
JS 不再负责 HN 聚合（loadData 只用于 Node dev，或读同形 JSON）
```

**不推荐**：继续 JS 递归 fetch，仅把 SQLite 当 Firebase 的 HTTP 替身却仍 N 次 host 调用——边界次数问题仍在。

现有 `-cache`（Firebase RoundTripper）在切到 SQLite 后变为次要或可删；HTTP `Cache-Control`（assets 1d / home 1m / item 3m）与读模型无关，可保留。

### 4.3 和「整页 HTML 缓存」的分工

| 层 | 作用 |
|----|------|
| SQLite CTE | 权威/半权威读模型，按 id/列表查询 |
| 可选整页 data/HTML 缓存 | 热点 URL 再省一次 SQL + render |
| Firebase FIFO | 迁移期或同步器用；不是终态读路径 |

---

## 5. 同步（入库）概要

非本文实现重点，但设计需预留：

1. **Bootstrap**：下载/导入一批 items + topstories → 填 `items` / `story_lists`。  
2. **增量**：轮询 Firebase `maxitem` / 列表，upsert；评论 `parent_id` 从 payload 写入。  
3. **一致性**：demo 可接受短暂落后；不追求与官网实时一致。

同步应在 **Go** 做（或独立 cmd），不要在 QJS 里跑导入。

---

## 6. 非目标

- 复制 HN 写路径、账号、投票。  
- GraphQL、完整 kids 顺序兼容（除非 UI 强依赖）。  
- 用 KV 手写递归替代 CTE。  
- 把 SQLite 做成独立网络进程（除非日后有多语言共享需求）。

---

## 7. 建议落地顺序（给下一 session）

1. 新建包（例如 `examples/hn-ssr/hnstore` 或 `internal/hnstore`）：schema + migrate + `GetTopStories` / `GetItemTree(id, depth)`。  
2. 用 fixture 或小同步脚本灌一批数据；单测 CTE 深度与嵌套装配。  
3. 改 `hn-ssr`：`loadData` 改为一次 host / 或 Go 侧注入 data；去掉详情/首页 Firebase 扇出。  
4. 文档：更新 `examples/hn-ssr/README.md`；Firebase `-cache` 标为可选/废弃。  
5. （可选）SQLite actor 包装写与查询队列。

---

## 8. 一句话

**HN 公网 API 的 kids 扇出不适合当 SSR 读模型；本地用 SQLite 邻接表 + recursive CTE 一次取树，Go 装配页 JSON，再交给 goqjs 只做（或主要做）render——把成本从「N 次 QJS↔Go fetch」降到「1 次查询 + 1 次渲染」。**
