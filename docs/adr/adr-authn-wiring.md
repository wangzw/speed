# Requires 只写排他功能，并立功能一律在 Init 里 ResolveAll

## 背景

`pkg/authn` 是一个功能目录：每个独立交付的功能一个叶子包、一个模块（[ADR-authn-packaging](adr-authn-packaging.md)）。模块之间的取用方式由 `core` 的规则决定，这些规则是本决策的前提：

- 声明了 `Requires` 的模块，其依赖闭包把所需功能的提供者纳入参与集；排他交付的提供者除外，闭包只取命令树上已点名的实现（[ADR-dependency-resolution](adr-dependency-resolution.md)）。标注 `Optional` 的需求不拉入任何提供者，提供者在场时仍参与构造排序。
- `Requires` 在任何模块构造之前校验：必需的功能没有提供者即 `core.ErrMissingProvider`，此时驱动除预置实例外没有构造任何东西，没有要回滚的内容。
- `Init` 运行时全部实例均已构造完毕，`ResolveAll` 整体取出某个功能全部已有的实例（[ADR-lifecycle-stages](adr-lifecycle-stages.md)）。同一处缺失若留到 `Init` 判定，`New` 与 `Migrate` 已经执行，`Migrate` 施加的数据变更不在回滚范围内。
- 被运行级纳入的模块必须启动，没有「表态禁用」这一档；经依赖闭包拉进参与集的模块同样必须启动，它的必填配置随之被判（[ADR-module-enablement](adr-module-enablement.md)）。
- 路由登记面的写入窗口借用 `Migrate` 与 `Start` 的阶段位，框住全局 `Init` 阶段，构造期不接受写入（[ADR-http-routing](adr-http-routing.md)）。
- 施加迁移的命令不运行带表模块，只需要它们的迁移声明；静态级纳入让声明进入收集范围，不构造、不表态、不判必填（[ADR-command-tree-inclusion](adr-command-tree-inclusion.md)）。

认证目录里的功能按排他性分为排他功能与并立功能。排他功能必须恰有一个提供者，由提供方在 `Provides` 里声明，例如网关、账户、会话、令牌、因素编排、冒充，以及租户准入层交付的 `ScopeGuard`。并立功能由宿主在命令树上挑出任意多个，各自一个模块、一律不排他，例如 `Verifier`、`LoginMethod`、`Factor`、`SSOMetadataRefresher`、`AccountLifecycle`、`ClaimsContributor`。完整名单由设计文档 design-authn 的模块清单承载。

哪些校验后端、登录方式与因素在一次运行里生效，决定了这个进程能发出什么身份，因此是宿主在命令树上写下的选择，不是 import 图的结果。

认证模块之间还存在相互取用，部分方向成环：会话可选地取用冒充，而冒充必需会话；账户在停用与删除之后通知各模块清理，而会话与因素编排都必需账户。

## 考虑过的选项

### 并立功能也写进 Requires

消费方把 `Verifier`、`LoginMethod`、`Factor` 等并立功能写进 `Requires`，缺任何一个都在构造之前报提供者缺失。

**否决**：并立功能的提供者不是排他交付，依赖闭包照常展开，凡被 import 进来的提供者全部进入参与集，而被纳入的模块必须启动。宿主 import 了某个登录方式却没有在命令树上列举它，只要有消费方把该功能写进 `Requires`，这个登录方式就被闭包强制纳入并要求配齐配置，命令树上的列举失效，一个宿主没有列举过的登录方式因此能够发出身份。同一条规则下，账户把 `AccountLifecycle` 写进 `Requires` 还会与会话、因素编排对账户的依赖成环。

### 并立功能写成 Optional 的 Requires

并立功能同样写进 `Requires`，标注 `Optional`，使闭包不替宿主纳入，缺席也不是错误。

**否决**：闭包不拉入 `Optional` 的提供者，这一点不构成否决理由。否决的理由是：`Optional` 陈述缺席是合法配置，而校验后端、登录方式在引擎或网关里一个都没有时是启动失败，描述符会陈述一件与事实相反的事；在场的 `Optional` 提供者还构成排序边，而认证模块之间的相互取用有成环的方向：会话对冒充写 `Optional`，冒充又必需会话，装配即 `ErrDependencyCycle`。`Init` 运行时全部实例已存在，取用不需要任何构造序，声明换来的只有环的风险。

### 排他功能也全部留到 Init 取

沿用网关的做法，整个目录的 `Requires` 恒为空，排他与并立功能一律在 `Init` 里取，取不到即启动失败。

**否决**：排他功能的闭包不替宿主挑选，写进 `Requires` 不会使命令树上的列举失效，而「宿主漏点名」是否在构造之前暴露，取决于这一处声明。留到 `Init` 判定，失败发生在 `New` 与 `Migrate` 之后，不可回滚的数据变更已经施加。对路由登记面的取用是这条理由下仅有的例外，见决策中网关一条。

### 由宿主把后端直接交给消费方

