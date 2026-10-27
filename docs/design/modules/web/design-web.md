# web

## 职责

前端模块交付一组发布的 `@speed/*` 包与一个装配入口。产品把包族组合成一个浏览器应用，并在自己的 `main` 模块里调用装配入口把它挂到页面上。

模块持有下列各件事：

- **包的分层。** 包按依赖方向分层，依赖只从上层流向下层，同层之间互不依赖。
- **契约边界。** 与后端耦合的只有一个从后端契约生成的包；传输运行时手写，与契约无关。
- **特性门控的分工。** 前端按后端装配定制自己：特性态由会话层持有，只有宿主读它，路由层按宿主给出的特性在场判定收窄注册面，能力 UI 包经属性或上下文组件接收特性在场的信息。
- **装配与内容分离。** 壳拥有框架与会话分支机，不做导航、不发请求；业务内容由宿主经一份定义传入。
- **新增业务的接入面。** 路由与导航独立成层，业务模块以一份声明接入，租户作用域的查询键由框架提供。
- **契约生成的两级形态。** 平台契约生成发布的包，应用自己的契约在应用内另生成一份。

### 划分轴与判据

包按下列各条轴划分，各条轴同时成立。

**按层。** `tokens` 与 `theme-presets` 处于底层，`i18n` 同层独立，其上依次是 `ui-kit`、`layout-kit`、能力 UI 包、`product-shell`。判据是依赖方向：一个包的依赖只指向同族中比它低的层，同层的两个包互不依赖。证据是各包 `package.json` 的依赖声明与 `src/index.ts` 的公开面——`theme-presets` 只以类型依赖取 `tokens`，`ui-kit` 取 `tokens`，`layout-kit` 取 `ui-kit` 与 `i18n`，`auth-ui` 与 `account-ui` 同层而互不 import（`web/packages/account-ui/src/index.ts` 的包文档写明同一层的包从不互相 import；两个包各自只在内部保有 provider 标签回退词表，不共享类型）。

**按后端能力。** 一个能力 UI 包对一个后端能力目录：`auth-ui` 对 authn、`tenancy-ui` 对 tenant、`account-ui` 对 authn 的账户面、`console-ui` 对租户与设置与审计三个管理面、`notification-ui` 对通知的收件箱、偏好与推送三个面。判据是每个能力 UI 包都是某个后端能力在浏览器里的面。证据是各包 `src/index.ts` 的包文档——`auth-ui` 驱动会话的登录切片，`account-ui` 驱动账户切片，`tenancy-ui` 驱动租户切换。

**按契约边界。** `api-sdk` 是唯一与后端耦合的包，且是从后端契约生成的产物：orval 从 `contracts/api.json` 生成操作面，特性名生成器从 `contracts/features.json` 生成特性名类型（[api-sdk](design-web-api-sdk.md)）；`api-client` 是契约无关的手写传输运行时。判据是包与后端契约的耦合程度。证据是 `web/packages/api-sdk/src/runtime.ts` 的包文档（生成面保持与 fetch、axios、tenant 无关）与 `web/orval.config.ts` 的 mutator 指向（生成面经该接缝转发给宿主绑定的请求函数）。

**装配与内容分离。** 壳拥有框架与会话分支机（未登录、已登录、会话结束），不做导航、不发请求；业务内容由宿主经 `SpeedAppDefinition` 传入。判据是导航与请求是否由壳做出。证据是 `web/packages/product-shell/src/index.ts` 的包文档（壳不做任何会话操作、不导航、不发请求）与 `web/packages/product-shell/src/bootstrap.tsx` 的 `bootstrapSpeedApp`（视图根由 `SpeedAppDefinition.view` 给出）。

**判据一句话：按后端能力切，不按页面切。** 加一个后端能力目录就加一个与它对应的 UI 包；契约只从 `api-sdk` 流出；装配由壳承担，业务内容由宿主传入。

### 非职责

