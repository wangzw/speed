# 请求路径的采集以上游 gin 采集库为基座，本模块只补 panic 处理、日志关联与端名

## 状态

已接受

## 背景

请求路径的采集要完成下列事：从入站头部提取父跨度上下文，开始服务端跨度，在响应结束时按状态与错误标记跨度，记录请求的计量。上游为 gin 提供了做这些事的中间件，`go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin`（下称上游采集库）。它接受调用方交给它的 tracer provider、meter provider 与传播器，在 `c.Next` 返回后从 `c.Writer.Status()` 与 `c.Writer.Size()` 读取响应，产出服务端跨度与直方图：请求时长、请求体大小、响应体大小。

上游采集库不接住 panic。panic 穿过它时，上游 SDK 在结束跨度的那一步给跨度记一个 `exception` 事件，事件不带堆栈，跨度状态不设；之后的计量记录不再执行，这条请求不进任何一个直方图。

本模块在请求路径上还要给出上游采集库没有的东西。panic 穿过时仍要把请求记成一次失败，并让它进时长直方图；日志要带 `trace_id` 与 `span_id`，因此跨度的标识要绑进请求上下文（字段名常量 `TraceIDKey` 与 `SpanIDKey` 由接口包给出）；服务端跨度要带请求到达的端名，登记面一次只交出一个端，只有登记的那一处知道它。

入口模块的 panic 兜底内建在 gin 引擎之外，不经登记面（[ADR-http-recovery-2026-09-22](adr-http-recovery-2026-09-22.md)）：gin 自带的恢复中间件会吞掉 `http.ErrAbortHandler`，这一处置不可配置。panic 因此要原样抛到引擎之外，由入口模块的兜底层收口。

登记层的位置由功能标识上的 `After` 与 `Before` 给出，`Order` 只在约束留有余地处取舍（[ADR-http-middleware-2026-09-14](adr-http-middleware-2026-09-14.md)）。

上游采集库的行为：

- 响应体大小直方图记 `c.Writer.Size()`，gin 的写出者在没有任何写入之前返回 `-1`；没有写出响应体的请求，包括空 `200`、`204` 与 panic 的请求，记 `-1`，总和可以为负。请求体大小直方图记请求的 `ContentLength`，长度未知时为 `-1`。服务端跨度只在写出字节数大于零时带 `http.response.body.size`。
- 没有匹配路由的请求，服务端跨度带 `http.route` 属性且取值为空串；计量侧此时不带 `http.route`。
- 跨度名取 `c.FullPath()` 非空时的「方法 路由模板」，否则只取方法。方法不在 `GET`、`HEAD`、`POST`、`PUT`、`PATCH`、`DELETE`、`CONNECT`、`OPTIONS`、`TRACE` 之内时，跨度名里写作 `HTTP`，而 `http.request.method` 属性写作 `_OTHER`，原值另记在跨度的 `http.request.method_original` 上。
- 计量属性含 `http.request.method`、`http.response.status_code`、`http.route`（匹配时）、`url.scheme`、`server.address`、`server.port`、`network.protocol.name`、`network.protocol.version`，以及 `error.type`（请求带有错误或请求上下文已结束时）。`server.address` 取自请求的 `Host`。服务端跨度另带 `url.path`、`client.address`、`user_agent.original`、`network.peer.address`、`network.peer.port`。
- 跨度与计量的仪器作用域名是上游采集库自己的 `ScopeName`，不是本模块的。
- 状态码取调用返回时写出者持有的值，不对 1xx 做折叠。

## 考虑过的选项

### 维持自写的采集层

采纳它意味着本模块自己写提取、跨度、状态映射与请求时长的计量，属性面与跨度名按本模块的决定收窄。

**否决**：属性名、单位、桶边界、跨度名的形式与方法的折叠都出自上游的语义约定，约定每换一版，自写的那一份都要跟着对一遍，而它与上游采集库产出的是同一份信号。本模块的定位是上游库的桥，信号的语义本体由上游库定义（[ADR-observability-scope-2026-09-22](adr-observability-scope-2026-09-22.md)）；自写等于在请求路径上另造一份等价实现。

### 原样使用上游采集库

采纳它意味着登记上游采集库的中间件，本模块在请求路径上不再有自己的层。

**否决**：panic 穿过它时，请求不进任何直方图，跨度没有错误状态也没有堆栈；而 panic 的请求正是计量最需要记下的那一类。日志关联字段与端名同样没有承接处。

### 上游采集库加一个守护层，守护层在它之内

采纳它意味着登记外层与守护层：外层是上游采集库的中间件，在它返回之后把守护层交来的 panic 值再抛出；内层是守护层，接住它之内的 panic，给请求登记一个错误，在没有写出响应时把状态置为 `500`，中止其后的链，把值交给外层，再给跨度记 `exception` 事件与堆栈，把标识绑进日志上下文，并写端名属性。

**采纳** — 见决策。

### 守护层在上游采集库之外

采纳它意味着登记一个层包住上游采集库，接住穿过它的 panic。

**否决**：panic 穿过上游采集库时，跨度已经结束，计量的记录已被跳过。守护层在外面能接住 panic，却给不了跨度错误状态，请求也进不了直方图。

### 上游采集库加 View、写出者包装与跨度名覆盖，保持方法与状态码的闭集属性面与 1xx 的折叠口径

采纳它意味着用 OpenTelemetry 的 View 把计量的属性收回方法与状态码、丢掉请求体大小与响应体大小的直方图，用一个包装 `c.Writer` 的层把 1xx 折叠为最终状态并让无响应体的请求记 `0`，用跨度名格式化函数把非标准方法折成 `_OTHER`。

