# 错误响应只承载成品文本，结构扩展点不带语言

## 背景

入口模块以 RFC 9457（[REF-rfc9457](../references.md)）的字段定义错误响应的内容（`Problem`），默认以 `application/problem+json` 写出，并允许一个模块交付 `ErrorResponder` 整体替换这一结构（[ADR-http-errors](adr-http-errors.md)）。`Problem.Type` 是客户端据以分支的机器码，不随语言变化（[ADR-i18n-scope](adr-i18n-scope.md)）。

一条错误消息由产生它的模块渲染，一个决定响应结构的扩展点不承载语言（[ADR-i18n-rendering-ownership](adr-i18n-rendering-ownership.md)）。`pkg/http` 自己写出四类拒绝：无匹配路由的 404、方法不允许的 405、请求体超限的 413、接住 panic 的 500。这些拒绝的文本要随请求语言变化，就需要一处渲染点与一个文案来源。

方向约束：入口模块是 `pkg/http`，本能力目录的根包只依赖标准库，`pkg/http` 可以取用这个根包而不增长它的依赖预算（[ADR-i18n-locale-type](adr-i18n-locale-type.md)、[ADR-i18n-catalogs](adr-i18n-catalogs.md)）。

## 考虑过的选项

### 由 respond 子包交付 `ErrorResponder`，`Problem` 增 `Key` 与 `Args`

本能力目录的 `respond` 子包交付 `http.ErrorResponder`，读请求语言、按 `Problem.Key` 取消息、用 `Problem.Args` 插值，渲染出 `Title`。

**否决**：`ErrorResponder` 决定错误响应的结构，而结构与语言分属两层（[ADR-i18n-rendering-ownership](adr-i18n-rendering-ownership.md)）；一个决定结构的扩展点同时承载语言，一个只想改结构的模块就被迫连语言一起改。`Key` 与 `Args` 是本地化输入，把它们放进 `Problem` 这个线上契约，是把本地化漏进 wire 格式；而所有写出方都要记得填它们，编译器不强制。

### 把本地化内建进入口模块

入口模块自己读语言、查目录、渲染它写出的每一条错误文本。

**否决**：结构与语言的耦合仍在：一个决定结构的模块依旧承载语言。入口模块也不认识每条消息的插值上下文，把语言内建进去与它“不认识语言资源”的定位相悖。

### `Problem` 只承载成品文本，结构扩展点不带语言

`Problem` 到达结构扩展点时文本已经渲染完成；结构扩展点只决定写出的结构；产生错误的模块渲染它自己的文本。

**采纳**，见决策。

## 决策

采纳：错误响应只承载成品文本，结构扩展点不带语言。

- `Problem` 去掉 `Key` 与 `Args` 两个字段；`Title` 与 `Detail` 是已经渲染好的文本。
- `http.ErrorResponder` 只决定错误响应的结构：它不再收到本地化输入，`i18n` 也不再实现它。签名不变。
- `pkg/http` 渲染它自己写出的四类拒绝，从它自己声明的消息目录取文本，并把 `i18n.Localizer` 作为可选依赖取用；没有装配本地化器，或某个键没有目录时，拒绝回退到 `net/http` 的标准状态文本。它另取用可选的 `i18n.Resolver`，在默认响应结构下用 `Content-Language` 写出这一次响应的语言；退役的 `i18n.respond` 原先写这个头，本模块接管后一并保留。
- `pkg/i18n/respond` 退役：它为本模块写出的四类拒绝提供的文案（`http.404`、`http.405`、`http.413`、`http.500`）移进 `pkg/http` 自己的目录，其余 `http.<status>` 条目没有渲染点，未随之迁入；模块名从各示例宿主的模块名册移除。

## 后果

每个错误消息由产生它的模块渲染，`Problem` 到达结构扩展点时文本已经完成。一个只发机器字段（`Type`、`Status`）的模块不承载任何文本，`pkg/http` 按状态码回退到 `net/http` 的标准状态文本。

**接受的代价：**

- `pkg/http` 的公开接口发生减法变更：`Problem` 去掉两个字段，`surface_test.go` 的接口基准要随之更新；任何以 `Problem` 为值类型或比较它的下游测试需复核新字段。
- `pkg/http` 取用 `pkg/i18n` 的根包，两个功能目录之间新增一条 import 边；方向由“入口模块不 import 本能力目录”改为它 import 根包（[ADR-i18n-locale-type](adr-i18n-locale-type.md)）。
- 本地化只在交付了引擎的装配里生效；没有它时四类拒绝回退到 `net/http` 的标准状态文本，这一缺席不产生失败信号，与“可选依赖的缺失是合法配置”一致。
- `pkg/http` 的目录只承载它自己写出的状态码文本；别的模块产生的错误文本不在其中，那些模块各自承载自己的文案。
- `pkg/i18n/respond` 退役是对本能力目录公开接口的减法变更：交付它的宿主须把它从模块名册移除，退役的模块名从各示例宿主的清单删去。

## 失效条件

若错误响应的文本要随某类全局来源变化（例如按租户定制），而这不属于产生它的模块，则“产生它的模块渲染”不成立，要另立承载。

若 `pkg/http` 要渲染的拒绝文本超出“状态码到一句标准说明”，而需要参数或用户可见的上下文，则它自己声明的目录不够，要重新考虑承载。