- **不做服务端渲染。** 产物在浏览器里渲染；服务端只交出产物，不渲染它（[ADR-web-static-hosting](../../../adr/adr-web-static-hosting.md)）。
- **不内建业务页面。** 模块交付框架、组件族与装配入口，不交付任何具体业务页面。
- **不决定业务模块的形态。** 业务模块的声明面由模块自己给出，前端只收集与渲染。
- **不拥有后端契约。** `api-sdk` 的面由后端模块的 OpenAPI 片段生成；前端不定义端点，也不定义请求形状。特性名由服务它的后端模块声明，前端只引用生成的特性名类型，不定义特性名（[feature](../design-feature.md)）。
- **不决定哪些特性在场。** 特性是否在场由后端的特性文档回答（[feature](../design-feature.md)）；前端只门控自己已带着代码的面，不按配置值、方法名或权限码推断一个面在不在。
- **不托管构建产物。** 把产物经 HTTP 交出（静态应答、客户端路由回退、缓存口径）归后端的静态托管模块（[webui](../design-webui.md)）；前端只产出它（[ADR-web-static-hosting](../../../adr/adr-web-static-hosting.md)）。

## 外部接口契约

### 交付的包与公开面

每个包以它的 `src/index.ts` 为公开面。另有几个文档化的子路径出口：

| 包 | 子路径出口 | 内容 |
|---|---|---|
| `@speed/product-shell` | `./bootstrap` | 装配入口 `bootstrapSpeedApp` 与 `SpeedAppDefinition` 等类型 |
| `@speed/api-sdk` | `./runtime` | 宿主绑定请求函数的接缝：`bindRequestFn` |
| `@speed/api-sdk` | `./features` | 由 `contracts/features.json` 生成的特性名类型 `FeatureName` 与全部特性名 `FEATURE_NAMES` |
| `@speed/i18n` | `./mui-locale`、`./platform-errors` | MUI 本地化助手与平台错误文案包 |
| `@speed/auth-core` | `./webauthn` | 通行密钥单步仪式的编解码与执行（不渲染、不推进流程） |

### 装配入口

应用在自己的 `main` 模块里调用一次：

```ts
bootstrapSpeedApp(container: Element, definition: SpeedAppDefinition): SpeedAppBootstrap
```

`SpeedAppDefinition` 是一个应用变化的部分：

```ts
interface SpeedAppDefinition {
  namespaces?: readonly SpeedAppNamespace[]   // 应用自带的 i18n 命名空间
  providers?: readonly SpeedAppProvider[]      // 应用级 provider，最外层在前
  view: ReactNode                               // 应用的视图根
  sessionEnded?: SpeedSessionEndedHandler       // 会话结束时的动作
  baseUrl?: string                              // 客户端 base URL，缺省 window.location.origin
}
```

**一次装配的接线次序固定**：i18n 实例（先注册框架命名空间并开启平台错误兜底，再依次注册应用的命名空间）→ 会话（内存 access token store 喂会话状态机并 attach）→ 客户端（`createClient` 以会话静默刷新为 401 腿、以 i18n 实例当前语言为语言源）→ 把客户端绑进 `api-sdk` 的 runtime 接缝 → 查询客户端与会话结束策略 → provider 栈（I18next → 主题 → QueryClient → 应用的 provider，应用 provider 最外层在前）→ 视图。装配返回 `SpeedAppBootstrap { root, i18n, queryClient, session }`。

**装配入口是一个子路径出口**（`@speed/product-shell/bootstrap`）：主出口维持组件包最小的依赖底（`auth-core`、`auth-ui`、`layout-kit`、`i18n`），这个出口额外 import 应用入口要驱动的那几件（`@speed/api-client`、`@speed/api-sdk/runtime`、`@speed/ui-kit` 与 react-query），它们都在主出口的传递闭包内。

### 契约生成的两级形态

