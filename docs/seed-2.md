# goqjs — seed-2（宿主扩展与 stdlib）

> 承接 `docs/seed-1.md` 的引擎结论；本文记录 **Runtime 变薄、Inject 闸门、stdlib、cmd 分工** 的产品决策。  
> 实现以代码为准；本文避免再踩「能力焊死在 C boot / 全局 resp」的坑。

---

## 1. 目标分层

```text
runtime     引擎宿主：loop、invoke、Inject 闸门、平台 timer 暴露
stdlib      可复用配方：console.log、fetch（可跳过 / 可替换）
pool        N 个 Runtime 编排（round-robin）
cmd/goqjs   CLI：Install stdlib + 可选 sleep 糖
cmd/goqjs-serve  HTTP：Install stdlib + 每请求 req/res（req_id 定位）
嵌入用户    New → Install 或自写 Inject → Run（或用 pool）
```

- **CPU**（fib、render）：留在 JS / QJS 线程。  
- **Timer**：QJS `os.setTimeout` → 挂到 `globalThis`；JS 用 `Promise + setTimeout`。  
- **真 IO**（log、fetch、HTTP 响应体）：Go 实现，经 host 桥回灌 loop。

---

## 2. Boot 在 Go，不在 C

- `bridge.c` 只保留：建 rt/ctx、wake pipe、`js_std_loop`、少量固定 stub（done / wake_drain / host_call / async_start）。  
- **核心 boot 字符串在 Go** 里 eval（低频，无性能问题）：`std`/`os`、`setTimeout`/`clearTimeout`、`__goqjs_invoke`。  
- **不再**在内核 boot 里装 `resp` / `respWrite`。

---

## 3. Inject 闸门

- `Eval` / `InjectHost` / `InjectAsyncHost` / `stdlib.Install`：仅允许在 **第一次 `Run` 之前**。  
- 首次 `Run`（投递前）**冻结**；之后再注入 → **panic**（编程错误）。  
- 冻结绑在 First Run，不绑在 `New` 返回，便于 `New` 后多次 Install。  
- **每请求** req/res（serve）不是全局 Inject，而是 `Run` 级绑定；不受「全局函数表冻结」影响（后续 serve 实现）。

---

## 4. stdlib（默认配方，非焊死内核）

```text
stdlib.Install(r, stdlib.Options{Console: true, Fetch: true, ...})
```

| 能力 | 默认 | 可替换 |
|------|------|--------|
| `console.log` | 写到 `Options.Log`（默认 stdout） | 自定义 Writer / 不装 Console |
| `fetch` | Go `net/http`（最小可用子集） | 自定义 Client / 不装 Fetch |
| `sleep` | **不在** stdlib 默认里 | `cmd/goqjs` 可注入糖：`Promise + setTimeout` |

多个 cmd 与嵌入方 **复用 Install**；也可以完全自写 Inject，不调用 stdlib。

---

## 5. cmd 分工

### `cmd/goqjs`

- `stdlib.Install`（至少 Console）。  
- 可选：`globalThis.sleep = ms => new Promise(r => setTimeout(r, ms))`。  
- examples 用 `console.log`，不再用 `resp.write`。

### `cmd/goqjs-serve`（规划）

- JS：`run(req, res)` 语义对象。  
- Go/C：用 **`req_id`（+ 已有 `go_id`）** 定位底层写回；**不用 fd 当公共协议**。  
- Go 实现层可以很「脏」（map + ResponseWriter），不必在 runtime 包导出 HTTP 语义类型。

---

## 6. Host 桥约定（实现层）

- **同步**：`__goqjs_host(name, jsonPayload) → string`（如 console）。  
- **异步**：`__goqjs_async_start(name, jsonPayload) → id`，完成后再经 loop 线程 settle Promise（如 fetch）。  
- 多 Runtime：回调一律带 `go_id`。

---

## 7. Pool

- 独立包 `goqjs/pool`：`pool.New(ctx, run, n, setup)` + `-c N`。  
- `setup(*runtime.Runtime) error` 在任意 `Run` 前 Install。  
- Pool 只依赖 runtime 公开 API，不碰内部字段。

---

## 8. 建议落地顺序

1. Boot → Go；去掉内核 `resp`；Inject 闸门 + host/async 桥  
2. `stdlib`：console，再 fetch 最小子集  
3. 改 `cmd/goqjs` + examples + 测试  
4. `cmd/goqjs-serve` 骨架（req_id + res.write）  

---

## 9. 一句话

**Runtime 只做可冻结的引擎宿主；console/fetch 是可选用的 stdlib；sleep 用标准 timer；业务 IO（含 HTTP）由 cmd/嵌入方绑定，C↔Go 用 id 定位而非语义对象。**
