# tenant.admin

## 职责

`tenant.admin` 是租户名册的 HTTP 管理面，挂在固定基路径 `/api/v1/system/tenants` 下，不交付任何功能接口，只把名册的读写暴露成路由。每条路由的动作是经 `tenant.Catalog` 调名册——登记、读回、改别名、改状态——模块自己不持数据、不持规则：名册由名册模块拥有，本模块只决定谁可以问、答案长什么样。

**本模块是框架为名册提供的那个提供者。** 租户能力本身不提供管理面：名册的写入是它唯一的运维面动作，而它交付的是接缝与实现，不是 HTTP 路由（[design-tenant](design-tenant.md)）。一个想要名册管理面的宿主把本模块写进命令树，不必自己手写同一组路由、同一套判定与同一条审计前置。它不改变租户能力的立场——`pkg/tenant` 仍不带管理面——只把这一面从「宿主自建」补成「框架提供一个可选用的提供者」（[ADR-console-tenant](../../../adr/adr-console-tenant.md)）。

路由挂在配置 `tenant.admin.endpoints` 列出的端上，基路径不做可配置。每条路由按标识寻址，路径里出现的只有租户标识这个自然键。

### 非职责

- **不持有名册。** 名册表、它的迁移与它在内存里的快照归名册模块；本模块不建表、不缓存、不迁移，每个动作都是一次 `tenant.Catalog` 调用。
- **不持有租户的规则。** 标识与别名的取值域判据取自根包的 `tenant.IsIdentifier`，状态的闭集取自根包的 `tenant.Status`，别名冲突与重复登记的判定在名册（`tenant.ErrAliasConflict`、`tenant.ErrTenantExists`、`tenant.ErrInvalidTenant`）；本模块只把取值域当形状检查，不重新判断一个标识或一个别名是否合法。
- **不为请求认定租户。** 一条请求属于哪个租户由解析层认定（[design-tenant](design-tenant.md)）；本模块在平台域判定（见「访问控制」），不读请求解析出的租户，也不写上下文里的租户。
- **不给自己授予任何权限。** 本模块向进程声明它自己的权限，但把每条路由登记为开放，让中间件把请求放行到 handler，由 handler 在内部判定。
- **不管理租户内的账户、成员与角色。** 账户与成员关系归认证，角色归授权（[design-tenant](design-tenant.md) 的非职责、[design-authz](../design-authz.md)）。
- **不导出、不删除、不搬迁租户数据，也不提供删除路由。** 名册上的删除是终态标记，由账户生命周期的宿主模块写入；本模块照此只暴露 `SetStatus`，把已删除的行渲染成只读，不提供写进那个状态的路由，也不触碰受约束表里那个租户的数据（[design-tenant](design-tenant.md)）。

## 外部接口契约

### 登记

`tenant.admin` 不交付任何功能接口（`Provides` 为空）。它 `Requires` 名册功能 `(*tenant.Catalog)(nil)`、handler 内判定 `(*guard.Guard)(nil)`、审计 `(*audit.Audit)(nil)`、注册验证状态读面 `(*authn.SignupVerification)(nil)`、写保护 `(*tenant.WriteGuard)(nil)` 与首任管理员的建立 `(*tenant.Provisioner)(nil)`，并可选取 `log.Logger`。名册与 guard 各由唯一模块排他交付，命令的闭合不把它们拉进来，宿主在命令树上写下它们。表态是 `StateEnabled`、永不 `StateAuto`：挂不挂这面管理面是一个命令树上的决定，不是一个配置能推出的结论。HTTP 入口点与路由声明面在 `Init` 里取用而非声明——一个挂路由而没有入口点的命令是要在接线期报出的故障，而声明面的缺席是合法的：没有 gate 时路由不带声明，受守护的端把它们当未声明处理，所以挂这面路由的命令应同时点名授权模块。

