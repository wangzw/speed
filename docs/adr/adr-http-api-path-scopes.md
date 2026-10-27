# 管理面路径以作用域段开头，普通面不带段

## 背景

平台的 API 分区此前由能力名与 `admin` 段表达：`/api/v1/auth/admin/accounts`、`/api/v1/tenant/admin/tenants`、`/api/v1/authz/admin/tenants/{tenant}/roles` 都带 `admin`，但它们的授权级别并不相同——前两条与最后一条都在平台域判定，而 `/api/v1/auth/admin/tenants/{tenant}/members` 在租户域判定。路径本身无法说明一条 operation 属于哪一级，也无法让网关按稳定前缀做来源限制（例如对系统级面加 IP 白名单）。

平台域的判定域是保留策略域 `"system"`（[authz](../design/modules/design-authz.md)），租户管理在租户域判定。需要的是让两类管理面在地址上可区分，同时不给普通面添加无信息的段。

## 考虑过的选项

### 只保留 `admin` 段，按「带不带租户」推断级别

**否决**：`/authz/admin/tenants/{tenant}/roles` 带租户却在平台域判定（管理路径里的租户是被操作的数据，不是请求的域），按形状推断会把系统级路由判成租户级。

### 全部路径都以作用域段开头（self / tenants / system / public）

普通面也拆成 `self` 与 `public` 两段，第一段总是级别。

**否决**：`self` 与 `public` 的区分没有安全或语义收益——两者都不是管理面，访问级别本来就由网关注册（豁免/可选/必需）逐条决定，不在路径里；拆开只让所有普通路径多一段，并把登录流程与自助路由放进两个命名空间。

### 只做端与监听器隔离，不改路径

**否决**：端隔离是更强的网络面控制，但不满足「从地址看出管理级别」的需要，也无法让契约读者与 SDK 不查模块就分清级别。端隔离是同一条防线的一部分，不是替代。

### 管理面加作用域段：tenants 与 system；普通面不带段

`/api/{version}/tenants/{tenant}/...` 与 `/api/{version}/system/...` 是两类管理面；普通面（调用者自己的资源与未认证的登录面）直接挂在 `/api/{version}/...` 下，访问级别仍是网关注册的逐条事实。`Spec` 增加 `Scope`，空值即普通面。

**采纳**，见「决策」。

## 决策

### 两个管理作用域

- `tenants` —— 租户管理：在租户域判定、由持有该租户管理权限的调用方执行。每个路径以 `{tenant}` 开头。
- `system` —— 系统管理：平台管理的命名空间，由平台运营执行，判定默认在平台域。名册、授权与审计是平台资产；租户可以作为被操作的数据出现在路径里（`/system/tenants/{tenant}/roles`、`/system/tenants/{tenant}/impersonations`），但不构成级别。设置的管理面也在这一段下：它的租户层由查询串点名、在该租户的域判定，但路径不按租户寻址，因此仍留在租户两层之外。
- 空作用域 —— 普通面：调用者自己的资源（档案、会话、因子、通知、对象）与未认证的登录面（流程、回调、公钥）。两条普通路由的访问级别可以不同，它们共享同一段路径空间。

路径形状：

```
/api/v1/auth/me
/api/v1/auth/flows
/api/v1/notifications/messages
/api/v1/tenants/{tenant}/members
/api/v1/system/tenants/{tenant}/roles
```

### 声明与校验

`http.Spec` 的 `Scope` 由模块静态声明：

- 空值是普通面；
- 非空值只接受 `tenants` 与 `system`；
- `tenants` 作用域的片段里，每条路径必须以 `/{tenant}/` 开头，租户是地址的一部分而不是查询值。

`APIRoot` 由 `Prefix`、`Version`、`Scope` 三段拼成，空作用域不贡献段。

### 挂载

认证一族经 `mount.Mounter` 挂载，同一模块可跨作用域：普通路由（登录流程、回调、自助面）挂在 `mount.bases.default`；管理模块的账户面与冒充记录挂在 `mount.bases.system`，成员与提供方挂在 `mount.bases.tenants`。冒充记录的路径是 `system` 根下的 `/tenants/{tenant}/impersonations`：它按租户寻址，但判定在平台域，租户只是记录被限定的数据。社会化登录的回调地址取默认基路径。

端也按作用域分开（`authn.gate.mount.endpoints`）：普通面与 `system` 挂在租户两层之外的端（示范里普通面是 `account`，`system` 是 `admin`——平台管理命名空间 `/api/v1/system/*` 由它认领），`tenants` 挂在租户解析与准入层所守的端（示范是 `api`，其 `/api` 认领覆盖 `/api/v1/tenants`）。

判据是判定域与寻址方式，不是路径里有没有租户：一条路由只有**既按租户寻址、又由该租户治理**时才交给租户解析与准入两层——按租户寻址指租户是路径段而不是查询值，由该租户治理指判定发生在该租户的策略域；缺任一条都留在两层之外。租户管理路由（成员关系、邀请、SSO 提供方）满足两条，因而走有这两层的端。当前留在 `system` 根下的租户相关面是这条判据的反面例证（例举，不是封闭集）：名册的集合路由（`/system/tenants`）不按租户寻址，单行路由（`/system/tenants/{tenant}`）与授权（`/system/tenants/{tenant}/roles`、`/system/tenants/{tenant}/bindings`）按租户寻址而判定在平台域，路径里的租户是被操作的行数据的标识；冒充记录（`/system/tenants/{tenant}/impersonations`）按租户寻址，判定在平台域，路径里的租户是记录被限定的数据，不是请求的域；审计（`/system/audit`）读的是整张表、行上带租户，设置（`/system/settings`）的租户层由查询串点名，两者都不按路径段寻址，第一条就不成立。登录、注册、流程与自助面是全局账户的请求，不得落在被租户解析或准入层所守的端上。

### 不做的校验

不要求 `system` 作用域的 `Spec` 必须挂在某个被标记为管理面的端上。端与作用域是两份各自维护的装配事实；来源限制本就可以按路径前缀配置。

## 后果

- 两类管理面各有稳定前缀（`/api/v1/system`、`/api/v1/tenants`），来源白名单不必逐能力维护；普通面保持最短路径。
- 首段不再总是表示级别：`/api/v1/auth` 与 `/api/v1/notifications` 是普通面，`/api/v1/system`、`/api/v1/tenants` 是管理面。这条规则由两个保留段加一个默认面给出，是闭集。
- 授权本身不变：每条 operation 仍由权限点判定（[ADR-authn-decision-seam](adr-authn-decision-seam.md)），路径只是分类与新的一层防线。
- 租户级中按 id 操作的提供方与冒充记录，路径因此新增 `{tenant}`；处理器必须校验记录属于该租户，不匹配按不存在答 404。提供方在 `tenants` 作用域下、按该租户的策略域判定；冒充记录在 `system` 作用域下、按平台域判定，路径里的租户只是记录被限定的数据。
- 这是一次破坏性契约变更：全部管理面路径、契约、两套生成 SDK、宿主配置的端认领都要同步改；普通面路径不变。