宿主在 `main` 里自己构造登录方式与校验后端，经构造参数交给网关与登录引擎，不经模块机制取用。把宿主构造的实例预置到注册表不属于这一条：预置实例是某个已登记模块的实例，消费方取到它的途径仍是 `Init` 里按功能取用（[ADR-supplied-instance](adr-supplied-instance.md)）。

**否决**：后端要读自己的配置命名空间、要依据配置表态、自带表的要声明迁移，这些时机都因它是模块才存在。绕开模块机制交给消费方的实例没有这些时机，宿主要为每个后端自行重写一遍。

### 模块按配置表态 StateAuto

模块在 `Prepare` 里读自己的配置，必填项齐备则启用，否则让出。

**否决**：`StateAuto` 表达的是排他提供者对他人排他主张的相让，不表达运行与否；模块没有自行让出的一档（[ADR-module-enablement](adr-module-enablement.md)）。是否运行是命令树上的决定，同一份命令树因配置不同装配出不同的登录方式集合，意味着认证面随部署漂移。

### Requires 只写排他功能，并立功能一律 ResolveAll

必须在场的排他功能与 `db.Database` 写进 `Requires`；日志、设置、事件、审计、`MeterProvider` 等缺席合法的功能写 `Optional`；并立功能与缺席合法的排他功能一律在 `Init` 里取。

**采纳**，见决策。

## 决策

采纳：认证模块的 `Requires` 只写排他功能与 `db.Database`，并立功能从不写进 `Requires`，由消费方在 `Init` 里 `core.ResolveAll`。

- **`Requires` 的内容。** 本目录每个模块的 `Requires` 只含它必需的排他功能与 `db.Database`；并立功能（判据是提供方不排他交付，例如 `Verifier`、`LoginMethod`、`Factor`、`SSOMetadataRefresher`、`AccountLifecycle`、`ClaimsContributor`）一律不写，哪怕消费方缺了它就无法工作。并立功能的零提供者由消费方在 `Init` 里用自己的哨兵拦下，不降级为放行（哨兵见设计文档 design-authn-failure）。宿主漏点名某个排他功能，在任何实例构造之前以 `core.ErrMissingProvider` 报出，早于迁移。
- **缺席合法的功能写 `Optional`。** 日志、设置、事件、审计、发件箱与 `MeterProvider` 在多数模块里写 `Optional`：缺席时对应的子功能不启用并在启动诊断里写一行，不是启动失败；在场时仍参与构造排序，持有秘密的模块因此能在 `New` 里先登记脱敏规则，再取日志器。缺席合法的排他功能（例如冒充对 `Decider` 的取用，缺席即全拒，见 [ADR-authn-decision-seam](adr-authn-decision-seam.md)）不写 `Requires`，在 `Init` 里按功能取用。
- **必需的判据。** 一个功能缺席会使某条安全相关路径无声变弱、而不只是少一项功能时，消费方把它写成必需：该功能的缺席不留到运行期才发现。每个模块的必需项由设计文档 design-authn 的模块清单给全，以下是判据的落点。`authn.impersonation` 把 `audit.Audit`、`MFA`、`ScopeGuard` 当必需：没有审计，强制档审计无处写、动作不能执行；没有因素编排，没有账户满足发起者的强认证要求；没有租户准入层，冒充无法限定在一个租户内（[ADR-authn-impersonation](adr-authn-impersonation.md)、[ADR-tenant-admission](adr-tenant-admission.md)）。`authn.login` 把 `MFA` 当必需：库里已有账户登记了因素、装配里却没有 MFA 模块时，登录会无声跳过第二因素（[ADR-authn-mfa](adr-authn-mfa.md)）。`authn.admin` 把 `audit.Audit` 当必需，其余能力缺席只少一项功能：`authn.admin` 的每组路由随对应能力在场才挂，启动诊断写出挂了哪几组。
- **网关。** `authn.gate` 的 `Requires` 为空，只对 `log.Logger` 写 `Optional`；路由登记面、全部 `Verifier` 与可选的 `ScopeGuard`、`MeterProvider` 在 `Init` 里取。网关只交付 `Gate`；`mount.Mounter` 由 `authn.mount` 交付，`authn.mount` 的 `Requires` 含 `Gate`（理由见 [ADR-authn-packaging](adr-authn-packaging.md)）。取不到路由登记面，或取出的校验后端为空，都在 `Init` 里判定，都是启动失败，都不降级为放行。网关与 `authn.mount` 对路由登记面的取用，是排他功能留到 `Init` 取的例外：路由登记面是排他功能，写进 `Requires` 本可以使缺失提前暴露；这两个模块的 `New` 用不上登记面，没有需要排的构造次序，路由登记面又不在构造期接受写入，取用点因此留在 `Init`，缺失的失败时点由后果一节记为代价。
- **账户生命周期钩子不构成依赖边。** `authn.account` 在 `Init` 里 `ResolveAll` 取全部 `AccountLifecycle`；`authn.session`、`authn.mfa` 交付钩子并必需账户，方向相反的 `Requires` 会成环；`authn.sms` 交付钩子，不依赖账户，由账户在 `Init` 里取用。某个模块没有纳入时，它的表不存在、钩子不在，账户一侧不为它做清理，由 `valid_after` 兜底（[ADR-authn-account-lifecycle](adr-authn-account-lifecycle.md)）。
- **表态。** 本目录每个模块的 `Prepare` 只表态 `StateEnabled`，永不 `StateAuto`：是否运行是命令树上的决定，不是配置能推断的结论。
- **迁移命令。** 施加迁移的命令静态级纳入全部带表的认证模块（`IncludeAllDeclarations`）。漏写一个，该模块的表不建且不报错；带表模块在 `Start` 里探测自己的表，探测不到即 `ErrNotMigrated` 终止启动，由它兜住这类漏写。
- **宿主一侧。** 每条需要认证的命令逐个写出所需模块的名字；不把任何认证模块放进根命令（根命令纳入的模块对每条子命令都有效）；不对纳入认证的命令使用 `IncludeAll()`。

