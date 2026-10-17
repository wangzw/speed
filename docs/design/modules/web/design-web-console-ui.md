# console-ui

## 职责

`@speed/console-ui` 是平台员工的控制台面组件族：把后端的管理面渲染成一组由平台员工操作的页面。它对应后端的下列管理面：

- **租户管理**：名册的读写，经 `/tenant/admin`（[tenant.admin](../tenant/design-tenant-admin.md)、[ADR-console-tenant-2026-10-15](../../../adr/adr-console-tenant-2026-10-15.md)）。
- **账户管理**：账户的列出、详情与写入，经 `/auth/admin/accounts`。
- **会话管理**：按账户列出会话并撤销，经 `/auth/admin/accounts/:id/sessions` 与 `/auth/admin/sessions/:id`。
- **冒充管理**：冒充记录的列出与终止，经 `/auth/admin/impersonations`；运营者自身会话上的冒充状态读取与发起与结束，经 `/auth/impersonation`。
- **权限管理**：角色、授权、权限目录与账户绑定，经 `/authz/admin`。
- **邀请与成员**：按租户的邀请与成员，经 `/auth/admin/tenants/:tenant/invitations` 与 `/auth/admin/tenants/:tenant/members`。
- **SSO 提供方**：按租户的提供方与域名，经 `/auth/admin/sso/providers` 与 `/auth/admin/tenants/:tenant/sso/providers`。
- **设置读写**：运行期设置的读写，经 `/settings/admin`（[settings.admin](../settings/design-settings-admin.md)、[ADR-console-settings-2026-10-15](../../../adr/adr-console-settings-2026-10-15.md)）。
- **短信模板**：运行期短信模板的读写，经 `/sms/admin`。
- **审计查询**：审计记录的检索，经 `/audit/admin`（[audit.admin](../audit/design-audit-admin.md)、[ADR-audit-admin-2026-10-15](../../../adr/adr-audit-admin-2026-10-15.md)）。

**控制台根（`/`）渲染仪表盘**，其指标与最近活动由名册读与审计读拼出；各管理面的列表页可链入其详情页。

**每个页面以 ui-kit 的 `PageHeader` 开篇**：页标题即该页的一级标题，一条从控制台到该页的面包屑，以及一个重读该页数据的刷新动作（页头操作）。标题层级因此不跳级——`AsyncSection` 的占位与各节的次级标题都渲染在它之下。

**它是平台侧的壳（第二种壳）。** 上述管理面挂在**不解析租户**的端上：名册是平台资产、审计表持有全部租户的行、设置的全局层是平台操作。故本包没有**当前租户**切换器，也就没有租户作用域的查询键——它不认识「当前租户」这个概念。

**外壳骨架是 [layout-kit](design-web-layout-kit.md) 的 `AppShell`。** 控制台壳把带标签的声明派生出的平台导航交给 `AppShell` 渲染：固定顶栏、随视口变化的导航抽屉（`md` 及以上常驻、以下为临时覆盖式）与内容区都由它提供（[layout](design-web-layout.md)），本包不另造一套帧。导航地标以本包的 `shell.navLabel` 命名（经 `AppShell` 的 `navLabel`），与其它外壳的导航地标可区分。两处外壳因此同一形状——侧栏不随内容滚走、窄视口是汉堡抽屉而非把导航堆在内容上方、桌面侧栏由宿主显式选择收起、内容区是唯一滚动层。

**按租户寻址的页面持租户选择器。** 权限面、邀请与成员面、SSO 提供方面把租户当作**路由段参数**（`/authz/admin/tenants/:tenant/...`）；冒充面把租户当作读的**必填查询参数**（`/auth/admin/impersonations?tenant=`）。这些页面仍是平台员工的页面，但每条请求指名它作用到的租户；租户是请求的一个实参，不是会话的当前租户。页面因此持一个租户选择器（数据来自平台名册读 `GET /tenant/admin/tenants`），在其取值为空前禁用该页的租户作用域读。选择器的初值取自页面 URL 的 `tenant` 查询参数（缺省为空），故一条链接能把租户上下文带过页界；选择器仍是此后改变它的途径。冒充页的发起表单与被冒充记录共用这一个选择：目标租户是请求的必填字段，被冒充账户必须是该租户的成员，故发起表单不另设租户输入，未选租户时提交不可用。

**它复用声明式注册面。** 每个页面导出的一份声明即 [route-kit](design-web-route-kit.md) 的 `RouteDeclaration`：路径 pattern、视图回调、可选导航标签、可选权限码、该页依赖的特性名（`requires`）（[ADR-web-route-seam-2026-10-15](../../../adr/adr-web-route-seam-2026-10-15.md)）。`permission` 是数据，本包不据它判定；`label` 缺席即该页不进导航。`requires` 同样是数据，本包不据它判定：宿主经 route-kit 按特性在场收窄注册面，特性缺席的页面与未登记路径走同一个未命中回退，导航里也不出现（[ADR-web-route-seam-2026-10-17](../../../adr/adr-web-route-seam-2026-10-17.md)）。声明由应用的组合层收齐后交给路由层渲染；本包不 import `route-kit`，也不自己做路径匹配或导航。壳另交出一个 `onNavigate` 回调接缝：宿主给了它，帧内点一条导航项即把该项的路径交回调（由宿主的路由器就地跳转，不整页重载），不给则该项是整页链接。租户侧的 [product-shell](design-web-product-shell.md) 用同一份声明形状，两者只是壳不同。

### 非职责

