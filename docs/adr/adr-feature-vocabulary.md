# 特性名由二进制写出的 `contracts/features.json` 生成为 api-sdk 里的类型，前后端各有新鲜度测试

## 背景

前端在路由声明的 `requires` 与宿主的门控代码里写特性名。应答里出现前端不认识的名字可以忽略，集合成员查询本来向前兼容；反过来不行：前端写了一个后端永远不会发布的名字，运行期与「特性缺席」无法区分，表现为永久的 404。

特性文档只发布名字，渲染知识归前端（[ADR-feature-document](adr-feature-document.md)）。登录发现记录否决「名字数组加前端映射」的理由，是前后端各自维护同一套视图、缓慢漂移（[ADR-authn-login-method-discovery](adr-authn-login-method-discovery.md)、[ADR-web-auth-channel-discovery](adr-web-auth-channel-discovery.md)）；这份漂移在特性名上必须由机械手段挡住，并且要在编译期挡住。

约束：

- 声明模块经 `feature.Board` 的 `Post` 报告的名字若不是它声明的条件特性，发布者在 Start 让启动失败；任何一次可能的应答因此都是可用集声明的子集；一份按可用集写出的静态名字清单覆盖全部可能的应答。
- 示范的 `gen` 从同一个二进制的 import 闭包写出 `contracts/api.json`；`@speed/api-sdk` 由 `orval`（[REF-orval](../references.md)）从它生成，是前端唯一与后端契约耦合的包。
- `@speed/route-kit` 不 import 任何同族前端包。
- 宿主可以有自己的应用模块，它们也可能声明特性。

## 考虑过的选项

### 运行期遇到未知的 `requires` 即告警

**否决**：运行期分不清拼错与「本部署没装」，告警无从区分，起不到检查的作用。

### 只做测试比对，名字在代码里保持字符串

**否决**：测试只能比对登记在表里的名字，覆盖不到散落在控件里的字面量。

### 从片段归属生成模块名类型

由 OpenAPI（[REF-openapi](../references.md)）的 `x-fragments` 生成一个模块名的联合类型。

**否决**：发布单位不是模块名（ADR-feature-document）。

### 从 `api.json` 生成 `operationId` 类型，并加一个契约检查目标

**否决**：发布单位不是 `operationId`（ADR-feature-document）。

### 生成类型放进独立的生成包

例如 `@speed/feature-names`。

**否决**：为一个类型多出一个发布包，别名清单、`tsconfig.json` 路径与锁定版本组都要多一项，而它与 api-sdk 耦合的是同一份契约。

### 生成类型放进 `@speed/auth-core`

**否决**：auth-core 是手写的会话包，混进契约产物；admin-ui、notification-ui 为一个类型要绕道 auth-core。

### 把名字枚举注入合并后的 OpenAPI，由 `orval` 一并生成

**否决**：片段是静态的，枚举只能在 `gen` 里注入；ops 在运行期合并的文档会与 `gen` 写出的不一致。

### 二进制写出名字清单，api-sdk 从它生成类型，前后端各有新鲜度测试

**采纳**，见决策。

## 决策

采纳：

- `gen` 接受输入项 `--features`，写出 `contracts/features.json`：可用集里的全部特性声明（名字、声明模块、是否条件），按名字排序。它与 `contracts/api.json` 同源，出自同一个二进制。
- 一个生成器由它写出 `@speed/api-sdk` 的 `src/features.ts`，经子路径 `@speed/api-sdk/features` 导出；平台包只登记所有者为平台的声明；示范宿主自己的模块可以声明特性，其项在 `contracts/features.json` 里标为应用所有，由应用内的模块增补承载。导出的形状：`FeatureName` 是一个可增补的登记接口的键类型，`FEATURE_NAMES` 是平台包登记的全部名字。api-sdk 仍是整份由契约生成、不接受手工编辑的包。
- 前端写特性名的位置一律取 `FeatureName` 类型：auth-core 的 `useFeature`、管理后台与通知页面声明的 `requires`、`AdminShell` 的特性判定。`route-kit` 自己的 `requires` 仍是 `string[]`。
- 新鲜度与类型检查：Go 测试比对已提交的 `contracts/features.json` 与本二进制的声明；api-sdk 测试比对 `features.ts` 与 `contracts/features.json`；api-sdk 与 auth-core 各有一条「拼错的名字必须编译失败」的类型断言；示范测试断言路由表里每个 `requires` 都属于 `FEATURE_NAMES`。全部随 `make check` 运行。
- 应用自己的特性名——应用的 `contracts/features.json` 里不在平台包 `FEATURE_NAMES` 中的名字——由生成器在应用内写成对登记接口的模块增补，并入同一个 `FeatureName`。

## 后果

拼错的名字编译不过；后端改名或删名而前端没有跟上时，新鲜度测试变红。

**接受的代价：**

- 契约再生成链多一步：后端声明每变一次，都要重新生成并提交 `contracts/features.json` 与 `features.ts`。
- api-sdk 的生成源除 `contracts/api.json` 之外还有 `contracts/features.json`，各有各的生成器，再生成时须一并运行。
- 凡经 `tsconfig.json` 路径把 `@speed/api-sdk` 解析到源码的包，以及示范应用与别名清单，都要为子路径多一个条目。
- `route-kit` 不认识特性名；宿主若用类型断言绕开带类型的声明，只能由示范的成员断言兜住，不跑这条测试的宿主没有这一层。
- 编译期类型证明的是「这个名字在可用集里有声明」，不是「本部署服务它」；「本部署服务它」仍只在运行期由特性文档回答。
- `contracts/features.json` 的每一项要带所有者，生成器据此分流：所有者由 `gen` 按声明模块的归属机械写出，写错会把应用名带进平台包，由 Go 侧一条按已知模块逐个核对所有者的测试与平台包一侧断言它不含应用名的测试共同守住；所有者按声明所在的包判定，经平台导出的工厂构造的宿主模块按平台所有。
- 一份前端构建对应一个二进制的声明：前端若要同时对接 import 闭包不同的多个二进制，`FeatureName` 只覆盖生成它的那一个。

## 失效条件

- 一份前端产物常态性地对接多个 import 闭包不同的二进制，单份生成类型不再能代表全部可能的应答，要换成合并多份清单或在运行期校验。
- 应用级特性名在运行期才能确定、无法经模块增补写进类型，类型落点要重审。
