# operation 声明覆盖完整的 HTTP 输入与应答形态

## 背景

绑定式 operation 的第一个版本只覆盖「参数 + JSON 请求体 + 固定成功应答」。而 `*gin.Context`（[REF-gin](../references.md)）在实际 handler 里还承担：动态响应头与 Cookie、重定向、流式与二进制应答、表单与 multipart 请求、handler 自选状态、client IP 与路由模板、请求内的错误清单。作为库，绑定式模型若表达不了这些，宿主遇到其中之一就只能退回 Gin handler，契约与实现重新分叉。这正是要消灭的那类分叉。

## 考虑过的选项

### 只保留简单形态，其余继续用 Gin handler

**否决**：把库的完备性让给宿主自己补；退回的路由没有声明与生成，等于在同一个框架里维护两套契约来源。

### 把 `*gin.Context` 传给 handler，运行时再核对

**否决**：handler 又能读请求，就回到了「读得到未声明参数」的原点；核对只能事后发现，而且无法阻止。

### 声明覆盖完整形态，handler 只拿类型化输入与应答构造器

**采纳**，见「决策」。

## 决策

采纳：

- **请求体按媒体类型声明与绑定。** `Operation.Request` 是 `*RequestBody`，其 `MediaType` 取值 JSON、表单、multipart 或二进制媒体类型；`In` 的字段按媒体类型取用：JSON 用 `json` tag，表单用 `form` tag，multipart 用 `form`/`file` tag，二进制用唯一的 `raw` tag 字段。未知成员按类别拒绝。解码后若 `In` 实现 `Validate() error`，按 422 拒绝。`RequestBody.Description` 与 `RequestBody.Required` 是契约发布的内容，不改变绑定。
- **应答用类型化的 `Reply` 构造。** `JSON`、`Empty`、`Bytes`、`Stream`（`func(context.Context, io.Writer) error`）、`SSE`、`Redirect` 六种；`WithHeaders`/`WithCookies` 携带动态头与 Cookie。`ReplyOperation[In]` 的 handler 返回 `Reply`，并拿到只读的 `Meta`（client IP、路由模板、请求 Content-Length，未知为 -1）。
- **声明仍是封闭集，动态部分运行期核对。** 应答的状态必须在 `Responses` 里；媒体类型必须与该状态的声明一致；响应体声明 `*/*` 表示 handler 在该状态内自选媒体类型，只接受 bytes 与 stream 应答。动态响应头的名字必须已声明；JSON 应答的 body 类型必须等于该状态声明的结构。`Fault` 携带的响应头同样只在匹配应答声明过其名字时写出，未声明的名字按内部错误处理。不满足按内部错误处理，不写出未声明的应答。
- **简单形态保留。** `Operation[In, Out]` 与 `Handle` 仍是编译期最强的路径，成功状态与 body 类型由类型参数给出；复杂形态按 operation 选用，两者都实现同一个 `Declared` 描述符，生成器一视同仁。
- **生成器按形态产出。** JSON 出 schema；表单/multipart 出对象 schema（文件字段为二进制）；`application/octet-stream` 出二进制字符串；`text/event-stream` 出事件流媒体类型；`*/*` 出二进制字符串的 content 项；重定向只出状态与 `Location`。请求体的媒体类型、描述与 required 一并发布。

## 后果

- 绑定式 operation 能表达本仓库使用中的全部 HTTP 形态；Gin handler 只留给 OpenAPI（[REF-openapi](../references.md)）描述不了或模型尚未覆盖的协议，且这种保留是显式的。
- 请求侧保持了编译期边界：handler 只能通过 `In` 读输入。
- 应答侧的状态/媒体类型/头名是运行期核对；JSON body 类型在构造应答时也只在运行期可比。这是动态应答无法做成编译期闭集的代价。

**付出的代价：**

- `Reply` 的构造与声明校验增加了入口模块的复杂度；错误路径变多（未声明状态、未声明媒体类型、未声明头名、body 类型不符）。
- multipart 与二进制请求体受端点的请求体上限约束；超限仍由请求体上限层拒绝。
- 流式应答的写失败无法改变已写出的状态，只能终止连接并记录。
- `Meta` 是只读快照；需要在 handler 中途改变响应的场景仍不在模型内。

## 失效条件

出现需要协商内容（同一状态多种媒体类型由请求决定）、HTTP/2 推送、或双向流这类形态时，`Reply` 的封闭集合不再够用，需要为那一类单独建模。若动态应答的校验规则无法覆盖宿主真实用法，说明封闭集的设计需要重新划界。
