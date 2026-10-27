# 请求路径的采集以上游 gin 采集库为基座，本模块只补日志关联与端名

## 背景

请求路径的采集要完成下列事：从入站头部提取父跨度上下文，开始服务端跨度，在响应结束时按状态与错误标记跨度，记录请求的计量。上游为 gin 提供了做这些事的中间件，`go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin`（下称上游采集库）。它接受调用方交给它的 tracer provider、meter provider 与传播器，在 `c.Next` 返回后从 `c.Writer.Status()` 与 `c.Writer.Size()` 读取响应，产出服务端跨度与直方图：请求时长、请求体大小、响应体大小。

上游采集库不接住 panic。panic 穿过它时，上游 SDK 在结束跨度的那一步调 `recover()` 记录它：值非 nil 时给跨度补一个 `exception` 事件（默认不带堆栈）并把值重抛，值为 nil（`GODEBUG=panicnil=1`）时既不记录也不重抛；之后的计量记录不再执行，这条请求不进任何一个直方图。

本模块在请求路径上还要给出上游采集库没有的东西：日志要带 `trace_id` 与 `span_id`，因此跨度的标识要绑进请求上下文（字段名常量 `TraceIDKey` 与 `SpanIDKey` 由接口包给出）；服务端跨度要带请求到达的端名，登记面一次只交出一个端，只有登记的那一处知道它。panic 的处理不由本模块承担：入口模块的恢复层内建在 gin 引擎之内，位置是观测阶段之内、其余各阶段之外，handler 的 panic 因此在采集层看到之前就被接住并应答（[ADR-http-recovery](adr-http-recovery.md)），上游采集库随后把这次请求按应答的 `500` 记进跨度与直方图。

登记层的位置由阶段给出：跨类别由固定的阶段决定，阶段内的先后由功能标识上的 `After` 与 `Before` 给出，`Order` 只在约束留有余地处取舍（[ADR-http-stages](adr-http-stages.md)）。

上游采集库的行为：

- 响应体大小直方图记 `c.Writer.Size()`，gin 的写出者在没有任何写入之前返回 `-1`；没有写出响应体的请求，包括空 `200` 与 `204`，记 `-1`，总和可以为负。请求体大小直方图记请求的 `ContentLength`，长度未知时为 `-1`。服务端跨度只在写出字节数大于零时带 `http.response.body.size`。
- 没有匹配路由的请求，服务端跨度带 `http.route` 属性且取值为空串；计量侧此时不带 `http.route`。
- 跨度名取 `c.FullPath()` 非空时的「方法 路由模板」，否则只取方法。方法不在 `GET`、`HEAD`、`POST`、`PUT`、`PATCH`、`DELETE`、`CONNECT`、`OPTIONS`、`TRACE` 之内时，跨度名里写作 `HTTP`，而 `http.request.method` 属性写作 `_OTHER`，原值另记在跨度的 `http.request.method_original` 上。
- 计量属性含 `http.request.method`、`http.response.status_code`、`http.route`（匹配时）、`url.scheme`、`server.address`、`server.port`、`network.protocol.name`、`network.protocol.version`，以及 `error.type`（请求带有错误或请求上下文已结束时）。`server.address` 取自请求的 `Host`。服务端跨度另带 `url.path`、`client.address`、`user_agent.original`、`network.peer.address`、`network.peer.port`。
- 跨度与计量的仪器作用域名是上游采集库自己的 `ScopeName`，不是本模块的。
- 状态码取调用返回时写出者持有的值，不对 1xx 做折叠。

## 考虑过的选项

### 维持自写的采集层

采纳它意味着本模块自己写提取、跨度、状态映射与请求时长的计量，属性面与跨度名按本模块的决定收窄。

**否决**：属性名、单位、桶边界、跨度名的形式与方法的折叠都出自上游的语义约定，约定每换一版，自写的那一份都要跟着对一遍，而它与上游采集库产出的是同一份信号。本模块的定位是上游库的桥，信号的语义本体由上游库定义（[ADR-observability-scope](adr-observability-scope.md)）；自写等于在请求路径上另造一份等价实现。

### 原样使用上游采集库