- **不管理「当前租户」。** 切换当前租户是租户侧的动作，归 [tenancy-ui](design-web-tenancy-ui.md)；管理名册（开租户、改名、停用）是运维侧的动作，归本包。切换改的是宿主的路由，管理改的是名册，两者不共用控件、不共用查询键。按租户寻址的页面的租户选择器选的是**请求的实参**，不是当前租户，它与切换控件不共用状态。
- **不做导航、不解析 URL。** 页面的路径匹配、链接构造、深链接解析与未命中回退归 `route-kit` 的收集方，特性缺席的页面落到未命中回退同样在那里发生；本包只提供页面与它们的声明，不做路径匹配。
- **不读特性态。** 本包不取回特性文档，也不调用 auth-core 读特性态的钩子；页内控件所依赖的特性是否在场，由宿主经 `ConsoleShell` 的 `hasFeature` 给出（[ADR-web-feature-state-2026-10-17](../../../adr/adr-web-feature-state-2026-10-17.md)）。
- **不做判定。** 名册、账户、会话、冒充、权限、邀请与成员、SSO、设置与审计的授权判定都在服务端的平台域（设置的租户层在那个租户的域）做出，本包只渲染应答；路由级的授权是宿主的组合（经 [layout-kit](design-web-layout-kit.md) 的路由守卫与 [auth-core](design-web-auth-core.md) 的权限读数），不在本包内。
- **不遮蔽敏感值。** 敏感设置项的值由 [settings.admin](../settings/design-settings-admin.md) 在应答里遮蔽；本包照渲染应答，不做第二处遮蔽。
- **不持规则、不持数据。** 每条路的动作是一次生成的调用；名册的写入语义、账户的状态机、权限的取值、审计的列都在各自的模块里定义。
- **不拥有查询客户端。** 读经 `@speed/api-sdk` 的生成 react-query 钩子跑在宿主拥有的 QueryClient 上，写经同一 `bindRequestFn` 绑定的生成 mutation；本包从不自造 QueryClient。查询键由生成钩子自带（`get<operation>QueryKey`），本包不自写键；按租户寻址的读的键由生成工厂按租户分段，故切租户即换键。

## 外部接口契约

本包的公开面由 `web/packages/console-ui/src/index.ts` 导出：各管理面的页面声明集与页面组件、控制台壳、导航分组表（`consoleNavGroups` 与 `ConsoleNavGroup`）、以及页面读写的操作束。页面声明的形状与壳的入参如下。

```tsx
// 一份页面声明：形状即 route-kit 的 `RouteDeclaration`——路径 pattern、视图回调、
// 可选导航标签、可选权限码、可选特性名列表。本包不 import `route-kit`，导出满足
// 该结构的值。`requires` 的元素类型是 `@speed/api-sdk/features` 的 `FeatureName`，
// 比 route-kit 的字符串更窄，故声明仍可赋值给 `RouteDeclaration`。
// `group` 与 `icon` 是本包自加的额外可选字段：前者指定该页归属的导航分组
// （见下），后者是导航项的装饰性前导图标；两者都只是数据，不在 route-kit
// 的形状里，故带这两者的声明仍可赋值给 `RouteDeclaration`。
interface ConsoleRoute {
  readonly path: string
  readonly view: (context: ConsoleRouteContext) => ReactNode
  readonly label?: ReactNode
  readonly permission?: string
  readonly requires?: readonly FeatureName[]
  readonly group?: string
  readonly icon?: ReactNode
}

// 一个导航分组：稳定 id、已翻译标题、可选装饰图标与可选初始折叠态。图标在
// 侧栏收起的窄栏里代替被裁剪的标题。本包导出 `consoleNavGroups`
// 作为缺省分组表；宿主也可传入自己的（改顺序或改标题）。
interface ConsoleNavGroup {
  readonly id: string
  readonly label: ReactNode
  readonly icon?: ReactNode
  readonly defaultCollapsed?: boolean
}

// 控制台壳：由带标签的声明派生平台导航，渲染宿主的路由内容。
interface ConsoleShellProps {
  readonly routes: readonly ConsoleRoute[]
  // 特性判定：答一个特性名在本进程是否在场。必填；壳经包内上下文把它交给
  // 页面，页内依赖特性的控件据它渲染与否（见「特性在场的接收」）。
  readonly hasFeature: (name: FeatureName) => boolean
  // 导航分组表：给了它即按分组渲染（组按表内顺序、组内按声明顺序，
  // 无分组的声明排在组之前）；不给即维持扁平单列，现有宿主不变。
  readonly navGroups?: readonly ConsoleNavGroup[]
  readonly children: ReactNode
  readonly title?: ReactNode
  readonly headerActions?: ReactNode
  // 帧内导航的点击回调：给了它即把被激活条目的路径交回调（宿主的路由器
  // 就地跳转）；不给即该项是整页链接。
  readonly onNavigate?: (href: string) => void
  // 接受链接的构造器：给一条已签发邀请的令牌，返回接受链接（宿主相对的
  // 路径或绝对 URL）。邀请与成员页在签发成功后据此展示链接；不给则展示
  // 裸令牌。落点是宿主的公开路由，只有宿主知道，故路径由宿主给出。
  readonly invitationLink?: (token: string) => string
  // 桌面侧边栏收起：AppShell 的受控 prop 对，原样透传给帧。给出后者即在帧的
  // 顶栏渲染收起／展开按钮并回报每次翻转；不给即维持一直展开的常驻抽屉。
  readonly sidebarCollapsed?: boolean
  readonly onSidebarCollapsedChange?: (collapsed: boolean) => void
  readonly sx?: SxProps<Theme>
}
```

- **框架 prop 原样透传。** `sidebarCollapsed` / `onSidebarCollapsedChange` 是 `AppShell` 的桌面侧边栏收起对，被拾起后逐字转给 `AppShell`，本包不重解释。给出后者即在帧的顶栏渲染收起／展开按钮并回报每次翻转，不给即维持一直展开的常驻抽屉；收起态是宿主持有的普通值。租户侧的 [product-shell](design-web-product-shell.md) 用同一对 prop，两处外壳在这条上也同形。

