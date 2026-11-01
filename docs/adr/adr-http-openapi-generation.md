# operation 声明同时生成 OpenAPI 片段并绑定 handler

## 背景

模块的 OpenAPI（[REF-openapi](../references.md)）片段此前是手写 JSON，与 handler 各自维护，靠一致性测试桥接。独立审查在这棵树上找到的偏差说明这个机制不够：不可达的应答应、可达而未声明的应答、被路由拒收的请求字段、声明为 `application/json` 而实现写 `application/problem+json`（[REF-rfc9457](../references.md)）的错误体，都能通过「方法+路径」这一层的核对。请求参数与请求体字段只有在少数模块有专门核对，新增一个参数而忘记文档不会被发现。

要的是「加参数忘文档」在代码里写不出来。文档是数据、handler 是代码，Go 编译器只把代码与代码联系起来；只有让一侧由另一侧生成才可能。

只把片段从 JSON 换成 Go 结构体并不够：声明与 handler 仍是两份事实，handler 照样可以用 `c.Query` 读一个声明里没有的参数。声明必须同时是 handler 的类型边界。

## 考虑过的选项

### 保持手写片段，扩大一致性测试

给每个模块补参数与请求体的 AST 核对，把现有 `authn/admin` 的检查推广到全部模块。

**否决**：仍然是测试期发现，且本次审查已经证明这类核对本身会漏（响应体、媒体类型、可达性都要各自一套规则）。每个模块一套 AST 检查也不可持续。

### 从契约生成 handler 接口

片段作为唯一来源，生成 typed input 与 handler 签名，读未声明参数写不出来。

**否决**：路由的挂载级别、条件路由、端点多路复用与特性声明都不在 OpenAPI 里，要先为它们设计扩展；这推翻 [ADR-http-routing](adr-http-routing.md) 的「Spec 不参与路由」，且宿主构建必须运行生成器。

### 只把片段换成 Go 声明

模块用 Go 结构体声明 operation 并生成片段，handler 仍直接读写请求。

**否决**：声明与 handler 之间没有类型联系。加一个 `c.Query("x")` 不需要改声明，门禁仍然发现不了——这正是要消灭的那类漂移。

### 从 Go 声明生成片段，并让声明绑定 handler

模块在 Go 里声明 operation 及其 handler 的输入与成功应答类型；生成器把类型反射成 JSON Schema（[REF-json-schema](../references.md)）并组装成片段，入口模块按声明绑定请求、调用 handler、写出声明的应答。生成器桥接现成库 `github.com/swaggest/openapi-go`（[REF-swaggest-openapi-go](../references.md)），只做 OpenAPI 3.1 的 schema 反射与文档组装。

**采纳**，见「决策」。

## 决策

采纳：**模块以 `http.Operation[In, Out]` 声明自己的 operation，片段由这些声明生成，声明同时绑定 handler。** 声明类型在 `pkg/http`，只依赖标准库；生成器在 `pkg/internal/openapigen`，是构建期依赖，不进任何宿主的运行时闭包。生成器用 `github.com/swaggest/openapi-go` 反射 DTO 与组装文档，并把输出规整成平台发布的形状（无 `null` 类型联合、参数 schema 不重复描述、响应头无默认 `style`、问题文档组件名为 `Problem`）。

**handler 的签名由声明给出**：`Operation.Handle` 只接受 `func(context.Context, In) (Out, error)`。入口模块从 `In` 的 `path`/`query`/`header`/`cookie` tag 绑定参数，从 `json` tag 解码请求体并拒绝未知成员，成功后按声明的状态与媒体类型写出 `Out`。handler 不再拿到请求，因而读不到声明没带的参数；它也写不出声明没描述的应答字段。

片段的漂移由生成测试守住：生成结果与已提交片段不一致即失败，修复方式是改声明并重新生成。handler 返回的错误经 `Failure` 接口给出状态，只在该状态已声明时写出，否则答 500。

## 后果

- 一个 operation 的路径、参数、请求体、应答与描述在一处声明；片段不能再与 handler 悄悄分叉。
- 描述与字段说明搬进 Go 的结构体 tag 与声明字段，代码审阅时与实现相邻。
- 迁移按模块进行；平台模块与宿主自带的片段都由同一份声明生成，没有手写片段残留。
- 声明拥有路径、输入绑定与成功应答的写出；**登记与访问级别仍归模块**（例如认证模块经 `mount.Mounter` 挂载），因为挂载级别与条件路由不属于 OpenAPI。宿主模块经 `pkg/http/openapigen` 的转出口使用同一个生成器。

**付出的代价：**

- `pkg` 的依赖多了一个构建期库（`swaggest/openapi-go`）。它不在运行时闭包里：`pkg/http/openapigen` 作为命名子包被列入 `pkg/http` 的依赖预算，只被生成测试导入，不进入宿主二进制。
- 生成片段需要多一步构建动作，忘记重新生成会让漂移测试变红。
- 生成器的规整规则是平台自己的约束；库新增一个默认字段就可能改变契约字节，需要相应更新规整或接受契约变化。
- 描述从 JSON 搬进 Go tag 后，长文本的可读性不如独立 JSON 文件。
- 迁移后的 handler 拿不到 `*gin.Context`（[REF-gin](../references.md)）；动态响应头、重定向、Cookie、流式响应由应答声明模型表达（见 [ADR-http-request-response-kinds](adr-http-request-response-kinds.md)），模型以外的协议仍留在 gin handler 写法上。
- 错误状态是否已声明只在运行期核对：handler 返回一个未声明的状态会答 500，而不是编译失败。

## 失效条件

出现生成器与库都表达不了的契约特性（例如需要跨 operation 的条件 schema、非 JSON Schema 能描述的消息形状）时，这条决策对那个模块不再成立，需要重新考虑手写后缀或扩展机制。生成器若被迫承担路由职责，也就越过了本记录划给它的边界。