采纳它意味着登记上游采集库的中间件，本模块在请求路径上不再有自己的层。

**否决**：日志关联字段与端名没有承接处——上游采集库不往日志上下文里绑跨度的标识，也没有登记方的端名可写，这两样只在本模块手里。panic 的处理不构成理由：它由入口模块的恢复层承担，与本模块是否登记守护层无关。

### 上游采集库加一个守护层，守护层在它之内

采纳它意味着登记外层与守护层：外层是上游采集库的中间件；内层是守护层，位于它之内，把跨度的标识绑进日志上下文，并给跨度写端名属性。panic 不在这里处理，接住它的是入口模块的恢复层。

**采纳** — 见决策。

### 守护层在上游采集库之外

采纳它意味着登记一个层包住上游采集库，由它绑日志关联字段并写端名。

**否决**：守护层要绑的标识与要写的属性都取自上游采集库开始的那个跨度，它必须在外层之内才读得到；放在外面时跨度尚未开始，标识与属性都没有来源。

### 上游采集库加 View、写出者包装与跨度名覆盖，保持方法与状态码的闭集属性面与 1xx 的折叠口径

采纳它意味着用 OpenTelemetry 的 View 把计量的属性收回方法与状态码、丢掉请求体大小与响应体大小的直方图，用一个包装 `c.Writer` 的层把 1xx 折叠为最终状态并让无响应体的请求记 `0`，用跨度名格式化函数把非标准方法折成 `_OTHER`。

**否决**：每一项都是改上游采集库的行为：View 作用在整个 meter provider 上，要对着上游的仪器名与作用域名写，上游改名即失效；写出者包装要转发写出者携带的 `Flusher`、`Hijacker` 与 `Unwrap`，漏掉一个会让每个被采集的端静默退化；跨度名格式化与状态折叠把上游约定的形式拉回自写的一份。采纳上游采集库的目的是不再持有这一面，这些做法把它又收了回来，并且每一项都要随上游的版本维护。

## 决策

采纳：**请求路径的采集以上游 gin 采集库为基座，本模块只补它没有的：日志关联字段与端名属性。** 对每个被采集的端登记外层与守护层：外层是上游采集库的中间件，配置的是本模块的 tracer provider、meter provider 与传播器，服务端名留空，位于观测阶段的最外侧；守护层以 `After` 指向追踪 provider 的功能标识放在外层之内，把跨度的标识绑进日志上下文，并给跨度写端名属性，不代表任何功能。

上游采集库的行为原样保留：不加 View，不包装 `c.Writer`，不改 `WriteHeaderNow` 的时机，不覆盖跨度名格式化。守护层与外层的相对位置由守护层的 `After`（指向追踪 provider 的功能标识）给出，不由相邻给出。

panic 的处理不归本模块。入口模块的恢复层内建在 gin 引擎之内，位置是观测阶段之内、其余各阶段之外，handler 的 panic 在采集层看到之前被它接住、应答 `500`，并把一个定型的错误登记进请求的错误列表；上游采集库据此把跨度标为错误、带上 `error.type`（那个定型错误的名字）与 `http.response.status_code` `500`，请求按 `500` 进时长直方图（[ADR-http-recovery](adr-http-recovery.md)）。

请求计量的属性面与服务端跨度的名称、属性取上游采集库的完整输出（[ADR-observability-instruments](adr-observability-instruments.md)）。

## 后果

属性名、单位、桶边界、跨度名与方法折叠随上游采集库对上游语义约定的跟进而更新，本模块不持有一份等价实现；请求路径上本模块自写的代码只有守护层。

**接受的代价：**

