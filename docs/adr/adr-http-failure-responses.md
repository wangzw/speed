# 错误应答按公共集加覆盖声明，Fault 可返回也可 panic

## 背景

多数 API 的错误应答是公共的：未认证、无权限、依赖不可用由网关与入口模块在每个受守端点上写出，与具体 operation 无关。少数 operation 有自己的应答，或需要改写公共应答的描述与结构。手写片段把每个 operation 的错误应答逐条抄一遍，既重复又会漂移；类型化 operation 的模型需要把这层公共性表达出来。

另外两类事实必须一起考虑：

- **自定义错误处理。** 入口模块允许宿主交付 `ErrorResponder` 整体替换错误响应的结构。契约若不描述那个结构，就与实现不符。
- **panic。** 一部分错误由 handler 以 panic 抛出，由上层的恢复层捕获并写出应答；恢复层此前对所有 panic 一律写 500。

## 考虑过的选项

### 每个 operation 逐条声明错误应答

维持现状。

**否决**：公共应答重复且会漂移；类型化 operation 已有正式声明模型，没有理由把错误留在模型之外。

### 只保留宿主级 ErrorResponder

由 responder 决定一切错误结构，契约固定写 Problem。

**否决**：responder 改变了实际写出的结构，契约就成了谎言；而且公共应答仍是每个 operation 各写一遍。

### 公共集加覆盖，Fault 统一承载，结构成为声明

采纳，见「决策」。

## 决策

采纳：

- **公共失败集加按 operation 覆盖。** 模块声明一组 `Response` 作为它的公共失败集；`Failures(common, specific...)` 合并两者：同状态的具体应答替换公共应答，任一侧独有的状态也加入。合并结果是 operation 的封闭应答集，既是片段发布的内容，也是入口模块接受 handler 返回状态的范围。
- **`Fault` 是类型化的失败。** 它携带状态、Problem 类型、标题与详情，实现 `Failure`；handler 既可以返回它，也可以 `panic` 它。
- **恢复层识别已声明的 Fault panic。** 入口模块在调用绑定式 handler 之前，把该 operation 的应答状态集放进请求上下文；恢复层遇到携带 Problem 的 panic 时，只有该状态在集合里才按它写出应答，否则仍按缺陷写 500。没有声明集合的路由（旧的 gin handler，[REF-gin](../references.md)）上，Fault panic 也是 500。
- **错误结构是声明。** `ErrorShape{MediaType, Structure}` 描述入口模块实际写出的错误体；`ErrorResponse` 用它声明一条错误应答，默认结构是 RFC 9457（[REF-rfc9457](../references.md)）问题文档。宿主交付自定义 `ErrorResponder` 时，用同一个结构声明该模块的错误应答，片段因此描述真正会写出的内容。

## 后果

- 一个模块的公共失败只写一次；operation 用同状态的应答覆盖它，用新状态扩展它。
- handler 返回或抛出的失败只有在 operation 声明过时才改变状态码；未声明一律 500，契约与实现不会脱节。
- 自定义错误处理有了明确的配对方式：responder 决定写什么，`ErrorShape` 让契约跟着声明。
- 恢复层对 Fault 的识别以请求上下文里的声明集为界，旧的 gin 路由不受影响。

**付出的代价：**

- 公共失败集是模块级的 Go 值，每条都经 `Failures` 合并进每个 operation 的声明；生成器不单独发布它。
- Fault 的状态集在编译期不是闭集：返回或抛出未声明状态的失败要到运行期才答 500。
- `ErrorShape` 与宿主实际交付的 `ErrorResponder` 是否一致，框架无法核对；这是宿主声明与宿主实现之间的约定。
- 已声明的 Fault panic 是正常拒绝，不写缺陷日志；未声明的 panic 仍写缺陷日志并答 500。

## 失效条件

错误结构若需要在运行期按租户或调用方变化，静态声明不再成立，需要把结构挪进运行期配置并重新考虑契约发布。Fault 若被要求成为编译期的封闭状态集，`StatusCode() int` 的开放接口需要换成类型化的枚举。