- **导航按分组表聚合。** 页面声明可带 `group`，指向本包分组表里的一个组；壳把带标签的声明按其 `group` 聚成 [layout-kit](design-web-layout-kit.md) 的 `AppShellNavGroup`（组内是导航项、标题即折叠控件），组的顺序与标题来自宿主传入的分组表，组内顺序即声明顺序。分组表经 `navGroups` prop 传入，本包另导出 `consoleNavGroups` 作为缺省：概览（仪表盘）／身份与访问（账户、会话、冒充、权限、邀请与成员、SSO）／租户（租户名册）／平台配置（设置、短信）／审计（审计）。未传分组表即维持扁平单列——十一个带标签的声明按声明顺序排成一组，现有宿主零改动；`group` 此时是数据、不起作用。无 `group` 或指向表外组的声明作为导航项排在组之前，空组（无声明指向）不渲染标题。组标题经 console-ui 命名空间解析（`nav.groups.*`），与页面标签同一套翻译路径；组标题可带装饰图标，收起的窄栏以它代替被裁剪的标题。

### 页面声明

一个管理面导出一份页面声明表，全部汇总为 `consoleRoutes`（扁平导航顺序即下表顺序；按分组渲染时组的顺序由分组表给出）。路径是相对控制台根的 pattern，导航标签是一个在渲染时经 console-ui 命名空间解析的 `ConsoleNavLabel`，权限码是该页数据所持的平台权限（数据；本包不据它判定），`requires` 是该页依赖的特性名（数据；全部特性在场时该页才可渲染视图、可导航，本包不据它判定），`group` 是该页归属的导航分组（数据；见「导航按分组表聚合」），`icon` 是导航项的装饰性前导图标（数据；本包自己的管理面各带一个手绘 SVG 图标，见 `src/internal/icons.tsx`，随标签一起交给帧的 `AppShellNavItem.icon`，故收起态是[布局](design-web-layout.md)所说的图标窄栏，而不是把标签截半的文字栏；图标本身 `aria-hidden`，可及名仍是标签）。**一个管理面可含多于一条声明**：列表页携带标签与图标（进导航），详情页两者都不带（可达但不列出），由列表行链入；下表列出带标签的各页，与导航一一对应。

| 声明 | 路径 | 导航标签键 | 权限码 | 依赖的特性（`requires`） | 分组 | 导航图标 |
|---|---|---|---|---|---|---|
| `dashboardAdminRoutes` | `/` | `dashboard.title` | `tenant.read` | `tenant.admin` | 概览 | `DashboardIcon`（四格仪表盘） |
| `tenantAdminRoutes` | `/tenants` | `tenants.title` | `tenant.read` | `tenant.admin` | 租户 | `TenantsIcon`（楼宇） |
| `accountsAdminRoutes` | `/accounts` | `accounts.title` | `authn.account.read` | `authn.admin` | 身份与访问 | `AccountsIcon`（人像） |
| `sessionsAdminRoutes` | `/sessions` | `sessions.title` | `authn.session.read` | `authn.admin.sessions` | 身份与访问 | `SessionsIcon`（屏幕） |
| `impersonationsAdminRoutes` | `/impersonations` | `impersonations.title` | `authn.impersonation.read` | `authn.impersonation`、`authn.admin.impersonations`、`tenant.admin` | 身份与访问 | `ImpersonationsIcon`（两个人像） |
| `permissionsAdminRoutes` | `/permissions` | `permissions.title` | `authz.role.read` | `authz.admin`、`tenant.admin` | 身份与访问 | `PermissionsIcon`（盾牌） |
| `invitationsAdminRoutes` | `/invitations` | `invitations.title` | `authn.membership.manage` | `authn.invitations`、`authn.admin`、`tenant.admin` | 身份与访问 | `InvitationsIcon`（信封） |
| `ssoAdminRoutes` | `/sso` | `sso.title` | `authn.idp.manage` | `authn.admin.sso`、`tenant.admin` | 身份与访问 | `SsoIcon`（钥匙） |
| `settingsAdminRoutes` | `/settings` | `settings.title` | `settings.read` | `settings.admin` | 平台配置 | `SettingsIcon`（齿轮） |
| `smsAdminRoutes` | `/sms` | `sms.title` | `sms.template.read` | `sms.admin` | 平台配置 | `SmsIcon`（消息气泡） |
| `auditAdminRoutes` | `/audit` | `audit.title` | `audit.read` | `audit.admin` | 审计 | `AuditIcon`（记录单） |

**本包导出的每条声明都带非空的 `requires`，含不进导航的详情声明。** 详情声明带与其列表页相同的特性与权限码：`/tenants/:id` 带 `tenant.admin` 与 `tenant.read`，`/audit/:id` 带 `audit.admin` 与 `audit.read`。宿主的路由守卫只取精确命中的那条声明的权限码，不经路径前缀继承（[starters](../design-starters.md)），详情声明因而各自带码。

**带租户选择器的页面把 `tenant.admin` 列进 `requires`。** 冒充、权限、邀请与成员、SSO 页的租户选择器取自平台名册读，名册由 `tenant.admin` 服务，该特性缺席时这些页面选不出作用域。

**导出带 `requires` 的声明，对宿主的路由层提出要求。** route-kit 拒绝一张含门控声明、却没给出特性在场判定与读取中、读取失败视图的路由表（[route-kit](design-web-route-kit.md)），故装配本包页面声明的宿主必须读取特性态（[auth-core](design-web-auth-core.md) 的 `featurePresence`），并作为 `presence` 交给路由层。

