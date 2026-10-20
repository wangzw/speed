# 契约文档承载分组、鉴权与顶层元数据，访问级别由 operation 声明

## 状态

已接受

## 背景

合并后的文档此前只有 `openapi`、`info`（标题与版本）、`paths`、`components` 与两个扩展键。一份给外部消费者读的契约缺少三类信息：operation 没有分组，读者只能按路径前缀猜测归属；没有 `securitySchemes` 与 `security`，读者无法知道哪条 operation 需要凭据、需要哪一种；顶层没有 `servers` 与 `info.description`，文档不说它描述的服务在哪里、是什么。生成的组件里也有一批只有字段说明、没有组件说明的类型。

访问级别在运行期由网关逐条决定：模块把路由登记为豁免、可选或受保护，默认受保护。这份事实此前只存在于挂载调用里，operation 声明不携带它；若生成器另写一份，两者会各自维护、各自漂移，而漂移的方向之一（文档说受保护、实际豁免）会让读者低估暴露面。

## 考虑过的选项

### 在生成器里按 operationId 维护一张访问级别表

**否决**：这张表与挂载代码是同一事实的第二份副本，而且没有任何检查会发现两者不一致；声明与运行期的关系正是本轮要消除的。

### 由合并器按路径前缀推断访问级别

**否决**：普通面同时承载未认证的登录流程与需要身份的自身资源，前缀说不出区别；管理面也有可选认证的路由。推断在两头都错，且错误无声。

### 把访问级别做成 operation 声明的字段，挂载从同一字段派生

`http.Operation` 与 `http.ReplyOperation` 增加 `Access`，零值为受保护；构造选项 `Public()` 与 `Optional()` 覆盖它。认证能力的挂载入口 `mount.Declared` 读描述符、按字段分派到网关的三种登记；生成器读同一字段写 `security`。

**采纳**，见「决策」。

### 让片段自己定义 securitySchemes

**否决**：载体是平台词表（`Authorization: Bearer` 与 `sid` cookie），由每份片段各写一遍会让同一载体出现多种拼法；合并器写一次，片段只引用名字。

## 决策

### 访问级别是 operation 声明的一部分

- `AccessRequired` 是零值：只接受已识别身份的请求。
- `AccessOptional`：不带凭据的请求以匿名身份通过；带了凭据但不成立的仍拒绝。
- `AccessPublic`：不读凭据。

生成器写出的 `security` 依次是：受保护的两个载体择一（`bearerAuth`、`sessionCookie`）；可选的在两者之外再加一个空要求；公开的是空数组。`security: []` 是 OpenAPI 表达「移除要求」的写法，因此公开必须显式声明。

认证能力的模块一律经 `mount.Declared` 挂载，它的级别来自描述符，不再由挂载调用另行指定。`Callback` 仍是独立入口：回调既公开又跳过跨源检查，只有登记方知道哪些方法来自别的站。

### 合并器补齐缺省与顶层元数据

- 片段里没有 `security` 的 operation 补上受保护要求：路由默认受保护，公开是显式声明。
- 每个 operation 的 `tags` 带上声明它的模块名；顶层 `tags` 汇总这些名字，按名字排序。
- `components.securitySchemes` 写出 `bearerAuth`（`Authorization: Bearer`）与 `sessionCookie`（`sid`，以及部署允许不安全用法时的无前缀写法）。片段自己声明同名载体时，内容必须一致。
- `Options` 增加 `Description` 与 `Servers`，分别写进 `info.description` 与顶层 `servers`；空值不写出。
- 合并结果按两个空格缩进输出，作为制品与运行时应答都更易读。

### 组件说明随类型声明

生成器读取类型上空字段的 `description` tag 作为组件说明，与字段说明并列；枚举类型通过 `Description()` 方法提供说明（与 `Enum()` 同一约定）。

## 后果

- 文档的鉴权要求与网关实际执行的级别来自同一字段，模块的挂载代码与片段不会互相漂移。
- 通过 `mount.Mounter` 的 `Public`/`Optional`/`Guarded` 仍可被测试代码直接调用；模块代码一律走 `mount.Declared`，新增模块须沿用它。
- 契约新增 `servers`、`tags`、`security`、`components.securitySchemes` 与 `info.description`，以及每个 operation 的 `tags` 与 `security`；读取这些键的一方（网关、前端生成器、文档站点）获得信息，不读取的一方不受影响。
- 一条 operation 只带一个标签。宿主写制品时可以把它声明模块的标签换成自己的所有者标签；`gen` 对应用所有的操作这么做，并从顶层 `tags` 摘掉该模块名，按标签分组的读者因此不会把同一条 operation 列两次。
- 合并文档改为缩进输出，字节内容变化，`/openapi.json` 的应答体随之变大；合并仍是确定性的纯函数，构建产物与运行时应答仍逐字节一致。
- 公开路由此后必须显式声明，新增一条未声明 `security` 的操作会以受保护的身份进入文档——错误方向是低估可达性而不是高估保护。