**本模块声明静态[特性](../../../glossary.md) `tenant.admin`。** 描述符的 `Resources` 带 `feature.Spec{Name: "tenant.admin"}`；这个特性在场，表示本进程挂出名册管理面的路由（[feature](../design-feature.md)）。`tenant.admin.endpoints` 必填、不接受空清单，挂路由而没有入口点是接线期故障，所以模块进入启用集、进程有路由器时这组路由无条件挂出，声明为静态特性符合架构不变量 18（[architecture](../../architecture.md#架构不变量)）。

### 路由

基路径 `/api/v1/system/tenants`，下表是路由的全集（封闭集）：组、方法、路径与每条需要的权限。

| 组 | 方法 路径 | 权限 |
|---|---|---|
| 租户 | `GET /api/v1/system/tenants` | `tenant.read` |
| 租户 | `POST /api/v1/system/tenants` | `tenant.manage` |
| 租户 | `GET /api/v1/system/tenants/:tenant` | `tenant.read` |
| 租户 | `PATCH /api/v1/system/tenants/:tenant` | `tenant.manage` |
| 租户 | `POST /api/v1/system/tenants/:tenant/status` | `tenant.manage` |

每条路由的动作是一次 `tenant.Catalog` 调用（方法集与各方法的语义见 [design-tenant](design-tenant.md)）：

| 路由 | 名册动作 |
|---|---|
| `GET /tenants` | `List`，列出全部租户，按标识排序 |
| `POST /tenants` | `Provisioner`，建立租户与它的首任管理员 |
| `GET /tenants/:tenant` | `Get`，按标识取一个租户 |
| `PATCH /tenants/:tenant` | `SetAlias` 换别名 |
| `POST /tenants/:tenant/status` | `SetStatus`，改一个租户的状态 |

**登记走 `Provisioner`，一个租户与它的首任管理员一起建立。** 开一个租户不是一个孤立的插入：租户的创建者是它的第一个租户管理员，租户、创建者的成员关系与那条绑定必须一同成立。本模块把这件事交给宿主提供的 `tenant.Provisioner`，它按角色 → 授予 → 名册行 → 内置标记 → 创建者的成员关系与绑定的次序写：名册行先于管理员行，失败后留下的是 seed 能对账的租户，内置标记先于管理员行，标记失败时留下的是未冻结的角色而不是半份管理关系。标识以 `personal-` 开头的租户例外：宿主把它当作个人域，它的 `tenant-admin` 不置内置，宿主才能撤销允许集之外的授予。名册行之前失败时它清掉自己写过的策略行，路由答 503 且不留租户；名册行之后失败时租户留在册，路由答 503，绝不报成功——`MarkRoleBuiltin` 失败重试一次，仍失败即返回错误，租户在册而角色未冻结，创建者的成员关系与绑定未写（见「并发与事务边界」的已知限制）。`Create` 以 `ErrTenantExists` 拒绝是例外：那表示另一个副本先登记了同一个标识，该域属于先到者；创建者的成员关系与绑定写在名册行之后，后到的这次尚未写这两行，因而只留下先到者的角色、授予与管理行，既不拆它的策略，也不冻结它的角色。一个已被持有的标识由 `Create` 以 `tenant.ErrTenantExists` 拒绝，本模块把它答成 409。这是 `Create` 与 `Put` 的分工：`Put` 改写既有行、别名与状态一并取新值，`Create` 只插入、什么也不覆盖（[design-tenant](design-tenant.md)）。既有租户的改名走 `PATCH`，改状态走状态路由；状态路由只写启用与停用，删除是名册上的终态标记，本模块不为它开路由。

**别名改写经 `SetAlias`，不写状态。** `Put` 取整行新值——`alias` 与 `status` 一并写回（`pkg/tenant/catalog/roster.go` 的 upsert 赋值两列），所以一次改名会把该次读取到的状态写回去，把另一个副本上刚停用的租户重新启用。名册因此要有一条只改别名的写 `SetAlias`；本模块用它，不用 `Get` 后 `Put`。

**状态路由的可写集合是 `active` 与 `disabled`。** 正文给出其中之一，其余取值是本模块拒绝的形状；`deleted` 是终态，不在可写集合里。登记的新租户起于 `active`：登记的正文给出标识与别名，状态不在这里给。改名与改状态遇到已删除的租户，由名册以 `tenant.ErrTenantDeleted` 拒绝，本模块答 409。

**受保护的租户拒绝停用。** 状态路由在一次停用的形状检查之后、审计之前问 `tenant.WriteGuard`；答复为拒绝时答 403 `urn:speed:tenant:protected`，动作不执行。`system` 租户与个人租户由宿主的实现声明为受保护（[design-tenant](design-tenant.md) 的写保护）。启用不是写保护的写，不经过它。

### 访问控制

**判定由本模块在 handler 内做出，不经请求中间件的声明判定。** 管理面路由在中间件的声明表里全部登记为开放，好让中间件把请求放行到 handler；handler 先读调用方的身份，再经 `authz.guard` 的 handler 内判定 API 就这条路由的权限问一句，不足即回绝。模块不给自己授予任何权限。

**判定顺序固定，写与读在判定之后分叉。** handler 先读调用方：请求没有身份是 401，只有状态、没有 `Type`，并带与准入层、授权层同形的 realm 质询，不询问 guard 即返回。写路由随后读发起者的注册验证状态（见下），再向 guard 要本条路由的权限：不足即由 guard 写出它自己的拒绝——拒绝 403、故障 503——并返回。判定之后才分叉：写解析正文、跑本条路由的形状检查、落强制档审计、调名册；读调名册并应答。

**注册验证状态的门控在权限判定之前。** 写路由（登记、改名与改状态）在读完调用方身份之后、权限判定与解析正文、落强制档审计之前，读发起者的注册验证状态（`authn.SignupVerification.SignupStateOf`）：发起者读作未验证（`SignupPending`：有邮箱而没有一条已证明的）时答 403 `contact-required`（`authn.ErrContactRequired`），动作不执行；服务账户与没有任何邮箱的账户（`SignupNone`）豁免，读路由不受这条门控影响。判据与账户怎么创建无关，管理员预置与导入的发起者带着未证明邮箱时同样被拦。读取状态失败答 503，不按放行处理。门控只收紧、不放行：它只多拒绝一类发起者，不改变权限判定本身，也不让任何未通过权限判定的请求通过。门控在服务端执行，不依赖前端的入口隐藏。

**判定在平台域做出。** 名册是平台资产、不是某个租户的，所以一次名册操作是平台操作；handler 经 `RequirePermissionIn(c, perm, authz.PlatformDomain)` 显式指定平台域判定。若按请求解析出的租户判定，一个在某租户获授权的调用方就能靠请求头触到整个名册。**枚举也是平台操作**：`List` 读的名册表不是受租户约束的表，它本就在过滤之外，所以列出全部租户是一次平台域的读，不是一次跨租户读——它不建跨租户上下文，也不要求跨租户的权限。按 [ADR-http-api-path-scopes](../../../adr/adr-http-api-path-scopes.md) 的判据，名册路由虽然按租户寻址（`/api/v1/system/tenants/{tenant}`），但判定在平台域，因此留在 `tenant.resolve` 与 `tenant.admission` 两层之外，不挂在它们所守的端上。

**每次写强制档审计，落在动作之前、事务之外。** 写路由在调名册之前写一行强制档审计；审计写不进则应答 503 且动作不执行。名册拒绝写时再尽力写一行 `failed` 行，与允许行以动作标识关联，它自己的失败只记 Error，不改变应答。模块不开自己的事务。登记、改名与改状态各是一行强制档审计；读路由不写审计行。

### 视图

**读的应答是 200 加一份视图，登记写的应答是 201 加一条记录视图，其余落地的写是 204 无正文。** 视图逐字段从根包的 `tenant.Tenant` 构造，从不把值原样交给客户端，记录日后新增一个字段不会因为它在就等于出现在应答里。租户视图的字段：`id`、`alias`、`status`（`active`、`disabled` 或 `deleted`，由 `tenant.Status` 的字符串形式给出），以及 `disabledBy`（`operator` 或 `account`，由 `tenant.DisabledBy` 的字符串形式给出，只在状态是 `disabled` 时出现）。

列出全部租户的应答是 200 加全部租户的视图，按标识排序（名册自身的次序）。名册是一个部署所服务的租户集合，不分页。

### 宿主可见面

以下是宿主写进配置文件、或在命令树与网关上点名的面。

- **模块名**：`tenant.admin`。宿主的命令树点模块名纳入，配置命名空间取同一取值。
- **配置命名空间与输入项**：`tenant.admin` 下的 `endpoints`（字符串列表，必填，无缺省，只收具体端名、不收 `*`，取主配置源）。
- **管理端点**：`tenant.admin.endpoints` 列出的端，`/api/v1/system/tenants/*` 下的名册读写路由（路由见「路由」）。
- **权限**：读路由询 `tenant.read`，写路由询 `tenant.manage`；本模块的权限经 `authz.PermissionSpec` 进入进程目录、经 `authz.TranslationSpec` 进入进程翻译（`tenant.read` 译成 `(tenant, read)`、`tenant.manage` 译成 `(tenant, manage)`）。
- **问题应答**：`Problem.Type` 前缀 `urn:speed:tenant:`，其下的码 `bad-request`、`conflict`。写路由另在发起者为未验证账户时答 403 `contact-required`，这个码不属于该前缀。

### 对外接口面

- 模块名 `tenant.admin`、配置键 `tenant.admin.endpoints`、管理面路由的路径与每条路由询问的权限（读 `tenant.read`、写 `tenant.manage`），以及权限常量 `tenant.read`、`tenant.manage` 与 `Problem.Type` 前缀 `urn:speed:tenant:` 及其下的码（`bad-request`、`conflict`），是本模块对宿主的接口面。写路由在发起者为未验证账户时答 403 `contact-required`，同样是接口面。改动它们是宿主可见的破坏性变更。
- 租户视图的字段名（`id`、`alias`、`status`、`disabledBy`）与容器成形态是应答形状：宿主与前端据此读应答。`status` 的取值集合与 `disabledBy` 的取值集合同样是应答形状。
- 特性名 `tenant.admin`。前端经生成的特性名类型引用它，改名或撤去是宿主可见的破坏性变更。
- 本模块可替换、可撤掉，不改变调用方或名册的代码。

## 依赖

| 依赖 | 用途 |
|---|---|
| `pkg/tenant` | `tenant.Catalog`（名册的读写面）、`tenant.Tenant`、`tenant.ID`、`tenant.IsIdentifier`、`tenant.Status`、写保护 `tenant.WriteGuard`、首任管理员建立 `tenant.Provisioner` 与哨兵 `tenant.ErrTenantExists`、`tenant.ErrAliasConflict`、`tenant.ErrInvalidTenant` |
| `pkg/authz`、`pkg/authz/guard` | 权限声明与翻译（`authz.PermissionSpec`、`authz.TranslationSpec`）、handler 内判定（`guard.Guard.RequirePermissionIn`）、平台域 `authz.PlatformDomain`、路由声明面 `authz.Gate` |
| `pkg/audit` | 强制档审计 |
| `pkg/authn` | 读请求上的身份（`authn.FromContext`）与发起者的注册验证状态（`authn.SignupVerification.SignupStateOf`） |
| `pkg/http` | 路由挂载与问题应答（`speedhttp.Router`、`speedhttp.Fail`、`speedhttp.Problem`） |
| `pkg/config` | 声明本模块的输入项 |
| `pkg/core` | 模块描述符与功能取用 |
| `pkg/feature` 根包 | 声明静态特性（`feature.Spec`）；根包只依赖标准库 |
| `pkg/log` | 可选：记诊断 |

**本模块可撤掉，不改变调用方或名册的代码。** 它 import `pkg/authz`、`pkg/audit`、`pkg/authn`、`pkg/http` 来挂一条受保护的管理面，是租户能力目录里唯一这样做的一层；名册与解析本身不因它背上这些依赖。这一形态与 `sms.admin` 相同（[design-sms](../design-sms.md)）。

**依赖方向满足架构的不变量。** `pkg/tenant` 根包只依赖标准库，不 import `pkg/authz`（[design-tenant](design-tenant.md)）；本模块 import `pkg/tenant` 的根包与 `pkg/authz`，而 `pkg/authz` 也 import `pkg/tenant` 的根包，这不构成环——判据是 `pkg/tenant` 根包不 import `pkg/authz`。本模块落在依赖图的上层，是一个叶子。

## UML 图

本模块不持状态、不做判定之外的分支，它的结构是一串跨模块的调用。图固定一次写路由的操作序：读调用方 → 读注册验证状态 → 向 guard 要权限 → 解析正文与形状检查 → 落强制档审计 → 调名册 → 映射拒绝。图上看不出的是这些约束：判定在**平台域**做出，不是请求解析出的租户；写路由在权限判定之前、审计之前读发起者的注册验证状态，**未验证账户答 403 `contact-required`、读取失败答 503**，动作都不执行，门控只收紧、不放行；审计落在**动作之前、事务之外**，写不进即 503 且动作不执行；guard 的拒绝（403）与故障（503）由 guard 自己写出，本模块不重写；名册的拒绝经本模块的封闭映射变成 400／404／409，其余失败落 503。读路由在「向 guard 要权限」之后直接调名册，不经正文、审计与写路径。

```mermaid
sequenceDiagram
    participant C as 调用方
    participant H as tenant.admin
    participant G as authz.guard
    participant A as audit
    participant R as tenant.Catalog
    C->>H: 写请求
    H->>H: 读调用方身份
    alt 无身份
        H-->>C: 401 带 realm 质询
    else 有身份
        H->>H: 读发起者的注册验证状态
        alt 未验证账户
            H-->>C: 403 contact-required，动作不执行
        else 读取失败
            H-->>C: 503，动作不执行
        else 已验证
            H->>G: RequirePermissionIn(权限, 平台域)
            alt 权限不足
                G-->>C: 403 或 guard 故障 503
            else 权限足够
                H->>H: 解析正文并做形状检查
                H->>A: 写强制档审计
                alt 审计写不进
                    H-->>C: 503，动作不执行
                else 审计落地
                    H->>R: Create / SetAlias / SetStatus
                    alt 名册拒绝
                        H->>A: 记一行 failed
                        H-->>C: 400 / 404 / 409
                    else 落地
                        H-->>C: 201 / 204
                    end
                end
            end
        end
    end
```

## 核心数据结构

模块不持数据，只有路由与它的正文、以及交出的视图。

| 概念 | 形状 | 说明 |
|---|---|---|
| 租户视图 | `{id, alias, status, disabledBy?}` | 从 `tenant.Tenant` 逐字段构造；`status` 是 `active`、`disabled` 或 `deleted`；`disabledBy` 是 `operator` 或 `account`，只在状态为 `disabled` 时出现 |
| 登记正文 | `{id, alias}` | `POST /tenants` 的正文；新租户起于 `active` |
| 改名正文 | `{alias}` | `PATCH /tenants/:tenant` 的正文 |
| 状态正文 | `{status}` | `POST /tenants/:tenant/status` 的正文；取值闭集 `active`、`disabled` |

视图与正文都是本模块自己的类型，不是根包的 `tenant.Tenant`：`Tenant` 是名册上的一行（含状态枚举），而交出去的是它的字符串化视图、收进来的是校验过的形状。

## 核心算法与流程

### 一次管理请求

一次管理请求的操作序固定（细节见「访问控制」）：读调用方（无身份 401）→ 写路由读发起者的注册验证状态（未验证账户即 403 `contact-required`，读取失败即 503）→ 向 guard 要本条路由的权限（不足由 guard 写出 403／503）→ 判定之后写与读分叉：写解析正文并做形状检查（越出形状即 400），再落强制档审计（落不进即 503、动作不执行），再调名册，把名册的回绝映射成应答；读直接调名册并应答。

### 判定在平台域做出

一次名册操作是平台操作，handler 经 `RequirePermissionIn` 显式指定 `authz.PlatformDomain`。判定的主体是调用方的生效账户 `Identity.Account()`、域是平台域、资源与动作由权限经进程翻译给出（`tenant.read` → `(tenant, read)`，`tenant.manage` → `(tenant, manage)`）。不按请求解析出的租户判定：否则一个在某租户获授权的调用方靠一个请求头就能触到整个名册。

### 登记

1. 读发起者的注册验证状态：读作未验证（`SignupPending`）时答 403 `contact-required`，读取失败答 503，动作不执行。
2. 形状检查：`id` 与 `alias` 都要落在 `tenant.IsIdentifier` 的取值域内，否则 400。
3. 落强制档审计（动作 `tenant.create`）。
4. `Provisioner.Provision(Tenant{ID: id, Alias: alias, Status: tenant.StatusActive}, 发起者的账户标识)`。
5. `ErrTenantExists` → 409；`ErrAliasConflict` → 409；其余 → 503。落地 → 201 加视图。

### 改名

1. 读发起者的注册验证状态：读作未验证（`SignupPending`）时答 403 `contact-required`，读取失败答 503，动作不执行。
2. 形状检查：路径的 `id` 与正文的 `alias` 都要落在 `tenant.IsIdentifier` 的取值域内，否则 400。
3. `Catalog.Get(id)`；名册不持有该标识 → 404。
4. 落强制档审计（动作 `tenant.update`）。
5. `Catalog.SetAlias(id, alias)`。
6. `ErrAliasConflict` → 409；`ErrTenantDeleted`（该租户是墓碑）→ 409；其余 → 503。落地 → 204。

### 改状态

1. 读发起者的注册验证状态：读作未验证（`SignupPending`）时答 403 `contact-required`，读取失败答 503，动作不执行。
2. 形状检查：路径的 `id` 落在取值域内，正文的 `status` 是 `active` 或 `disabled`，否则 400。
3. 停用时问 `tenant.WriteGuard`；答复为拒绝时答 403 `urn:speed:tenant:protected`，动作不执行；读失败答 503。
4. 落强制档审计（动作 `tenant.status`）。
5. `Catalog.SetStatus(id, 状态)`；`ErrInvalidTenant`（标识无名册行）→ 404；`ErrTenantDeleted`（该租户是墓碑）→ 409；其余 → 503。落地 → 204。

## 错误与失败语义

**回绝映射是封闭的、fail-closed 的。** handler 不把任何名册失败原样透给调用方，也不让一个映射不到的失败落成成功。

| 情形 | 表现 |
|---|---|
| 请求在形状上不可读（路径参数或正文）、状态取值不在闭集、标识或别名越出取值域 | 400（`Problem.Type` 为 `urn:speed:tenant:bad-request`），在调名册之前 |
| 登记点名一个已被持有的标识 | 409（`urn:speed:tenant:conflict`），名册报 `tenant.ErrTenantExists` |
| 别名被别的租户持有 | 409（`urn:speed:tenant:conflict`），名册报 `tenant.ErrAliasConflict` |
| `GET`／`PATCH`／状态路由点名一个名册不持有的标识 | 404，不带 `Problem.Type` |
| 改名或改状态点名一个已删除的租户 | 409（`urn:speed:tenant:conflict`），名册报 `tenant.ErrTenantDeleted` |
| 发起者读作未验证（`SignupPending`），而这是写路由 | 403 `contact-required`（`authn.ErrContactRequired`），动作不执行 |
| 状态路由停用一个受保护的租户 | 403（`urn:speed:tenant:protected`），`WriteGuard` 答复拒绝，动作不执行；读失败答 503 |
| 注册验证状态读取失败 | 503，动作不执行，成因只进日志 |
| 强制档审计写不进 | 503，动作不执行 |
| 名册的其余失败、guard 的故障 | 503，不带 `Problem.Type`，成因只进日志 |

**403 与 401 由别处写，注册验证门控的 403 除外。** 权限不足的 403 与 guard 故障的 503 由 guard 写出（[design-authz](../design-authz.md)）；无身份的 401 由本模块写出，与准入层、授权层同形（只有状态、无 `Problem.Type`、带 realm 质询）。本模块不重写它们；注册验证门控的 403 `contact-required` 由本模块在 handler 内写出，是它自己的拒绝。

## 并发与事务边界

- 模块不持状态，可被多个协程并发使用。
- **改名与改状态的竞争由名册自己的写入仲裁。** 一次改名只改 `alias` 一列，一次改状态只写状态两列，两者都不读整行再写回；两个副本同时改同一行时后写者生效，`ErrAliasConflict` 与 `ErrTenantDeleted` 由名册在写入时判定。
- **创建不是跨模块原子的，名册行之后的失败留下一个未冻结的角色。** `Provisioner` 按角色、授予、名册行、内置标记、创建者成员关系与绑定的次序写，名册行之后的任一步失败都不回滚名册行，路由答 503。`MarkRoleBuiltin` 失败重试一次；仍失败时租户在册、角色未冻结、创建者的成员关系与绑定未写。名册行之后的失败留下一个角色未被冻结的租户；下一次部署的 seed 会修复它；在那之前该角色照常生效，理论上只有管理面能碰到它，而这落在安全的一侧。seed 在每次运行对账名册里每个非个人租户的角色、授予与内置标记；成员关系与绑定只对 `seed.tenants[].admins` 点名的管理员对账。
- 模块不开事务：一次动作是一次 `tenant.Catalog` 调用，事务在名册自己的句柄上开，与调用方的事务无处合并（[design-tenant](design-tenant.md)）。
- 强制档审计落在任何事务之外，且在动作之前：审计不落则动作不执行，所以一次被审计拦下的写什么都没改。
- **改名的写只动别名。** `SetAlias` 是一条只改 `alias` 列的语句，不改 `status`；`Put` 两列同写的形态在这里不可用（见「登记走 `Create`」与 [design-tenant](design-tenant.md) 的写面）。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 管理面 `tenant.admin`（路由、判定、审计、回绝映射） | 已实现 | `pkg/tenant/admin/admin.go` `Module`；`pkg/tenant/admin/routes.go` `routes`、`mount`；`pkg/tenant/admin/tenants.go` `handleTenantList`、`handleTenantGet`、`handleTenantCreate`、`handleTenantSetAlias`、`handleTenantSetStatus`；`pkg/tenant/admin/pipeline.go` `require`、`auditRequired`、`answer` |
| 管理面 `tenant.admin` 的 operation 声明、`speedhttp.Spec` 资源与片段生成 | 已实现 | `pkg/tenant/admin/operations.go`；`pkg/tenant/admin/openapi_generated_test.go`；片段 `pkg/tenant/admin/openapi/tenant.admin.json` |
| 静态特性 `tenant.admin` 的 `feature.Spec` 声明 | 已实现 | `pkg/tenant/admin/admin.go` `Module`；`pkg/tenant/admin/feature_test.go` `TestTheModuleDeclaresTheTenantAdminFeature` |
| 租户写面的注册验证门控（读作 `SignupPending` 时答 403 `contact-required`，经 `SignupVerification.SignupStateOf` 读取） | 已实现 | `pkg/tenant/admin/pipeline.go` `(*product).guardRoute`、`(*product).requireVerifiedAccount`；`pkg/tenant/admin/routes.go` `routes`；`pkg/tenant/admin/gate_test.go` |
| 登记经 `tenant.Provisioner` 建立租户与它的首任租户管理员 | 已实现 | `pkg/tenant/admin/tenants.go` `handleTenantCreate`；`pkg/tenant/admin/admin.go` `Module`；`pkg/tenant/protection.go` `Provisioner` |
| 状态路由经 `tenant.WriteGuard` 拒绝受保护租户（`system`、个人租户） | 已实现 | `pkg/tenant/admin/tenants.go` `handleTenantSetStatus`；`pkg/tenant/admin/admin.go` `Module`；`pkg/tenant/protection.go` `WriteGuard`、`WriteOp` |
| 租户视图的 `deleted` 状态与 `disabledBy` 字段，以及墓碑上的改名与改状态拒绝 | 已实现 | `pkg/tenant/admin/views.go` `tenantViewOf`；`pkg/tenant/admin/pipeline.go` `(*product).failureOf`；`pkg/tenant/admin/tenants_test.go` `TestTheViewCarriesTheNamedFieldsPerState`、`TestTheFailureMappingIsClosed` |