### 页型的骨架

控制台页按 [layout](design-web-layout.md) 的页型组装：

- **仪表盘**（`DashboardPage`）：控制台根的路由（`/`）。一行等宽的租户指标卡、一行平台健康度指标卡，其下是最近活动列表。租户指标由名册读拼出——名册读返回完整的租户集，租户总数即其长度、启用与停用的计数即其状态分布。平台健康度由审计查询读拼出，它是唯一跨租户的读：`since` 取当前时刻往前 24 小时，一读数出窗口内的全部记录，另一读以 `action=authn.login` 只数其中的成功登录。两个计数取自窗口化读的最新一页，故应答带下一页游标时该计数按**下界**渲染（数值后带 `+`）。最近活动取审计查询读的最新一页（`useAuditRecords` 带 `limit`），其列与优先级与审计查询页同一套。平台不声明统计端点，也不声明失败登录的读，故页面不发明契约不携带的聚合值，也不把审计表不持有的失败登录计入。平台健康度与最近活动只在特性 `audit.admin` 在场时渲染，见「特性在场的接收」。
- **列表页**（`TenantRosterPage`、`AccountsPage`、`SessionsPage`、`ImpersonationsPage`、`PermissionsPage`、`InvitationsPage`、`SsoPage`、`SmsPage`、`AuditPage`）：页头、工具栏或写入区、数据表、分页。
- **详情页**（`TenantDetailPage`、`AuditDetailPage`）：标题区加主栏与固定宽侧栏。主栏承载主体与其写入，侧栏承载标识与回链。路径是列表页路径加一段 `:id`（`/tenants/:id`、`/audit/:id`），声明不带标签，故不进导航。列表页接收声明传入的该列表页匹配路径（`basePath`），据此为每行构造详情链接 `${basePath}/${id}`；详情页接收 `parentHref`（匹配路径去掉 id 段），据此回链列表页。租户详情页另接收控制台根路径（匹配路径去掉 `/tenants/:id`），据此给出通往按该租户寻址的兄弟页面的链接（权限与角色、邀请与成员、SSO 提供方、冒充记录），每条链接把租户放进 `tenant` 查询参数——目标页从该参数取得选择器初值。审计详情页对记录携带的空字段渲染占位符而不是只留标签：字段为空本身是记录的一部分（平台级事件没有租户、动作可以没有用途），省掉这一行会掩盖记录携带空值。
- **设置页**（`SettingsPage`）：一条二级导航（按 namespace 分组，取出自全量设置读）、一张占满内容区的分页表（键／类型／当前值／状态／操作）、底部一条粘性保存条。行的状态取自该层视图的 `has_row`：该层没有行时值走回落（标「默认」），持有行时标「已覆盖」。`written` 只用于显示，不参与判定——显式写入的空串与无行在文本上不可区分，行是否存在由 `has_row` 单独给出。只有持有行的行渲染可点的移除控件，默认行不渲染它——默认值没有可撤的覆盖行，摆一个只能空转的开关只会误导读屏与操作者；204 的幂等删除语义不变。**敏感项**：[settings.admin](../settings/design-settings-admin.md) 在应答里把敏感项的 `written` 与 `value` 换成遮蔽串，`has_row` 照常报出行是否存在，故页面按同一判据统计状态，并只对持有行的敏感项渲染移除控件；键格的「敏感」标记说明该行的值是遮蔽串。

**冒充页承载两个面。** 记录面是一个按租户寻址的列表页：它列出租户的冒充记录并终止一条。运营者自身会话面读调用方自己会话上的冒充（`GET /auth/impersonation`，无冒充时是裸 404）、以 `POST /auth/impersonation` 发起一条、以 `DELETE /auth/impersonation` 结束它。自身会话面作用到调用方自己的会话，不按租户寻址，故它不随租户选择器启用，其读取的键也不分租户；发起控件只在当前会话没有在force的冒充时呈现——模块对同一会话上的第二次发起答 409。这三个操作是生成的调用方会话操作（`useAuthnImpersonationStatus`、`useAuthnImpersonationStart`、`useAuthnImpersonationEnd`），页面直接调用它们并经生成键工厂（`getAuthnImpersonationStatusQueryKey`）失效状态读，与记录面的操作束不是同一组。

**页内导航复用壳的接缝。** 控制台壳把它的 `onNavigate` 经内部上下文交给它渲染的页内容；页内的详情链接经该回调就地路由（不做整页重载，否则内存态会话会丢），壳未给回调时是普通链接。这条接缝与壳自身的导航项同一来源，故二者重载与否的行为一致。

### 特性在场的接收

**页内依赖特性的控件经 `hasFeature` 判定。** `ConsoleShell` 的必填属性 `hasFeature` 是宿主给出的同步判定，壳经包内上下文把它交给页面，形态同壳交会话的 `ConsoleSessionProvider`。本包不认识特性态的读取中与读取失败：宿主在特性态就绪之后才渲染控制台壳（[starters](../design-starters.md)），壳拿到的判定恒有确定的答案。

**壳不按 `requires` 过滤导航。** 宿主交给 `routes` 的是已按特性在场收窄的声明（route-kit 的 `presentDeclarations` 的结果），壳照常由其中带标签的声明派生导航。

下表列出页内按特性门控的控件，是穷举的；表外的控件不受特性门控。