**否决**：每一项都是改上游采集库的行为：View 作用在整个 meter provider 上，要对着上游的仪器名与作用域名写，上游改名即失效；写出者包装要转发写出者携带的 `Flusher`、`Hijacker` 与 `Unwrap`，漏掉一个会让每个被采集的端静默退化；跨度名格式化与状态折叠把上游约定的形式拉回自写的一份。采纳上游采集库的目的是不再持有这一面，这些做法把它又收了回来，并且每一项都要随上游的版本维护。

## 决策

采纳：**请求路径的采集以上游 gin 采集库为基座，本模块只补它没有的：panic 的处理、日志关联字段与端名属性。** 对每个被采集的端登记外层与守护层：外层是上游采集库的中间件，配置的是本模块的 tracer provider、meter provider 与传播器，服务端名留空；守护层位于它之内，接住它之内的 panic，交给外层在上游采集库记录完成后原样重抛。

上游采集库的行为原样保留：不加 View，不包装 `c.Writer`，不改 `WriteHeaderNow` 的时机，不覆盖跨度名格式化。守护层与外层的相对位置由守护层的 `After`（指向追踪 provider 的功能标识）给出，不由相邻给出。

请求计量的属性面与服务端跨度的名称、属性取上游采集库的完整输出（[ADR-observability-instruments-2026-09-22](adr-observability-instruments-2026-09-22.md)）。

## 后果

属性名、单位、桶边界、跨度名与方法折叠随上游采集库对上游语义约定的跟进而更新，本模块不持有一份等价实现；请求路径上本模块自写的代码是守护层与重抛。

**接受的代价：**

- **属性面是上游采集库的，不是闭集。** 计量带 `server.address`，取值取自请求的 `Host` 头，调用方可写，一个不在反向代理之后固定 `Host` 的宿主，序列数随调用方写下的主机名增长；服务端跨度带请求路径、客户端地址与 `User-Agent`。属性面的取舍见 [ADR-observability-instruments-2026-09-22](adr-observability-instruments-2026-09-22.md)。
- **属性集合与仪器作用域名是上游采集库的，宿主按它们写看板与告警的查询。** 计量的属性集合与仪器作用域名（上游采集库的 `ScopeName`）都是宿主读到的东西，随上游采集库的版本而定。除请求时长 `http.server.request.duration` 之外，`http.server.request.body.size` 与 `http.server.response.body.size` 的直方图也被导出。
- **1xx 状态不折叠。** 处理器只设了 1xx（101 除外）就写出响应体时，调用方收到的最终状态是 `200`，写出者持有的仍是那个 1xx，计量与跨度记 1xx。这是一条非保证。
- **无响应体的请求在响应体大小直方图里记 `-1`。** 空 `200`、`204` 与 panic 的请求都是如此，该直方图的总和因此可以为负。这是上游采集库的缺陷，本模块不修补。
- **没有匹配路由的请求，服务端跨度带取值为空的 `http.route`。** 上游采集库的行为，本模块不修补。
- **非标准方法在跨度名与属性里的写法不同。** 跨度名里是 `HTTP`，`http.request.method` 属性里是 `_OTHER`；上游语义约定列为开发中取值的 `QUERY` 同样落在这两处。
- **上游采集库的版本是对外面的一部分。** 它换版可以改属性、仪器与作用域；请求时长仪器 `http.server.request.duration` 的名称、单位与桶边界由测试钉住，`http.server.request.body.size`、`http.server.response.body.size` 与属性集合不由测试钉住。
- **panic 的请求，兜底层记下的堆栈起于外层的重抛处。** 守护层已经返回，崩溃的那一帧不在栈上；起点保留在跨度的 `exception` 事件的 `exception.stacktrace` 上。只读兜底层那一行日志的人看不到崩溃的位置。
- **守护层按 `After` 放置，不按相邻放置。** 别的层可以被放在外层与守护层之间，这样的层里抛出的 panic 不被守护层接住：它直接穿过上游采集库，跨度带一个不含堆栈的 `exception` 事件而状态不设，请求不进任何直方图。
- **handler 调用 `runtime.Goexit` 的请求，跨度带错误状态而不在任何直方图里。** `Goexit` 的展开穿过守护层而不回到上游采集库，上游采集库只结束跨度、不记录响应与计量；守护层在跨度上设错误状态并记一个 `exception` 事件，请求不进任何直方图，这一部分是上游采集库的行为，本模块不修补。`GODEBUG=panicnil=1` 下的 `panic(nil)` 在守护层同样读到 `nil`，守护层按「链没有返回」认出它，登记错误、置状态并中止其后的链，外层再抛一个 `nil` 交给兜底层记录与应答。
- **依赖随上游采集库与 OTLP 导出器的版本而定。** `pkg` 模块依赖上游采集库；OpenTelemetry 的各模块与导出器与它所要求的版本对齐，上游采集库自己的 `go.mod` 要求更高最低版本的间接依赖（gin 的序列化与校验一侧的库等）取该版本；OTLP 导出器的该版本要求更高的 `google.golang.org/grpc` 与 `google.golang.org/genproto/googleapis/api`、`google.golang.org/genproto/googleapis/rpc`，这一部分不出自上游采集库。依赖 `pkg` 的宿主的 `go.mod` 与 `go.sum` 带着同样的版本。
- **守护层记录的 panic 值文本进入跨度。** `exception` 事件带 panic 值的文本与堆栈，属性不经日志的处理链。

## 失效条件

若上游采集库不再提供请求计量，或其属性面的基数在宿主的部署里不可接受——`server.address` 随调用方写下的 `Host` 增长到指标后端的基数限制——则属性面应当由本模块自己持有，回到自写的采集层。