- **属性面是上游采集库的，不是闭集。** 计量带 `server.address`，取值取自请求的 `Host` 头，调用方可写，一个不在反向代理之后固定 `Host` 的宿主，序列数随调用方写下的主机名增长；服务端跨度带请求路径、客户端地址与 `User-Agent`。属性面的取舍见 [ADR-observability-instruments](adr-observability-instruments.md)。
- **属性集合与仪器作用域名是上游采集库的，宿主按它们写看板与告警的查询。** 计量的属性集合与仪器作用域名（上游采集库的 `ScopeName`）都是宿主读到的东西，随上游采集库的版本而定。除请求时长 `http.server.request.duration` 之外，`http.server.request.body.size` 与 `http.server.response.body.size` 的直方图也被导出。
- **1xx 状态不折叠。** 处理器只设了 1xx（101 除外）就写出响应体时，调用方收到的最终状态是 `200`，写出者持有的仍是那个 1xx，计量与跨度记 1xx。这是一条非保证。
- **无响应体的请求在响应体大小直方图里记 `-1`。** 空 `200` 与 `204` 都是如此，该直方图的总和因此可以为负。这是上游采集库的缺陷，本模块不修补。panic 的请求不在此列：恢复层以 problem 响应应答，写出者有正文可记。
- **没有匹配路由的请求，服务端跨度带取值为空的 `http.route`。** 上游采集库的行为，本模块不修补。
- **非标准方法在跨度名与属性里的写法不同。** 跨度名里是 `HTTP`，`http.request.method` 属性里是 `_OTHER`；上游语义约定列为开发中取值的 `QUERY` 同样落在这两处。
- **上游采集库的版本是对外面的一部分。** 它换版可以改属性、仪器与作用域；请求时长仪器 `http.server.request.duration` 的名称、单位与桶边界由测试钉住，`http.server.request.body.size`、`http.server.response.body.size` 与属性集合不由测试钉住。
- **panic 的请求，恢复层记下的堆栈在接住的那一帧取得。** 崩溃处及其穿过的各帧仍在栈上，崩溃的位置就在这条记录里；跨度不带 `exception` 事件，因为 panic 在跨度结束之前已被接住，没有穿过跨度的结束。
- **守护层按 `After` 放置，不按相邻放置。** 别的层可以被放在外层与守护层之间，这样的层是观测层的，在恢复层之外：它抛出的 panic 不被恢复层接住，直接穿过上游采集库与引擎抵达 `net/http`，连接被关闭，跨度以状态未设结束（上游 SDK 在结束跨度时给它记一个不含堆栈的 `exception` 事件），请求不进任何直方图。
- **链没有正常返回时，跨度以未标记结束、请求不进任何直方图。** handler 调用 `runtime.Goexit`，或用 `http.ErrAbortHandler` 请恢复层放行连接时，展开越过两层而不回到上游采集库的计量记录：跨度结束时没有本模块设的状态，请求不进任何直方图。客户端主动断开的那一档如此是应当的；`Goexit` 的那一档是一条真缺陷，跨度不显示它，恢复层自己写一条日志记录承接。守护层不 `recover`，因为能看见这两种展开的 `recover` 正是会吞掉 `ErrAbortHandler` 的那个，连接将不再被关闭。
- **`GODEBUG=panicnil=1` 下的 `panic(nil)` 由恢复层按「链没有返回」处理。** `recover` 读到的是 `nil` 而不是 panic 值，恢复层照 handler 停下的缺陷处理：把一个定型的错误登记进请求的错误列表、应答 `500`、中止其后的链，跨度随之标错。
- **依赖随上游采集库与 OTLP 导出器的版本而定。** `pkg` 模块依赖上游采集库；OpenTelemetry 的各模块与导出器与它所要求的版本对齐，上游采集库自己的 `go.mod` 要求更高最低版本的间接依赖（gin 的序列化与校验一侧的库等）取该版本；OTLP 导出器的该版本要求更高的 `google.golang.org/grpc` 与 `google.golang.org/genproto/googleapis/api`、`google.golang.org/genproto/googleapis/rpc`，这一部分不出自上游采集库。依赖 `pkg` 的宿主的 `go.mod` 与 `go.sum` 带着同样的版本。
- **panic 值的文本进恢复层的日志记录，不进跨度。** 恢复层登记到请求错误列表里的是一个定型错误、不带 panic 值，跨度的状态描述因此不携带它，`error.type` 只是那个类型名；panic 值与堆栈留在恢复层的日志记录里。

## 失效条件

若上游采集库不再提供请求计量，或其属性面的基数在宿主的部署里不可接受——`server.address` 随调用方写下的 `Host` 增长到指标后端的基数限制——则属性面应当由本模块自己持有，回到自写的采集层。