| 页面与控件 | 特性 | 特性缺席时 |
|---|---|---|
| 仪表盘的平台健康度指标与最近活动 | `audit.admin` | 不渲染，不发审计查询读 |
| 审计页操作者列的身份解析 | `authn.admin` | 不发账户读，该格渲染记录值并标注身份未解析 |
| 账户详情的因素列表、重置 MFA 与 MFA 要求控件、口令重置 | `authn.admin.factors` | 不渲染，不发因素读 |
| 账户详情的服务密钥（列出、新建、撤销） | `authn.admin.secrets` | 不渲染，不发密钥读 |
| 账户详情的会话（列出、撤销全部会话） | `authn.admin.sessions` | 不渲染，不发会话读 |
| SSO 页的元数据刷新控件 | `authn.admin.sso.metadata` | 不渲染 |
| 邀请与成员页成员表的 SSO 豁免列与切换控件 | `authn.admin.sso` | 不渲染 |

口令重置随因素组门控：它的路由挂在因素组的挂载分支之内（[authn-admin](../authn/design-authn-admin.md)）。

**特性缺席与无权限的处置不对称。** 特性缺席的控件不渲染，那个面在本进程不存在；特性在场而调用者无权限的写入控件照常渲染，授权由服务端判定（[architecture](../../architecture.md#特性与前端定制)）。

### 写入的请求参数由平台的 OpenAPI 片段声明

本包的写请求经生成的客户端发出，请求体来自平台 OpenAPI 片段里该端点的 `requestBody`。片段为每个变更端点声明了请求体，故生成的客户端为每次写发送请求体，本包为每个写渲染控件。请求体的字段与后端处理器 `mount.Decode` 解码的目标结构一致，且片段里声明的 `requestBody` 与处理器是否解码请求体一致（`pkg/authn/admin/requestbody_test.go` 把这条当成契约守住：处理器解码请求体而片段未声明 `requestBody`，即该写从生成客户端不可达，测试即失败）。

**对外接口面。** 公开面是 `src/index.ts` 的导出集；每条路的请求经绑定的客户端，写经生成的 mutation。变更按 TypeScript 的源码语义分类：新增导出、新增可选字段是源码兼容的；删除导出或字段、改既有导出或字段的含义或名、改签名（参数或返回类型），是不兼容变更。把必选与可选互转按位置判：输入位（宿主提供的 prop 或参数）放宽兼容、收紧不兼容；输出位（宿主读取的返回类型或结果字段）相反——放宽为可选使读出的值成为 `T | undefined`，strictNullChecks 下只有在要求 `T` 处使用它的消费者无法编译（对可能为 undefined 的值做成员访问报 TS18048，赋给 `T` 或传入要求 `T` 的形参报 TS2322/TS2345），仅转发或在宽松处使用（模板串、比较、`??`、可选链）的消费者仍编译，故可能破坏消费者、是不兼容变更。本包不提供当前租户，不 import `route-kit` 与 `tenancy-ui`。

## 依赖

| 依赖 | 用途 |
|---|---|
| `@speed/api-sdk` | 各管理面端点的生成读钩子与写 mutation；子路径 `@speed/api-sdk/features` 的特性名类型 `FeatureName`，用作 `requires` 的元素类型与 `hasFeature` 的参数类型 |
| `@speed/auth-core` | 会话：抬高会话的步进（step-up）流程由它驱动——宿主把会话交给 `ConsoleShell`，页面在特权写被答 401 步进时打开重认证对话框、重放该写。仅此一处：本包不读当前租户 |
| `@speed/ui-kit` | 主题、表单、对话框、数据表与空态原语 |
| `@speed/layout-kit` | 外壳骨架：`AppShell` 的固定顶栏、响应式导航抽屉与内容区 |
| `@speed/i18n` | 命名空间注册与文案的翻译钩子 |
| `@tanstack/react-query` | peer（必需）：宿主供给的 QueryClient |
| `react`、`react-dom`、`@mui/material`、`@emotion/*` | peer（必需）：宿主拥有的 React／MUI 树 |

**本包不 import `route-kit` 与 `tenancy-ui`。** 页面声明即 route-kit 的 `RouteDeclaration`，收集方在应用的组合层；本包没有当前租户切换器，故租户侧的切换控件不在这里出现。按租户寻址的页面持的租户选择器由本包自造，取自平台名册读，与 tenancy-ui 的切换控件无关。壳交出的 `onNavigate` 是一个回调，由宿主接上它自己的路由器，本包与 `route-kit` 之间仍无 import 边。

## UML 图

页面经生成操作打向管理面端；页面声明由应用的组合层收齐，特性判定由宿主交给壳、经壳交给页面。图里画不到的约束：本包所在的平台管理面挂在**不解析租户**的端上，故壳里没有当前租户切换器；按租户寻址的页面的租户是请求实参；判定在服务端做出，本包只渲染应答；声明形状即 route-kit 的 `RouteDeclaration`，但本包不 import `route-kit`；`requires` 由收集方据特性在场收窄，本包不据它判定。

```mermaid
graph TD
    pages["控制台页面\n概览 · 名册（含详情）· 审计（含详情）\n账户 · 会话 · 冒充 · 权限 · 邀请与成员 · SSO · 设置 · 短信"]
    decl["页面声明\n路径 · 视图 · 标签 · 权限 · 特性"]
    collect["应用的组合层\nroute-kit 的收集方"]
    host["宿主\n特性判定 hasFeature"]
    api["@speed/api-sdk\n生成操作"]
    platform["平台管理面\n/tenant/admin · /auth/admin\n/authz/admin · /settings/admin\n/sms/admin · /audit/admin"]
    pages --> decl
    decl --> collect
    host --> pages
    pages --> api
    api --> platform
```

## 核心数据结构

| 概念 | 形状 | 说明 |
|---|---|---|
| 页面声明 | `ConsoleRoute` | 即 route-kit 的 `RouteDeclaration`：路径 pattern、视图回调、可选导航标签、可选权限码、特性名列表，外加本包自加的可选导航分组与装饰性图标 |
| 特性判定 | `(name: FeatureName) => boolean` | 宿主经 `ConsoleShell` 的 `hasFeature` 给出，经包内上下文交给页面 |
| 租户作用域选择 | 页面自身状态 | 按租户寻址的页面持有的租户 id，作为请求实参，缺省为空即禁用该页的租户作用域读 |
| 命名空间 | `consoleUiResources` | 双语文案目录，叶子键集跨语言相同 |

## 核心算法与流程

### 一次管理请求

1. 页面从生成钩子或 mutation 发出请求，经宿主绑定的客户端。
2. 服务端在平台域（设置的租户层在那个租户的域）判定调用方的权限；无身份 401，权限不足 403。
3. 应答落地：读渲染列表或详情的视图，写渲染结果并失效相关查询；被拒渲染错误码文本。
4. 写路由的强制档审计落在服务端动作之前；本包不写审计行。

### 邀请的交付

签发一张邀请的应答里带一枚只交出一次的接受令牌（投递归签发者，见 [authn-invite](../authn/design-authn-invite.md)）。邀请与成员页不丢弃它：签发成功后，以宿主经 `ConsoleShell` 的 `invitationLink` 交出的构造器把令牌变成接受链接并展示，供签发者经自己的渠道交给被邀请人；宿主未给构造器时展示裸令牌。链接指向的落点（形如 `/invitations/:token`）是宿主的公开路由，本包不知道它，故路径由宿主给出；构造器返回宿主相对的路径时，页面按当前 origin 解析成绝对链接供复制。

### 按租户寻址的请求

按租户寻址的读以租户选择器的取值为查询键的一段，故切租户即换键、旧租户的数据自行失效。取值为空时该读声明为禁用（不发出请求）；页面渲染一段说明，提示先选择租户。

### 读的失败面：权限拒绝与其它失败

每个读经 `ConsoleAsyncSection`（ui-kit 的 `AsyncSection` 在本包内的绑定）渲染。一个 403 是权限拒绝而非加载失败——面应答了，是拒绝——故渲染权限拒绝文案（console-ui 命名空间的 `forbidden.title` 与 `forbidden.description`），且不给重试动作；任何其它失败（5xx、传输错误）落回可重试的未应答占位，带重试。判定按应答本身做出（HTTP 状态 403，或无可读信封的裸 403 经 `client.http.403`），不按路由级权限读数——服务端写出 403，页面渲染应答。

### 审计查询的筛选与分页

审计页把工具栏的 `since`、`until`、`action` 集合、`outcome` 集合与 `actor` 集合作为读的查询参数，在提交时应用（一次键入不触发请求）。`action`、`outcome` 与 `actor` 各自按逗号或空白切成集合送出，每项与行上的同名列相等即命中。审计读按游标分页：应答带出下一页的游标，页脚据此前进与后退；游标式读不数总数，故页脚不显示总数。「重置」清空全部筛选并回到第一页。

两张审计记录表——审计查询页与仪表盘的最近活动——共用同一套列与优先级。记录的身份是动作（HTTP 方法）与对象（路由）的组合，故时间与动作不带优先级、永不隐藏，对象紧随其后（容器 ≥600px 起显示，`sm` 档），操作者与结果同在 `md` 档（≥900px 起显示）。结果不能最先让位：在 13/14 寸笔记本上，容器宽 1184px（低于 `lg` 的 1200px），若结果按 `lg` 隐藏，失败的记录与成功的记录在列表上就看不出差别，而排查恰恰先看失败；故它与操作者一起留在 `md` 档。判定宽度取自表格自身的容器（`container-type: inline-size`），不取自视口。

两张表的操作者列共用同一个单元格单元，把记录的账户标识解析成可读身份：特性 `authn.admin` 在场时，它经 `GET /auth/admin/accounts/:id` 读账户的显示名；特性缺席时不发这条读，见「特性在场的接收」。该读受 `authn.account.read` 把守，页面自身的 `audit.read` 不含它，因此只持 `audit.read` 的查看者的解析会被拒；该格此时渲染记录值并标注身份未解析，而不是把它当作已解析（或系统）的操作者。同名操作者的多行共用一次缓存读。

### 账户查询的筛选

账户页把工具栏的筛选作为读的查询参数，在提交时应用（一次键入不触发请求）：登录标识前缀与它的类型成对送出（模块的索引按两者寻址，前缀缺类型即被拒），显示名前缀、状态与账户类型各自单独送出，未选的筛选不出现在请求里。显示名前缀是存储值的字节前缀、大小写敏感，页面的搜索框按用户所写原样送出。列表读按游标分页（应答带出下一页游标），页面只渲染读到的一页。账户详情在列表页内展开：行的「管理」控件切换展开与收起；展开时详情标题滚进视口并获得焦点——详情排在列表与创建表单之下，否则选择的可感变化只剩行高亮。

### 破坏性动作的确认

会移除或作废既有数据的写入在发出前经 ui-kit 的确认对话框确认：控件的点击只打开对话框，写入在确认点击时发出，未确认即不发出请求。对话框一律用 `ConfirmDialog` 的 `variant="danger"`；确认的**轻重**随动作是否可逆而分：一个连同其从属记录一并销毁、界面上无从恢复的删除（删除账户、删除角色）额外带 `doubleConfirm` 两步守卫，第一次点击只把按钮改题为「再次点击确认」，第二次才发出；其余的可恢复的移除与撤销（移除成员、移除短信模板、撤销会话、撤销账户全部会话、撤销服务密钥、移除登录标识、重置 MFA、撤销邀请、移除 SSO 域名、移除设置项、删除 SSO 提供方）用单次确认点击——可恢复的动作不该比不可逆的更重。不改变既有数据的写入直接发出，不经确认；这些是名册登记与改名、设置写值、授权的授予与撤销、绑定与解绑、模板登记与提交审核、开关与冒充终止。

### 内建角色只读

服务端在角色行上持有内建标记（`builtin`），并对内建角色的改写、删除与授权写入答 409（`authz.ErrBuiltinRole`）：其权限集固定，删除还会连同其授权与绑定一并撤销；读不受限。权限页据角色列表里的该标记，去掉内建行的「管理授权」与「删除」两个写控件，改留一个只读的「查看授权」入口（`permissions.roles.viewGrants`，可及名为 `permissions.roles.viewGrantsAria`），旁边是一句可读说明（`permissions.roles.builtinLocked`）。选中内建角色后，授权区照常列出它持有的权限，但授予表单与撤销列都不渲染，另以一句只读说明（`permissions.grants.builtinReadOnly`）表明原因。写控件换掉而不是禁用，是因为禁用的按钮不可聚焦、不进 Tab 顺序，依赖焦点或悬停的 tooltip 与 `aria-describedby` 对只用键盘的使用者不可达（它仍留在无障碍树里，读屏的浏览模式仍会读到它）；换成正文文字后，原因进入每个人的阅读顺序，也不向任何人呈现一个必然失败的控件。非内建角色不受影响。内建标记不由控制台设置：角色表单只写角色名与描述键，标记由宿主的种入步骤置上。

### 与租户侧的分工

租户这个概念在两处是两类动作：**切换当前租户**（改变宿主路由解析出的租户）在租户侧，归 [tenancy-ui](design-web-tenancy-ui.md)；**管理租户名册**（开租户、改名、停用）在运维侧，归本包。按租户寻址的页面持的租户选择器既不是当前租户、也不是名册管理：它是平台请求的一个实参。三者不共用控件、不共用查询键。

## 错误与失败语义

本包的失败面如下。

| 情形 | 表现 |
|---|---|
| 无身份 | 401，由服务端写出，宿主转入登录面 |
| 权限不足 | 403，由服务端写出；页面渲染权限拒绝文案且不给重试（重试只会再被拒一次）；路由级守卫由宿主组合 |
| 其它读失败（5xx、网络、404，以及缺席副本的配置没有认领该路径、请求落到前端时被传输层以 `client.html` 拒答的入口文档） | 可重试的未应答占位，带重试动作；页面的写入控件照常渲染 |
| 请求形状不可读 | 服务端写出 400，页面渲染错误码文本 |
| 名册的登记冲突 | 409（`urn:speed:tenant:conflict`），页面渲染冲突文本 |
| 敏感设置项 | 值由服务端遮蔽，页面渲染遮蔽串 |
| 审计操作者解析被拒（无 `authn.account.read`），或特性 `authn.admin` 缺席 | 该格渲染记录值并标注身份未解析，页面本身仍可读 |
| 自身会话无冒充（`GET /auth/impersonation` 的裸 404） | 渲染「当前未冒充任何账户」的说明，按未冒充处理，不落入失败占位 |
| 白名单外的码 | 渲染通用失败文本，不渲染裸键 |

页面的特性缺席不经本包的失败面：那样的页面命中后由宿主的路由层渲染回退视图，与未登记路径的 404 相同。特性在场的页面读取仍答 404 或入口文档，都出自同一个源背后副本装配不一致，后者发生在缺席副本的配置没有认领该路径时，见 [feature](../design-feature.md) 的非保证；两者都落到上表的可重试占位。

### 非保证

- **设置页照列键表里的全部键，含本进程未运行模块的键。** 键表取可用集，一个键可能由另一进程读取（[settings](../settings/design-settings.md#声明的收集与键表的建立)）；本包不按特性在场或启用集过滤设置项。
- **权限页与授予面照列权限目录里的全部码，含本进程未运行模块的码。** 目录取全部已登记模块的声明（[authz](../design-authz.md#动作到查询的翻译)），一个码可以先于装配变更授予；本包不按特性在场过滤权限码。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 控制台壳与声明面接入 | 已实现 | `web/packages/console-ui/src/ConsoleShell.tsx` `ConsoleShell`、`ConsoleShellProps`；`web/packages/console-ui/src/route.ts` `ConsoleRoute`、`ConsoleRouteTable` |
| 导航按分组表聚合（壳侧） | 已实现 | `web/packages/console-ui/src/ConsoleShell.tsx` `ConsoleShell`、`buildNavEntries`；`web/packages/console-ui/src/route.ts` `ConsoleNavGroup`、`ConsoleRoute.group`；`web/packages/console-ui/src/nav-groups.tsx` `consoleNavGroups` |
| 导航项图标（收起窄栏为图标栏） | 已实现 | `web/packages/console-ui/src/route.ts` `ConsoleRoute.icon`；`web/packages/console-ui/src/ConsoleShell.tsx` `navItemFor`；`web/packages/console-ui/src/routes.tsx`；`web/packages/console-ui/src/internal/icons.tsx` |
| 桌面侧边栏收起（框架 prop 对透传） | 已实现 | `web/packages/console-ui/src/ConsoleShell.tsx` `sidebarCollapsed`、`onSidebarCollapsedChange` |
| 控制台仪表盘（租户 KPI、平台健康度与最近活动） | 已实现 | `web/packages/console-ui/src/DashboardPage.tsx` `DashboardPage`；`web/packages/console-ui/src/routes.tsx` `dashboardAdminRoutes` |
| 租户管理页（PageHeader 开篇） | 已实现 | `web/packages/console-ui/src/TenantRosterPage.tsx` `TenantRosterPage`；`web/packages/console-ui/src/routes.tsx` `tenantAdminRoutes` |
| 租户详情页（主栏与侧栏） | 已实现 | `web/packages/console-ui/src/TenantDetailPage.tsx` `TenantDetailPage` |
| 审计详情页（主栏与侧栏） | 已实现 | `web/packages/console-ui/src/AuditDetailPage.tsx` `AuditDetailPage` |
| 设置页的二级导航、默认／已覆盖状态与粘性保存条 | 已实现 | `web/packages/console-ui/src/SettingsPage.tsx` `SettingsPage`、`settingState` |
| 读的失败面（权限拒绝与其它失败分开） | 已实现 | `web/packages/console-ui/src/internal/async-section.tsx` `ConsoleAsyncSection`、`isForbidden` |
| 页内导航接缝（详情链接就地路由） | 已实现 | `web/packages/console-ui/src/internal/navigation.tsx` `ConsoleNavigationProvider`、`ConsoleLink`；`web/packages/console-ui/src/ConsoleShell.tsx` `ConsoleShell` |
| 详情页的按 id 读 | 已实现 | `web/packages/console-ui/src/operations.tsx` `useTenant`、`useAuditRecord` |
| 账户页（按标识前缀、显示名前缀、状态与类型筛选列出，详情、创建与全部写入） | 已实现 | `web/packages/console-ui/src/AccountsPage.tsx` `AccountsPage`、`AccountCreateForm`、`AccountDetailWrites`；`web/packages/console-ui/src/routes.tsx` `accountsAdminRoutes` |
| 会话页（按账户列出与撤销） | 已实现 | `web/packages/console-ui/src/SessionsPage.tsx` `SessionsPage`；`web/packages/console-ui/src/routes.tsx` `sessionsAdminRoutes` |
| 冒充页（记录列出与终止；本会话冒充的状态、发起与结束） | 已实现 | `web/packages/console-ui/src/ImpersonationsPage.tsx` `ImpersonationsPage`、`StartImpersonationForm`；`web/packages/console-ui/src/routes.tsx` `impersonationsAdminRoutes` |
| 权限页（角色、授权、目录、绑定） | 已实现 | `web/packages/console-ui/src/PermissionsPage.tsx` `PermissionsPage`；`web/packages/console-ui/src/routes.tsx` `permissionsAdminRoutes` |
| 内建角色只读（保留查看授权入口，隐藏授予与撤销控件） | 已实现 | `web/packages/console-ui/src/PermissionsPage.tsx` `PermissionsPage`；`web/packages/console-ui/src/locales/zh-CN.json`、`en-US.json` |
| 邀请与成员页（邀请读写、成员列出与移除） | 已实现 | `web/packages/console-ui/src/InvitationsPage.tsx` `InvitationsPage`；`web/packages/console-ui/src/routes.tsx` `invitationsAdminRoutes` |
| SSO 页（创建、改动、列出、删除、元数据、域名认领与操作） | 已实现 | `web/packages/console-ui/src/SsoPage.tsx` `SsoPage`、`SsoProviderWriteForm`；`web/packages/console-ui/src/routes.tsx` `ssoAdminRoutes` |
| 设置读写页（PageHeader 开篇） | 已实现 | `web/packages/console-ui/src/SettingsPage.tsx` `SettingsPage`；`web/packages/console-ui/src/routes.tsx` `settingsAdminRoutes` |
| 短信模板页（列出、登记、提交、开关、移除） | 已实现 | `web/packages/console-ui/src/SmsPage.tsx` `SmsPage`；`web/packages/console-ui/src/routes.tsx` `smsAdminRoutes` |
| 短信模板页的读取失败一律落到通用失败面，写入表单不收起 | 未实现 | — |
| 审计查询页（筛选、游标分页、操作者可读身份） | 已实现 | `web/packages/console-ui/src/AuditPage.tsx` `AuditPage`；`web/packages/console-ui/src/routes.tsx` `auditAdminRoutes` |
| 按租户寻址页面的租户选择器 | 已实现 | `web/packages/console-ui/src/internal/tenant-scope.tsx` `TenantScope` |
| 账户的创建、改动、状态变更、MFA 要求、口令重置、标识新增、服务密钥新建 | 已实现 | `web/packages/console-ui/src/AccountsPage.tsx` `AccountCreateForm`、`AccountDetailWrites`；`web/packages/console-ui/src/operations.tsx` `useCreateAccount`、`usePatchAccount`、`useSetAccountStatus`、`useSetMFARequired`、`useResetPassword`、`useAddIdentifier`、`useCreateSecret` |
| SSO 提供方的创建与改动、域名认领 | 已实现 | `web/packages/console-ui/src/SsoPage.tsx` `SsoProviderWriteForm`；`web/packages/console-ui/src/operations.tsx` `useCreateSsoProvider`、`useUpdateSsoProvider`、`useClaimSsoDomain` |
| 成员的写入与 SSO 豁免 | 已实现 | `web/packages/console-ui/src/InvitationsPage.tsx` `MemberWriteForm`；`web/packages/console-ui/src/operations.tsx` `useWriteMember`、`useSetMemberSsoExempt` |
| 页头、双语资源与错误码白名单 | 已实现 | `web/packages/console-ui/src/locales/zh-CN.json`、`en-US.json`；`web/packages/console-ui/src/internal/error-text.ts` |
| 页面声明的 `requires`（每条声明非空，元素类型 `FeatureName`） | 未实现 | — |
| 详情声明的权限码（`/tenants/:id` 带 `tenant.read`，`/audit/:id` 带 `audit.read`） | 未实现 | — |
| `ConsoleShell` 的 `hasFeature` 与包内特性判定上下文 | 未实现 | — |
| 页内控件按特性在场渲染（仪表盘、审计页操作者解析、账户详情、SSO 元数据刷新、成员 SSO 豁免） | 未实现 | — |