平台契约生成发布的 `@speed/api-sdk`：它由后端模块的 OpenAPI 片段合并后经 orval 生成（`web/orval.config.ts`），被多个包 import，因此只含平台端点。应用自己的契约在应用内另生成一份：应用自己的合并契约经第二份 orval 配置生成到应用内（[ADR-web-two-build-artifacts](../../../adr/adr-web-two-build-artifacts.md)）。两级都经 `api-sdk` 的 runtime 接缝发请求，没有一处生成码或桥接码自己做 HTTP。

特性名类型同样分两级：平台模块声明的特性名生成进 `@speed/api-sdk/features` 的 `FeatureName`；应用模块声明的特性名由生成器写成对同一个登记接口的模块增补，并入同一个 `FeatureName`（[api-sdk](design-web-api-sdk.md)、[ADR-feature-vocabulary](../../../adr/adr-feature-vocabulary.md)）。

### 运行期的 API origin 注入

`bootstrapSpeedApp` 的 `baseUrl` 缺省是 `window.location.origin`，即假设前后端同源。跨域部署要在运行期注入：产物在入口文档旁带一份 `runtime-config.json`，应用入口取它、把其中的 API origin 作为 `baseUrl` 传给 `bootstrapSpeedApp`；文件不存在、不可读或没有 origin 时回落到同源（[ADR-web-runtime-config](../../../adr/adr-web-runtime-config.md)）。这份文件按**入口文档所在源下的根路径**解析，不按当前 URL 解析：深链接（如控制台页的 `/console/tenants`）因而仍取到根下的文件，而不是它自己目录下的同名文件。该解析不依赖已解析的 origin，故不陷入「取配置本身需要配置」的循环；它也不随部署拓扑变化，故同一份产物在合一拓扑与分离拓扑下共用。这份文件由部署方写出，不反映后端装配；后端服务哪些面由特性文档回答（见「特性门控」）。

**是否配置 CORS 由部署拓扑决定。** 合一拓扑是单端口同源：产物与 API 由同一个进程在同一个源答出，浏览器不发跨域请求，部署面里因而没有 CORS 这一环——demo 骨架即此拓扑。分离拓扑把前端交静态主机、后端只做 API，两者异源，跨域请求才出现，CORS 在此时进入部署面。跨域部署是受支持的形态，但它不是 demo 骨架的形态。

### 新增三件的契约

**路由与导航独立成层（`@speed/route-kit`）。** 壳保持不做导航不变；路由与导航是独立一层，新增业务接入它而不动壳（[ADR-web-route-seam](../../../adr/adr-web-route-seam.md)）。

**声明式注册面。** 业务模块导出结构相同的声明——路径、视图、导航标签、所需权限、所需特性——由收集方收齐后渲染。它与后端的命令树点名模块对称：它是「加一个业务模块」在前端的对应动作。所需特性（`requires`）与所需权限一样是数据；带它的声明只在其中特性全部在场时可渲染、可导航（[route-kit](design-web-route-kit.md)）。

**租户作用域的查询键归框架。** 框架提供查询键工厂（形状 `tenantQueryKey(tenantId, ...tail)`，产出以租户标识为最外层段的键）与「切换租户时逐出整棵子树」的助手，落在 [auth-core](design-web-auth-core.md)（[ADR-web-tenant-query-key](../../../adr/adr-web-tenant-query-key.md)）。`api-sdk` 的生成面保持与 tenant 无关，不改。

### 特性门控

特性缺席的页面，前端一律按 404 处理：导航里不出现，直接访问其链接得到与不存在的路径相同的 404；特性缺席的区块与控件不渲染。前端据后端发布的[特性文档](../../../glossary.md)（[feature](../design-feature.md)）门控页面、导航项与页内控件，各包的分工如下：

