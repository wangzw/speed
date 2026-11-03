# 账户恢复经 `AccountEnabled` 钩子观察，与停用同一条提交后同步调用

## 背景

账户恢复之后，随停用一起停下的东西要跟着恢复（REQ-tenancy-044）。账户模块在 `SetStatus` 提交之后调用 `AccountDisabled`，账户转回启用时一个钩子都不调；钩子集合里没有与停用对称的那一个（[ADR-authn-account-lifecycle](adr-authn-account-lifecycle.md)）。

钩子的契约是同步的：账户事务提交之后调用全部实现，每个实现幂等，失败让账户调用返回 `ErrCleanupIncomplete`，状态已经生效，重复同一次调用补完。调用来自管理面的写请求，管理员当场看得到失败并可以重试。

账户事件在提交之后发布：发件箱在场时至少一次、同一 `Event.ID`，缺席时尽力而为；经发件箱投递的事件在任何一种总线上交给订阅方的是中继的上下文，不是发起那次变更的上下文（[design-authn-account](../design/modules/authn/design-authn-account.md)、[design-audit](../design/modules/audit/design-audit.md) 的事件级通路）。

`AccountLifecycle` 是根包的公开接口，实现者在账户模块之外：会话、多因素、短信与授权各自实现，宿主也可以实现。增删方法与改签名是破坏性变更。

## 考虑过的选项

### 订阅 `authn.account.status-changed` 观察恢复

宿主订阅账户状态变更事件，事件里转回启用时把租户恢复。

**否决**：投递是异步的，装配里既没有发件箱也没有总线时那次事件根本不发，租户永远不恢复而没有任何一处报出；失败不会回到发起启用的管理员，`ErrCleanupIncomplete` 在恢复这一侧没有对应物；经中继投递的事件带的是中继的上下文，恢复动作因此没有实际操作者；订阅方还要按 `Event.ID` 自己去重。这几种性质与停用那一侧已经定下的同步语义不对称。

### 宿主下一次 ensure 时顺便恢复

个人租户的 ensure 读到停用的租户时，按账户状态决定是否重新启用。

**否决**：删除之后的收尾钩子不得读账户行（墓碑之外的内容已抹除）；ensure 对停用的租户按设计不写任何东西，运营者的停用正是靠这条不被推翻（REQ-tenancy-030）；要让 ensure 判断来源，它得先拿到账户状态，而租户模块不认识账户，方向会反过来。

### 账户模块为恢复新增一个公开方法

账户模块另交付一个「读某账户是否启用」的方法，宿主在需要时问它。

**否决**：它解决的是「怎么读」，不解决「什么时候恢复」。宿主仍要在每次进入租户区时轮询，恢复的时刻不确定，失败也没有回传的路径；同一个问题在停用那一侧已经由提交后钩子回答。

### `AccountLifecycle` 新增 `AccountEnabled`

钩子集合增加与停用对称的 `AccountEnabled`，`SetStatus` 在提交之后按目标状态调用。

**采纳**，见决策。

## 决策

采纳：`AccountLifecycle` 新增 `AccountEnabled(ctx context.Context, id AccountID) error`，契约与其余方法相同——提交之后调用、幂等、失败让调用返回 `ErrCleanupIncomplete`。不得读账户行这一条只约束删除之后的收尾钩子，即 `AccountDisabled`、`AccountEnabled` 与 `AccountDeleted`；`AccountCreated` 在账户创建提交之后调用，读得到账户行，不受这条约束（[ADR-tenant-administration](adr-tenant-administration.md)）。

**`SetStatus` 按目标状态调用对应的钩子，不论那一行是否真的改了。** 转停用调 `AccountDisabled`，转启用调 `AccountEnabled`；账户已经是目标状态时同样调用一次。失败之后重复同一次调用补齐清理，停用与启用因而都成立。

**不恢复任何东西的实现写空操作。** 会话、多因素、短信与授权持有以账户为键的数据，但启用不让其中任何一样回来：旧会话因 `valid_after` 不回退而不复活，因素与挑战留在原处，绑定不随停用消失。它们的 `AccountEnabled` 返回 nil。

**宿主用它恢复与账户同一生命周期持有的东西。** 示范的个人租户宿主在 `AccountEnabled` 里经 `SetStatusByAccount` 恢复租户，在 `AccountDisabled` 里经同一个方法停用它（[ADR-tenant-suspension](adr-tenant-suspension.md)），在 `AccountDeleted` 里做收尾（[design-starters](../design/modules/design-starters.md) 的个人租户一节）。

## 后果

- **公开接口多一个方法。** 每一个实现——本仓的会话、多因素、短信、授权，以及仓库之外的宿主实现——都要补上它，编译器是唯一的提醒。这是破坏性变更。
- **启用可能以 `ErrCleanupIncomplete` 结束。** 账户已经启用而某个钩子失败时，管理员拿到 503 并要重试同一次调用；停用那一侧本来就是这个结构，恢复这一侧从此对称。
- **一次没有改状态的启用也会调用全部钩子。** 管理员重复发起一次启用时，每个实现的空操作被再调一次；这是补齐失败清理的代价。
- **钩子集合仍是装配的结果。** 装配里没有个人租户模块时，恢复没有要做的事，钩子集合里也就没有它；纳入它之后，实现它成为该模块的义务。

## 失效条件

若持有以账户为键的数据的模块出现了启用与停用之外的另一种状态——例如一个锁定态——每多一种状态加一个方法的做法不再成立。判据是一个状态无法由「账户是否启用」推出，且它停止或恢复的东西不能由现有钩子重做。
