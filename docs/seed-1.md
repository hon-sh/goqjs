# hon — seed（前世今生）

> 给后续 agent / 人类：本目录不是凭空玩具，而是一次「Go 宿主做 React SSR」讨论收敛后的最小实验床。  
> 实现细节以代码与 `README.md` 为准；本文保留**动机、结论与边界**，避免重复踩坑。

---

## 1. 问题从哪来

目标图景：**Go 服务端**跑 React **SSR**，用可嵌入的 JS 引擎，而不是默认「Go API + 独立 Node SSR 进程」。

讨论中逐步澄清的约束：

- React 侧希望能按条件 **动态 `await fetch`**（不能只做「Go 先拉数再 `renderToString`」的 A 方案）。
- 在 JS 线程上 **同步堵等**网络（B）不可接受。
- 需要类似 Node 的模型：**一个 Isolate/Runtime + 事件循环**，I/O 等待时多路复用多个 SSR/任务（C）。

---

## 2. 关键结论（架构）

### 2.1 Go 与 V8/引擎不是对等并排

能力上两者都像「完整运行时」，但嵌入后是：

- **Go = 宿主进程**（调度、业务、真正的网络栈）
- **JS 引擎 = 客人库**（堆、GC、执行 JS）

裸 V8/`v8go` **没有** Node 的 libuv 事件循环；只有引擎 +（可选）microtask。macrotask（timer / fetch 完成回调）必须由宿主提供。

### 2.2 同步 Eval 与池化的误区

- `renderToString` 纯 CPU 的 10ms 会占住 **进入该 Isolate 的 OS 线程**。
- 「100 并发 ⇒ 100 条 OS 线程」**仅当**无上限地并发 Enter；应用 **Isolate/会话池** 限制峰值。
- 等 `fetch` 时若仍占着 Isolate：**不要**幻想把同一 Isolate「借」给另一个 HTTP 请求（堆状态在 V8/QJS 堆里，不能便宜换出）。  
  能释放的通常是 **worker OS 线程**；会话仍钉住自己的堆，直到该次渲染结束。  
  真正的多路复用靠 **单 Runtime + event loop**（Node 模型），不是「借 Isolate」。

### 2.3 正确收敛：mini-node，而不是裸引擎池

```text
理想：1× JS Runtime + event loop + fetch/timer/…
      Go 把 SSR/任务丢进去；I/O 在等待时跑别的任务的 JS。
多核：再开 N 个这样的实例（类似 Node cluster），而不是一个 Isolate 多 goroutine 共用。
```

单实例能撑的是 **I/O 并发与中等 QPS**；CPU 型 render 仍串行在一条 JS 线程上。

### 2.4 Go 生态里没有「完整 mini-Node」库

探索结论（当时）：

| 方向 | 代表 | 评价 |
|------|------|------|
| goja + 兼容层 | `dop251/goja_nodejs`（loop/require/buffer…） | 最接近拼装底座；**无完整 Node**；fetch 需另接（如 gojax/ski） |
| Web API 向 | `shiroyk/ski` | fetch/streams/timers 更全，星少 |
| V8 | `v8go` + polyfills | 上游维护问题；polyfill 线程安全常有坑；仍非 Node |
| QuickJS 绑定 | `buke/quickjs-go`、`fastschema/qjs` | 偏引擎/沙箱，不带完整 libc loop 哲学 |
| 真 Node 兼容 | 侧车 Node / 非 Go 的 jsrt 等 | 「整机」仍在 Node 侧 |

**没有**可直接当生产 Node 用的 Go 库 → 若坚持同进程，应 **自建薄宿主** 或侧车真 Node。

### 2.5 为何落到 QuickJS（qjs）

Bellard / quickjs 的 CLI（`qjs.c`）路径清晰：

```text
Runtime → js_std_init_handlers → std/os 模块 → Eval → js_std_loop
```

「准运行时」在 **`quickjs-libc`（std/os + `js_std_loop`）**，不在引擎内核。  
策略：**以 qjs libc 宿主为内核**，用 C 扩展固化，再 cgo 包成 Go；**真实 IO 放 Go**，完成事件 **回灌 JS loop 线程**。

双 loop 规则（必须守）：