- **会话层持有特性态。** [auth-core](design-web-auth-core.md) 随会话提交取回特性文档，作为快照的 `features` 字段与会话一起生效；读取失败是独立的状态，既不当作特性缺席，也不当作特性在场（[ADR-web-feature-state](../../../adr/adr-web-feature-state.md)）。
- **只有宿主读特性态。** 读特性态的钩子（`useFeature`、`useFeatureState`）与 `featurePresence` 的使用者是宿主。能力 UI 包不读特性态：一个包的面依赖特性时，宿主经该包的属性或上下文组件给出特性在场的信息，各包「不读会话」「对 auth-core 只依赖步进认证」「会话只以属性传入」这类界不变（ADR-web-feature-state）。
- **路由层收窄注册面。** 页面声明以 `requires` 列出所需特性；[route-kit](design-web-route-kit.md) 按宿主给出的特性在场判定，让特性缺席的页面与未登记的路径走同一个回退视图，导航里也不出现（[ADR-web-route-kit](../../../adr/adr-web-route-kit.md)）。
- **判定次序是先存在性、后权限。** 特性缺席的页面落到回退视图，不进入宿主的权限守卫；特性在场的页面再由宿主组合的 `RouteGuard` 按权限判定。
- **特性缺席的控件隐藏，无权限的写入控件照常渲染。** 特性缺席的控件所对的面不存在；无权限的写入控件只是示能，授权由服务端判定。
- **特性名在编译期对齐。** 前端写特性名的位置一律取 `@speed/api-sdk/features` 的 `FeatureName`，名字拼错即编译失败；`route-kit` 不 import 同族包，它的 `requires` 是字符串数组。应答里出现前端不认识的名字一律忽略（ADR-feature-vocabulary）。

### 对外接口面

- 每个包的 `src/index.ts` 公开面、文档化的子路径出口，以及 `bootstrapSpeedApp` 与 `SpeedAppDefinition`、`SpeedAppBootstrap`、`SpeedAppServices`、`SpeedAppNamespace` 的形状，是对使用者的接口面。变更按 TypeScript 的源码语义分类：新增导出、新增可选字段是源码兼容的；删除导出或字段、改既有导出或字段的含义或名、改签名（参数或返回类型），是不兼容变更。把必选与可选互转按位置判：输入位（宿主提供的 prop 或参数）放宽兼容、收紧不兼容；输出位（宿主读取的返回类型或结果字段）相反——放宽为可选使读出的值成为 `T | undefined`，strictNullChecks 下只有在要求 `T` 处使用它的消费者无法编译（对可能为 undefined 的值做成员访问报 TS18048，赋给 `T` 或传入要求 `T` 的形参报 TS2322/TS2345），仅转发或在宽松处使用（模板串、比较、`??`、可选链）的消费者仍编译，故可能破坏消费者、是不兼容变更。
- 包以锁定版本组发布（`web/.changeset/config.json` 的 fixed 组），使用者不会同时持两个版本的同名包。
- `SpeedAppDefinition.baseUrl` 的缺省同源：合一拓扑不写任何配置的前提就是它。

## 依赖

依赖方向满足架构不变量：依赖只从上层流向下层，同层不互相依赖。

表现层的层自下而上：`tokens` 与 `theme-presets`；`i18n`；`ui-kit`；`layout-kit`；能力 UI 包；`product-shell`。

与后端契约相关的一条链路自下而上：`api-client`（契约无关的传输运行时）；`api-sdk`（生成的契约面）；`auth-core`（会话状态机）；能力 UI 包与 `product-shell`。

下表是本模块的每个包、它对这个能力的职责、以及它依赖哪些同族包。

