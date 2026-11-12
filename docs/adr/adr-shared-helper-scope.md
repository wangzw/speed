# 宿主辅助函数只抽第二个宿主真实复用的那一项进 pkg

## 背景

示例宿主各自是一个独立程序，各自装配模块。同一件事在每个宿主里各写出了结构相同的代码：把一个模块的 logger 从注册表取出、按模块名命名；把一条路由绑到端点；生成标识符、拼出键值键；用一张表登记本宿主的模块、按命令筛选、经产品的 logger 写释放记录。这些逐字相似之处引出抽取主张：把某个宿主辅助函数抬进 `pkg`，成为宿主之间共用的组件。

抽取的边界不清晰，主张的依据不统一：“看起来重复”是主观判断，而 `pkg` 的分层有硬约束。`pkg/core` 是每个模块都 import 的零第三方依赖公共基础设施（架构不变量 1），它不解释任何资源、不认识任何功能（不变量 2）。`pkg/internal` 下的共享单元是为多个实现子包共用第三方客户端与测试夹具而设（[ADR-shared-backend-unit](adr-shared-backend-unit.md)），不是宿主辅助函数的去处。抽取因此要有一条可核对的判据，被否的项也要留下记载，以免日后被当作漏项重提。

## 考虑过的选项

### 抽 `namedLogger` 进 `pkg/log` 作 `NamedFor`

一个宿主的 `namedLogger` 取 `(*log.Logger)(nil)` 能力、以模块名命名。第二个宿主的两个模块各写了一遍同样的 `core.Resolve[log.Logger](reg)` 接 `logger.Named(模块名)`。第二宿主检验通过。

**采纳**，见决策。

### 抽宿主模块表进 `pkg/core`

一个宿主的 `application`、`registerApplication`、`modulesOn`、`released`、`recordStop`、`recordClose`、`subtree`、`errForeignInstance` 构成一张宿主模块表：登记模块，按命令筛选它们，经产品的 logger 写释放记录。

**否决**：第二宿主检验不通过。第二个宿主的每个模块在自己的 `init` 里调一次 `core.ProcessRegistry.Register(...)`，没有这张表、没有按命令筛选的模块清单、没有经产品 logger 写释放记录的回调。它的区分性字段（`application.models` 与 `application.journalled`）只被第一个宿主自己的测试文件读，是宿主测试夹具的数据模型。把宿主测试夹具的数据模型抬进 `pkg/core`，与不变量 1（`core` 是零第三方依赖的公共基础设施容器）、不变量 2（`core` 不解释任何资源、不认识任何功能）都不相容。

### 抽 `mount` 进 `pkg/http`

一个宿主的 `mount` 把写在一条字符串里的“方法 空格 路径”拆开，绑到端点。

**否决**：第二宿主检验不通过。第二个宿主直接调 `public.Route(method, path, handler)`，不套这一层。`mount` 是对已有的单行 API（`speedhttp.Endpoint.Route`）的薄包装，抽它只是把一行调用改个名。

### 抽 `pkg/core/ids`（`newID`／`storeKey`／`keyDigest`）进 `pkg/core`

一个宿主的 `newID` 生成带前缀的随机标识符，`storeKey` 把键值键的分段拼起来，`keyDigest` 把任意长度的值折成定长分段。

**否决**：第二宿主检验不通过，第二个宿主里没有一处命中。框架另有既有的标识符口径（`pkg/event/id.go` 的 `NewID`、`pkg/authn/account/admin.go` 对 `uuid.NewV7` 的取用、`pkg/authz/admin/audit.go` 的 `uuid.NewString`），在同一层再添一套是发明而非收敛。`storeKey` 的 `crm:` 前缀替 `pkg/kv` 决定了一件它刻意不管的事：它只声明键是不透明字节（`pkg/kv/kv.go` 的包注释），对键的名字空间没有任何表述，拼一个键因此是调用方自己的事；`pkg/kv/module.go` 里的命名空间是实现自己的**配置**命名空间，与键的命名无关。

### 把重复的 `newActionID` 归拢

`pkg/authz/admin/audit.go` 与 `pkg/authn/admin/audit.go` 各定义一次 `newActionID()`，二者逐字相同。

**否决**：这处重复落在 `pkg` 内部，不适用第二宿主检验。重复本身存在，但它是 `return uuid.NewString()` 这样一行的函数，为它立一个共享单元、引一层间接，成本高于它消除的重复。记在这里，不改。

## 决策

采纳：**只把 `namedLogger` 抽成 pkg 组件，除它之外的候选不抽**。

- 抽出的组件是 `pkg/log.NamedFor`：取 `(*log.Logger)(nil)` 能力，返回以模块名命名的 logger。宿主里的 `namedLogger` 删除，两个宿主都改调它。
- 抽取的判据是**第二宿主检验**：一项声称“被重复”，第一句就问第二个宿主有没有它：把符号在第二个宿主里 grep 一遍，有命中才算被复用，据此才谈抽取。
- 立一项的依据必须属于**重复型**（第二个宿主真实写了同一件事）、**已声明未实现型**（某处已声明该组件而代码未实现）或**需求点名型**（既无第二使用方、也无已声明的缺口，唯一依据是用户明确要求它）。依据不属于这三类的提案不立项；被否的项以本条记录为准，不当作漏项重提。

## 后果

宿主侧那一处重复消除：模块的 logger 怎么取、怎么命名，只有一处实现。

`pkg/log` 的公开接口多一个函数，抽取不新增依赖边：`pkg/log` 已经 import `pkg/core` 并导出 `Module() core.Module`（`pkg/log/module.go`），`NamedFor` 不使这个包的公开接口首次出现 `core` 类型，也不使 `pkg/log` 新依赖 `pkg/core`。

**接受的代价：**

- 其余宿主辅助函数仍各写各的。第二宿主检验不是一次性的：一个新辅助函数出现时，同一个检验要对它重跑一遍，在第二个宿主里找不到使用方的都不抽。
- `NamedFor` 把“模块的 logger 以模块名命名”定为框架层面的约定；一个要以别的名字命名自己的 logger 的模块不再能用它。
- `pkg/core` 不承载任何宿主辅助函数；宿主模块表、`mount`、标识符与键构造留在宿主，读者遇到它们时看不到抽取的依据：本记录即是那份依据。

## 失效条件

- 若第二个宿主长出某一被否项的第二个使用方（例如一张按命令筛选的模块表，或一处 `storeKey` 式的键构造），该项的否决依据即失效，应重新评估。
- 若框架择定一套统一的标识符口径并替掉现有的几套，`pkg/core/ids` 一项的否决理由（在同一层再添一套是发明）随之改变，可以重提。
