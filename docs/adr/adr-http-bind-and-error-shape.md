# 入口模块交付请求体绑定入口，宿主的 handler 错误体统一到它的 RFC 9457 结构

## 背景

入口模块 `pkg/http` 已定义错误响应的内容与结构：`Problem` 取 RFC 9457（[REF-rfc9457](../references.md)）的字段，`Fail` 是写出入口，`ErrorResponder` 是整体替换结构的可选接口（`pkg/http/problem.go`；[ADR-http-errors](adr-http-errors.md)）。`Problem` 的 `Title` 与 `Detail` 是已经渲染完成的成品文本，替换结构的接口不承载语言；`Type` 与 `Status` 是机器码，不随语言变（[ADR-i18n-error-rendering](adr-i18n-error-rendering.md)）。

宿主各自处理请求体绑定。示例宿主的 `bindBody` 读 body、按 `*nethttp.MaxBytesError` 把失败分 413 与 400、再调 `dst` 自带的 `Validate` 分 422。第二个宿主的 `bindFailureStatus` 重复了同一套 413 与 400 的判定。

宿主也各自决定错误体的结构。一个示例宿主自造 `{error: string}`（`apiError` 与 `writeError`），handler 的拒绝经它写出。HTTP 模块的错误响应不覆盖 handler 自己写出的响应（[ADR-http-errors](adr-http-errors.md) 的后果一节）。

## 考虑过的选项

### 绑定与错误体都留在宿主

**否决**：绑定失败的判定在每个宿主各写一遍，一处修正不传导到另一处；错误体结构各写各的，同一个框架下的宿主给出的错误体互不相同。

### 抽 `BindJSON`，但保留宿主自己的错误体

绑定的拒绝经 `Fail` 写出 problem 对象，业务拒绝仍经宿主的 `{error}` 写出。

**否决**：同一个宿主的错误体，绑定拒绝写 problem 对象、业务拒绝写 `{error}`，客户端对同一个 API 的拒绝要按结构分别解析。

### 抽 `BindJSON`，并把宿主的业务错误体改用 `Fail`

**采纳** —— 见决策。

### 不抽，让 handler 直接用 gin 的 `ShouldBindJSON`

**否决**：gin（[REF-gin](../references.md)）把「超限」与「JSON 不可读」归成同一个 error，状态码要调用方自己按 `errors.As` 分；不抽就是把这段判定留在每个宿主，这正是重复发生的地方。

## 决策

采纳：**入口模块交付请求体绑定入口 `BindJSON`，宿主的 handler 错误体统一到本模块的 RFC 9457 结构。**

- 新增 `func BindJSON(c *gin.Context, dst any) bool`（`pkg/http`）。语义自宿主的 `bindBody` 迁移：`ShouldBindJSON` 失败 → 413（`*nethttp.MaxBytesError`）否则 400；成功且 `dst` 实现 `interface{ Validate() error }` 而它报错 → 422。这些失败都经既有的 `Fail` 写出 `Problem`，并返回 false。
- 第二宿主检验通过：它也重复了同一套 `ShouldBindJSON` 与 `*nethttp.MaxBytesError` → 413 的判定。
- 宿主自造的错误体删除：`apiError` 与 `writeError` 撤掉，业务 handler 改调 `speedhttp.Fail`。响应体从 `{"error":"…"}` 变成 RFC 9457 的 problem 对象。
- 错误体的内容与结构仍由 `Problem` 与 `ErrorResponder` 决定（[ADR-http-errors](adr-http-errors.md)），文本仍是渲染完成的成品文本（[ADR-i18n-error-rendering](adr-i18n-error-rendering.md)）。

## 后果

handler 侧的绑定失败与业务失败，其错误体出自同一个结构定义，绑定失败的状态码划分只有一处实现。

**接受的代价：**

- 宿主对客户端的错误体契约发生破坏性变更：从 `{"error":"…"}` 变成 problem 对象。须按破坏性变更标注。
- `pkg/http` 的公开接口多一个函数 `BindJSON`，它是本模块包装请求体绑定过程的入口；本能力现行表述「handler 里的请求体绑定、校验与响应渲染直接用 `gin.Context` 的方法，本模块不包装它们」随之不再成立。
- 本模块自己写出的拒绝覆盖无匹配路由的 404、方法不允许的 405、请求体超限的 413 与接住 panic 的 500；`BindJSON` 的 400 与 422 不在其中，它们的文本由谁渲染没在本记录里定，落在设计文档。

## 失效条件

- 若 gin 提供了统一的绑定校验入口，把 `*nethttp.MaxBytesError`、格式错误与 `Validate` 一并归一，则本模块自造的这一段划分应改为桥接。
- 若第二个宿主不再需要 `BindJSON` 的 413／400／422 划分（例如它自定状态码、自写应答），第二宿主依据失效。