| 包 | 职责 | 依赖的同族包 |
|---|---|---|
| `tokens` | 设计令牌的默认值与覆盖入口 | 无 |
| `theme-presets` | 命名的主题预设货架：相对默认树的成品风格差异 | `tokens`（仅类型） |
| `i18n` | 每宿主一个 react-i18next 实例、按包命名空间登记与校验、缺键纪律 | 无 |
| `ui-kit` | `tokens` 到 MUI 主题的适配与一组受控组件 | `tokens`、`i18n` |
| `layout-kit` | 与认证无关的框架与路由门禁 | `ui-kit`、`i18n` |
| `api-client` | 契约无关的手写 HTTP 运行时与错误、重试、超时、上报接缝 | 无 |
| `api-sdk` | 唯一与后端耦合的包，从后端契约生成：操作面与特性名类型 | `api-client` |
| `auth-core` | 浏览器会话状态机、特性态与 hooks | `api-sdk`、`api-client` |
| `auth-ui` | 登录组件族 | `ui-kit`、`i18n`、`auth-core`（`api-sdk` 只是开发期的类型依赖，公开面不引用它的类型） |
| `account-ui` | 已登录账户管理组件族 | `ui-kit`、`i18n`、`auth-core`、`api-sdk` |
| `tenancy-ui` | 租户切换控件 | `ui-kit`、`i18n` |
| `product-shell` | 组装壳与装配入口 | `layout-kit`、`auth-ui`、`auth-core`、`i18n`（主出口的直接依赖；`./bootstrap` 出口另直接取 `api-client`、`api-sdk`、`ui-kit`） |
| `route-kit` | 路由与导航独立层、声明式注册面、按特性在场收窄注册面 | 无 |
| `console-ui` | 控制台面组件族，第二种壳 | `ui-kit`、`layout-kit`、`i18n`、`auth-core`、`api-sdk` |
| `notification-ui` | 站内通知面：未读铃铛、收件箱与已读、偏好矩阵、实时订阅与声明式入口 | `ui-kit`、`i18n`、`auth-core`、`api-sdk`、`api-client` |

**`api-sdk` 是唯一 import 后端契约的包**，它只依赖 `api-client` 的请求函数类型，本身不 import 任何 UI 包。**`api-client` 不依赖任何同族包**，也不认识 tenant。**能力 UI 包之间同层不互依赖**：`auth-ui` 与 `account-ui` 各自只在内部保有 provider 标签回退词表，不共享类型。**`route-kit` 与 `product-shell` 都不 import 对方**，两者由应用的组合层分别消费。

**`billing-ui` 不在新树里。** 后端不做计费，对应的前端包随早期实现一并删除，不进本模块的包清单（[ADR-web-reuse](../../../adr/adr-web-reuse.md)）。

### 不经特性门控的后端模块

平台自身的装配（cookie 会话）不经特性门控、直接调用下表各后端模块的路由，服务前端的命令必须纳入它们，清单是穷举的。其余后端模块服务的面，前端按事实的归属门控：登录后才需要的存在性经特性文档门控，模块不在本进程运行时按特性缺席处理；登录前的事实经拥有它的模块的匿名发现面门控，例如企业提供方的发现（`authn.sso` 的 `POST /api/v1/auth/sso/discover`）只在 `GET /api/v1/auth/methods` 列出 `enterprise` 类方法时调用。宿主另传 `AccessTokenStore` 走令牌会话时，刷新另需令牌交付的路由，那是宿主自选的装配，不在此列。

| 模块 | 前端依赖它的什么 |
|---|---|
| `authn.login` | 登录方法发现、登录流程与已登录身份上的自助服务；它的 `Requires` 带来 `authn.Passwords` |
| `authn.session` | `GET /api/v1/auth/session`、登出与会话管理 |
| `authn.profile` | `GET /api/v1/auth/me` 与资料、偏好的读写 |
| `feature.board` | 特性文档 `GET /api/v1/features`（[feature](../design-feature.md)） |

## UML 图

图给出包之间的依赖方向。图里画不出的约束：依赖只向上，同层的包之间没有边；`api-client` 与 `api-sdk` 这条契约链路与 UI 分层是两条链路，在能力 UI 包处汇合（能力 UI 包既取 `ui-kit` 也取 `auth-core`）；`route-kit` 与 `product-shell` 并列在顶层，应用分别消费两者。

