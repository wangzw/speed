# account-ui

## 职责

`@speed/account-ui` 是一个 speed 前端已登录一方的账户管理组件族：一组节（section）组成宿主的账户页。账户自助写面归本包的理由见 [ADR-web-account-ui-selfservice-2026-10-16](../../../adr/adr-web-account-ui-selfservice-2026-10-16.md)。它交付下列面：

- `ProfileSection`：账户显示名的编辑，读 `GET /auth/me` 取存下的值、写 `PATCH /auth/me` 提交单字段的修改，走账户档案的自写面（[authn-profile](../authn/design-authn-profile.md)）。
- `PasswordSection`：改口令，走登录自助面的 `PUT /auth/password`（[authn 登录](../authn/design-authn-login.md)）。账户 `password_set` 为假（经提供方开户、无口令）时不显示当前口令字段，只给新口令两项；确认口令是客户端自己的校验，不上线。账户没有可作登录名的标识（没有用户名也没有已验证邮箱）时，`PUT /auth/password` 答 409 `login-identifier-required`，口令节渲染这条码的文本，说明需要先添加用户名或已验证邮箱。
- `PhoneSection`：添加或更换手机号的两步——`POST /auth/phone` 发送验证码、`POST /auth/phone/verify` 提交它；第二步只带验证码，号码是服务端按会话存下的（[authn 登录](../authn/design-authn-login.md)）。
- `IdentifiersSection`：账户登录标识的列表、逐条删除与自助添加。标识读自 `GET /auth/me` 的 `identifiers` 字段（规格无独立的清单路由），删除走 `DELETE /auth/identifiers/{id}`。添加面走登录自助的 `POST /auth/identifiers`（体 `{kind, value}`，`kind` 取 `username` 或 `email`）：用户名一步写入；邮箱先发验证码、再经 `POST /auth/identifiers/verify` 提交码，通过才写标识（[authn 登录](../authn/design-authn-login.md)）。邮箱一项只在宿主经 `emailAvailable` 表明可用时出现在添加面里。
- `PreferencesSection`：语言与时区两个下拉，编辑账户存下的偏好，走账户档案的自写面（`PATCH /auth/me`，[authn-profile](../authn/design-authn-profile.md)）。语言的取值集是 i18n 实例自己的受支持集；语言的选取先写账户、确认写入后再落设备（手选槽位），清空偏好只动账户；时区取值集是引擎的 IANA 列表加一个显式的「未选」。
- `SessionsSection`：账户的会话与设备列表，当前会话所属的那条打上当前徽标，逐条撤销与批量撤销「其它会话」。撤销自己的会话是低损失动作（行上不再确认），撤销其余走 ui-kit 危险 `ConfirmDialog` 的单次确认——它是可恢复的动作（被登出的设备再登录即可），不设 `doubleConfirm` 两步守卫，免得比不可逆的删除还重。
- `SocialBindingsSection` 加 `BindingCallbackHandler`：社交绑定面与在宿主回调路由上完成一次绑定的处理器。添加区的渠道取自部署的发现文档（`GET /auth/methods` 中 `kind=redirect` 的方法）。
- `MfaSection`：经加强认证（step-up）门控的两步验证设置——清单经读发现，登记入口只给本部署装配的因素（TOTP 认证器、通行密钥等）。

这些面是节，不是一个带路由的屏：拥有它们的页面、它们之上的标题、以及本包之外的其它面（通用属性集的编辑、登录历史）都是宿主内容。