- **C/QJS 拥有 loop**（第一版推荐），Go 做网络后 wakeup / 投递；或
- Go 拥有 loop（等于自研 goja_nodejs，丢掉现成 `js_std_loop`）
- **禁止** `qjs:os` 与 Go netpoller 抢同一批 fd

所有 `JS_*` 只能在 loop 线程调用。

---

## 3. 开源 SSR 相关背景（只作地图）

曾扫过的 Go⊕React SSR 探索（嵌入引擎路径）：

- `natewong1313/go-react-ssr` — QuickJS（`buke/quickjs-go`），偏插件化
- `yejune/gotossr` — QuickJS/V8 可选 + pool
- `olebedev/go-starter-kit` — 早期 goja SSR
- `highercomve/go-react-ssr` / 博文 — v8go + polyfill（`process` / `MessageChannel` 等）
- `millken/inertia` — goja / QuickJS / v8go 可插拔

共识流水线：`esbuild` 打 SSR bundle → 引擎 `renderToString` → 模板注入 → hydrate。  
**边界**：适合同进程首屏 SSR；不指望完整 Next/RSC。

---

## 4. 本仓库现状（已实现的最小证明）

路径：`_misc/hon/`  
模块名：`hon`（内部短名，非完整 module path）  
引擎：Bellard QuickJS **`2026-06-04`**（`https://bellard.org/quickjs/quickjs-2026-06-04.tar.xz`），vendored 在 `third_party/quickjs/`。

### Demo 契约

```bash
cd _misc/gohon
go run ./cmd/gohon 3 5 6
```

- **一个** QJS 实例
- 每个参数 `c` 启动一个并发 async job
- 逻辑等价于：

```js
for (let i = 0; i < c; ++i) {
  await resp.write(/*callId*/, String(i));
  await sleep(1000);
}
```

- `resp.write` 经 cgo 立刻打到 Go stdout（带 `[c]` 前缀），实时交错输出
- `sleep` → `os.sleepAsync`（不堵死 JS 线程）
- 全部结束后 `js_std_loop` 空闲退出（`3 5 6` 约 6s）

### 当前桥接方式

- Go：`runtime.LockOSThread` + cgo `hon_run`
- C：建 Runtime/Context，挂 host `respWrite`，eval 脚本，**`js_std_loop` 直到 idle**
- 证明点：**单 Runtime + loop 上多路 async 任务 + 宿主 write 回调** —— 即「mini-node」骨架，尚非 SSR/fetch 产品

---

## 5. 明确的非目标 / 尚未做

- 完整 Node / npm 兼容、完整 WHATWG Streams
- React `renderToString` / hydration / esbuild 管线
- 与 Go 协作的通用 **read/write stream** 后端（设计已讨论：粗粒度 chunk、有界队列、回灌 loop；**未实现**）
- 多 Runtime 池化吃多核
- 无 cgo 方案（Wazero/QJS 等）— 本线刻意走 libc/`js_std_loop`

---

## 6. 建议的后续顺序（若继续）

1. 把「整包 `fetch` → Promise resolve」接到 Go `net/http`，完成事件只经 loop 线程 resolve  
2. 再考虑 chunked stream / backpressure  
3. SSR：esbuild 打 CJS/IIFE bundle，在同一 loop 上多请求多路复用  
4. CPU 成为瓶颈后再 ×N 个 hon 实例  

优先保持：**API 面尽量窄**（SSR 不需要整份 `qjs:os` 文件/进程能力）。

---

## 7. 给后续 agent 的操作提示

- 先读 `README.md` + `runtime/` + `cmd/gohon/`，再改；本 `seed.md` 不替代代码。  
- 改 loop/线程模型前重读 §2.5：不要在非 loop 线程调 QuickJS API。  
- 依赖 tarball 版本与 `third_party/quickjs/VERSION`；升级引擎需回归 `go run ./cmd/gohon 3 5 6`。  
- 模块名目前是故意的短名 `hon`；若要进主 module 树再改 path。  

---

## 8. 一句话

**hon = 用 QuickJS 的 `js_std_loop` 宿主证明「Go 进程内单 JS 运行时可多路异步任务」；它是 React-SSR-on-Go 讨论的实验内核，不是完整 Node，也尚不是 SSR 框架。**