```mermaid
graph TD
    tokens
    theme-presets --> tokens
    i18n
    api-client
    ui-kit --> tokens
    ui-kit --> i18n
    layout-kit --> ui-kit
    layout-kit --> i18n
    api-sdk --> api-client
    auth-core --> api-sdk
    auth-core --> api-client
    auth-ui --> ui-kit
    auth-ui --> auth-core
    auth-ui --> i18n
    account-ui --> ui-kit
    account-ui --> auth-core
    account-ui --> i18n
    account-ui --> api-sdk
    tenancy-ui --> i18n
    tenancy-ui --> ui-kit
    console-ui --> ui-kit
    console-ui --> layout-kit
    console-ui --> auth-core
    console-ui --> api-sdk
    console-ui --> i18n
    notification-ui --> ui-kit
    notification-ui --> i18n
    notification-ui --> auth-core
    notification-ui --> api-sdk
    notification-ui --> api-client
    product-shell --> layout-kit
    product-shell --> auth-ui
    product-shell --> auth-core
    product-shell --> i18n
    route-kit
```

## 核心数据结构

| 概念 | 类型 | 说明 |
|---|---|---|
| 应用定义 | `SpeedAppDefinition` | 一个应用变化的部分：`namespaces`、`providers`、`view`、`sessionEnded`、`baseUrl` |
| 装配产物 | `SpeedAppBootstrap` | 装配交出的句柄：`root`、`i18n`、`queryClient`、`session` |
| 装配服务 | `SpeedAppServices` | 装配传给宿主 provider 的服务：`session`、`api`、`queryClient` |
| 应用命名空间 | `SpeedAppNamespace` | 应用自带的一个 i18n 命名空间：`namespace` 与 `resources` |
| 会话结束上下文 | `SpeedSessionEndContext` | 会话结束动作拿到的值：`session`、`queryClient`、`evictAllQueries` |
| 路由声明 | 结构性类型 | 一个业务模块导出的一份声明：路径、视图、导航标签、所需权限、所需特性 |
| 特性态 | `FeatureState`（[auth-core](design-web-auth-core.md)） | 会话快照的 `features` 字段：未取回、读取中、读取失败，或就绪并带名字集合 |
| 特性名 | `FeatureName`（`@speed/api-sdk/features`） | 生成的特性名联合类型；前端写特性名的位置取它 |
| 租户查询键 | 数组 | `tenantQueryKey(tenantId, ...tail)` 产出以租户标识为最外层段的键 |

## 核心算法与流程

### 一次装配

1. 应用在自己的 `main` 模块里构造一份 `SpeedAppDefinition`，调用 `bootstrapSpeedApp(container, definition)`。
2. 装配建一个 i18n 实例：注册框架命名空间（`ui-kit`、`layout-kit`、`auth-ui`、`product-shell`），注册平台错误文案包并把它设为实例的兜底命名空间，再依次注册定义里的应用命名空间。注册发生在渲染之前。
3. 装配建一个内存 access token store 与会话，把会话 attach 到 hooks 面。
4. 装配以 `createClient` 建客户端（`baseUrl` 取定义的值或缺省同源，语言源读 i18n 实例的当前语言，静默刷新接会话的 `refresh`），并把客户端绑进 `api-sdk` 的 runtime 接缝。
5. 装配建查询客户端（查询与变更都不重试）并装上会话结束策略。
6. 装配把 provider 栈按次序套在定义的视图根之外（I18next → 主题 → QueryClient → 应用的 provider，应用 provider 最外层在前），挂到容器上。
7. 装配返回 `SpeedAppBootstrap`。

**引入一份新契约不改变这条次序。** 平台契约与应用契约都经同一个绑定的请求函数发请求，装配只绑一次。

### 装配与内容的分离

壳渲染一台视图机：认证快照下渲染框架包住应用内容；已到达过应用而快照转匿名时渲染会话结束面（可被宿主的槽替换）；从未到达的匿名访客渲染登录槽或空。壳不驱动会话、不导航、不发请求——分支由快照驱动，登录与登出由宿主槽里的视图调用。框架的字符串取自 `layout-kit` 命名空间，默认会话结束面的字符串取自 `auth-ui` 命名空间，其余字符串是宿主内容。