## 后果

进入参与集的并立功能，是宿主在命令树上运行级纳入的那些，加上预置了实例的那些。被 import 而既未列举、也没有预置实例的校验后端、登录方式与因素不参与这次运行，一个宿主没有列举过的登录方式不会发出身份。

排他功能的漏列在构造之前暴露，早于迁移；跨目录的必需依赖（冒充必需的 `ScopeGuard` 由 tenant 目录交付）同样如此，没有租户准入层的装配不能纳入冒充。

模块之间经 `Init` 里的取用形成的关系不出现在描述符里，只能从代码中读出，与环上的引用改在 `Init` 动态取得所付出的代价同类（[ADR-lifecycle-stages](adr-lifecycle-stages.md)）。

**接受的代价：**

- **并立功能漏列没有失败信号。** 哨兵只在零提供者时触发，漏列一个校验后端而另有后端在场，"少了一个"与"本来就只有这些"区分不开；该后端认领的 scheme 与 cookie 无人认领，携带它们的请求一律 401（[ADR-authn-credential-dispatch](adr-authn-credential-dispatch.md)）。漏列一个登录方式，则该方式的入口不存在。模块机制那一侧也不主动告知：未被本次命令纳入、也没有预置实例的模块不进启动诊断。认证自己的启动诊断列出生效的校验后端、登录方式与因素种类，漏列只能靠读诊断发现；个别缺失由各自的哨兵拦住，例如库里已有账户登记了某个种类而该种类没有实现因素。
- **取用点在 `Init` 与启动期的失败发生在 `Migrate` 之后。** `ErrNoRouter` 与 `ErrNoVerifier` 在 `Init` 判定，`ErrNoLoginMethod` 与 `ErrNoFactor` 在启动期判定，具体阶段由设计文档 design-authn-failure 给出；这些失败发作时 `New` 与 `Migrate` 已经执行，`Migrate` 施加的数据变更无法回滚；路由登记面是排他功能，网关把它留在 `Init` 取，同样承担这一点。
- **漏写带表模块的迁移在施加迁移时不报错。** 迁移命令漏纳一个带表模块，表不建，直到服务命令在 `Start` 里以 `ErrNotMigrated` 终止才暴露。
- **宿主要为必需的依赖多纳入模块。** 只要口令登录而不要第二因素的宿主也必须纳入 `authn.mfa`；用冒充的宿主必须纳入 `tenant.admission`。模块数量多，命令树里逐个点名。
- **收益取决于宿主逐一列举。** 一条纳入全部可用模块的命令，或在根命令上纳入认证模块，或不写命令树、经 `Run` 纳入全部已登记模块的调用方（[ADR-drive-entry](adr-drive-entry.md)），都使参与集重新成为 import 图的函数，这道闸自行让开。
- **缺席合法的功能在缺席时静默降级。** 没有事件总线则不发事件，没有设置则取默认值；降级只在启动诊断里留一行。

## 失效条件

依赖闭包的展开规则变化时，本决策需要重议：

- 闭包若开始为必需功能自动纳入排他交付的提供者，排他功能写进 `Requires` 不再保证宿主在命令树上选择实现，并立功能与排他功能的区分不再是写与不写 `Requires` 的依据。
- 闭包若不再按功能展开并立功能的提供者（例如依赖声明可以指向具体模块，或按启用结果展开），并立功能可以写进 `Requires`，把零提供者的失败提前到任何实例构造之前，同时不牺牲宿主的选择。

以下行为是未经运行核实的假设，依赖它们的断言以对应的测试守住，测试失败即视为本决策的前提不成立：

- 假设 `authn.account` 在自己的 `Init` 里 `ResolveAll[AccountLifecycle]` 能取到 `authn.session`、`authn.mfa`、`authn.sms` 已构造的实例。装配测试用真实注册表纳入这些模块，断言取到的钩子齐全。
- 假设 `authn.gate` 对 `ScopeGuard` 的可选取用，不与准入层在链上的位置（阶段与阶段内的 `After`，只定链序）构成构造环。装配测试用真实注册表纳入网关与准入层，断言构造成功且链序正确。
- 假设本目录每个模块交付的能力接口都与声明一致。每个模块在包内有 `var _` 编译期断言，装配测试用真实注册表跑一遍构造后的交付核对。