**特性在场由宿主给出，本包不读特性态**（[ADR-web-feature-state-2026-10-17](../../../adr/adr-web-feature-state-2026-10-17.md)）。宿主只在特性 `authn.phone` 在场时挂 `PhoneSection`，只在特性 `authn.mfa` 在场时挂 `MfaSection`；`IdentifiersSection` 的邮箱添加可用性经必填属性 `emailAvailable` 传入，宿主取特性 `authn.identifiers.email` 是否在场。其余节读写的面由[不经特性门控的后端模块](design-web.md#不经特性门控的后端模块)服务，不受特性门控；`PasswordSection` 依赖的 `PUT /auth/password` 随登录引擎恒挂。社交绑定可用的渠道集合只由发现文档发布，不经特性文档（[feature](../design-feature.md) 的「特性文档承载的事实」）。

### 非职责

- **不做通用属性集的编辑。** 自助档案面（`PATCH /auth/me`）的自写字段集是显示名、通用属性集、语言与时区（[ADR-authn-selfservice-profile-2026-10-15](../../../adr/adr-authn-selfservice-profile-2026-10-15.md)）；本包交付显示名（`ProfileSection`）与语言、时区（`PreferencesSection`），不交付自由键值对的通用属性集编辑——那需要一个不属于账户页固定字段的通用编辑器，属宿主内容。
- **自助添加标识的 `kind` 是封闭集，限于用户名与邮箱。** 手机走它自己的两步面（`PhoneSection`，`POST /auth/phone`）；`IdentifiersSection` 的添加面只给用户名与邮箱两种（[authn 登录](../authn/design-authn-login.md)）。
- **不交付登录历史节。** 登录历史没有自助端点，只有管理员面（[authn-admin](../authn/design-authn-admin.md) 的审计读）；普通账户看不到自己的登录历史，故本包不交付这一节。
- **不交付恢复码。** 认证的因素面不提供恢复码（[authn-mfa](../authn/design-authn-mfa.md)），故本包不交付恢复码的显示或重生成。
- **不拥有查询客户端。** 每次读经 `@speed/api-sdk` 的生成 react-query 钩子，跑在宿主拥有的 QueryClient 上；本包从不自造 QueryClient，也不手写查询键，失效一律用导出的查询键构造器。
- **不读存储、不 attach 会话、不导航、不直接触网。** 会话只在一条生在流程之上、由本包节驱动的会话操作存在时以属性传入——绑定添加区起一条 `purpose: 'link'` 流程（`session.startAuthenticatedFlow`），两步验证挑战与自助写的提权门控各起一条 `purpose: 'step-up'` 流程（同）。没有会话操作要驱动的节不接会话属性（会话列表节就不接）。
- **不发明规格没有的操作。** 认证的因素面只提供列出与删除（`GET`/`DELETE /auth/factors`），登记的入口是流程；故 `MfaSection` 的状态经读发现，登记走流程，不声明一个不存在的启用端点。
- **不 import `@speed/auth-ui`。** 同层包互不 import；`SocialProvider`、`SocialProviderConfig` 是本包自己的定义，与 auth-ui 的同名类型逐字一致并保持同步。`SocialProvider` 是部署的登录方法名（字符串，不是名字闭集），`SocialProviderConfig` 带方法自己的品牌标签。
- **不做服务端判定的替代。** 权限、租户隔离与账户归属都由服务端判定；本包只渲染应答。

## 外部接口契约

`web/packages/account-ui/src/index.ts` 是公开面：`ProfileSection`（连同 `ProfileSectionProps`）；`PasswordSection`（连同 `PasswordSectionProps`）；`PhoneSection`（连同 `PhoneSectionProps`）；`IdentifiersSection`（连同 `IdentifiersSectionProps`）；`PreferencesSection`（连同 `PreferencesSectionProps`）；`SessionsSection`；`SocialBindingsSection`（连同 `SocialBindingsSectionProps`、`SocialProvider`、`SocialProviderConfig`）；`BindingCallbackHandler`（连同 `BindingCallbackHandlerProps`）；`MfaSection`（连同 `MfaSectionProps`）；以及命名为 `ACCOUNT_UI_NAMESPACE`、`accountUiResources` 的资源对。`web/packages/account-ui/src/internal/` 下的错误文案解析器、失败横幅、加强认证流程对话框、自助写的提权门控与翻译钩子是族内管线，不外导。

```tsx
interface ProfileSectionProps {
  readonly session: AuthSession // 提权门控驱动一条 step-up 流程
}

interface PasswordSectionProps {
  readonly session: AuthSession
}

interface PhoneSectionProps {
  readonly session: AuthSession
}

interface IdentifiersSectionProps {
  readonly session: AuthSession
  readonly emailAvailable: boolean // 假时添加面不给邮箱一项
}

interface PreferencesSectionProps {
  readonly session?: AuthSession // 可选；只为提权门控的对话框服务，不给时被 401 拒绝的写只渲染失败横幅
}

interface SocialBindingsSectionProps {
  readonly session: AuthSession
  readonly returnTo: string // 宿主的绑定回调路由；作为 link 流程的顶层 return_to 发出
  readonly onAuthorizeUrl?: (url: string) => void
}

interface BindingCallbackHandlerProps {
  readonly session: AuthSession
  readonly onBound?: () => void
  readonly absent?: ReactNode // 读不到流程、且部署没有 redirect 方法时渲染
}

interface MfaSectionProps {
  readonly session: AuthSession // 登记与挑战都驱动流程
}
```

自助写节（`ProfileSection`、`PasswordSection`、`PhoneSection`、`IdentifiersSection`）的 `session` 属性必选，`PreferencesSection` 的可选；它们的 `session` 都只为提权门控的对话框服务：一次写被答 401 `authn.insufficient_user_authentication` 时开一条 `purpose: 'step-up'` 流程（`session.startAuthenticatedFlow`），成功后重跑那次被拒的写恰好一次；它们的读与写本身是走共享访问令牌的生成调用，不经会话操作。

`SocialBindingsSection` 的 `session` 属性经 `session.listMethods`（`GET /auth/methods`）读部署的发现文档以决定添加区给哪些渠道，并为添加区起一条 `purpose: 'link'` 的流程（`session.startAuthenticatedFlow`），`returnTo` 作为该流程请求的顶层 `return_to` 发出；`MfaSection` 的 `session` 属性为登记起一条 `purpose: 'enroll'` 的流程、为挑战起一条 `purpose: 'step-up'` 的流程，并经 `session.listMethods`（`GET /auth/methods`）读本部署装配的因素以决定提供哪些登记入口——读因子清单、删因子与解绑是走共享访问令牌的生成调用，不经会话操作。`SessionsSection` 不接属性：它显示谁的会话，来自调用方绑定的客户端与它的访问令牌。

**查询键构造器是公开面。** 失效用本包导出的查询键构造器（如 `getAuthnSessionListQueryKey`），不经手写键。账户自己的标识与绑定读自 `GET /auth/me`（其 `identifiers` 与 `links` 字段），无独立的清单端点，故无对应查询键。

**对外接口面。** 公开面是上述导出集；`SocialProvider`（方法名字符串）、`SocialProviderConfig` 的形状与 auth-ui 的一致。变更按 TypeScript 的源码语义分类：新增导出、新增可选字段是源码兼容的；删除导出或字段、改既有导出或字段的含义或名、改签名（参数或返回类型），是不兼容变更。把必选与可选互转按位置判：输入位（宿主提供的 prop 或参数）放宽兼容、收紧不兼容；输出位（宿主读取的返回类型或结果字段）相反——放宽为可选使读出的值成为 `T | undefined`，strictNullChecks 下只有在要求 `T` 处使用它的消费者无法编译（对可能为 undefined 的值做成员访问报 TS18048，赋给 `T` 或传入要求 `T` 的形参报 TS2322/TS2345），仅转发或在宽松处使用（模板串、比较、`??`、可选链）的消费者仍编译，故可能破坏消费者、是不兼容变更。文档化的用法由 `web/packages/account-ui/src/usage-example.test.tsx` 端到端编译并运行，故 README 的装配不能与 API 漂移。

## 依赖

| 依赖 | 用途 |
|---|---|
| `@speed/auth-core` | 流程操作与本部署方法发现的 `AuthSession` 类型与操作（`startAuthenticatedFlow`、`answerFlow`、`readFlow`、`listMethods`） |
| `@speed/api-sdk` | 每次读的生成 react-query 钩子、每次写的生成 mutation、查询键构造器，以及档案读取（`authnProfileGet`）与社交回跳读取（`authnLoginCurrent`）的调用 |
| `@speed/ui-kit` | `AsyncSection`、`ConfirmDialog`、`ListSkeleton` 与跨面状态原语（`errorCodeOf`、`InlineError`、`InlineSuccess`、`WriteFeedbackRegion`、`useWriteFeedback`、`visuallyHiddenSx`） |
| `@speed/i18n` | 命名空间注册与文案的翻译钩子 |
| `@tanstack/react-query` | peer（必需）：宿主供给的 QueryClient 与它上面的钩子 |
| `react`、`react-dom`、`@mui/material`、`@emotion/*` | peer（必需）：宿主拥有的 React／MUI 树 |

`@speed/api-client` 是 devDependency：公开类型不引用它，只有测试夹具绑定真实客户端。方向是 `@speed/account-ui` 比 `@speed/auth-ui` 的层高一档，处于生成钩子这一层之上；`@speed/auth-ui` 从不被 import。

## UML 图

一次读取与一次写入的路径。图里画不到的约束：读经生成的钩子跑在宿主 QueryClient 上，写经同一 `bindRequestFn` 绑定的生成 mutation；两步验证的登记入口另经会话操作 `session.listMethods`（`GET /auth/methods`）读本部署装配的因素，绑定添加区经同一操作读本部署的 redirect 方法，自助写被拒提权时另经会话操作 `session.startAuthenticatedFlow` 起一条 `purpose: 'step-up'` 流程；时间一律以账户的时区链显式格式化，不用 Intl 的进程本地缺省；显示时间之外，服务端词表（登录方式、因素种类、渠道）只在白名单内渲染。

```mermaid
sequenceDiagram
    participant U as 用户
    participant S as 账户节
    participant Q as QueryClient(宿主)
    participant C as 绑定的客户端
    U->>S: 打开账户页
    S->>Q: 生成钩子（useAuthnSessionList、useAuthnMfaFactors、authnProfileGet）
    Q->>C: 请求（宿主访问令牌）
    C-->>Q: 应答
    Q-->>S: 数据
    S->>S: 按账户时区与语言渲染
    U->>S: 一次写（撤销、解绑、存偏好）
    S->>C: 生成 mutation
    C-->>S: 应答
    S->>Q: 用查询键构造器失效相关查询
```

## 核心数据结构

| 概念 | 形状 | 说明 |
|---|---|---|
| 时区链 | `resolveTimeZone` | 存下的偏好，否则设备时区，否则 UTC；每个时间渲染都显式传 `timeZone` |
| 渠道词表 | `SocialProvider`、`SocialProviderConfig` | 本包自己的定义，与 auth-ui 的同名类型同步；`SocialProvider` 是方法名字符串，`SocialProviderConfig` 带方法自己的品牌标签 |
| 加强认证流程 | `StepUpFlow` | 一条 `purpose: 'step-up'` 流程的驱动；成功后重跑被门控的动作 |
| 命名空间 | `accountUiResources` | `zh-CN` 与 `en-US` 两份目录，叶子键集相同 |

## 核心算法与流程

### 自助写的提权门控

改显示名、改口令、改手机、加标识与删标识，以及改偏好（语言、时区，给了 `session` 时），都要求新鲜认证：一次写被服务端判为未提权时答 401 `authn.insufficient_user_authentication`。节把该次写的输入记在提权门控里（`src/internal/step-up-gate.ts` 的 `useStepUpGate`），打开加强认证流程对话框驱动一条 `purpose: 'step-up'` 流程；流程落定提权后，门控重跑被记住的那次写恰好一次，取消则不重跑。

提权码与登录失败码都在 401 上：改口令的当前口令不匹配是 401 `login-failed`，与提权 401 靠问题码区分——前者进失败横幅，后者开门控。手机的两步各自门控：发码被拒重跑发码、验证被拒重跑验证，不混用。加标识的两步同此：添加被拒重跑添加、验证被拒重跑验证。

写成功后按「改一次就失效」失效相关查询（读档的失效档案读、改口令、加手机与加标识再失效会话列表，因为这些路由会撤销其余会话），让视图收敛到服务端应答。

### 经流程门控的两步验证

`MfaSection` 不声明因子状态，状态经读发现（`GET /auth/factors`）；它提供的登记入口也只取本部署装配的因素（`GET /auth/methods` 的 `mfa.factors`，经 `session.listMethods` 读回）——装配没有通行密钥的部署不显示通行密钥入口，宿主不得提供本部署无法完成的能力。两次入口动作走同一条「先生成流程、再按步答复」的机器：

1. 登记一个因素。一次 `startAuthenticatedFlow({purpose: 'enroll', input: {kind}})` 打开向导，按应答的 `next.kind` 分派：`webauthn` 步把该因素自己的 JSON 对象交给步进渲染器——TOTP 的 `otpauth_uri` 渲染为文本加一个 `code` 字段，通行密钥的创建选项由本包的 `internal/webauthn.ts` 驱动 `navigator.credentials.create` 并把返回的凭据序列化成答复键 `credential`；`enroll` 步给出可登记的种类。答完即因素登记完成，它不改变本次会话的等级。
2. 提权以做安全类动作。一次 `startAuthenticatedFlow({purpose: 'step-up'})` 起一条加强认证流程；账户有可用因素时从 `factor` 步进入（清单在应答里）。挑战对话框驱动这条流程，成功后重跑被门控的动作。起手之前，对话框读一次本人已登记的因素（`GET /auth/factors`）：没有任何启用因素时，重新登录只能得到同样强度的单因素会话，对话框提示先在账户设置里登记两步验证，不渲染前往入口（本包不导航），不发起流程；该读未落定时对话框显示起手中的占位；读失败不判定为没有因素，照常起手，由服务端的拒绝决定；有因素而服务端以 401 `authn.insufficient_user_authentication` 拒绝起手（有因素但没有能强化本会话的）时，对话框判定本部署无法完成升级，给出重新登录的出口，不渲染任何步骤。

一次门控动作在被拒时打开加强认证流程，记住被门控的动作；一次成功的流程落定提权，成功路径重试那次被门控的操作恰好一次，取消则不重试。提权只活在那次会话的存续期内，对话框从不承诺不再询问。

### 绑定添加区的渠道与回跳

添加区的渠道取自 `session.listMethods()` 读回的发现文档：其中 `kind=redirect` 的每个方法、且账户尚未绑定的，渲染一个按钮，名称取描述符的 `label`，缺省用方法名。被部署关闭的方法不在发现文档里，因而不出现在添加区。点击起一条 `purpose: 'link'` 的流程，请求以顶层 `return_to` 携带 `returnTo`：起流程只读顶层 `return_to`，回跳只用流程记录里的值（[authn 登录](../authn/design-authn-login.md)），提供方的往返因而落回宿主的绑定回调路由，由 `BindingCallbackHandler` 续接。

### 绑定回调的续接

`BindingCallbackHandler` 挂在宿主的回调路由上，以 `session.readFlow()` 读回那条 `purpose: 'link'` 流程：流程已就绪（`next.kind = complete`）时以 `step: 'complete'` 取走结果，`link` 落地（`state: 'linked'`）后失效身份读取并触发 `onBound`；流程仍停在一步时渲染该步续接；交换失败渲染错误码文本与一个重试按钮。绑定的 `link` 不把任何人签进去。

**本部署不提供社交绑定时的落点。** 回调路由在租户框架内，只有已认证者到达。宿主给了 `absent` 时，`readFlow()` 返回 `null` 之后处理器读一次 `session.listMethods()`：发现文档里没有 `kind=redirect` 的方法，即本部署不提供社交绑定，渲染 `absent`，宿主借此给出与未登记路径相同的 404（[ADR-web-route-seam-2026-10-17](../../../adr/adr-web-route-seam-2026-10-17.md)）；有这类方法或发现读取失败时，照常渲染错误码文本与重试按钮。读到流程时不读发现文档。宿主没给 `absent` 时，处理器不读发现文档，`readFlow()` 返回 `null` 即渲染错误码文本与重试按钮。

### 写入的反馈

每个节按 [ui-kit](design-web-ui-kit.md) 的「写入的反馈」组织反馈：一个节是一个反馈作用域，区域紧贴该节的控件之下（[ADR-web-write-feedback-2026-10-17](../../../adr/adr-web-write-feedback-2026-10-17.md)）。

- **成功确认只经区域渲染。** 成功由 `WriteFeedbackRegion` 渲染，间距经区域的 `sx`；本包没有第二种成功区域，调用点不单独渲染 `InlineSuccess`。
- **每个由用户发起、落定且无一失败的动作都有成功文案**，含撤销单个会话、删除标识、两步验证的登记与移除，文案点明被改动的对象。社交绑定的完成是例外：`onBound` 是宿主离开回调路由的信号，离开的宿主以刷新后的绑定行呈现结果；留在该路由或未给 `onBound` 的宿主得到 `done` 阶段的绑定成功文案，挂在 `role="status"` 区域里。
- **一个节一个失败横幅。** 标识节的失败只经区域渲染一次，添加、验证与移除共用同一个槽；取消添加与每次移除都 `reset`，使「已添加」的成功文案不在其后的移除之后停留，取消也不留下失败横幅。口令节的客户端校验失败同样以 `reset` 开头，不让上一次的服务端横幅与新的字段错误并存。
- **「验证码已发送」的目的地文字在验证码一步内持续显示。** 它说明验证码发往哪个号码或邮箱，不是槽的结果，验证失败、`reset` 与无关的移除都不清除它；一个先挂空区域、再填文本的 `role="status"` 只在发码落地时播报一次，发码成功后焦点落在验证码字段。本包没有重发控件，回退再发码的路径以 `reset` 开头，播报区的文本因此经空到非空。
- **偏好节的写入被 401 `authn.insufficient_user_authentication` 拒绝时走提权门控。** 偏好节接受类型同为 `AuthSession` 的可选 `session`：给了会话，被拒的写由门控记住，成功提权后重放一次；没给会话，失败横幅沿用该码的失败文案（要求重新验证身份），不开对话框，宿主只能在同页其它带会话的节完成提权（提权对整页的会话生效）或重新登录。参考装配把 `session` 传给偏好节。偏好写入失败渲染一条横幅，控件退回存下的值；语言写入在 `switchLanguage` 落定之后才调用 `succeed`，使确认文案取自新语言。
- **通行密钥仪式的失败与区域里的失败横幅互斥。** 仪式失败是客户端原因，文本取自本包 bundle 的键，不经错误码白名单；一条出现时另一条被清掉。
- **读的失败占位由各节自带，刷新失败由各节标明。** 每个以读为首的节在自己的读失败时渲染占位与重试；会话列表节读档案只为取显示时区，档案读失败时不渲染占位，回落到设备时区。已有数据时的读刷新失败（含写入后的失效再取）经 `AsyncSection` 的 `staleState` 标明：旧内容保留，其上渲染陈旧提示与重试，例如撤销之后重读失败时，旧行留在陈旧提示之下。
- **错误白名单组合 `SESSION_LIFECYCLE_ERROR_CODES`。** 本包的白名单在自己的可达码与 `CLIENT_TRANSPORT_ERROR_CODES` 之外组合该族（[i18n](design-web-i18n.md) 的「扩展点」），每个码在 zh-CN 与 en-US 的 `errors.*` 下各有一条叶子，使会话在途中失效时到达的这类码渲染各自的文案，而不是 `errors.unknown`。

## 错误与失败语义

**失败面是一个错误码白名单加受守卫的词表。** 每条服务端与传输失败解析成一个错误码，渲染在一条 `role="alert"` 横幅里；通行密钥仪式的失败是唯一的客户端来源，文本取自本包 bundle 的键，不经白名单；白名单外的码渲染 `errors.unknown`，永不渲染裸键。因素种类与渠道同样受守卫：令牌只在组件已知的取值表上才经 `t()` 渲染，否则渲染通用的其它标签。

| 情形 | 表现 |
|---|---|
| 一次写被拒（`authn.bad_request`、`authn.throttled`、`authn.unavailable`、`client.*`） | 一条横幅渲染错误码文本，相关查询在选择处失效 |
| 自助写被答 401 `authn.insufficient_user_authentication` | 打开提权对话框，成功后重跑那次被拒的写一次 |
| 改口令的当前口令不匹配（401 `authn.login_failed`） | 一条横幅渲染该码文本；与提权 401 同状态码，靠问题码区分，不误开门控 |
| 加手机时号码已属另一账户（409 `authn.conflict`） | 一条横幅渲染该码文本 |
| 删标识会抽掉最后一个登录方式（409 `authn.conflict`） | 一条横幅渲染该码文本，服务端按自己的登录方式计数判定 |
| 撤销自己的会话命中已死的会话 | 一条横幅渲染该码文本，列表重取以收敛 |
| 解绑会抽掉最后一个登录方式 | 一条横幅渲染该错误码文本，服务端按自己的登录方式计数判定 |
| 两步验证登记被拒（他人的登记抢先、因素消失） | 关掉向导并渲染错误码文本；一次错码是字段级错误、可重试 |
| 加强认证流程的码已被用过 | 清空字段、渲染用过的码文本，绝不用同一码重试 |
| 本人账户没有启用的因素（`GET /auth/factors` 读回的行里没有处于启用状态的） | 对话框提示先在账户设置里登记两步验证，不渲染前往入口，不发起流程 |
| 读本人因素失败 | 不判定为没有因素，照常发起流程，由服务端的拒绝决定 |
| 加强认证流程的起手被 401 `authn.insufficient_user_authentication` 拒绝（有因素，但没有能强化本会话的） | 对话框判定本部署无法完成升级，给出重新登录的出口，不渲染步骤表单 |
| 加强认证流程的起手因其它的原因失败（400 等） | 一条横幅渲染错误码文本 |
| 删除因素时它是账户最后一个 | 一条横幅渲染该错误码文本，服务端判定 |
| 偏好写入被拒（`authn.bad_request`） | 一条失败横幅，控件退回存下的值（天然回退，无撤销记账） |
| 偏好写入被 401 `authn.insufficient_user_authentication` 拒绝 | 给了 `session` 时打开提权对话框，成功后重放那次写一次；未给时渲染失败横幅 |
| 绑定回调读不到流程，宿主给了 `absent`，且发现文档里没有 `kind=redirect` 的方法 | 渲染 `absent` |
| 绑定回调读不到流程的其余情形（有 `kind=redirect` 的方法、发现读取失败、宿主没给 `absent`） | 错误码文本与一个重试按钮 |

## 并发与事务边界

- **写后失效。** 每次成功的写（撤销、解绑、绑定、改显示名、改口令、加手机、加标识、删标识）都失效相关查询，让视图收敛到服务端应答；改口令、加手机与加标识另失效会话列表，因为这几条路由会撤销账户的其余会话。失效用导出的查询键构造器。
- **提权的存续期随会话。** 一次加强认证流程的完成更新会话的等级与认证时刻；未被提权的安全类动作由服务端判 401 加强认证，本包据此打开挑战流程。
- 本包不打开事务；读与写都是宿主 QueryClient 与绑定客户端上的往返。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 档案显示名节 | 已实现 | `web/packages/account-ui/src/ProfileSection.tsx` `ProfileSection` |
| 口令节 | 已实现 | `web/packages/account-ui/src/PasswordSection.tsx` `PasswordSection` |
| `login-identifier-required` 的文本点名用户名或已验证邮箱 | 未实现 | — |
| 手机号节 | 已实现 | `web/packages/account-ui/src/PhoneSection.tsx` `PhoneSection` |
| 登录标识节 | 已实现 | `web/packages/account-ui/src/IdentifiersSection.tsx` `IdentifiersSection` |
| 自助写的提权门控 | 已实现 | `web/packages/account-ui/src/internal/step-up-gate.ts` `useStepUpGate` |
| 偏好节 | 已实现 | `web/packages/account-ui/src/PreferencesSection.tsx` `PreferencesSection` |
| 各节的反馈作用域与成功确认 | 未实现 | — |
| 读的刷新失败的陈旧提示 | 未实现 | — |
| 错误白名单组合会话生命周期码族及其双语文案 | 未实现 | — |
| 偏好节的提权门控 | 未实现 | — |
| 「验证码已发送」的 live region 与焦点 | 未实现 | — |
| 绑定回调完成阶段的成功文案 | 未实现 | — |
| 会话列表与撤销 | 已实现 | `web/packages/account-ui/src/SessionsSection.tsx` `SessionsSection` |
| 社交绑定与回调 | 已实现 | `web/packages/account-ui/src/SocialBindingsSection.tsx` `SocialBindingsSection`；`web/packages/account-ui/src/BindingCallbackHandler.tsx` `BindingCallbackHandler` |
| 两步验证设置 | 已实现 | `web/packages/account-ui/src/MfaSection.tsx` `MfaSection` |
| 命名空间资源 | 已实现 | `web/packages/account-ui/src/resources.ts` `ACCOUNT_UI_NAMESPACE`、`accountUiResources` |
| 登录标识节的邮箱可用性属性 `emailAvailable` | 未实现 | — |
| 社交绑定的 link 流程以顶层 `return_to` 发出回跳地址 | 未实现 | — |
| 绑定添加区的渠道取自发现文档，`SocialBindingsSectionProps` 不含 `providers` | 未实现 | — |
| 绑定回调在本部署不提供社交绑定时渲染 `absent` | 已实现 | `web/packages/account-ui/src/BindingCallbackHandler.tsx` `BindingCallbackHandler` |