### 声明式注册面的收集

一个业务模块导出它的路由声明（路径、视图、导航标签、所需权限、所需特性）。收集方把各模块的声明收齐，按路径匹配渲染对应视图，并按标签构造导航。带 `requires` 的声明只在宿主给出的特性在场判定就绪、且其中特性全部在场时渲染视图并进入导航；特性缺席时与未登记的路径同走回退视图，判定读取中或读取失败时渲染宿主给出的对应视图（[route-kit](design-web-route-kit.md)、[ADR-web-route-kit](../../../adr/adr-web-route-kit.md)）。路径与权限之间的关系不在这里建立，授权仍是宿主在内容区里的组合，排在存在性之后（[ADR-web-route-seam](../../../adr/adr-web-route-seam.md)）。

### 浏览器层

jsdom 不求值媒体查询与容器查询，不做布局，也不判定可聚焦性，所以版面与可聚焦性的不变量由独立的浏览器层在真实 Chromium 里断言（[ADR-web-browser-tier](../../../adr/adr-web-browser-tier.md)）。浏览器用例写在与被测组件相邻的 `*.browser.test.tsx` 里，不进各包默认的 `test`，由根 `Makefile` 的 `web-browser` 入口运行，定时的 CI 任务调用同一个入口；它不进 `make check`。

## 错误与失败语义

| 情形 | 表现 |
|---|---|
| 同一容器装配两次 | 双重挂载；一个页面只装配一次 |
| 同一 i18n 命名空间注册两次 | 注册时抛错 |
| 生成的调用在宿主绑定请求函数之前发生 | 以程序员错误快速失败，不发未配置的请求 |
| 跨域部署未注入 origin（`runtime-config.json` 缺失） | 回落到同源，请求打向错误 origin |
| 已认证访问一个特性缺席的页面 | 与未登记的路径同走回退视图（404）；导航里没有该页；不进入权限守卫 |
| 特性文档读取失败 | 门控的页面渲染可重试的错误，门控的导航项与控件不渲染，不门控的页面照常可用；不当作特性缺席，也不当作特性在场 |
| 特性文档读取中（只在显式重试时出现） | 门控的路径渲染等待视图，未登记的路径照常立刻回退；导航里不出现门控项 |
| 特性文档里有前端不认识的名字 | 忽略 |

## 扩展点

**新增一个后端能力 UI 包。** 每条准入条件是：一个能力 UI 包对一个后端能力目录；依赖只指向比它低的层，同层不互相依赖；自带 i18n 命名空间与资源，由宿主登记；经 `api-sdk` 的生成面发请求，不自己写 HTTP；不读特性态，包的面依赖特性时由宿主经属性或上下文组件给出特性在场的信息。

**新增一个业务页面。** 业务模块导出它的路由声明，经声明式注册面接入（见核心算法与流程）；不动壳。页面依赖的后端面不属于「不经特性门控的后端模块」时，声明以 `requires` 列出所需特性。

**新增一个控制台面。** 控制台面挂在不解析租户的端上，是第二种壳：没有租户切换器，也就没有租户作用域的查询键。它复用声明式注册面，用另一套壳。

**新增一个应用。** 应用在自己的 `main` 模块里写一份 `SpeedAppDefinition` 交给装配入口，并跑应用自己的第二级契约生成。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 浏览器层的版面与可聚焦性用例 | 已实现 | `web/vitest.browser.config.ts`；`web/test-utils/src/browser-setup.ts`；`web/packages/layout-kit/src/components/AppShell.browser.test.tsx`；`web/packages/ui-kit/src/components/DataTable.browser.test.tsx`；`web/packages/console-ui/src/*.browser.test.tsx`；`./Makefile` `web-browser`；`.github/workflows/browser.yml` |
| 设计令牌 | 已实现 | `web/packages/tokens/src/index.ts` `defaultTokens`、`deepMerge` |
| 主题预设 | 已实现 | `web/packages/theme-presets/src/index.ts` `themePresets`、`ThemePreset` |
| 平台 i18n 层 | 已实现 | `web/packages/i18n/src/index.ts` `createI18n`、`registerNamespace` |
| 组件族与主题适配 | 已实现 | `web/packages/ui-kit/src/index.ts` `createAppTheme`、`AppThemeProvider` |
| 与认证无关的框架与门禁 | 已实现 | `web/packages/layout-kit/src/index.ts` `AppShell`、`RouteGuard` |
| 契约无关的传输运行时 | 已实现 | `web/packages/api-client/src/index.ts` `createClient`、`ApiError` |
| `@speed/api-client` 的公开面不含 `./react` 子路径与预认证配置取回 | 已实现 | `web/packages/api-client/src/index.ts`、`web/packages/api-client/package.json` |
| 生成的契约面 | 已实现 | `web/packages/api-sdk/src/runtime.ts` `bindRequestFn`、`speedRequest`、`speedRequestCredentialless`；生成面见 `web/packages/api-sdk/src/index.ts`；特性名见 `web/packages/api-sdk/src/features.ts` |
| 浏览器会话状态机与 hooks | 已实现 | `web/packages/auth-core/src/session.ts` `createAuthSession`；`web/packages/auth-core/src/hooks.ts` `attachSession`、`useAuthState`、`usePermission`、`useFeature`、`useFeatureState`、`featurePresence` |
| 登录组件族 | 已实现 | `web/packages/auth-ui/src/SignInScreen.tsx` `SignInScreen`；`web/packages/auth-ui/src/SignOutButton.tsx` `SignOutButton`；`web/packages/auth-ui/src/SessionEndedScreen.tsx` `SessionEndedScreen` |
| 账户管理组件族 | 已实现 | `web/packages/account-ui/src/index.ts` `ProfileSection`、`PasswordSection`、`PhoneSection`、`IdentifiersSection`、`PreferencesSection`、`SessionsSection`、`SocialBindingsSection`、`MfaSection` |
| 租户切换控件 | 已实现 | `web/packages/tenancy-ui/src/index.ts` `TenantSwitcher`、`TenantOption`、`TenantSwitcherProps`；`web/packages/tenancy-ui/src/resources.ts` `TENANCY_UI_NAMESPACE`、`tenancyUiResources` |
| 组装壳与装配入口 | 已实现 | `web/packages/product-shell/src/index.ts` `ProductShell`；`web/packages/product-shell/src/bootstrap.tsx` `bootstrapSpeedApp` |
| 路由与导航独立层 | 已实现 | `web/packages/route-kit/src/index.ts` `RouteProvider`、`RouteView`、`RouteLink`、`useNavItems`、`useNavigate` |
| 控制台面组件族 | 已实现 | `web/packages/console-ui/src/index.ts` `ConsoleShell`、`TenantRosterPage`、`SettingsPage`、`AuditPage` |
| 站内通知面组件族 | 已实现 | `web/packages/notification-ui/src/index.ts` `NotificationBell`、`NotificationInbox`、`NotificationPreferences`、`NotificationCenter`、`NotificationStreamProvider`、`notificationRoutes` |
| 声明式注册面 | 已实现 | `web/packages/route-kit/src/types.ts` `RouteDeclaration`、`RouteTable`；`web/packages/console-ui/src/route.ts` `ConsoleRoute` |
| 租户作用域的查询键工厂与逐出助手 | 已实现 | `web/packages/auth-core/src/query-key.ts` `tenantQueryKey`、`useTenantQueryKey`、`evictTenantQueries` |
| 运行期的 API origin 注入 | 已实现 | `starters/demo/web/src/runtime-config.ts` `loadRuntimeConfig`；`starters/demo/web/src/main.tsx` `bootstrapDemoApp` |
