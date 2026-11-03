# authn 登录

## 职责

- 持有登录流程。一次登录是 kv 里的一条流程记录，由引擎驱动的多步状态机推进，每次迁移是对流程记录的比较并交换。引擎、流程记录、绑定、迁移表与终态动作只在 `authn/login` 一处实现（[ADR-authn-login-flow](../../../adr/adr-authn-login-flow.md)）。
- 定义 `LoginMethod` 接缝与方法的准入条件。接缝在根包 `pkg/authn` 导出；方法只回答「这个人证明了什么」，以 `Proof` 交出事实，不做账户解析、开户、首登开户的用户名收集、关联、第二因素判定、建会话或发令牌。
- 在第一因素通过之后做与方式无关的全部动作：把证明解析成账户（含开户与显式关联）、向 `MFA` 询问是否还要第二因素或登记因素、建登录会话或发令牌。首登开户在建号之前先取得用户名：账户解析判定要开户时，流程停在待建号，由引擎向客户端索取一个合法用户名，用户名通过之后才建账户（[ADR-authn-username-required](../../../adr/adr-authn-username-required.md)）。账户解析的规则归 [authn-account](design-authn-account.md)，因素、策略与保障等级的合成归 [authn-mfa](design-authn-mfa.md)，本文只写引擎怎样调用它们、怎样处理它们的结果。
- 规定口令登录只接受用户名或账户选作登录方式的已验证邮箱，短信登录只接受账户选作登录方式的已验证手机号；提供方声明的地址不作口令登录名，提供方登录与口令登录不混用（REQ-signin-006、REQ-signin-011、REQ-signin-038）。
- 让目的 `login`、`link`、`step-up`、`enroll` 共用同一台状态机，终态动作按目的区分。目的取值是封闭集。
- 在 `authn.gate.mount.endpoints.default` 指定的端、普通面的基路径之下挂登录路由、回跳路由、方式列表与账户自助路由：未认证的登录流程与回调挂普通面的基路径。登录、注册与自助面是全局账户的请求，不带租户名，因而不得落到任何被租户解析或准入层所守的端上。路由与访问级别经 `mount.Mounter` 用同一个调用声明（[authn-gate](design-authn-gate.md)）。账户自助里随装配成立的面声明为[条件特性](../../../glossary.md)，在决定它们是否成立的同一处判定里经 `feature.Board` 的 `Post` 报告：手机号自助在挂其路由的分支里，邮箱分支在与该分支答 503 同源的判定处（见「声明的特性」）。
- 挂匿名注册路由 `POST B/register`，经账户模块的 `Signups.Register` 一次建号：注册收集用户名、口令与邮箱，不建立会话、不发送邮件；同一事务插入的邮箱预留以唯一索引仲裁地址占用，预留不是标识，既不作登录方式，也不作联系方式或默认联系地址（REQ-signup-007；[ADR-authn-signup](../../../adr/adr-authn-signup.md)、[ADR-authn-contact-claim](../../../adr/adr-authn-contact-claim.md)）。开户后客户端走既有的 `POST B/flows` 口令方法登录，邮箱在登录后经账户自助回路验证。
- 规定认证前的失败退避与枚举防护在登录路径上的形状：在何处计数、何时拒绝校验、应答怎样同形同时。退避状态的存取面 `Throttle` 与 `authn_throttles` 表归 [authn-account](design-authn-account.md)（[ADR-authn-throttling](../../../adr/adr-authn-throttling.md)）。
- 规定口令、社交登录（Google，[REF-google-openid-connect](../../../references.md)、GitHub，[REF-github-oauth-apps](../../../references.md)）与微信（[REF-wechat-website-login](../../../references.md)、[REF-wechat-oa-web-auth](../../../references.md)）各方式的流程。短信、通行密钥（[REF-webauthn](../../../references.md)）、企业 OIDC（[REF-openid-connect-core](../../../references.md)）与 SAML（[REF-saml2](../../../references.md)）作为方式接入同一接缝，各自的状态机与校验分别归 [authn-sms](design-authn-sms.md)、[authn-mfa](design-authn-mfa.md)、[authn-sso](design-authn-sso.md)。

### 非职责

- **不存账户，不定账户解析的规则。** 账户、标识、联系方式记录、外部身份绑定、成员关系与口令凭据的存取，以及首登开户、自助注册开户与显式关联的判据，归 [authn-account](design-authn-account.md)；引擎只按那里的规则调用。注册路由不自己写账户表：校验、开户事务与注册验证状态都由 `Signups.Register` 承担。
- **不建会话、不签令牌。** 会话的建立、cookie 的写出与撤销归 [authn-session](design-authn-session.md)，令牌的签发归 [authn-token](design-authn-token.md)；引擎经 `Sessions` 与 `Tokens` 调用，不自己拼 cookie。
- **不实现因素，不合成保障等级。** 因素的登记、校验与保障等级的唯一计算处是 `authn.mfa`；方法只交出 `Proof.UserVerified` 与 `Proof.IDPAsserted` 这类事实。
- **不持有短信验证码的状态。** 挑战行、预算、尝试次数与发送归 [authn-sms](design-authn-sms.md)。
- **不登记企业 IdP，不校验 SAML 断言。** 提供方行、域声明、元数据与断言校验归 [authn-sso](design-authn-sso.md)。
- **不认识租户。** 账户全局，登录请求不带租户名；没有按租户启停登录方式的能力，只有全局的紧急开关。租户级的认证强度策略与成员判定在请求期由 [tenant-admission](../tenant/design-tenant-admission.md) 执行，`pkg/authn` 的任何子包都不 import `pkg/tenant`。
- **不经 `pkg/ratelimit` 限流。** 该模块的配额层落在限流阶段、排在鉴别阶段之内、无身份即放行、维度只有（租户，责任键），认证前的失败退避由 `Throttle` 承担（算法归 [authn-account](design-authn-account.md)）。
- **不写网关自产的 401。** 网关为各端建成的挑战与「没有凭据」的 401 归 [authn-gate](design-authn-gate.md)；本模块只写自己处理器的应答。
- **不提供登录页，不绘制人机验证控件。** 界面是宿主的前端，本模块只给 JSON 接口；人机验证的凭证由前端控件向部署选定的机制取得，本模块只校验它并据此放行发送，入口层面的洪泛限流是宿主的部署责任。
- **不做授权判定。** 本模块的 403 只转述认证之外的策略，且只出现在 authn 自己的处理器里（[authn-admin](design-authn-admin.md)）。

## 外部接口契约

### 登记单元

根包 `pkg/authn` 只放接口与数据类型，不登记模块。`authn.login` 不交付任何功能，是流程引擎加路由；每个方法住在一个叶子包、登记一个模块、非排他地交付 `LoginMethod`，引擎经 `core.ResolveAll` 取用（[ADR-authn-wiring](../../../adr/adr-authn-wiring.md)、[ADR-authn-packaging](../../../adr/adr-authn-packaging.md)）。本表列出本仓库交付的方法，是该范围内的封闭集；第三方方法自带名字，不在表内。配置键与设置键的全表见 [authn-config](design-authn-config.md)。

| 包 | 模块名 | 方法名（`Name()`） | 说明 |
|---|---|---|---|
| `authn/login` | `authn.login` | — | 引擎与路由，不交付功能 |
| `authn/login/password` | `authn.login.password` | `password` | 本文规定流程 |
| `authn/login/sms` | `authn.login.sms` | `sms` | 验证码状态机归 [authn-sms](design-authn-sms.md) |
| `authn/login/google` | `authn.login.google` | `google` | 本文规定流程 |
| `authn/login/github` | `authn.login.github` | `github` | 本文规定流程 |
| `authn/login/wechatweb` | `authn.login.wechat-web` | `wechat.web` | 网站应用扫码；本文规定流程 |
| `authn/login/wechatmp` | `authn.login.wechat-mp` | `wechat.mp` | 公众号网页授权；本文规定流程 |
| `authn/mfa/webauthn` | `authn.mfa.webauthn` | `passkey` | 同一模块还交付 `Factor`，见 [authn-mfa](design-authn-mfa.md) |
| `authn/sso/oidc` | `authn.sso.oidc` | `oidc` | 企业 OIDC，见 [authn-sso](design-authn-sso.md) |
| `authn/sso/saml` | `authn.sso.saml` | `saml` | 企业 SAML，见 [authn-sso](design-authn-sso.md) |

**编入二进制不等于本次提供这个方式。** 每个方法在 `Prepare` 中依据自己那份配置表态：配置缺席时表态 `StateDisabled` 并给出原因，方法不交付，发现文档不列出它，登录页不出现对应入口；配置只给了一部分（例如客户端标识给了而密钥没有）是半配置，`ErrInvalidConfig` 终止启动并点名缺的那项；配置齐备时 `StateEnabled`，其余该项要求的值（网关的公开地址、出口接缝）照常校验，不合法同样终止启动。判据落在方法自己手里，`core` 不解释原因（[ADR-module-enablement](../../../adr/adr-module-enablement.md)）。

- **Google、GitHub 与两个微信方法**以客户端标识与客户端密钥合在一起作为「配置了这家应用」的陈述：两项都没有即未配置，自行关闭；只给一项即半配置。
- **企业 OIDC 没有可供推断的凭据**：提供方是运行期的行，注册表为空不代表部署不要这项能力，因此它由显式开关 `authn.sso.oidc.enabled`（缺省关）决定是否交付。
- **短信方法**跟随短信发送能力的载体选择：选择项为 `none`，或选择项为空且一个短信模板也没有配置时，它随 `authn.sms` 一起关闭（见 [authn-sms](design-authn-sms.md)）。
- **口令方法**不需要外部配置，装配纳入即提供；运行期设置不关闭它，`authn.login.disabled-methods` 的取值域排除 `password`，读取侧也只过滤掉越域取值里的 `password`。

### LoginMethod 接缝

接缝与它的数据类型在根包导出，引擎之外没有任何调用方。以下是签名，裁剪到承载设计含义的部分。

```go
// LoginMethod 是一种第一因素。非排他，经 core.ResolveAll 取用。
type LoginMethod interface {
	// Name 是稳定名，出现在路由、流程记录、指标属性里；全局唯一，重名即 ErrMethodConflict。
	Name() string
	// Describe 自报该方法在发现端点里的描述符；它是纯常量，不读存储与配置。
	Describe() MethodDescriptor
	Begin(ctx context.Context, s Start) (Step, error)
	// Answer 处理客户端对上一步的答复，或外部回跳（OAuth 的授权码、SAML 的断言）。
	Answer(ctx context.Context, f FlowState, in StepInput) (Step, error)
}

// MethodAbandoner 是方法为自己在流程里保存了状态时可选实现的接口：引擎在删除流程记录前调用它。
type MethodAbandoner interface {
	Abandon(ctx context.Context, f FlowState) error
}

type Start struct {
	Purpose   string            // login、link、step-up、enroll
	Account   AccountID         // link 与 step-up 时已知
	Fields    map[string]string // 开始请求的 input，至多 8 个、每个至多 512 字符
	Client    ClientInfo        // 来源地址与用户代理：供日志与审计，不作判定与绑定依据
	SendsLeft int               // 本流程还可发的短信条数，见「短信发送的每流程上限」
	Locale    string            // 请求语言：i18n 协商结果原样带出，命名语言时为空串（见「流程记录」的 locale）
}

// FlowState 是引擎交给方法的、方法不得改写的视图，加上方法私有的字节。
type FlowState struct {
	ID        string    // 公开的流程标识，只用于日志关联，不是密钥
	Purpose   string
	Step      string    // 方法自己命名的当前步骤
	Provider  string    // 回跳路径必须点名的提供方：社交方法是方法名，企业提供方是标识，空表示不限定
	Account   AccountID
	Session   SessionID // authenticated 流程：发起它的登录会话
	Private   []byte    // 方法私有、不透明、至多 8 KiB，引擎原样保存
	SendsLeft int       // 本流程还可发的短信条数，见「短信发送的每流程上限」
	Locale    string    // 同 Start.Locale，从发起请求沿用
	Source    string    // 发起因素挑战那次请求的来源地址；引擎只在 challengeFactor 处填入，空表示没有可信来源，发码时不施加来源预算
}

type StepInput struct {
	Action string            // submit 或 callback
	Fields map[string]string
	Query  url.Values        // 仅外部回跳
	Form   url.Values        // 仅外部回跳（SAML 的 POST 绑定）
	Client ClientInfo
}
```

**放弃流程时的释放。** 流程被放弃时，引擎在删除流程记录前把流程的私有状态交给为它保存状态的一方：登记流程交给多因素模块，处在第一因素的流程交给它点名的方法；该方实现了 `MethodAbandoner` 才调用。第二因素的私有状态属于某个因素，已落定的流程已经花掉了自己的状态，两者都不在此列——把一方的字节交给另一方只会让它误读。回调在单次调用预算内运行；它失败或 panic 只记一行日志，流程记录照常删除，因为放弃是幂等的，记录留在原处反而会保住客户端已经放弃的绑定。

`Step` 是方法对引擎的回答，字段不导出，只能由构造器与 `WithSends` 产生；零值读作「没答」。回答的种类是下列构造器给出的封闭集：

```go
func StepPrompt(step string, private []byte, fields ...Field) Step // 向客户端要这些字段
func StepRedirect(step string, private []byte, to string) Step     // 让用户代理去外部方；to 必须是 https
func StepData(step string, private []byte, v any) Step             // 回给客户端一段 JSON（因素或方法自己的对象）
func StepProven(p Proof) Step                                      // 第一因素通过
func StepRefused(cause error) Step                                 // 凭据不成立；对外统一答复，成因只进日志
func StepThrottled(retryAfter time.Duration) Step                  // 429 加 Retry-After
func StepUnavailable(cause error) Step                             // 此刻答不了；503
func StepPassed(p Pass) Step                                       // 因素阶段：该因素通过
func StepEnrolled(r FactorRecord) Step                             // 登记流程：因素已登记

func (s Step) WithSends(n int) Step // 报告本次调用已成功调用 SMSCodes.Send 的次数；加在任一种非零回答上
```

`Field` 是 `{Name, Kind, Required, Secret}`，`Kind` 的取值是封闭集：`text`、`password`、`code`、`phone`，对应根包常量 `FieldText`、`FieldPassword`、`FieldCode`、`FieldPhone`。`StepPassed` 与 `StepEnrolled` 只由因素实现经 `authn.mfa` 交出，第一因素的方法不使用它们。`WithSends` 的用法见「短信发送的每流程上限」。

**`Step` 的读取面是包级函数，只在 `pkg/authn` 树内可见。** 判据与 `Verdict` 的读取函数相同：方法会随别名带到任何包，包级函数才把读取限在树内，而只有登录引擎有理由拆开一个回答；`WithSends` 是唯一的方法，因为它是方法与因素往回答上添东西的途径，属于方法作者看得见的那一面。读取函数按回答的种类各有一个：`KindOf` 报种类（零值报没答）；`NameOf`、`PrivateOf`、`FieldsOf` 读 `StepPrompt`、`StepRedirect`、`StepData` 带的步骤名、方法私有状态与字段；`RedirectOf`、`DataOf` 读跳转目标与 JSON 载荷；`ProofOf`、`PassOf`、`RecordOf` 读 `StepProven`、`StepPassed`、`StepEnrolled` 的载荷；`CauseOf` 读 `StepRefused` 与 `StepUnavailable` 的成因，`RetryAfterOf` 读 `StepThrottled` 的等待；`SendsOf` 读 `WithSends` 设置的条数。读取函数对不属于它那一种的回答返回零值：空串、`nil`、零 `Proof`、零 `Pass`、零时长。在错误的种类上读不到任何东西，零 `Proof` 不指任何账户，账户解析因而失败，零 `Pass` 不带任何方式；引擎因此先按 `KindOf` 分支再读。返回切片、映射或指针的读取函数交出副本，构造器收进来的同类值也先拷贝；`StepData` 的载荷是 `any`，无法深拷贝，按原值持有，方法交出之后不得再改它。

`Proof` 是方法交出的全部事实。来源字段 `Account`、`Identifier`、`External` 恰有一个非空，其余字段是附加事实：

```go
type Proof struct {
	Account      AccountID      // 方法自己已确定账户（口令、通行密钥）
	Identifier   *Identifier    // 方法证明了一个标识的所有权（短信验证的手机）
	External     *External      // 外部身份
	Methods      []Method
	UserVerified bool           // 通行密钥登录断言要求用户验证：该因素自己就相当于两个因素
	IDPAsserted  bool           // 企业 IdP 声明已做多因素，且提供方行的 trust_idp_mfa 与 mfa_acr_values 命中
	Provider     string         // 企业 SSO 提供方标识
	Aliases      []ExternalLink // 同一外部身份此前可能绑定的旧键
	PasswordChanged bool        // 口令方法在自己重设了凭据时置真，引擎据此在完成时撤销其余会话
}
```

`Proof` 只报事实，不报保障等级；等级只在 `authn.mfa` 的 `MFA.Assurance` 一处合成。引擎据 `Proof` 解析账户，不信任方法对账户存在与否的任何断言。`Aliases` 供微信把 `wechat-openid:<appid>` 键升级为 `unionid` 键，规则见 [authn-account](design-authn-account.md)。

### 方法的准入条件

下列条件的全集约束每一个 `LoginMethod`，第三方方法同受约束：

- 必须响应 `ctx`。引擎对单次方法调用另设预算（设置 `authn.login.call-budget`，含外部提供方往返），超出即作废该次回答。
- 宕机时交 `StepUnavailable`，不交 `StepRefused`：503 与 401 不混桶。
- 不自己开户、并入或建会话；不碰 HTTP、gin（[REF-gin](../../../references.md)）、会话与令牌；不自己读写流程记录，比较并交换归引擎。
- 失败时不得因标识存不存在而在耗时与错误形态上有差别。
- `Name()` 全局唯一。
- 发送短信的方法在每次调用 `SMSCodes.Send` 之前核对 `Start.SendsLeft` 或 `FlowState.SendsLeft`，并按「短信发送的每流程上限」报告已发条数。
- panic 由引擎 `recover`，按该方法故障收口为 503，记 Error 与调用栈；一个方法的缺陷不波及其他方法。

方法不依赖 HTTP 与存储，所以可以脱离二者单测：测试直接构造 `Start`、`FlowState`、`StepInput`，断言回答的种类。

### 路由

基路径 `B` 取普通面的 `authn.gate.mount.bases.default`（默认值见 [authn-config](design-authn-config.md)）：普通面的登录路由、回调与账户自助共用这一条基路径。「访问」是 `Mounter` 登记时同时声明的级别：`Open` 即豁免，`Optional` 即可选认证，`Required` 即网关认证。本表是本模块挂的路由的全集：

| 方法 路径 | 访问 | 作用 |
|---|---|---|

| `GET B/methods` | Open | 列出已纳入且未被 `disabled-methods` 关掉的登录方式描述符；口令方式不因该设置隐藏；并发布注册开关、可用的第二因素种类与人机验证门槛 |
| `POST B/flows` | Open | 开始 `login` 流程，body `{method, delivery, return_to, binding, input, human_proof}`；不读 cookie；流程绑定在此建立；口令方式不因 `disabled-methods` 被拒 |
| `POST B/register` | Open | 匿名自助注册，body `{username, email, password, display_name?, locale?, time_zone?}`；邮箱必填并作为未证明预留写入，不是登录方式、联系方式或默认联系地址；一次事务建号，不建会话、不发邮件 |
| `POST B/flows/authenticated` | Required，`Impersonable` | 在已登录的身份上开始 `link`、`step-up` 或 `enroll`（body 同上加 `purpose`）；流程记录绑定生效账户与会话；冒充期间绑定被冒充账户与发起者的会话；`enroll` 是因素登记的唯一入口；起始状态与前置要求见「在已登录身份上开始流程」 |
| `POST B/flows/answer` | Open | 答复当前步，body `{seq, step, input, human_proof}`；`step` 取 `answer`、`factor`、`enroll`、`complete`；要求流程绑定。待建号状态下的 `step=answer` 由引擎处理用户名，不交给方法 |
| `GET B/flows/current`、`DELETE B/flows/current` | Open | 读当前流程状态，供界面刷新后续接；放弃流程，幂等，删除记录前先让为流程保存状态的一方释放它（见「LoginMethod 接缝」）；要求绑定 |
| `GET\|POST B/callback/:provider` | Open | OAuth（[REF-rfc6749](../../../references.md)）与企业 OIDC 的回跳、SAML 的 ACS；`:provider` 对社交类方法是方法名，对企业 SSO 是 IdP 标识；`POST` 登记跨源检查旁路；SAML 回跳的体上限见 [authn-sso](design-authn-sso.md) |
| `PUT B/password`、`POST B/phone`、`POST B/phone/verify`、`POST B/identifiers`、`POST B/identifiers/verify`、`PUT B/identifiers/:id`、`DELETE B/identifiers/:id`、`DELETE B/links/:id` | Required，`Impersonable` | 账户自助：改口令、添加或更换邮箱与手机、添加用户名、设置标识的登录方式与默认联系地址、删标识、解绑外部身份；改口令、添加用户名、选作或取消登录方式、设置默认联系地址、删除标识与解绑外部身份要求新鲜认证并过 CSRF，登记并验证一条邮箱或手机本身不要求新鲜认证、不轮换会话、不结束其他会话，但其证明改变账户据以证明或找回自己的事实时例外——一条将取代默认联系地址或成为唯一邮箱地址的邮箱、一个将取代已启用短信因素发码目标的新手机号，其证明要求新鲜认证；冒充身份与被冒充账户本人相同；`POST B/phone` 与 `POST B/phone/verify` 只在 `SMSCodes` 在场时挂；`POST B/identifiers` 的邮箱分支只在 `EmailCodeSender` 在场时可用；发送验证码的分支（`POST B/phone` 与邮箱分支）先校验人机凭证 |
| `POST B/password-reset` | Open | 匿名口令重置的第一步：body `{channel, identifier, human_proof?}`；按渠道发送验证码或走抑制路径，合格与不合格目标同形；建立独立的 `__Host-authn-reset` 绑定，不读登录流程 cookie |
| `GET\|DELETE B/password-reset/current` | Open | 读当前重置流程或放弃它；要求重置绑定；`GET` 只答状态与期限，不答账户或标识 |
| `POST B/password-reset/verify` | Open | 提交验证码，body `{code}`；通过则进入已证明状态，错码、过期与抑制路径同答 401 `login-failed` |
| `POST B/password-reset/complete` | Open | 提交新口令，body `{new_password}`；写口令、推进 `valid_after`、撤销该账户全部会话；不建立会话 |

`B/me` 的两条路由——本人的资料视图与它的写——归 [authn-profile](design-authn-profile.md)，本模块不挂。

该端不得被 `tenant.resolve` 所守：全局账户的登录请求不带租户名，被守时拿到的是解析层的 404。装配期对此做核对，核不了（读不出 `tenant.resolve.endpoints` 的解码形状）时写一行 `Warn`，不终止启动。

### 声明的特性

本模块声明下表的[条件特性](../../../glossary.md)，描述符的 `Resources` 为每一个带一条 `feature.Spec{Name: <名字>, Conditional: true}`（[feature](../design-feature.md)）。下表是本模块声明的特性的全集：

| 特性 | 指的面 | 报告的判定 |
|---|---|---|
| `authn.phone` | 手机号自助：`POST B/phone` 与 `POST B/phone/verify` | `SMSCodes` 在场；与挂这组路由的是同一个分支 |
| `authn.identifiers.email` | `POST B/identifiers` 的邮箱分支 | `EmailCodeSender` 在场；路由恒挂，与邮箱分支在它缺席时答 503 的判定读同一个依赖 |

- **报告与判定写在同一处。** 表中的判定都在 `Init` 里挂自助路由时做出（`pkg/authn/login/internal/selfservice/selfservice.go` `(*Service).Mount`，由 `pkg/authn/login/routes.go` `(*product).mountRoutes` 调用）。自助件不取 `feature.Board`：引擎把一个报告函数交给它的 `Mount`，它在表中的判定处调用。条件与报告写在一起，符合架构不变量 18（[architecture](../../architecture.md#架构不变量)，[ADR-feature-ownership](../../../adr/adr-feature-ownership.md)）。
- **`feature.Board` 经 `core.Resolve` 取用，不列入 `Requires`。** 取不到时（没有纳入 `feature.board` 的进程）不报告，路由照表中的条件挂出。
- **本模块不声明静态特性。** 发现、流程、回跳与其余账户自助路由随模块恒挂；服务前端的命令必须纳入本模块，前端不经特性门控直接依赖它（[web](../web/design-web.md)）。改口令 `PUT B/password` 同样恒挂，不立特性名。
- **守住它的测试**断言：`SMSCodes` 在场当且仅当报告 `authn.phone`、当且仅当 `POST B/phone` 已登记；`EmailCodeSender` 在场当且仅当报告 `authn.identifiers.email`。

### 请求与应答的形状

请求体一律 `application/json`，否则 415；拒绝未知字段；体上限 64 KiB（SAML 回跳例外），超过上限归 400 `bad-request`（与体不合法同答，不为超限另立码）。开始请求的 `input`（`Start.Fields`）至多 8 个字段、每个至多 512 字符；答复的 `input`（`StepInput.Fields`）至多 8 个字段，每个的上界更大——通行密钥的凭据 JSON 超过 1 KiB——且仍远小于 64 KiB 的体上限；两者越界一律归 400 `bad-request`。经 `Mounter` 登记的处理器所写的响应带 `Cache-Control: no-store`：`Wrap` 在调用处理器之前写好该头，处理器可以覆盖它（如密钥集自带缓存头）。网关在处理器之前自产的 401/503，以及入口模块对 panic 的缺陷响应，不在这一范围内：前者不经 `Wrap`，后者由入口模块按它自己的缺陷应答形状处置，会删去 `Cache-Control` 一类表示性头。

`GET B/methods` 的应答是方法描述符文档：`{"methods":[{name, kind, protocol, label, opens_account, binding_providers, fields}], "signup":{"enabled","password":{"enabled"}}, "mfa":{"factors":[...]}, "human":{"required","provider","site_key"}, "password_reset":{"enabled","methods":["email","sms"]}}`。`human.required` 是本部署是否要求每次发送先提交人机凭证；`provider` 是校验机制（`off`、`pow` 或 `turnstile`），没有接缝与 `off` 模式都读作 `off`；`site_key` 是客户端控件需要的公开标识，`off` 与 `pow` 不带。`methods` 按方法名排序，每项是一个方法自报的描述符：`kind` 是渲染类别（`form`/`redirect`/`enterprise`/`webauthn`），`protocol` 是 `redirect` 与 `enterprise` 类方法所讲的线协议（`oauth2`/`oidc`/`saml`，其余为空），`label` 是方法品牌名（如 `Google`、`WeChat`，缺失时客户端按方法名查 i18n 回退），`binding_providers` 是该方法 link 流程把绑定存下的提供方取值或命名空间前缀——`google`、`github`、`wechat` 与 `wechat-openid:`（微信无 union id 时按应用标识存）、`oidc:` 与 `saml:` 之后接提供方标识——为空表示绑定落在方法名下；它是方法的常量命名，不表示任何账户是否已绑定，客户端据此把一条已绑定的 `links` 取值归到方法，`fields` 是该方法首步所要的字段（仅 `form` 类给出，取自 `Begin` 以空字段调用时所答的首个 prompt），`opens_account` 是该法能否在首登为未知标识开户。`signup.enabled` 在 `authn.account.signup` 或 `authn.account.password-signup` 任一为 `open` 时为真；本模块按路径读取这两个设置项的当前值、只作预检查，不重复声明，注册开关的权威检查在 `Signups.Register`。`signup.password.enabled` 单独发布 `authn.account.password-signup` 的取值。`opens_account` 只描述方法自己的开户能力：口令方法始终不带 `opens_account`，`authn.account.signup` 关闭时短信与社交方法的 `opens_account` 在应答里清为 false，避免前端把已关闭的首登开户显示成入口。`mfa.factors` 是装配收到的第二因素种类，属部署级，与 `GET B/factors` 所答的本人已登记因素语义不同。企业 SSO 的提供方清单不进此应答，仍走 `POST B/sso/discover`。`password_reset` 发布本部署是否提供匿名口令重置与可用的渠道：`enabled` 在设置 `authn.login.password-reset` 为 `open`、审计在场、且至少一个渠道可用时为真；`methods` 只列已装配、模板齐备且未被 `disabled-methods` 关闭的渠道，按 `email`、`sms` 排序。它是部署能力，不是任何账户的事实：不说明某个账户有没有已证明的邮箱或手机。

**`GET B/methods` 是认证的匿名发现面，只承载登录前就要用的认证事实**：方法描述符、注册开关与可登记的因素种类。进此应答的事实满足匿名发现面的准入判据（[architecture](../../architecture.md#特性与前端定制)）；往此应答加字段是本模块对外接口面的变更。此应答的边界由该准入判据与下列规则一起划定，这些规则是穷举的：

- **口令方式不因 `disabled-methods` 从应答里消失。** 该设置的取值域排除 `password`，读取侧对库里的越域取值也只过滤掉 `password` 并记一行 `Error`（见 [authn-config](design-authn-config.md)），所以装配里只要有口令模块，`methods` 就带它的描述符；`password_reset.methods` 仍可因该设置关闭短信渠道。
- **登录后才需要的存在性事实不进此应答。** 手机号自助与自助添加邮箱是否成立经[特性文档](../../../glossary.md)发布（见「声明的特性」），特性文档只对已认证调用者可读（[feature](../design-feature.md)）。
- **描述符发布的是方法的常量，不是账户状态。** `binding_providers` 只说该方法把绑定存在哪些提供方取值下，不说任何账户是否已绑定；某个账户已绑定什么仍只经 `GET B/me` 的 `links` 读。
- **同一事实只有一个发布点。** 可登记的因素种类只在 `mfa.factors` 里；特性文档里只有多因素的自助面是否存在（`authn.mfa`，[authn-mfa](design-authn-mfa.md)），不按因素种类分出特性名（[ADR-feature-ownership](../../../adr/adr-feature-ownership.md)）。
- **只是参数、经流程应答送达或会暴露租户、IdP、账户维度的事实不发布。** 例：需先绑定的结果经 401 `link-required` 送达，`authn.sms.allowed-prefixes` 的违反答 400 `unsupported-region`，全局强制 MFA（`authn.mfa.global-required`）经登录流程的登记步送达。
- **匿名口令重置的能力位只发布部署事实。** `password_reset` 不按账户变化：同一个部署对所有匿名调用者给出同一份渠道清单；某个账户是否具备重置条件只在提交该账户的标识后由同形应答处理。
- **别的模块的登录前事实不进此应答。** 它们经拥有该事实的模块自己的匿名发现面发布。

成功应答是 `{"seq": 2, "state": "...", "next": {"kind": "..."}}`。`next.kind` 的取值是封闭集：

| `next.kind` | 内容 |
|---|---|
| `challenge` | 要提交的字段、`expires_in`、`resend_after`；待建号时字段随开户路径而定 |
| `redirect` | `url`、`method` |
| `factor` | `factors: [{id, kind, label}]`，种类须与第一因素不同 |
| `enroll` | `kinds` |
| `complete` | 流程已就绪，客户端以 `step=complete` 取走结果 |
| `webauthn` | 数据步：`options` 是该因素自己的 JSON 对象——通行密钥的仪式取库生成的创建或请求选项，TOTP（[REF-rfc6238](../../../references.md)）登记取 `otpauth_uri`（二维码由客户端画） |

完成时，`delivery=session` 设 cookie，体为 `{"state":"authenticated","session":{"id","expires_at"}}`；`delivery=token` 体为 `{"state":"authenticated","token_type":"Bearer","access_token","expires_in","refresh_token"}`。

待建号的应答是 `{"state":"signup","next":{"kind":"challenge","fields":[...]}}`，字段随开户路径而定：短信首登的这一步是 `username`、`password` 与可选的 `display_name` 三个字段；企业即时开通的这一步只有 `username` 一个字段；社交首登的这一步是一个完整的开户面，含 `username` 与 `password` 两个字段，并把三件事随应答带出（都在 `next` 上，各自缺省即不出现，提交时都不回传）：`provided_method` 是发起这次登录的方法名（与发现文档的 `methods[].name` 同一词汇，例如 `wechat.mp`）；`provided_display_name` 是提供方给出的账号名；`provided_email` 是提供方声明为已验证的地址，客户端只读展示、不提供编辑。提供方没有给出其中一项时该成员缺席；其余开户路径的应答不带这三者。客户端以 `POST B/flows/answer {seq, step:"answer", input:{"username":"...", "display_name":"...", "password":"..."}}` 提交，`display_name` 只在短信首登的这一步接受且可缺省；`password` 在短信首登与社交首登的这一步都接受，企业即时开通不接受。用户名形状不合答 400 `username-invalid`，保留字答 400 `username-reserved`，已被占用答 409 `username-taken`；口令不合策略或缺失答 400 `bad-request`。这些拒绝都不建账户，流程停在待建号可重试；问题码不同，界面分别说明。拒绝不推进流程的序号：序号只随流程前进而变，拒绝只把答复计数加一，因此客户端用同一个序号原地重试即可，不必先重读流程；重试仍计入答复次数上限。

### 匿名注册

`POST B/register` 是匿名路由，过跨源检查，不读 cookie、不建立流程记录，因此没有流程绑定，`seq` 与 `step` 不适用。正文：

```json
{
  "username": "alice",
  "email": "alice@example.com",
  "password": "...",
  "display_name": "Alice",
  "locale": "zh-CN",
  "time_zone": "Asia/Shanghai"
}
```

- `username`、`email` 与 `password` 必填；邮箱先作为未证明预留写入，验证前不是登录名，也不是联系方式或默认联系地址；验证之后它登记为邮箱标识，即成为联系方式，登录方式仍由持有人选择（REQ-signup-007、REQ-account-061、REQ-account-064）。`display_name`、`locale` 与 `time_zone` 可选，分别写到显示名与属性集的 `i18n.locale`、`i18n.tz` 键；`locale` 与 `time_zone` 按语言与时区的取值域校验，越界答 400。
- 成功是 `201` 与 `{"state":"registered","account_id":"...","verification":{"state":"pending","email":"..."}}`。开户在一次事务里完成（`Signups.Register`），不建立会话、不发送邮件。
- 客户端随后以 `POST B/flows` 的 `password` 方法登录，`input` 带 `identifier`（用户名）与 `password`；注册与验证都不自行铸造会话。
- 邮箱验证在登录后走 `POST B/identifiers` 与 `POST B/identifiers/verify`。这两条路由本身不要求新鲜认证，也不轮换会话或结束其他会话；一条将取代默认联系地址或成为账户唯一邮箱地址（因而自动登录方式）的邮箱，其证明（`verify`）要求新鲜认证。只有邮箱被证明才把账户的注册验证状态转为已验证，短信验证手机不解除限制（[ADR-authn-signup](../../../adr/adr-authn-signup.md)）。验证不改变登录方式上的选择；账户此前没有默认联系地址时它成为默认联系地址，持有人可另经 `PUT B/identifiers/:id` 改选（REQ-account-062、REQ-account-063）。
- 失败：400 体不合法、口令不合策略或用户名是保留用户名（`ErrUsernameReserved`）；403 `signup-closed`（`authn.account.password-signup` 不是 `open`）；409 `username-taken`、409 `email-taken`；429 `throttled` 带 `Retry-After`；503 `unavailable`（db 或派生容量不可用）。
- **注册开关的权威检查在 `Signups.Register`。** 本路由与 `GET B/methods` 读同一个设置项只作预检查，便于在校验与派生之前早拒；预检查放过的请求仍可能被 `Signups.Register` 以 `ErrSignupClosed` 拒绝，两处答同一个 403 `signup-closed`。
- 注册按来源地址的分钟预算与全局小时预算限流，超限答 429；查用户名之前先按 `signup-username` 读一次退避，退避中同样答 429 带 `Retry-After`。用户名冲突与邮箱占用都是显式的 409：`Signups.Register` 在开户事务里插入邮箱预留，`(kind, value)` 的唯一索引是地址的唯一仲裁点，冲突返回 `ErrContactTaken`，路由答 409 `email-taken`；注册不在插入之前检查邮箱占用，冲突时整个开户事务回滚。

### 匿名口令重置

匿名口令重置让忘记口令的人在没有已认证会话的情况下设定新口令。它不是登录流程：不写登录流程记录、不进入 `POST B/flows` 的目的闭集、不建立会话，也不复用登录流程的绑定 cookie。它只写口令，不触碰因素；重置之后的下一次登录仍要先过第一因素，再按账户原来的要求过第二因素（[authn-mfa](design-authn-mfa.md)、[ADR-authn-mfa-recovery](../../../adr/adr-authn-mfa-recovery.md)）。

**渠道与资格。** 请求带 `channel`（`email` 或 `sms`）与对应的标识。`email` 只接受 `VerifiedVia = email-code` 的邮箱标识，`sms` 只接受 `VerifiedVia = sms-code` 的手机标识；IdP 断言、邀请、管理员断言、`self` 与未证明预留都不作重置依据。是否被选作登录方式或作默认联系地址不影响这项资格：重置只看该值已被验证码证明的所有权（REQ-security-020、REQ-security-041）。账户必须是启用的用户账户并持有口令凭据；没有口令的账户不借这条路径设置首个口令，停用与删除的账户不合格。任一项不成立时不发送消息，走与合格目标相同的应答与验证路径。

**渠道由用户选择。** 同一个账户同时具备已证明邮箱与已证明手机时两种渠道都可走，只有一种时只有那一种可走。邮箱经根包可选的 `EmailResetCodeSender` 投递，短信经 `SMSCodes.Send` 的 `reset` 用途发送，模板是 `authn.code.reset`。每个渠道的发送都先核验人机验证门槛，再核验预算。

**绑定与状态。** 重置有独立的绑定 cookie（`__Host-authn-reset`，`Secure`、`HttpOnly`、`SameSite=Strict`、`Path=/`；允许不安全传输时用不带 `__Host-` 的名字且不设 `Secure`，[REF-rfc6265bis](../../../references.md)）与独立的 KV 记录，键是 cookie 令牌的 SHA-256。记录保存账户标识与标识行标识（不合格目标为空）、渠道、规范化标识的带密钥摘要、真实或抑制的标记、码的摘要（邮箱为发码时的码摘要；真实短信在验证前是挑战标识，通过后改记该码的本地摘要，使同一个码可以按成功再答一次；抑制记录为本地随机摘要）、状态、每码尝试计数、已发送条数、码过期时刻与记录期限，不保存邮箱或手机原文；记录期限严格长于码过期时刻，因此码过期答 `401 login-failed`、记录过期答 `401 flow-invalid` 都可达。状态是 `sent`、`verified`、`processing`、`failed`；`processing` 是写口令期间的内部态，`GET current` 对它答 `401 flow-invalid`；写入成功即删除记录，没有 `done`。每次迁移是比较并交换，并发败者按 `flow-invalid` 拒绝。记录过期、被删除、绑定不符或状态不符都读作 `401 flow-invalid`，唯一的例外是 `verify` 在 `failed` 上仍答 `401 login-failed`，与错码同形。每条记录只属于请求它的 cookie，另一个 cookie 不能兑现它的码；不同 cookie 提交同一地址或号码、落在该地址或号码的重发窗口内时，各自得到自己的记录，发送被通道的重发窗口抑制并答 202。`expires_in` 在 `sent` 取该渠道的码有效期（短信 `authn.sms.code-ttl`、邮箱 `authn.login.email-code-ttl`），在 `verified` 取 `authn.login.password-reset-ticket-ttl`；验证通过时同时把绑定 cookie 的 `Max-Age` 刷新到票据期限。

| 路由 | 行为 |
|---|---|
| `POST B/password-reset` | 体 `{channel, identifier, human_proof?}`。规范化标识、判定资格、预留预算、发送或写抑制记录，应答 `202 {state:"sent", expires_in, resend_after}`；合格与不合格目标的应答体、响应头与 cookie 逐字相同。同一 cookie 的存活 `sent` 记录、渠道与标识摘要都相同时视为重发：未到重发间隔答 429；短信渠道的已发送条数达到 `authn.sms.per-flow-sends` 后抑制发送并仍答 202。不同 cookie 提交同一地址或号码、落在该地址或号码的重发窗口内时抑制发送并仍答 202。 |
| `GET B/password-reset/current` | 读绑定记录：`sent` 答 `{state:"sent", expires_in, resend_after}`，`verified` 答 `{state:"verified", expires_in}`；不答账户、标识或码；`processing` 与其他不符状态答 `401 flow-invalid`。 |
| `DELETE B/password-reset/current` | 删除记录并清 cookie；幂等，没有绑定、没有记录或开关已关闭都答 204，依赖不可用答 503；它不发送、不改账户。 |
| `POST B/password-reset/verify` | 体 `{code}`。真实短信经 `SMSCodes.Verify` 核验，真实邮箱比对带盐摘要；抑制记录比对记录里的本地诱饵摘要，不调用 `SMSCodes.Verify`；每码尝试由本记录自己的原子计数约束。通过后 `sent` 换成 `verified`，应答 `{state:"verified", expires_in}`。已 `verified` 的记录收到同一个码时按成功答同样的体（幂等：一次应答丢失不该把已证明的码变成失效流程）。错码、码过期、已用、抑制路径与记录自己的尝试用尽同答 `401 login-failed`；尝试用尽后记录转 `failed`，后续答复仍答 `401 login-failed`，与错码同形。 |
| `POST B/password-reset/complete` | 体 `{new_password}`，只在 `verified` 上接受。先重读开关与该渠道的可用性，关闭时答 403 `password-reset-closed`；再重核资格（账户仍启用、仍有凭据、标识仍属该账户且仍由同一证明渠道证明），再写强制档审计 `authn.password.reset` 的 `attempted` 行，再 `Passwords.Set`（`By=reset`、`Channel` 取本次渠道、`MustChange=false`、`RequireActive`、`RequireExisting`、`RequireIdentifier` 取记录里的标识行、`AdvanceValidAfter`），成功后撤销该账户全部 browser 与 token 会话，删除记录并清 cookie，应答 204。不建立会话、不签发令牌。口令策略拒绝答 400 `bad-request`，记录留在 `verified`，可用另一个口令重试；账户不再合格答 403 `password-reset-unavailable`。 |

**同形与抑制。** 不合格目标不发送消息，但走与合格目标相同的记录与验证路径：邮箱写一条码摘要为随机诱饵的记录，短信先尝试 `SMSCodes.Send`（`Deliver=false`）取得抑制挑战行；若 `Send` 因任何发送预算或重发窗口超限返回 `ErrThrottled`，同样写一条抑制记录并答 202。发送步一律答 202；验证步对抑制记录用本地随机摘要做比较与计数，不调用 `SMSCodes.Verify`，恒不通过并答同一个 401 `login-failed`；真实记录才调用 `SMSCodes.Verify`。发送预算的任何超限都抑制发送并仍答 202；只有同一 cookie 的重发间隔与登录的 `ip-limit` 答 429，二者都与账户无关。

**预算与重发。** 邮箱复用 `authn.login.email-*` 的数值设置，按重置用途使用独立的计数键；按账户的预算只在合格目标上计数，超限抑制。短信复用 `authn.sms` 的按号码与按来源预算，另加 `authn.sms.global-hourly-reset` 的全局小时预算；`reset` 不填 `Account`，不设按账户维度。重发窗口按发往的地址或号码持有，跨渠道用途共享：一个收件箱或号码在窗口内只发一条码。邮箱的发送交给有界工作池，响应路径不等待载体；工作池满时在写记录之前答 503，合格与抑制目标走同一次容量判定。短信发送本来就在 `authn.sms` 的有界工作池里。人机凭证在任何预算被消耗之前核验。

**写入与结算。** `POST B/password-reset/complete` 在写口令之前写强制档审计 `authn.password.reset` 的 `attempted` 行；审计写不成即答 503，口令不变，记录留在 `verified`。`Passwords.Set` 的 `AdvanceValidAfter` 在同一事务里把 `valid_after` 推进到本秒之后，撤销语句随后结束该账户的 browser 与 token 会话；`Set` 成功后补一条 `success` 审计行，拒绝时补一条 `denied` 行，两条都用同一个 `action_id` 关联。`Set` 拒绝时口令不变，记录回到 `verified`；撤销失败只记 Error，口令已改，应答仍是 204。`authn.password.changed` 由 `Set` 发出，`by=reset`、`channel` 取本次渠道；`authn.session.revoked` 由撤销语句发出，`reason_class=password-change`。重置不结束发起者的 cookie 冒充会话（它属于发起者）；目标账户的 bearer（[REF-rfc6750](../../../references.md)）冒充令牌按 `valid_after` 失效，这是目标凭据变更的自然结果。`success`/`denied` 行写失败只记 Error 与指标，不改变已提交的口令；`attempted` 行是这条路径的强制留痕。

**MFA。** 因素行、`mfa_required` 与因素启用时长都不改变。重置不解除、不降级、不重新登记任何因素；下一次用新口令登录仍按账户原来的要求过第二因素。

**失败方向。** kv、db、审计或口令派生不可用答 503，不写口令；记录丢失只使流程失败，用户重新开始。短信前缀不在放行表答 400 `unsupported-region`，发送预算超限抑制并答 202，短信通道不可用答 503。邮箱发送的结果不影响已返回的 202：工作池在入队后失败只记日志与指标，用户按重发间隔重试。设置 `authn.login.password-reset` 不为 `open`、`disabled-methods` 含 `sms` 且请求的是短信渠道、渠道未装配或审计缺席时，发现应答不发布对应渠道；直接调用时，关闭的开关与 `disabled-methods` 含 `sms` 的短信渠道答 403 `password-reset-closed`，审计缺席答 503 `unavailable`，未装配的渠道答 503 `unavailable`。`GET`、`verify` 与 `complete` 在动手前重读开关与该渠道的可用性，关闭时答 403 `password-reset-closed`；`DELETE` 不因开关关闭而变，仍答 204，依赖不可用答 503。知道受害者地址或号码的人可以在预算窗口内耗尽该地址或号码的发送预算，使受害者的重置被抑制；这是发送预算的既有代价，受害者可换渠道或等窗口。

### 问题码

失败一律是 `application/problem+json`（[REF-rfc9457](../../../references.md)），机器可读的码放在 `Problem.Type`，取 `urn:speed:authn:<码>`；入口模块的 `Problem` 没有扩展成员，`type` 是现成字段。`Fail` 之前设置的响应头保留，响应体不带成因。码的集合是封闭集，新增码是兼容的，删除、改名或改变既有码的含义是破坏性变更：

| 状态 | 码 | 场景 |
|---|---|---|
| 400 | `bad-request`、`unsupported-region` | 体不合法、字段缺失、未知字段、体超过 64 KiB 上限、方法不存在、`step-up` 显式命名 `password` 而账户没有口令可再证明、`return_to` 不合规；短信号码前缀不在放行表里。用户名格式不合或保留用户名同答此码，不占 409 `username-taken`；标识设置的字段不合法、`sign_in` 落在未证明的标识上、标识不属于本人或默认联系地址不是已证明邮箱 |
| 401 | `login-failed` | 未知标识、错口令、停用、无口令、验证码错、方法内校验失败；**同一个应答**，成因是号码前缀不在放行表或标识无法规范化时按 400 答 |
| 401 | `flow-invalid` | 流程过期、已用、绑定不符、步序不符、被并发消费 |
| 401 | `link-required` | 社交或企业即时开通无绑定，而提供方声明的地址已登记在现有账户的邮箱标识上；登录不进入该账户，也不另开账户（REQ-signin-017） |
| 401 | `insufficient_user_authentication`（[REF-rfc9470](../../../references.md)） | 加强认证，同时带 `WWW-Authenticate`（[authn-mfa](design-authn-mfa.md)） |
| 401 | `sso_required` | 租户准入层要求经本租户的企业单点登录重新认证，由 [tenant.admission](../tenant/design-tenant-admission.md) 写出；本地因素解不了它 |
| 403 | `method-disabled`、`account-unavailable`、`impersonation-restricted`、`forbidden`、`factor-not-seasoned`、`signup-closed`、`contact-required`、`human-verification-required`、`password-reset-closed`、`password-reset-unavailable` | 对认证之外的策略的转述，只在 authn 自己的处理器里写；`signup-closed` 是自助注册开关关闭，`contact-required` 是未验证账户（有邮箱而没有一条已证明的）调用了要求已验证邮箱的能力，`human-verification-required` 是部署要求人机验证而请求缺少或提交了无效的凭证且因此不发送，`password-reset-closed` 是匿名口令重置开关关闭或短信渠道被 `disabled-methods` 关闭，`password-reset-unavailable` 是重置的账户在验证之后不再满足资格（停用、删除、失去口令或标识不再由同一证明渠道证明）；`method-disabled` 只来自 `disabled-methods` 关闭的非口令方式，口令方式不在可关闭集合内，读取侧也不把库里的越域取值当作关闭口令方式 |
| 404 | `factor-not-found` | 因素的改名路由：标识不存在或不属于调用者，两种情况共用一个码，区分会变成存在性探测（[authn-mfa](design-authn-mfa.md)） |
| 409 | `conflict`、`login-identifier-required`、`username-immutable`、`username-taken`、`email-taken` | 外部身份冲突、删最后一个因素、某种因素已达上限；账户没有用户名、也没有被选作登录方式的已验证邮箱而设口令，或删除带口令凭据账户的最后一个被选作登录方式的已验证邮箱；删除用户名标识；自助注册或首登开户的用户名已被占用；邮箱地址已被别的账户预留或持有 |
| 409 | `last-login-method` | 解绑最后一种登录方式、取消登录方式或删除标识会留下没有口令凭据、被选作登录方式的已验证手机号与外部身份中任何一种的账户 |
| 415、429、503 | `unsupported-media-type`、`throttled`（带 `Retry-After`）、`unavailable` | `unavailable` 对外不区分是哪个依赖不可用 |

401 一律带 `WWW-Authenticate`。挂在本模块路由上的 401 有三种来源。**网关自产的 401**（没有凭据、头不合规、出口检查不符、冒充范围不符，以及网关自己对浏览器会自行附带的凭据做的跨源检查不过）取网关为该端建成的挑战，含口令表后端在场时的 `Basic`。**本模块处理器答的 401**（`login-failed`、`flow-invalid`、`link-required`、`insufficient_user_authentication`）与 **`Mounter` 的 `Wrap` 为挂载路由写的跨源检查 401**，挑战去掉 `Basic`，只用 `Bearer` 与 `Cookie`：同源页面的 `fetch` 收到带 `Basic` 的 401 会让浏览器弹出原生口令框，而这些 401 不是在要求 Basic 凭据（[ADR-authn-credential-dispatch](../../../adr/adr-authn-credential-dispatch.md)）。三种 401 的 realm 都取该端点的端名，与网关写的 realm 同源：处理器与 `Wrap` 经 `mount.Mounter.Endpoint()` 得到端名，再交给路由部件的 401 写出函数（[authn-gate](design-authn-gate.md)）。

登录与注册的自助路由没有 404 问题码：`ErrAccountNotFound` 在自助路由上读作身份已失效，答裸 401（不带问题码）。因素路由的 404 只写 `factor-not-found`，见上表。自助路由不因冒充拒绝：判据只取被冒充账户的权限，处理器不以 `Impersonating()` 写出 `impersonation-restricted`。

### 校验顺序与 return_to

读请求体之前，按下列次序校验，任一项不过即拒绝：

1. `CrossOriginProtection.Check`：先看 `Sec-Fetch-Site`，缺失时比较 `Origin` 与 `Host`；可信来源是 `public-url` 的来源加 `trusted-origins`；回跳端点旁路。
2. `Content-Type`。
3. 流程绑定，`POST flows` 与 `POST flows/authenticated` 除外。
4. `seq` 与 `step`。

`POST B/register` 没有流程绑定，第 3、4 项不适用；它仍过第 1、2 项，也没有 `return_to`。

跨源检查由 `Mounter` 的 `Wrap` 在处理器之前施加（[authn-gate](design-authn-gate.md)）：不过即 `Wrap` 写的 401（只带 `Bearer` 与 `Cookie`），处理器不被调用，所以下节的问题码不含它；`Content-Type` 的 415 因而只对已通过跨源检查的请求给出。

`return_to` 在 `Begin` 时校验并存入记录，取值是：以单个 `/` 开头的相对路径，不以 `//` 起头，不含反斜线、控制字符与 `@`，至多 2048 字节；或来源在 `trusted-origins` 里的绝对 URL。其余一律 `bad-request`。回跳时只用记录里的值，不读回跳请求的任何参数。`redirect_uri` 由配置的 `public-url` 拼出，从不取自请求的 `Host` 或转发头。

### 对外接口面

- 方法名的变更：新增是兼容的，改名或删除是破坏性变更；它出现在路由、流程记录与指标属性里，看板与回跳登记都会引用它。
- 问题码是封闭集，新增码是兼容的，删除、改名或改变既有码的含义是破坏性变更。
- 路由 `POST B/register` 与 `GET B/methods` 的 `signup.enabled`、`signup.password.enabled` 是宿主可见面；往发现应答加字段是兼容的，删除或改变语义是破坏性变更。
- `PUT B/identifiers/:id` 与它的 `{contact, sign_in, default_contact}` 请求体是宿主可见面：新增字段是兼容增量，删除或改变字段语义是破坏性变更。
- 流程记录带版本字段，读到不认识的版本一律拒绝。
- `LoginMethod`、`Step` 构造器、`Proof` 的字段是第三方方法依赖的面，增加字段不破坏既有方法；删改它们是不兼容变更，按宿主可见面的变更处理。
- 声明的特性名 `authn.phone`、`authn.identifiers.email` 与各自指的面：前端经生成的特性名类型引用它们，改名、撤去或改变所指的面是宿主可见的破坏性变更；它们在不在随装配变化，不在接口面内。
- `POST B/password-reset` 系列路由与发现应答的 `password_reset` 段是宿主可见面：新路由与新字段是兼容增量，删除或改变语义是破坏性变更。
- `EmailResetCodeSender` 是宿主可见的投递接缝；`SMSCodes` 的可选 `PurposeSupport` 与模板键 `authn.code.reset` 是契约增量。`SetPasswordOptions` 的新字段、`authn.password.changed` 的 `by=reset` 与 `channel` 同样是接口面的增长，订阅方须容忍新值。

## 依赖

| 依赖 | 必需 | 用途 |
|---|---|---|
| `mount.Mounter` | 是 | 路由与访问级别成对登记（[authn-gate](design-authn-gate.md)） |
| `kv.KV` | 是 | 流程记录、手机号验证记录与来源地址减负的计数 |
| `Accounts`、`AccountAdmin` | 是 | 账户解析、开户、显式关联、标识的登录方式与默认联系地址的写入、`Touch`（[authn-account](design-authn-account.md)） |
| `Signups` | 是 | 匿名注册路由的开户入口；注册校验、事务与注册验证状态归账户模块 |
| `SignupVerification` | 是 | `purpose=link` 的门控读发起者的注册验证状态；邮箱分支发码前反查地址的未证明预留；读失败答 503 |
| `Passwords` | 是 | 自助改口令的处理器；口令方式的校验在方法包里经同一接口完成 |
| `Memberships` | 是 | 企业 SSO 首登开户与 JIT 建户所需的成员关系读写 |
| `Throttle` | 是 | 失败退避的存取面 |
| `Sessions` | 是 | `Create`、`Current`、`Revoke`、`StatusFresh`、`Elevate`、`RevokeAccount`、`Rotate`、`SetCookie`：`session` 与 `token` 交付都先建会话；请求带着可解析的旧会话 cookie 时撤销旧会话；`link`、`step-up`、`enroll` 的完成复核 |
| `Impersonation` | 可选 | `Active`：完成复核确认冒充流程开始时绑定的那条记录仍然有效，并取它绑定的生效账户；缺席时冒充流程的完成一律 401 `flow-invalid` |
| `MFA` | 是 | 第二因素与登记的编排、保障等级的合成 |
| 全部 `LoginMethod` | 经 `ResolveAll` | 缺席合法，但没有任何方法即启动期 `ErrNoLoginMethod` |
| `Tokens` | 可选 | `Issue` 与 `NewRefresh`；缺席时 `delivery=token` 不启用，启动诊断写一行 |
| `SMSCodes` | 可选 | `POST B/phone` 与 `POST B/phone/verify` 经它发码与校验；匿名口令重置的短信渠道经它发送 `reset` 用途并核验挑战；缺席时不挂手机路由、不发布短信重置渠道，启动诊断写一行。短信登录方式的发码在 `authn.login.sms` 里，不经引擎 |
| `EmailCodeSender` | 可选 | `POST B/identifiers` 的邮箱分支经它把验证码投递到该地址；缺席时该分支答 503 `unavailable`，启动诊断写一行。验证码的产生、记录与比对在本模块的自助件里，不经它 |
| `EmailResetCodeSender` | 可选 | 匿名口令重置的邮箱渠道经它投递重置验证码；未实现或未装配时邮箱重置渠道不发布。它的投递在有界工作池里执行，响应路径不等待 |
| `SSOProviders` | 可选 | IdP 发起的 SAML 读提供方行的 `allow_idp_initiated` |
| `Provisioner` | 可选 | 首登开户之前的宿主钩子 |
| `settings.Store` | 可选 | 本模块的设置，缺席取声明的默认值 |
| `audit.Audit`、`event.Bus` | 可选；口令重置面要求 `audit.Audit` 在场 | 登录审计与 `authn.login.succeeded`；缺席时对应输出不做，启动诊断写一行。匿名口令重置的强制档审计要求 `audit.Audit` 在场，缺席时不发布重置能力 |
| `feature.Board` | 可选，`Init` 经 `core.Resolve` 取用，不列入 `Requires` | 报告 `authn.phone` 与 `authn.identifiers.email`；缺席时不报告 |

方向满足架构不变量：`authn/login` 只依赖根包、内部包、`pkg/feature` 根包与依赖表里的功能接口，不依赖任何方法叶子包，也不 import `pkg/tenant`；`pkg/feature` 根包只依赖标准库，`authn/login` 为声明特性 import 它不扩大自己的闭包，也不 import `pkg/feature/board`（[feature](../design-feature.md)）；叶子包只依赖根包与 `internal/egress`、`internal/password` 这类内部包，库类型只出现在叶子包里，不出现在根包签名里（[ADR-authn-packaging](../../../adr/adr-authn-packaging.md)、[ADR-authn-wiring](../../../adr/adr-authn-wiring.md)）。`authn.login` 把 `MFA` 当必需：装配里没有 MFA 模块而库里已有账户登记了因素时，登录不得无声跳过第二因素。

状态放置按丢失的方向判：流程记录丢了只让流程失败，放 kv；退避计数、会话、账户丢了会放宽校验，放 db（[ADR-authn-state-placement](../../../adr/adr-authn-state-placement.md)）。

## UML 图

### 状态机

一次登录在 kv 里是一条流程记录，每步用比较并交换推进。图里的 `账户解析` 与 `策略判定` 是同一次请求内的瞬时步骤，不落入流程记录；落入记录的状态是 `first`、`processing`、`signup`、`second`、`enroll`、`ready` 与终态墓碑 `done`、`failed`（对应图中的第一因素、处理中、待建号、第二因素、登记因素、就绪、完成、失败）。`signup` 是首登开户在建号之前索取用户名的持久步骤，跨请求保留。`POST flows` 总是从第一因素进入；`POST flows/authenticated` 按目的从第一因素、第二因素或登记因素进入，见「在已登录身份上开始流程」。回跳类的一步先把记录置为处理中再调用方法，使授权码与断言至多被处理一次；`完成` 之后才建登录会话或发令牌。目的 `link`、`step-up`、`enroll` 复用这台机器，终态动作不同，见「终态动作」。逐条迁移、答复与副作用见「答复与迁移」。

```mermaid
stateDiagram-v2
    [*] --> 第一因素: POST flows 选定登录方式 或 已登录身份上从第一因素起始的 link 与 step-up
    [*] --> 第二因素: 已登录身份上有可用因素的 step-up
    [*] --> 登记因素: 已登录身份上的 enroll
    第一因素 --> 第一因素: submit 需要多步或被拒后重试 每流程至多八次答复
    第一因素 --> 账户解析: submit 方法交出证明
    第一因素 --> 处理中: callback 回跳 引擎先置处理中
    第一因素 --> 失败: 方法关闭 次数用尽 超时
    处理中 --> 账户解析: 方法交出证明
    处理中 --> 失败: 被拒 外部方不可用 处理中的记录一律作废
    账户解析 --> 失败: 账户停用 未开户 需要先关联 域不符
    账户解析 --> 待建号: 判定要开户
    账户解析 --> 策略判定: 账户确定
    待建号 --> 待建号: 用户名被拒后重试 每流程至多八次答复
    待建号 --> 策略判定: 合法用户名 建号
    待建号 --> 失败: 次数用尽 超时
    state 策略判定 <<choice>>
    策略判定 --> 就绪: 第一因素已是多因素 或账户无因素且无策略要求
    策略判定 --> 第二因素: 账户有有效因素
    策略判定 --> 登记因素: 策略要求而账户没有因素
    第二因素 --> 第二因素: 换因素或重试 受次数与退避约束
    第二因素 --> 失败: 次数用尽 超时
    第二因素 --> 就绪: 一个种类不同的因素通过
    登记因素 --> 就绪: 因素登记完成 完成时才启用 本次登录不计入保障等级
    就绪 --> 完成: complete 建登录会话或发令牌
    完成 --> [*]: 流程记录转墓碑
    失败 --> [*]: 流程记录转墓碑
```

### 社交登录的跨模块交互

下图是一次 `delivery=session`、`binding=cookie` 的社交登录。它固定下列次序：回跳先置处理中再调方法；`ready` 先交换成墓碑 `done`，再建会话；会话 cookie 在回跳的 `303` 响应里写出，在最后一个可能失败的步骤之后。账户解析判定要开户时，`E->>A: 账户解析` 之后不进入 `Require`，而是把记录停在待建号并 `303` 回 `return_to`；用户名答复被接受后才建户，再走 `Require` 与后续步骤。

```mermaid
sequenceDiagram
    participant B as 浏览器
    participant E as 引擎
    participant K as kv
    participant M as 方法
    participant P as 外部提供方
    participant A as 账户模块
    participant F as MFA
    participant S as 会话模块
    B->>E: POST flows 选定方法
    E->>K: 写流程记录 first
    E->>M: Begin
    M-->>E: StepRedirect
    E-->>B: 200 redirect 并设流程 cookie
    B->>P: 授权
    P-->>B: 303 回跳 带 code 与 state
    B->>E: GET callback 带流程 cookie
    E->>K: CompareAndSwap first 到 processing
    E->>M: Answer
    M->>P: 换取令牌
    P-->>M: 令牌
    M-->>E: StepProven
    E->>A: 账户解析
    E->>F: Require
    E->>K: CompareAndSwap processing 到 ready
    E->>K: CompareAndSwap ready 到 done
    E->>S: Create
    E-->>B: 303 return_to 并设会话 cookie
```

### 接缝的类型

`LoginMethod` 的 `Begin` 与 `Answer` 返回 `Step`，`Describe` 返回 `MethodDescriptor`；`Step` 的真身在内部包，根包只导出构造器；`Proof` 是 `StepProven` 的载荷。`FlowState` 与 `StepInput` 是引擎交给方法的只读输入。

```mermaid
classDiagram
    class LoginMethod {
        <<interface>>
        +Name() string
        +Describe() MethodDescriptor
        +Begin(ctx, Start) Step
        +Answer(ctx, FlowState, StepInput) Step
    }
    class MethodDescriptor {
        +Name string
        +Kind MethodKind
        +Protocol MethodProtocol
        +Label string
        +Fields Field
        +OpensAccount bool
        +BindingProviders string
    }
    class Start {
        +Purpose string
        +Account AccountID
        +Fields map
        +Client ClientInfo
        +SendsLeft int
        +Locale string
    }
    class FlowState {
        +ID string
        +Purpose string
        +Step string
        +Provider string
        +Account AccountID
        +Session SessionID
        +Private bytes
        +SendsLeft int
        +Locale string
        +Source string
    }
    class StepInput {
        +Action string
        +Fields map
        +Query values
        +Form values
        +Client ClientInfo
    }
    class Step {
        <<sealed>>
        +WithSends(n) Step
    }
    class Proof {
        +Account AccountID
        +Identifier Identifier
        +External External
        +Methods Method
        +UserVerified bool
        +IDPAsserted bool
        +Provider string
        +Aliases ExternalLink
        +PasswordChanged bool
    }
    LoginMethod ..> Start : Begin 的输入
    LoginMethod ..> FlowState : Answer 的输入
    LoginMethod ..> StepInput : Answer 的输入
    LoginMethod ..> Step : 回答
    LoginMethod ..> MethodDescriptor : Describe 的返回
    Step o-- Proof : StepProven 携带
```

## 核心数据结构

### 流程记录

kv 键是 `authn:flow:<流程令牌的 SHA-256 十六进制>`。kv 不划分命名空间，与限流的键共处，所以键自带 `authn:` 前缀。值是 JSON，有效期由设置 `authn.login.flow-ttl` 决定，缺省与上限见 [authn-config](design-authn-config.md)。字段的全集：

| 字段 | 含义 |
|---|---|
| `v` | 记录格式版本；读到不认识的版本一律拒绝 |
| `purpose`、`method`、`state` | 目的（`login`、`link`、`step-up`、`enroll`）、方法名、状态机状态 |
| `provider` | 回跳路径 `:provider` 的期望值：社交方法的记录取方法名，企业 SSO 的记录取 `Begin` 时 `input.provider` 给的值；回跳时分发之前与路径逐字比对（见「回跳类的一步」） |
| `seq` | 步序号，每次迁移加一；请求必须带当前值，不符即 `ErrStepOutOfOrder`。置为处理中的那次迁移不改 `seq`，因为回跳请求不带它 |
| `binding` | 绑定秘密的 SHA-256；外部回跳与每次答复都以请求带来的流程令牌重算后常量时间比对 |
| `account`、`proof` | 解析出的账户标识与方法交出的证明，解析之后才有 |
| `pending` | 待建号的开户事实：账户解析判定要开户之后、用户名与口令取得之前，方法交出的开户所需部分（外部身份或手机标识、来源租户、`EmailVerified`、显示名、属性、`Aliases`）与开户用途；显示名在短信首登的这一步由当事人选填，在社交首登与企业即时开通取提供方的值，任何一条路径没有给出时建号取用户名。它只在 kv 的流程记录里，账户、绑定、标识与成员关系行在用户名答复之前都不存在 |
| `mfa` | `{required, offered, challenge_ref, attempts, passed, enrolled}`：`passed` 是第二因素通过的结论，形如 `{methods, user_verified}`，供完成时经 `MFA.Assurance` 合成保障等级；`enrolled` 是 `enroll` 状态里已登记的因素标识（因素行在 `MFA.Enroll` 的最后一步已置为启用，记录只留它的标识） |
| `method_state` | 方法私有状态：OAuth 的 `state` 摘要、PKCE（[REF-rfc7636](../../../references.md)）校验串、`nonce`；SAML 的请求标识与提供方标识；WebAuthn 仪式数据 |
| `method_step` | 方法自己的步名，`FlowState.Step` 取它：方法在 `StepPrompt` 上命名的步（如口令的 `new_password`），答复时按它分派回方法 |
| `merged` | 恒为假；自动并入不再发生，完成时不再执行 A10。读到旧的 `merged = true` 记录按流程失效处理 |
| `delivery`、`return_to` | `session` 或 `token`；登录后的回跳，`Begin` 时校验，回跳时只用记录里的值 |
| `session`、`answers`、`sends` | `session`：`link`、`step-up`、`enroll` 流程发起时的登录会话标识，完成时复核；`answers`：已接受的答复数，至多 8；`sends`：本流程已发的短信数，由引擎把方法与因素经 `Step.WithSends` 报告的条数累加进同一次比较并交换，上限与核对见「短信发送的每流程上限」 |
| `require_multi` | `link` 与 `enroll` 在开始时取定的判据：账户当时有有效因素为真。完成复核按它读回同一份要求，见「在已登录身份上开始流程」的「完成时的复核」 |
| `locale` | `Begin` 时取到的请求语言：`i18n.FromContext` 定下的那个（`localeOf`），取不到时为空串；没有 i18n 中间件的装配里 `localeOf` 退回读 `Accept-Language` 的第一个标签、原样带出。它原样交给 `SendCode.Locale`，由短信模块解析到载体声明的语言、落不到时用配置的默认语言 |
| `created_at`、`expires_at` | 绝对到期，与键的生存期一致 |

`state` 的取值是封闭集：`first`（第一因素）、`processing`（处理中，仅回跳类）、`signup`（待建号）、`second`（第二因素）、`enroll`（登记因素）、`ready`（就绪），以及终态墓碑 `done` 与 `failed`。墓碑与记录同寿，交换不改动过期。

**记录格式与滚动升级。** 记录按拒绝未知字段解码，`v` 是格式版本，读到不认识的 `v` 一律拒绝。`provider` 与 `require_multi` 是记录的可选字段，都不抬高 `v`，缺省取各自的零值。滚动升级期里，旧二进制读到带 `provider` 的记录会因未知字段被拒读，新二进制读到旧记录时 `provider` 为空即与回跳路径不符——两条路径都 fail-closed，被拒的是该次回跳，用户重新开始。`require_multi` 缺省为假：新二进制读到缺该字段的旧记录时，完成复核按「账户无有效因素」取要求，对一份账户当时有有效因素的记录，这让完成按新鲜的第一因素核对，其窗口比开始时按的新鲜本地多因素更短，方向是更严而非放宽。在途流程因此作废或有界，窗口有界：流程 TTL 至多 30 分钟。

流程记录里的 PKCE 校验串对持有 kv 读权限的人不是秘密：能读 kv 的人加上受害者的授权码才能完成交换，而授权码一次有效且短命、回调还要求流程绑定。kv 的读权限因此要按凭据对待。

### 流程令牌与绑定

流程令牌是 256 位随机数，库里只存 SHA-256；它同时是绑定秘密。承载由开始请求的 `binding` 字段决定：

| `binding` | 承载 | 响应体 |
|---|---|---|
| `cookie`（缺省） | cookie `__Host-authn-flow` | 不含 `flow_token`，脚本读不到绑定秘密，XSS 取不到 |
| `header` | 请求头 `Authn-Flow` | 返回 `flow_token`，不设 cookie |

cookie 的属性是 `Secure; HttpOnly; Path=/`、无 `Domain`、**`SameSite=None`**。SAML 的断言是 IdP 页面发起的跨站 POST，`Lax` 的 cookie 不随跨站 POST 发送，绑定会丢。`SameSite=None` 使它随所有跨站请求发出；它只承载一个不透明值、只对流程端点有意义、流程完成即作废、服务端只存哈希，对其他端点没有权限；`__Host-` 前缀使子域不能写入同名 cookie。`allow-insecure`（仅开发）时去掉前缀与 `Secure`。

回跳请求是用户代理的导航，带不了自定义请求头，所以回跳类方法只能在 `binding=cookie` 的流程里走完；`binding=header` 的流程里它们的回跳因绑定缺失以 `flow-invalid` 失败。回跳与每次答复都要求绑定匹配，这是对登录 CSRF 与流程固定的主要防线：`state` 与 SAML 的 `InResponseTo` 各自只证明请求与某个流程对得上，不证明流程与发起它的浏览器对得上。待建号的外部身份同样只由流程记录承载，读取要过绑定比对；授权码与断言一次性，因此另一个浏览器不能接手一条停在待建号的流程。

## 核心算法与流程

每步迁移的代价是对 kv 的一次读与一次 `CompareAndSwap`，与方法的复杂度无关；没有性能敏感的算法，不作复杂度分析。

### 开始流程

`POST B/flows` 按下列次序处理：

1. 按「校验顺序与 return_to」校验；不读 cookie，所以浏览器里残留的过期会话 cookie 不会挡住登录。
2. 取 `method`：方法不存在按 `bad-request`，已被设置 `authn.login.disabled-methods` 关闭按 `method-disabled`；口令方式不在该设置的可关闭集合内，永不因它被拒。
3. 取 `delivery`，缺省 `session`。`token` 要求 `Tokens` 在场且设置 `delivery-modes` 允许；令牌发放把刷新令牌交给脚本可读的环境，对浏览器应用是更弱的选择，所以它是显式选择，宿主可以关掉。
4. 校验并保存 `return_to`，确定 `binding`，生成流程令牌，写入 `first` 状态的记录。kv 不可用时答 503。
5. 带 `call-budget` 调用方法的 `Begin`，按「答复与迁移」处理它交回的 `Step`。

发送短信的第一步先过人机验证：`input` 带着方法描述符声明的 `phone` 字段且该字段非空时，引擎在写流程记录之前校验 `human_proof`。部署不要求人机验证时整步跳过；缺少、无效、过期或已用的凭证答 403 `human-verification-required`，超过 8 KiB 的答 400 `bad-request`，校验后端或它依赖的存储不可用答 503。三者都不写记录、不调用方法、不发送，也不消耗任何发送预算。

一个浏览器同一时刻只有一个流程 cookie，所以后开始流程的标签页会顶掉先前的流程。

### 在已登录身份上开始流程

`POST B/flows/authenticated` 的访问级别是 `Required`，请求到达处理器时网关已认定身份。按下列次序处理，任一项不过即拒绝，不写流程记录：

1. 按「校验顺序与 return_to」校验。冒充身份照常通过，生效账户是被冒充账户。
2. 取 `purpose`：`link`、`step-up`、`enroll` 之外一律 `bad-request`。
3. `purpose=link` 而发起者是未验证账户（`SignupStateOf` 为 `SignupPending`：有邮箱而没有一条已证明的）时，403 `contact-required`，不写记录。没有邮箱的账户读作 `SignupNone`，不受这条门控约束，`step-up` 与 `enroll` 也不受。外部身份绑定是平台强制门控的身份面，门控由本模块执行。判定读 `SignupVerification.SignupStateOf`，读失败答 503，不按放行处理。
4. `Sessions.StatusFresh(身份的会话)` 须有效，账户与身份一致。
5. 按下表确定记录的 `method`、起始状态与起始要求；起始要求不满足，401 `insufficient_user_authentication`。
6. 写记录：`account` 与 `session` 取身份的账户与会话，`delivery` 取会话的种类（`browser` 种为 `session`，`token` 种为 `token`），`binding` 与 `return_to` 取自请求，`answers` 与 `sends` 从零起。
7. 按起始状态做第一次调用，按「答复与迁移」处理回答：`first` 以 `Start{Purpose, Account, Fields, SendsLeft}` 调方法的 `Begin`；`enroll` 以 `Fields` 调 `MFA.Enroll`；`second` 不调用，应答 `next.kind = factor`，清单即记录里的 `mfa.offered`。

`step-up` 从第一因素起始时，`method` 声明的 `phone` 字段非空同样在写记录之前校验 `human_proof`，判据与拒绝形状同「开始流程」。

下表是各目的在起始阶段的取值，目的取值是封闭集：

| 目的 | 请求里带 | 记录的 `method` | 起始状态 | 起始要求 |
|---|---|---|---|---|
| `link` | `method`、`input` | 请求的 `method` | `first` | 账户安全类动作的要求；发起者须不是未验证账户，`SignupStateOf` 为 `SignupPending` 时在写记录之前即 403 `contact-required` |
| `step-up`，账户有可用因素 | 不带 `method` | 空 | `second` | 无附加要求 |
| `step-up`，账户没有因素 | `method` | 请求的 `method` | `first` | 无附加要求 |
| `step-up`，账户的因素都在本次会话认证后启用 | 带 `method` 且部署收下它 | 请求的 `method` | `first` | 无附加要求 |
| `step-up`，账户的因素都在本次会话认证后启用 | 不带 `method`，或带的 `method` 部署没有收下；账户有口令 | `password` | `first` | 无附加要求 |
| `step-up`，账户的因素都在本次会话认证后启用 | 不带 `method`，或带的 `method` 部署没有收下；账户没有口令 | — | 拒绝，401 `insufficient_user_authentication` | 无附加要求 |
| `step-up`，请求显式命名 `password` 而账户没有口令可再证明 | `method` 为 `password` | — | 拒绝，400 `bad-request` | 无附加要求 |
| `enroll` | `input.kind`；可选 `input.label` | 空；因素种类交给 `MFA.Enroll` | `enroll` | 账户安全类动作的要求 |

「账户安全类动作的要求」是 `Requirement`：生效账户有有效因素（`MFA.Factors` 里状态为启用的行）时为新鲜的本地多因素，没有因素时为新鲜的第一因素，取值与单位归 [authn-mfa](design-authn-mfa.md)。引擎以 `StatusFresh` 交回的 `Assurance`、`AuthTime`、`IDPAssured` 按 `Identity.Satisfies` 的同一判据核对。

`step-up` 的可用清单这样产生：引擎以会话的方式集构造只含 `Methods` 的 `Proof`，调 `MFA.Require(证明账户, 生效账户, Proof)` 取认证方式与会话方式不相交的启用因素，再按启用时刻分成两类（`FactorRecord.ActivatedAt`）：启用时刻早于会话 `AuthTime` 的因素可直接加强会话；启用时刻不早于它的因素是本次会话期间登记的因素，只有在账户再次证明一个第一因素之后才可加强会话。证明账户是发起者自己的账户，不冒充时就是生效账户；结局按生效账户取定，清单取证明账户的因素。`Require` 的结局不是 `MFAFactor` 即生效账户没有因素，起始状态取 `first`；结局是 `MFAFactor` 且其中存在早于会话认证时刻的因素，起始状态取 `second`，清单是这类因素；结局是 `MFAFactor`、证明账户只有本次会话期间登记的因素时，请求带了部署收下的 `method`（显式命名 `password` 而账户没有口令可再证明时除外）的起始状态取 `first`、记录的 `method` 为该名，该第一因素证明后这些因素作为第二因素提供；请求没带 `method`、或带的是部署没有收下的名字时，账户有口令则起始状态取 `first`、记录的 `method` 为 `password`，没有口令则 401 `insufficient_user_authentication`，不写记录。清单为空在只有一种因素种类的装配里是常态：会话在登录时已答过该种类，没有不相交的因素可提供，所以这样的装配取到满足要求的 200 靠登录时完成第二因素，加强认证的入口留给种类多于一种的装配。

**删除因素时的排除。** `step-up` 的 `input.exclude_factor` 命名一个因素，引擎把它从可用清单里去掉：其它判据不变，值不是本账户的因素时等于不过滤。去掉后清单为空、证明账户有口令时，起始状态取 `first`、记录的 `method` 为 `password`，口令证明后流程直接 `ready`——这是删除最后一个可用因素的通道，那个因素可能正是丢失的那一个；账户没有口令则仍 401 `insufficient_user_authentication`。该键只过滤可用清单，不改变要求本身，也不让任何一次证明多出别的效力。

请求显式命名 `password` 而账户没有口令可再证明时，起点即按「请求命名了一个部署不能服务的方式」答 400 `bad-request`：不写记录，也不把失败推到证明阶段。该判定先于表中「账户没有因素」与「账户的因素都在本次会话认证后启用」两行取定起始状态。

`step-up` 在 `first` 起始时，方法证明的账户必须是那条证明所属的人，否则落入 `failed`，401 `login-failed`：不是冒充身份时它就是记录的 `account`；冒充期间需要证明现有因素的是发起者，方法证明的因此是发起者自己的账户（见下段）。`link` 的外部身份不按绑定解析账户，账户取记录的 `account`。

**冒充期间的因素核验对象是发起者。** 流程记录的 `account` 仍是生效账户，也就是被冒充账户，登记因素、关联外部身份这些终态动作落在它上面。要求本身按生效账户取定：它名下有启用因素时要求新鲜的本地多因素，没有时要求新鲜的第一因素。满足这个要求的那几步——`step-up`，以及 `enroll` 与 `link` 的起始证明——核验的是发起者名下的因素与发起者会话的方式集，因为能被证明的只有动手的人；被冒充账户名下的因素决定要求的高低，不参与证明。

**完成时的复核**由 `complete` 在终态动作之前做，`link`、`step-up`、`enroll` 共用：`StatusFresh(记录的 session)` 有效、生效账户与记录的 `account` 一致，任一项不过为 401 `flow-invalid`。冒充期间生效账户由流程开始时绑定的记录给出：复核要求 `Impersonation.Active(记录)` 为真、冒充记录的 `Actor` 与 `Target` 分别等于流程记录的 `actor` 与 `account`，会话属于发起者并仍带同一记录标识，任一项不过同为 401 `flow-invalid`；`Impersonation` 能力缺席时无法确认记录，冒充流程的完成按同一条拒绝。`link` 与 `enroll` 另按上表的起始要求再核对一次，不满足为 401 `insufficient_user_authentication`；该要求取流程开始时账户有无有效因素取定的那一份（记录的 `require_multi`），使登记在其最后一步刚置启用的因素不抬高它自己完成所按的门槛（[ADR-authn-login-flow](../../../adr/adr-authn-login-flow.md)）。

### 答复与迁移

`POST B/flows/answer` 先校验绑定，读出记录，再核对：`seq` 等于记录的 `seq`，否则 `ErrStepOutOfOrder`；`step` 与记录状态相容，`answer` 在 `first` 或 `signup`，`factor` 只在 `second`，`enroll` 只在 `enroll`，`complete` 只在 `ready`；`answers` 未达 8。`seq`、`step` 与 `answers` 任一不符都应答 `flow-invalid`。`step=answer` 在 `first` 交给方法的 `Answer`，在 `signup` 由引擎按用户名处理；`step=factor` 与 `step=enroll` 交给 `MFA`，具体字段由因素决定（[authn-mfa](design-authn-mfa.md)）；`step=complete` 由引擎执行终态动作。

答复带着第一因素方法描述符声明的 `phone` 字段且该字段非空时，引擎在进入这一步之前校验 `human_proof`；`step=factor` 选中的因素种类是 `sms` 时，在启动该因素挑战之前校验。两者都在任何发送预算之前，判据与拒绝形状同「开始流程」。

迁移是读出旧字节、改完后 `CompareAndSwap(旧, 新)`。交换落空说明被并发改动，当前请求按流程失效拒绝，不重试。每个方法的 `StepRefused` 只对 `submit` 类的一步保留流程，对 `callback` 类的一步作废流程：授权码与断言一次性，重来要重新开始。

流程的完整迁移表如下，「答复」是对客户端的应答，「副作用」是引擎做的事：

| 当前状态 | 事件 | 下一状态 | 答复 | 副作用 |
|---|---|---|---|---|
| `first` | `submit`，方法交出 `StepPrompt`、`StepData`、`StepRedirect` | `first`，`seq` 加一，`answers` 加一，`sends` 累加 | 200 | 无 |
| `first` | `submit`，`StepRefused` | `first`，`seq` 不变，`answers` 加一，受 8 次上限；方法的步与私有状态保留 | 401 `login-failed`；成因是 `ErrUnsupportedRegion` 或 `ErrInvalidIdentifier` 时按 400 答（`unsupported-region`、`bad-request`） | 方法自己记退避 |
| `first` | `submit`，`StepThrottled` | 不变 | 429 带 `Retry-After`；`retryAfter` 为零时不带 | 无 |
| `first` | `submit`，`StepUnavailable` | 不变 | 503 | 无 |
| `first` | `submit`，`StepProven` | 账户解析，随后按策略判定落入 `second`、`enroll` 或 `ready`；解析判定要开户时落入 `signup`；解析失败落入 `failed` | 200 或 401 | 见「账户解析与策略判定」；落入 `signup` 时写 `pending` |
| `first` | `callback` | 先交换成 `processing`，再调 `Answer` | 无 | 无 |
| `processing` | `StepProven` | 同上，账户解析与策略判定；判定要开户时落入 `signup` | 303；例外：`delivery=token` 且无后续步骤、回跳又不是顶层导航时答 200 | 落入 `signup` 时写 `pending` |
| `signup` | `submit`，合法用户名（短信首登与社交首登另需合法口令） | 建号，随后按策略判定落入 `second`、`enroll` 或 `ready` | 200 | 账户与 `via = self` 的用户名标识在同一事务写入，绑定或手机标识随开户写入（短信首登写下的手机标识建号时即为登录方式，见 [authn-account](design-authn-account.md)）；短信首登与社交首登在同一事务写入口令凭据，企业即时开通不写口令；`pending` 清空，`account` 写入；显示名取待建号的显示名，去掉无效文本与控制字符、去首尾空白并截断到 100 个字符，待建号没有显示名时由账户模块建号时取用户名，这条回退覆盖短信、社交与企业即时开通三条首登开户路径（`pkg/authn/account/admin.go` `displayNameOrDefault`） |
| `signup` | `submit`，口令不合策略或缺失（短信首登与社交首登） | `signup`，`seq` 不变，`answers` 加一，受 8 次上限 | 400 `bad-request` | 不建号 |
| `signup` | `submit`，用户名格式不合或保留字 | `signup`，`seq` 不变，`answers` 加一，受 8 次上限 | 400 `username-invalid` 或 `username-reserved` | 不建号 |
| `signup` | `submit`，用户名已被占用 | `signup`，`seq` 不变，`answers` 加一，受 8 次上限 | 409 `username-taken` | 不建号 |
| `processing` | `StepRefused`、`StepUnavailable`、账户解析失败 | `failed` | 401 `login-failed`、503、401 | 授权码已用，用户重新开始 |
| `processing` | 并发的第二次回跳，或崩溃遗留 | 不改 | 401 `flow-invalid` | 无 |
| `second` | 因素通过 | `ready` | 200 | 无 |
| `second` | 因素被拒 | `second`，`seq` 不变，`answers` 加一；挑战与方法的步保留 | 401 `login-failed` | 因素退避（`ScopeFactor`，由 `MFA` 记） |
| `second` | `StepThrottled`（`ScopeFactor` 退避中） | 不变 | 429 带 `Retry-After` | 无 |
| `second` | `StepUnavailable` | 不变 | 503 | 无 |
| `second` | 答复数用尽、超时 | `failed` | 401 `login-failed` | 无 |
| `enroll` | 因素实现交出 `StepEnrolled` | `ready` | 200 | 因素标识存入记录的 `mfa.enrolled`；本次登记的因素不计入保障等级 |
| `enroll` | 因素实现交出 `StepRefused`（确认码不符等） | `enroll`，`seq` 不变，`answers` 加一；登记中的因素与方法的步保留 | 401 `login-failed` | 可原地重试确认码 |
| `challenge` | 选中因素的 `Challenge` 交出 `StepRefused` | `second`，`seq` 不变，`answers` 加一；因素清单保留 | 401 `login-failed` | 可改选另一因素或重试 |
| `ready` | `complete` | `done` | 见「终态动作」 | 终态动作 |
| 任意 | `answers` 达 8、过期、`DELETE flows/current`、方法被 `disabled-methods` 关 | `failed` | 401 `flow-invalid` | 无 |

**拒绝不推进序号。** `submit` 类的一步被拒时，序号不动、答复计数加一，方法的步与私有状态保留：当事人改正后带同一个序号原地重试即可成功，不必先重读流程；序号只在流程真正前进（提示、数据、跳转、证明）时加一。回跳类的一步是例外：授权码与断言一次性，`processing` 的拒绝置 `failed`，客户端按导航重新开始。

过期靠键的生存期兜底，不需要任何清扫。submit 类的一步落入 `ready` 时应答 `next.kind = complete`，客户端以 `step=complete` 取走结果；回跳类的收尾见「回跳类的收尾」。

### 回跳类的一步

`GET|POST B/callback/:provider` 由引擎处理，分发给方法。引擎先核对：流程存在且处于 `first`；回跳路径的 `:provider` 与记录里的 `provider` 一致（社交方法的记录记方法名，企业 SSO 的记录记 `Begin` 时 `input.provider` 给的值）；绑定匹配。通过后引擎**在调用 `Answer` 之前**先把记录交换成 `processing`，`seq` 与方法状态不变。并发的第二次回跳败于这一步，当场得到 `flow-invalid`，所以授权码至多换一次、SAML 断言至多处理一次。

`Answer` 返回之后，引擎再把记录交换成新状态；方法交回 `StepRefused` 或 `StepUnavailable` 时置 `failed` 墓碑。停在 `processing` 的记录，即进程崩溃而 `Answer` 没有返回的情形，一律按失效处理，用户重新开始。方法自己不对流程记录做任何写。这一做法的取舍见 [ADR-authn-login-flow](../../../adr/adr-authn-login-flow.md)。

### 账户解析与策略判定

方法交出 `StepProven` 之后，引擎在同一次请求内依次做账户解析与策略判定。

**账户解析。** 引擎按 `Proof` 的来源字段解析账户：

- `Account` 非空：引擎仍读取账户并核对状态为启用，不信任方法的断言。
- `Identifier` 非空（短信验证的手机）：按标识查账户；查到而该手机未被持有人选作登录方式时按凭据失败处理（401 `login-failed`），不当作未知标识、不进入开户（REQ-signin-011）；查不到时按注册设置决定开户或失败；决定开户时不立即建户，而是把流程停到待建号，取得用户名之后建户，建号时该手机即为登录方式（见 [authn-account](design-authn-account.md) 的首登开户）。
- `External` 非空：按（提供方，主体）查绑定；无绑定时不按提供方声明的地址关联一个既有账户或建立绑定（企业预置的匹配是唯一例外，归 A5）：该地址已登记在现有账户的邮箱标识上时按 `ErrLinkRequired` 拒绝，否则按外部身份的来源与设置走首登开户，决定开户时同样先停到待建号，新建的账户只带提供方给的显示名，提供方没有给出时取用户名（REQ-signin-017、REQ-signup-048、REQ-signup-049、REQ-signup-051）。`Aliases` 用于旧键升级。键永远是（提供方，主体），不用邮箱。社交证明的提供方不带 `oidc:`、`saml:` 命名空间，企业证明的提供方属于一个租户；社交证明落在 `oidc:`、`saml:` 命名空间，或企业证明的 `ext.Tenant` 与提供方行的租户不一致，都按 `ErrInvalidArgument` 拒绝（fail-closed，判据隐含于 A1 的命名空间与 A5 的租户归属）。

已有绑定或已有标识的账户直接进入策略判定，不进入待建号；待建号只在账户解析判定「要开户」时进入。库中没有用户名的行只来自绕过写入面，照常以它已有的标识登录。

显式关联、企业 SSO 的域核对与预开户、首登开户、`Provisioner` 钩子的判据全部归 [authn-account](design-authn-account.md)（[ADR-authn-external-binding](../../../adr/adr-authn-external-binding.md)、[ADR-authn-contact-and-login-ways](../../../adr/adr-authn-contact-and-login-ways.md)）。自动关联与自动并入不再存在（REQ-signup-049）。引擎承担的是这些规则的结果怎样落到流程上：

| 解析结果 | 流程 | 对外 |
|---|---|---|
| 账户确定 | 进入策略判定 | — |
| 账户非启用，来源是外部登录 | `failed` | 403 `account-unavailable`，调用方已在 IdP 处认证过 |
| 账户非启用，来源是口令或短信 | `failed` | 401 `login-failed`，与错口令同形 |
| 账户是 `service` 类型（任何来源） | `failed` | 401 `login-failed`：服务账户是 API 客户端，不是交互登录目标 |
| 无绑定且注册关闭（`ErrSignupClosed`）、域不符（`ErrDomainMismatch`）、未预开户（`ErrNotProvisioned`）、宿主拒绝开户（`ErrProvisionRefused`） | `failed` | 401 `login-failed`，成因进日志 |
| 无绑定而提供方声明的地址已登记在现有账户的邮箱标识上，且不属于企业预置的匹配（`ErrLinkRequired`） | `failed` | 401 `link-required` |
| 开户事务里提供方声明的地址被另一个账户取得（`ErrContactTaken`：判定「无人持有」到建户之间输了竞态） | `failed` | 401 `link-required`，与该地址早已登记时同一结果；落地的片段同样带掩码地址 |
| 外部身份已被别的账户占用（`ErrBindingConflict`） | `failed` | 409 `conflict` |

「对外」一列是问题文档的形状。回跳请求是顶层导航时，同一失败改按「回跳的失败落地」答 `303` 并携带同一个码；表里这些拒绝本身不变。

自动并入不再发生；旧行为下已并入的账户按写入时的方式继续工作，不回溯改写（REQ-signin-034）。显式关联（`link`）不建会话；它按 A6 撤销发起会话之外的会话，不删除口令凭据。

**策略判定。** 引擎调用 `MFA.Require(账户, Proof)`。结局的判据与可用清单的计算归 [authn-mfa](design-authn-mfa.md)；引擎按结局落入流程状态：`MFANone` 落入 `ready`，`MFAFactor` 落入 `second` 并把清单存入记录的 `mfa.offered`，`MFAEnroll` 落入 `enroll`。

`MFAFactor` 的可用清单为空，或装配里没有账户所登记因素种类的实现时，该次登录无法完成，应答 `login-failed`，运维从日志与事件发现；不得当作账户没有因素。租户级的强度策略在请求期由 [tenant-admission](../tenant/design-tenant-admission.md) 执行，登录时账户全局、租户未知，登录期间的任何范围线索都能被绕开。

### 终态动作

`ready` 上的 `complete` 先把记录交换成墓碑 `done`，再执行终态动作表里的动作。交换的败者拒绝，所以每条流程的终态动作至多执行一次；交换之后的动作失败时，流程已消耗，用户重新开始。`link`、`step-up`、`enroll` 的动作以「在已登录身份上开始流程」里的完成复核开头。

| 目的 | 完成时 |
|---|---|
| `login` | 以 `MFA.Assurance` 合成强度；记录的 `mfa.enrolled` 非空（策略要求登记而账户没有因素的登录）时本次会话仍记单因素；`Sessions.Create` 建会话，`delivery=session` 时 `Kind` 为 `browser`、cookie 经 `Sessions.SetCookie` 写出，`delivery=token` 时 `Kind` 为 `token`、不写 cookie，并对该会话调 `Tokens.Issue` 与 `Tokens.NewRefresh`；账户 `Touch`；写 `authn.login` 审计（尽力）；发 `authn.login.succeeded`；请求带着可解析的旧会话 cookie（`Sessions.Current`）时经 `Sessions.Revoke` 撤销旧会话 |
| `link` | 复核；`AccountAdmin.LinkExternal`，外部身份已被别的账户占用则 409 `conflict`，不并入；这是账户面显式绑定提供方的唯一路径，账户取记录的 `account`，不按提供方声明的地址选择账户（REQ-signin-017）；`Sessions.RevokeAccount(except = 发起会话)`；发 `authn.binding.changed`；不建新会话 |
| `step-up` | 复核；`Sessions.Elevate` 更新等级、认证时刻与方式集并轮换令牌，不建新会话；cookie 会话写新 cookie，令牌会话在应答里带新访问令牌。`Elevation` 的方式集、等级与 `IDPAssured` 取 `MFA.Assurance`（以会话方式集构成的 `Proof` 与通过的 `Pass`）的结果 |
| `enroll` | 复核；`Sessions.RevokeAccount(except = 发起会话)`；不改会话等级 |

`login` 建会话时 `NewSession` 的取值：`Account` 是解析出的账户；`Methods` 是 `Proof.Methods` 加第二因素 `Pass.Methods`；`Assurance` 与 `IDPAssured` 取 `MFA.Assurance` 的结果（结局为 `MFAEnroll` 时为单因素、`IDPAssured` 为假）；`AuthTime` 是完成时刻；`Provider` 取 `Proof.Provider`，`Attributes` 取 `Proof.External.Attributes`；`Client` 取请求的 `ClientInfo`。令牌的 `IssueToken` 由同一份取值与 `Issued.ID` 构成。

**完成应答体。** `login` 见「请求与应答的形状」的 `authenticated` 体（`delivery=session` 设 cookie 并回 `session`，`delivery=token` 回令牌对）。`step-up` 的 cookie 会话写新 cookie 并回与 `login` 同形的 `authenticated` 体，令牌会话回 `{"state":"elevated","token_type":"Bearer","access_token","expires_in"}`。`link` 回 `{"state":"linked"}`。`enroll` 回 `{"state":"enrolled"}`；因素行在 `MFA.Enroll` 的最后一步已置为启用，引擎不再调任何启用方法。`step-up`、`link`、`enroll` 在回跳类里的收尾形态见「回跳类的收尾」。

说明：

- `login` 撤销旧会话不是会话固定防护所依赖的环节：登录总是新建会话、不沿用任何旧令牌。SAML 的跨站 POST 不带 `SameSite=Lax` 的 cookie，旧会话无从撤销，这是已知的例外。
- 口令方式的 `must_change` 路径完成后，引擎调 `Sessions.RevokeAccount(except = 新会话)`，因为口令变了。引擎判断这条路径有没有走由 `Proof.PasswordChanged` 承载：口令方法在自己重设了凭据（`Passwords.Set` 成功）时把它的证明上的该字段置真，引擎据此在完成时撤销其余会话。撤销其余会话只对 `login` 生效；`step-up` 的 `must_change`（以口令走第一因素时）同样置真，但 `step-up` 完成时不撤销其余会话，本设计不为它加这一动作。
- `login` 的审计行用新认定的身份派生上下文，使 `actor` 是登录的账户而不是空串；登录失败写同名审计行，`actor` 为空；本实现不写 `identifier_mac`，失败因此不经审计按标识关联。失败不发事件，只走审计、日志与计数器，否则 `login.failed` 会成为枚举旁路（[authn-events](design-authn-events.md)、[ADR-authn-events-and-audit](../../../adr/adr-authn-events-and-audit.md)）。
- 会话与令牌的期限、cookie 的属性与 CSRF 归 [authn-session](design-authn-session.md) 与 [authn-token](design-authn-token.md)。

### 回跳类的收尾

回调证明通过后，按流程的 `delivery` 与后续步骤分情况收尾，收尾情况的全集如下：

| 情况 | 收尾 |
|---|---|
| 登录，`delivery=session` 且无后续步骤 | 直接在回调响应里完成：建会话、设 cookie、`303` 到记录里的 `return_to` |
| 登录，仍需第二因素或登记因素 | `303` 到 `return_to`，流程仍在 cookie 里；前端用 `GET flows/current` 读状态继续 |
| 登录，回跳后账户解析判定要开户 | `303` 到 `return_to`，流程停在待建号；前端用 `GET flows/current` 读到 `next.kind=challenge` 与 `username` 字段，以 `POST flows/answer {step:"answer"}` 提交用户名后继续 |
| 登录，`delivery=token` | 令牌不能放进回跳 URL，流程停在 `ready`。回跳是顶层导航（`Sec-Fetch-Mode: navigate`）时以 `303` 落到 `return_to`，不建会话；不是导航的 API 回跳答 200 完成体。客户端随后以带流程绑定的 `POST flows/answer {"step":"complete"}` 取走令牌 |
| 非登录目的（`link`，以及无因素账户经回跳方法的 `step-up`）证明后落到 `ready` | `303` 到 `return_to`，不建会话；流程仍在 cookie 里停在 `ready`，前端用 `GET flows/current` 读到 `next.kind=complete`，再以 `POST flows/answer {"step":"complete"}` 执行终态动作 |

`return_to` 为空时，落到用户代理的收尾改用设置 `authn.login.default-return-to`：它按与 `return_to` 相同的规则校验，不合规时用 `/`，与 IdP 发起的路径一致。

会话 cookie 在回调的 `303` 响应里设立不引入新的伪造面：流程绑定、`state` 与一次性消费已经把回调限定在发起流程的那个浏览器上。

### 回跳的失败落地

回跳是顶层导航（`Sec-Fetch-Mode: navigate`）而这次登录被拒时，用户代理不停在问题文档上：回调答 `303`，把机器码放进 URL 片段，落到产品内的一个页面。

```
Location: <落地页>#authn_error=<码>[&authn_email=<掩码地址>]
```

- **落地页。** 目的为 `login` 的落到设置 `authn.login.callback-error-return-to`，缺省 `/`；目的为 `link`、`step-up`、`enroll` 的落到该流程记录里的 `return_to`，即发起它的那个页面。两个来源都按 `return_to` 的同一规则校验。**发起面负责读片段**：本仓库的界面发起重定向类流程时一律带 `return_to` 指向发起的那一面（提权对话框、登记向导与绑定添加区都是如此），该面把问题码交给自己的错误呈现；落地面不读片段时，宿主的外壳在落地时读一次片段并以一次性提示呈现，不让拒绝静默消失（[starters](../design-starters.md)）。
- **码。** 取问题类型 `urn:speed:authn:<码>` 中 `<码>` 一段，是已有的封闭词汇（`login-failed`、`link-required`、`account-unavailable`、`conflict`、`throttled`、`unavailable` 等）；不带码的 500 落成 `unavailable`。
- **地址（仅 `link-required`）。** `link-required` 的落地在片段里多带 `authn_email`，值是冲突地址的掩码：本地部分的首个字符、`***`，再按原样接 `@` 与域名（`ann@home.org` 落成 `a***@home.org`）。只有这一种拒绝带它；其他码的落地不带这个键，`link-required` 没有可命名的地址时也不带。地址在服务端掩码，片段会写进浏览器历史并显示在页面上，只保留足以让调用方认出自己哪个地址冲突的位数。错误文本不带地址，因为错误会进日志。
- **片段而非查询串。** 片段不随请求发往服务器，码与掩码地址都不进访问日志，也不进页面能发出的 `Referer`。
- **不改变 API 客户端的形状。** 没有 `Sec-Fetch-Mode: navigate` 的请求照旧得到 `application/problem+json`。落地页读不出、或落地页不合规时同样退回问题文档，不把用户代理送到一个未经校验的地址。
- **客户端呈现。** 登录面把片段里的码按 api-client 的推导归一到 `authn.<码>`（连字符换下划线），交给已有的内联错误渲染；白名单之外的码落通用文案。`link-required` 携带的 `authn_email` 另在拒绝句下方、同一条内联错误横幅内渲染为一行地址（[web-auth-ui](../web/design-web-auth-ui.md)）。

取舍见 [ADR-authn-callback-failure-landing](../../../adr/adr-authn-callback-failure-landing.md)。

### IdP 发起的 SAML

`POST B/callback/:provider` 没有流程 cookie、而提供方行的 `allow_idp_initiated` 为真时，引擎当场新建一条 `purpose=login`、`delivery=session` 的流程记录，`return_to` 取设置 `authn.login.default-return-to`（按 `return_to` 的规则校验），并直接置 `processing`，其余同回跳；新流程 cookie 随应答设立。方法不创建流程记录。没有可比对的绑定，所以这一路径防不了登录 CSRF，该方式由提供方行缺省关闭（[authn-sso](design-authn-sso.md)）。

### 失败退避

退避的算法、`Throttle` 的范围集合、账户级静默、读不到退避状态时的方向与口令派生的并发上限归 [authn-account](design-authn-account.md)；本节只写登录路径上的调用形状：

- 方法在校验之前先 `Check`：退避中交 `StepThrottled`，应答 429 带 `Retry-After`，不派生、不比对。
- 校验成功 `Succeed`，被拒 `Fail`，各方式使用的范围见各方式一节与迁移表。
- 账户级的退避对调用方不可见：达到阈值后，应答与普通的凭据错误同形（401 `login-failed`），不返回 429；可见的 429 来自标识级、因素级、号码级与客户端级的退避。
- `Throttle` 读写失败时登录路径答 503、不校验。

**来源地址的前置减负**放 kv，由设置 `authn.login.ip-limit` 控制，缺省每分钟 60 条验证消息。它按实际发出的消息计数，不按打开的流程计数：一次调用报几条发送就扣几条，不发送的流程不消耗额度；报告就是「短信发送的每流程上限」里方法与因素带出的那个条数，短信方法与其因素各报自己的发送条数。检查在请求入口、调用方法之前做：额度已用尽的来源在本窗口内的登录、注册与应答请求一律答 429，不再调用方法，也不再发出验证消息（一次调用报多条发送时，跨过上限的那一次仍会把它报的几条发出，超出量至多是一次调用的发送条数减一）。自助路由（邮件码与手机号验证）的发码不经流程引擎，不计入这一层，各由自己的发送预算约束。入口模块默认不信任任何代理：端及其继承的模块级默认都未列出可信代理时，`ClientIP()` 是对端地址，一个未配置可信代理的代理之后全站共用一个桶——缺省值因此取一个对共享出口留有余量的粗粒度上限，把 0（关闭）留给明确要放开这一层的部署，改到 0 时在启动诊断里点出。它是权威退避之外的一层粗粒度减负，不是防线：kv 不可用时跳过该层并留 Warn 与计数，放宽的上界是自由次数。

**地址盾与发送预算不是同一件事。** `ratelimit` 的地址盾按来源地址限制单位时间内的请求数，落在身份确定之前，不看消息；发送预算按收件人、来源与账户限制消息数，归发送它的能力（[authn-sms](design-authn-sms.md) 的预算维度、[authn-config](design-authn-config.md) 的设置表）。本节这一层按来源地址限制单位时间内的验证消息数，是登录路径上的粗粒度前置减负，既不替代地址盾，也不替代任何一条发送预算。

### 枚举防护

所有可能泄露账户、标识、号码是否存在的分支都走同一个失败应答，耗时相当。口令路径上的诱饵派生与哈希参数变更后的计时差归 [authn-account](design-authn-account.md)；其余分支在登录路径上的形状如下：

- 口令、验证码、因素、断言不成立，以及账户不存在、无口令、已停用，统一 `login-failed`。
- 短信号码未登记且注册关闭时，照样写一行 `suppressed = true` 的挑战行，不入队、不发送，使重发间隔与各项预算按与已登记号码完全相同的路径累计；应答与「已发送」同形；随后的答复一律按 `login-failed` 失败，并经一次诱饵校验耗费同样的时间。预算与挑战行归 [authn-sms](design-authn-sms.md)（[ADR-authn-sms](../../../adr/adr-authn-sms.md)）。
- 发现接口只回答与域名有关的公开信息（[authn-sso](design-authn-sso.md)）。
- 外部登录路径上，调用方已在 IdP 处证明过身份，才会看到 `account-unavailable` 与 `link-required`；`link-required` 在社交路径上只在 `External.EmailVerified` 为真时给，企业 JIT 路径则以邮箱域属于该提供方所属租户已验证的域为前提（IdP 的域证明替代 `EmailVerified`，见 [authn-account](design-authn-account.md) 的 A5），因此只对已被提供方证明拥有该邮箱或其域的调用者暴露其账户存在。
- 因素清单只在第一因素通过之后给。

### 短信发送的每流程上限

每流程至多发 `authn.sms.per-flow-sends` 条短信。调用 `SMSCodes.Send` 的是方法（`authn.login.sms`）与因素（`authn.mfa.sms`），引擎看不到这次调用，所以上限的分工如下：

- **引擎供数。** 每次调用方法或经 `MFA` 调用因素之前，引擎把设置值减去记录的 `sends`（下限为零）写进 `Start.SendsLeft` 或 `FlowState.SendsLeft`。
- **方法与因素在 `Send` 之前核对。** 一次调用里发送的条数不超过 `SendsLeft`；`SendsLeft` 为零时不调 `Send`，交 `StepThrottled(0)`，对外 429 `throttled`、不带 `Retry-After`。
- **方法与因素报告。** 回答上用 `Step.WithSends(n)` 带出本次成功返回的 `Send` 调用数；它设置条数而不累加，负数按零读，因为引擎据此核减上限，一个负的条数会放宽上限；加在没答的零值回答上不生效，一份报告做不成一个回答。`MFA` 转交因素的回答时保留该报告。
- **引擎累加。** 不论回答是哪一种，引擎在写这一步迁移的比较并交换里把 `n` 加进 `sends`。报告值超过交给方法的 `SendsLeft` 是方法缺陷：记 Error，按该方法故障收口为 503，流程置 `failed`。

`POST B/phone` 与 `POST B/phone/verify` 不在流程里，不受这一上限约束，由 [authn-sms](design-authn-sms.md) 的号码级与账户级预算约束。

### 口令（password）

`Begin` 收 `identifier` 与 `password`。方法只服务 `login` 与 `step-up` 两个目的；`link`、`enroll` 或空目的一律交 `StepRefused`，既不读入参也不计数——口令证明的是账户，不是外部身份或因素。口令登录接受用户名，或账户持有并已选作登录方式的已验证邮箱；提供方声明的地址只有在该标记为真时才是口令登录名，账户唯一的邮箱地址在被证明后由系统置真（REQ-account-068、REQ-signin-006、REQ-signin-038）。手机号不作口令登录名。

口令方法先以提交的标识为键 `Throttle.Check(ScopeIdentifier, 键)`，退避中（等待非零，含为负的保守情形）交 `StepThrottled`；然后调 `Passwords.Verify`。键只把提交的标识去首尾空白并折小写再取 SHA-256，不含账户模块 `NormalizeIdentifier` 的其余规则（NFKC、邮箱域名、手机 E.164，[REF-unicode-uax15](../../../references.md)、[REF-itu-t-e164](../../../references.md)）；某些拼写（NFKC 等价的全角与半角等）在这里落到不同键，靠账户模块的账户级静默退避把它们合在一起计数（[authn-account](design-authn-account.md)）。逐条：

- 成功：`Throttle.Succeed` 清零，交 `StepProven`，`Methods = [pwd]`，`Proof.Account` 是校验出的账户。
- `ErrInvalidCredentials`、`ErrAccountNotFound`：`Throttle.Fail`，交 `StepRefused`，成因 `ErrInvalidCredentials`，对外 401 `login-failed`。
- `ErrInvalidIdentifier`：交 `StepRefused`，成因保留 `ErrInvalidIdentifier`，**不计入退避**（标识无法规范化即不对应任何账户，不是一次猜测）；引擎按 `StepRefused` 的成因把它答成 400 `bad-request`，与 [authn-account](design-authn-account.md) 的运行期哨兵表把该哨兵映到 400 一致。所以迁移表里 `first` 状态的 `StepRefused` 对外不都是 401：成因是 `ErrInvalidIdentifier` 时是 400。
- `ErrBusy` 与其他依赖故障：交 `StepUnavailable`，应答 503（`ErrBusy` 带 `Retry-After: 1`）。

`Passwords.Verify` 交回 `VerifyResult.MustChange` 为真时，方法不立即 `StepProven`，而返回 `StepPrompt(step = "new_password", private = <口令已验证的标记，不含口令>, Field{new_password})`，并把随后那次成功的证明置 `PasswordChanged`。答复 `new_password` 时方法调 `Passwords.Set`，走策略校验，新口令不得与当前口令相同；`ErrPasswordPolicy` 经 `(Step, error)` 两个返回值同时交回——步仍是 `new_password`（流程保留，客户端可再答），错误链里带 `ErrPasswordPolicy`，引擎据此答 400 `bad-request`；成功才 `StepProven`。这一步不经 `Throttle`：它跟在一次已通过的口令校验之后，不是一次猜测。管理员创建账户与重置口令缺省置 `must_change`，临时口令因此不会长期有效。

账户口令不经 `Basic`：`Basic` 只属于口令表后端（[authn-gate](design-authn-gate.md)）。

### 社交登录与企业 OIDC 的共同事务

适用于 `google`、`github` 与企业 `oidc`。库的分工与自做部分的判据见 [ADR-authn-oauth-flow](../../../adr/adr-authn-oauth-flow.md)。本节与「微信」一节里的社交方法，连同企业 `oidc` 与 `saml`，服务 `login`、`link` 与 `step-up` 三个目的，不受理 `enroll`：只有交回 `External` 的方法能承担 `link`（外部绑定经它建立与解绑），而 `enroll` 是登记第二因素、由 `MFA.Enroll` 承担。空目的与 `enroll` 在读到任何参数之前被拒。

**`Begin`。** 方法生成 `state`（32 字节随机数的 base64url）、PKCE 校验串（`oauth2.GenerateVerifier`）、OIDC 的 `nonce`（32 字节随机数），连同绑定存入 `method_state`，`state` 存摘要。授权地址由 `oauth2.Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))` 生成，PKCE 一律 S256。`redirect_uri` 是 `public-url + B/callback/<提供方>`，每个提供方的回跳路径不同，把 A 的回跳投给 B 因路径不同而不可能；回跳里若带 `iss` 仍与该提供方的 issuer 比对（RFC 9207，[REF-rfc9207](../../../references.md)）。

**回跳。** 引擎已核对流程状态、方法与路径、绑定，并已置 `processing`（见「回跳类的一步」）。方法依次：

1. 回跳带 `error` 参数则按 `cancelled` 交 `StepRefused`，不读任何其他参数；按 callback 类被拒处理，流程作废，对外 401 `login-failed`。
2. `state` 经 SHA-256 后与记录里的摘要常量时间比较。
3. 比对 `iss`（若带）。
4. `Exchange(ctx, code, oauth2.VerifierOption(verifier))`。
5. OIDC：`Provider.Verifier` 验 `id_token` 的签名、`iss`、`aud`、`exp`；**另自行校验** `nonce` 与多受众时的 `azp`；不调 userinfo，只取 ID 令牌里的声明；`email_verified` 为布尔真才视为已验证邮箱。

回跳证明通过、账户解析判定要开户时，引擎把流程停在待建号并应答 `challenge`；用户名与口令由引擎收集，方法不与这一步交互。短信首登与社交首登的这一步都收集口令；短信首登的这一步另收集一个可选的显示名，企业即时开通只收集用户名。

出站客户端全经 `internal/egress`：超时、体上限、不跟随重定向、连接时拒绝非公网地址；并以 `oidc.ClientContext` 注入，未注入时库用无超时的默认客户端，所以未经注入的出站调用本身是缺陷。`google`、`github` 与企业 `oidc`、`saml` 的客户端在 `observability.Transport` 之内套 `log.Transport`，每次出站请求成功与失败各写一条记录：方法、主机、不含 query 的路径、对端状态、`outcome` 与耗时，无应答时带底层成因；授权码与令牌都不进记录，请求头与正文同样不记。方法交出的 `Proof.External` 带（提供方，主体）、邮箱与 `EmailVerified`，邮箱只是线索：除企业预置的匹配（A5）外，引擎不用它解析账户，也不把它写入新账户；它只在提供方声明已验证且该地址已登记在现有账户时触发 `link-required`（REQ-signin-017、REQ-signup-048）。

`google` 与 `github` 各有一组可选覆盖项把方法指向别的提供方：前者是 `authn.login.google.issuer`，后者是 `authn.login.github.authorize-url`、`token-url` 与 `api-base`；缺省值就是内置的 Google issuer 与 GitHub 端点。两者的受限出站客户端另由 `allow-loopback` 与 `root-ca-file` 成形：缺省为拒回环、用系统根，根证书文件追加到系统根（不替换）并在启动时读取。指向自定义端点即由部署方承担对该端点与其证书的信任；缺省路径上的校验与行为不变（[ADR-authn-social-endpoints](../../../adr/adr-authn-social-endpoints.md)）。

**Google。** 对 issuer `https://accounts.google.com` 用 go-oidc（[REF-go-oidc](../../../references.md)）做发现（[REF-openid-connect-discovery](../../../references.md)）（`authn.login.google.issuer` 可覆盖，覆盖值须是绝对 https 地址，回环主机的 http issuer 仅在 `allow-loopback` 为真时接受），首次使用时懒发现、缓存，失败不终止启动，期间该方式答 503。`sub` 作主体。配置了 `hosted-domain` 时检查 ID 令牌的 `hd` 声明：限定组织的依据是 `hd`，不是邮箱域名，授权请求里的 `hd` 参数只是界面优化。

**验证路径上的取键集不受调用预算约束。** go-oidc 构造密钥集时用 `context.Background()` 而非本次调用的 `ctx`，叠加 `internal/egress` 按设计不设总超时，一个接受连接却不作答的密钥集端点能把这次登录请求拖过 `authn.login.call-budget`。这条缺陷的修法在 `egress`（加总超时选项）或引擎（并发跑方法、作废迟到的回答），不在方法叶子包里。受此影响，验证路径上取键集失败一律答 `StepRefused`，不答 `StepUnavailable`：go-oidc 把「取不到密钥集」与「验签失败」压成同一个错误字符串，两者无法区分；发现与换令牌的不可用仍答 `StepUnavailable`。

**GitHub。** `x/oauth2`（[REF-golang-x-oauth2](../../../references.md)）加 `endpoints.GitHub`（授权、令牌与 API 基址可由 `authn.login.github.authorize-url`、`token-url` 与 `api-base` 覆盖，覆盖值须是绝对 https 地址），作用域 `read:user user:email`，PKCE S256。GitHub 是纯 OAuth 2.0，没有 discovery、没有 issuer，也从不发 `iss`，所以共同事务里「回跳带 `iss` 就与该提供方的 issuer 比对」对它不适用。授权码换到访问令牌后自写对 `/user` 与 `/user/emails`（[REF-github-rest-users](../../../references.md)）的 GET：`/user` 取数字 `id` 作主体；`/user/emails` 取 `primary` 且 `verified` 的邮箱，没有这样的一条时邮箱为空、`EmailVerified` 为假，不报错。`/user` 返回的 `email` 没有已验证标记，不采用。**这两次 GET 返回非 200、或 `/user` 里没有正整数 `id`，都答 `StepRefused`（拒绝，fail-closed），不是 `StepUnavailable`；只有传输失败或响应体解不开才答 `StepUnavailable`。** 因此 `/user/emails` 临时抖动会让一个此前已绑定的身份也登录不上，这是 fail-closed 的代价。访问令牌用完即弃，不存、不刷新。取舍见 [ADR-authn-oauth-flow](../../../adr/adr-authn-oauth-flow.md)。

### 微信（wechat.web 与 wechat.mp）

`wechat.web` 是网站应用扫码登录（`qrconnect`），作用域 `snsapi_login`；`wechat.mp` 是公众号网页授权，作用域 `snsapi_userinfo`。两个入口的授权地址末尾都必须带 `#wechat_redirect`——微信的 `qrconnect` 与网页授权端点都要求该片段——由共用客户端对两者统一带上。`authn.login.wechat-web` 与 `authn.login.wechat-mp` 各是一个模块，共用 `authn/login/internal/wechat` 里的薄客户端，客户端自写，不引任何微信 SDK（[ADR-authn-wechat](../../../adr/adr-authn-wechat.md)）。

- **`state`**：32 字节随机数的十六进制，64 字符，满足公众号侧「字母数字、至多 128 字节」的限制。
- **换令牌**：`GET https://api.weixin.qq.com/sns/oauth2/access_token?appid&secret&code&grant_type=authorization_code`；HTTP 200 加 `errcode` 非零按失败。
- **`secret` 在查询串里**：携带 `secret` 的 URL 不得以任何形式离开客户端包。出站错误一律剥去 URL 再记日志（`*url.Error` 带全 URL）；配置里 `app-secret` 的值另登记为日志脱敏的字面量匹配器，登记在模块的 `New` 里、先于日志器绑定。
- **出站记录**：客户端套 `observability.Transport(log.Transport(...))`，外层注入追踪头，内层为每次授权码换令牌写一条记录，成功与失败各一条：方法、主机、不含 query 的路径、对端状态、`outcome` 与耗时，无应答时记 `*url.Error` 之下的底层成因。query 里的 `secret` 与授权码既不进追踪头也不进记录，请求头与正文同样不记。
- **不调 `/sns/userinfo`**：昵称头像不是授权依据，也省一次出站调用；显示名固定「微信用户」；不合成邮箱。
- **主体**：有 `unionid` 时，`Proof.External` 的主体取 `unionid`，`Aliases` 填 `wechat-openid:<appid>` 命名空间加 `openid` 的键，使此前以 `openid` 键建立的绑定能升级为 `unionid` 键；没有 `unionid` 时，主体取该 `openid` 键，`Aliases` 为空。升级规则见 [authn-account](design-authn-account.md)。
- **残余风险**：协议没有 PKCE、没有 `nonce`、没有 ID 令牌，回跳的防线只有 `state` 加流程绑定。
- 回跳后的首登开户同样由引擎停在待建号收集用户名，方法不参与。
- 本地开发与自动化测试只能对模拟的微信服务端测。

### 短信与其他方式

- **短信（`sms`）**：`phone`（E.164）步与 `code` 步。号码前缀必须在放行表里，缺省为空即一律拒绝（`unsupported-region`）。发码、预算、挑战行与抑制行归 [authn-sms](design-authn-sms.md)；方法在发码前后按「短信发送的每流程上限」核对并报告条数；验证码通过时交 `StepProven`，`Proof.Identifier` 是被证明的手机，`Methods = [sms]`；已登记的手机只有被持有人选作登录方式时才完成登录，未被选中的号码按凭据失败处理，不进入开户（REQ-signin-011）；账户解析判定要开户时，引擎停在待建号收集用户名、口令与可选的显示名。**单凭短信码只算一个因素。** 发码时方法把请求的来源地址填进 `SendCode.Source`：`Begin` 取 `Start.Client.IP`，`Answer` 取 `StepInput.Client.IP`；引擎把两者都从本次请求填入，所以短信发送计入来源预算（[authn-sms](design-authn-sms.md) 的「预算维度」）。
- **通行密钥（`passkey`）**：WebAuthn 仪式数据放在 `FlowState.Private`，由 `StepData` 把选项交给客户端；登录断言要求用户验证（`userVerification: required`），因此 `Proof.UserVerified` 为真（[authn-mfa](design-authn-mfa.md)）。
- **企业 OIDC 与 SAML**：提供方是库里的行，不是配置；校验与补充校验归 [authn-sso](design-authn-sso.md)。

### 账户自助

下列路由在已认证的身份上执行，并登记为可冒充。改口令、添加用户名、选作或取消登录方式、设置默认联系地址、删除标识与解绑外部身份要求新鲜认证并过 CSRF；登记并验证一条邮箱或手机只要求身份与登录会话，本身不要求新鲜认证，也不轮换当前会话或结束其他会话，但其证明改变账户据以证明或找回自己的事实时例外——一条将取代默认联系地址或成为唯一邮箱地址的邮箱、一个将取代已启用短信因素发码目标的新手机号，其证明要求新鲜认证。冒充身份与被冒充账户本人相同，判据只取被冒充账户的权限，认证事实取自发起的会话。新鲜认证的强度取「账户安全类动作的要求」：账户有因素时是新鲜的本地多因素，没有因素时退回新鲜的第一因素（[authn-mfa](design-authn-mfa.md)）。

| 路由 | 行为 |
|---|---|
| `PUT B/password` | 改口令；请求体的当前口令可空（经提供方开户的账户没有口令），经 `SetPasswordOptions.Current` 交给 `Passwords.Set`，由账户模块校验当前口令与策略、失败即 `ErrPasswordPolicy`（400）或 `ErrInvalidCredentials`（401），本路由不单独计数退避；成功后处理器调 `Sessions.Rotate` 与 `Sessions.RevokeAccount(except = 当前会话)`；账户没有可作登录名的标识（没有用户名也没有被选作登录方式的已验证邮箱）时 409 `login-identifier-required` |
| `POST B/phone`、`POST B/phone/verify` | 添加或更换手机，经短信验证新号码（`verified_via = sms-code`）；通过后是已验证的手机标识，不自动选作登录方式；是否作登录方式经 `PUT B/identifiers/:id` 选择（REQ-account-014）；两步本身都不要求新鲜认证，验证不轮换当前会话也不结束其他会话，但新号码取代已启用短信因素的发码目标时，证明要求新鲜认证；服务端状态与步骤见「手机号验证」 |
| `POST B/identifiers` | 添加本人登录标识，体 `{kind, value}`：`kind` 取 `username` 或 `email`，手机走 `POST B/phone`。用户名是登录名，无所有权可证，一步写入，要求新鲜认证，`verified_via = self`；邮箱先经 `EmailCodeSender` 发码并在 kv 写一条按会话的待验证记录，应答 `{state: "pending", expires_in, resend_after}`（用户名成功应答 `{state: "added"}`）；没有 `EmailCodeSender` 时邮箱分支答 503 `unavailable`；发码之前读 `Accounts.Lookup` 与 `SignupVerification.PendingAccountByContact`，地址已被**其他**账户持有或留有未证明预留时答 409 `email-taken`，不发信、不占重发窗口、不花预算，读失败答 503。账户自己已经持有的地址不在此列：它是一次重发到该地址的证明请求，发码照常，兑现后由账户模块就地证明那一行。对同一收件箱的预算形式（局部部分从第一个 `+` 起的后缀在所有域丢弃，`gmail.com` 与 `googlemail.com` 另外去掉局部部分的点并归一为同一个域，[REF-gmail-dots](../../../references.md)）、间隔内的重发答 429 `throttled`（窗口按收件箱持有，跨会话与账户共享，一个收件箱在一次窗口内只发一条码）；用户名的成功写入撤销其余会话；邮箱分支要求身份与登录会话，本身不要求新鲜认证，也不轮换会话，其证明的后果见 `POST B/identifiers/verify`。`verified_via` 由写标识的这一步定，非管理员自陈，见 [ADR-authn-selfservice-identifier](../../../adr/adr-authn-selfservice-identifier.md)。邮箱验证通过后是已验证的邮箱记录，它从登记起就是联系方式、`contact` 为真，`sign_in` 为假；账户此前没有默认联系地址时它成为默认联系地址，是否选作登录方式经 `PUT B/identifiers/:id` 选择（REQ-account-061、REQ-account-062、REQ-account-063、REQ-account-064） |
| `POST B/identifiers/verify` | 提交自助邮箱验证码，体 `{code}`：读会话的待验证记录，比对码；不符计数、用尽五次即废、记录过期靠键的生存期；通过才以 `verified_via = email-code` 调 `AccountAdmin.AddIdentifier` 证明该地址、删记录；证明本身不要求新鲜认证，也不轮换当前会话或撤销其他会话，但若这条邮箱将成为账户的默认联系地址或唯一邮箱地址（因而自动登录方式），证明要求新鲜认证。写标识是权威的占用判定：账户自己已经持有的同值邮箱标识由 `AddIdentifier` 就地提升为已证明（幂等，不新增第二行），所以对注册时留下的未证明地址或管理员预置的地址兑现验证码会成功；地址在发码之后被**别的**账户取得时 `ErrContactTaken` 答 409 `email-taken`，`ErrAccountExists` 只在绕过预留表的重复行上出现，同样答 409；没有待验证记录（或记录不属于本账户）与错码同答 401 `login-failed`。验证不改变登录方式上的选择；账户此前没有默认联系地址时它成为默认联系地址（REQ-account-062、REQ-account-063） |
| `PUT B/identifiers/:id` | 设置本人一条邮箱或手机的登录方式与默认联系地址，体 `{contact, sign_in, default_contact}`，字段可省略：`contact` 对登记的邮箱与手机恒为真，写成假答 400 `bad-request`，写成真不改变任何事实；`sign_in` 为真要求该标识已证明；`default_contact` 只接受已证明的邮箱，为真时把它设为默认联系地址并清除同一账户此前的选择。未证明的 `sign_in`、不属于本人的标识或不是邮箱的默认选择答 400 `bad-request`；`sign_in` 置假会留下没有登录方式的账户时答 409 `last-login-method`，带口令凭据的账户因此失去最后一个被选作登录方式的已验证邮箱时答 409 `login-identifier-required`，两种情况都不生效。改动 `sign_in` 或 `default_contact` 要求新鲜认证并过 CSRF；写 `contact` 不要求新鲜认证，只过 CSRF。 |
| `DELETE B/identifiers/:id` | 删本人持有的标识；标识不属本人与不存在同形，都答 400 `bad-request`；删除用户名答 409 `username-immutable`（`ErrUsernameImmutable`），删除不生效（[ADR-authn-username-permanent](../../../adr/adr-authn-username-permanent.md)）；带口令凭据的账户删除最后一个被选作登录方式的已验证邮箱时答 409 `login-identifier-required`，删除不生效，这条规则先于下一条且答案不变；删除任何标识后，账户若不再持有口令凭据、被选作登录方式的已验证手机号与外部身份中的任何一种，答 409 `last-login-method`，删除不生效，通行密钥不算，未证明或未被选中的手机号也不算，无口令账户的最后一个登录名也按此判；不撤销会话；`authn.identifier.changed` 由 [authn-account](design-authn-account.md) 在删除时发布 |
| `DELETE B/links/:id` | 解绑外部身份；账户有因素时，IdP 声明的多因素不算；解绑后账户至少还剩一种可用登录方式（口令凭据、被选作登录方式的已验证手机号或另一绑定；未证明或未被选中的手机号不算），否则 409 `last-login-method`；成功后撤销其余会话并发 `authn.binding.changed` |

**设置首个口令只在账户面进行。** 没有口令的账户经 `PUT B/password` 设第一个口令，当前口令可空；完成设置页不再存在，首个口令没有其他设置入口。

邮箱与用户名此前只由管理员或 IdP 断言成为标识；自助添加见上表的 `POST B/identifiers` 与 `POST B/identifiers/verify`，登录方式与默认联系地址的选择见 `PUT B/identifiers/:id`，`verified_via` 的归属与验证回路见 [ADR-authn-selfservice-identifier](../../../adr/adr-authn-selfservice-identifier.md)。证明只回答所有权，不决定它是否作登录方式（REQ-account-062）。规则的判据归 [authn-account](design-authn-account.md)。

用户名加口令的自助注册是匿名路由 `POST B/register`，不属于本节的已认证自助路由；建号、登录与邮箱验证的次序见「匿名注册」。`authn.account.password-signup` 与首登开户的 `authn.account.signup` 相互独立；后者仍由短信、社交方法自己的 `opens_account` 表达。

自助路由上的新鲜认证从设置 `authn.mfa.recent-window` 取 `MaxAge`，手机路由从 `authn.sms.code-ttl` 与 `authn.sms.resend-interval` 取记录的期限与应答里的 `resend_after`，邮箱路由从本模块声明的时间与预算键取码的期限、重发间隔与各层发送预算：`authn.login.email-code-ttl`、`authn.login.email-resend-interval`、`authn.login.email-per-address-hourly`、`authn.login.email-per-address-daily`、`authn.login.email-per-account-hourly`、`authn.login.email-per-account-daily`、`authn.login.email-per-source-hourly`、`authn.login.email-per-source-daily` 与 `authn.login.email-global-hourly`。手机路的两个键由 [authn-mfa](design-authn-mfa.md) 与 [authn-sms](design-authn-sms.md) 声明，邮箱回路的这些键由本模块声明；读它们的是使用方——本模块的自助路由处理器、登录流程与准入层——各经设置存储从全局层读同一个键，读取出错或取值越出取值域时取声明的缺省。

`authn.identifier.changed` 由 [authn-account](design-authn-account.md) 在写标识时发布（管理面的 `AddIdentifier`、`RemoveIdentifier` 与自助路由都落到它），所以本模块的自助删标识不自己发它。本模块只发 `authn.binding.changed`（解绑），`authn.password.changed` 由 `Passwords.Set` 发。解绑的绑定行提供方不在 `authn.binding.changed` 载荷的封闭类集合（`google`、`github`、`wechat`、`oidc`、`saml`）内时，为不给事件写一个集合外的值，本模块不发该事件，改为记一行 `Warn`；解绑本身照常完成。`PUT B/password` 在 `Set` 成功后 `Rotate` 失败时留下部分成功：口令已改、当前会话的令牌未轮换，处理器按依赖不可用答 503，用户会话仍有效。

### 手机号验证

手机号验证不是流程，没有流程记录；验证码与号码的对应关系放在 kv 里，键是 `authn:phone:<发起会话标识的 SHA-256 十六进制>`，一个会话同一时刻至多一条，值是 `{account, phone, challenge_id}`，有效期不长于挑战行的有效期（[authn-sms](design-authn-sms.md)）。客户端不提交挑战标识与号码之外的凭据，也无从选择验证哪个号码。

- **`POST B/phone {phone}`**：规范化号码；`SMSCodes.Send(Purpose = enroll, Account = 身份的账户, Deliver = true)`；写记录，覆盖该会话先前的记录；应答 `{expires_in, resend_after}`，不含挑战标识。这条路由不经流程记录，`SendCode.Locale` 取 `smsLocale(ctx)`（`i18n.FromContext` 定下的语言，取不到时为空串），由短信模块解析到载体声明的语言、落不到时用配置的默认语言。号码已被别的账户占用时同样应答并发码，占用情况不外露，冲突在 `verify` 写入标识时以 `ErrAccountExists` 为准。
- **`POST B/phone/verify {code}`**：读取该会话的记录，账户与身份不一致或记录不存在为 401 `flow-invalid`；先 `Throttle.Check(ScopePhone, 号码摘要)`，再 `SMSCodes.Verify(challenge_id, code)`，被拒 `Throttle.Fail`，通过 `Throttle.Succeed`；通过后以记录里的号码调 `AccountAdmin.AddIdentifier(账户, IdentifierPhone, 号码, sms-code)`（账户已有手机标识时即更换，规则归 [authn-account](design-authn-account.md)），删除记录；证明本身不要求新鲜认证，不轮换当前会话，也不撤销其他会话，但账户有已启用的短信因素、且新号码将取代它发码的目标时，证明要求新鲜认证。通过后手机是已验证标识，不自动选作登录方式（REQ-account-014）。
- **kv 不可用**：`POST B/phone` 与 `POST B/phone/verify` 都答 503，不发码、不校验。记录丢失只使验证失败，用户重发。

## 错误与失败语义

登录路径的默认方向是依赖不可用时拒绝：对应路径答 503，不答 200，也不答 401——客户端会把 401 当作凭据无效而丢弃令牌与会话。对外不区分是哪个依赖不可用，应答是 `urn:speed:authn:unavailable`，成因只进日志。全部哨兵与全部路径的失败方向的权威表在 [authn-failure](design-authn-failure.md)，原则见 [ADR-authn-failure](../../../adr/adr-authn-failure.md)；本节只写登录引擎与登录路由涉及的部分。

### 失败方向

| 条件 | 路径 | 行为 | 方向 |
|---|---|---|---|
| kv 不可用 | 开始与继续流程、通行密钥的仪式 | 503 | 拒 |
| kv 不可用 | 已建立的会话、访问令牌、刷新令牌 | 不受影响，它们在 db | — |
| kv 不可用 | 来源地址前置减负（若开启） | 跳过该层，写 Warn 与计数 | 放，上界是自由次数：权威退避在 db |
| kv 计数失败 | `POST B/register` 的来源地址分钟预算 | 跳过该前置减负层，写 Warn 与计数 | 放，与来源地址前置减负同向：这层只是粗粒度减负，权威是 `Signups.Register` |
| kv 计数失败 | `POST B/register` 的全局小时预算 | 503 `unavailable`，不开户 | 拒 |
| db 不可用 | 全部登录方式、因素校验 | 503，不校验 | 拒 |
| db 不可用 | `POST B/register` 的 `Signups.Register` | 503 | 拒 |
| 注册派生容量已满 | `POST B/register` | `ErrBusy`，503 带 `Retry-After: 1` | 拒 |
| 注册开关读取 | `Signups.Register`（权威），`POST B/register` 与 `GET B/methods`（预检查） | 读内存快照；没有声明覆盖时按缺省 `closed`：注册答 403 `signup-closed`，发现面的 `signup.password.enabled` 为假 | 拒 |
| 退避状态读不到 | 登录路径 | 503，不校验：读不到退避状态就不能放行校验 | 拒 |
| 方法超过 `call-budget` | 该次方法调用 | 503；超预算后交回的答案一律作废 | 拒 |
| 方法 panic | 该次方法调用 | `recover`，503，记 Error 与调用栈 | 拒 |
| 方法交回零值 `Step` | 该步 | 500，记 Error | 拒 |
| 外部提供方不可用 | 回跳 | 方法交 `StepUnavailable`，503；callback 类的一步，流程作废 | 拒 |
| `Provisioner` 返回 `ErrProvisionRefused` | 首登开户 | 拒绝开户，401 `login-failed` | 拒 |
| `Provisioner` 返回其他错误 | 首登开户 | 503，不带着半份结果放行 | 拒 |
| 账户登记的因素种类没有实现 | 登录 | 按该因素不可用处理，`login-failed` | 拒 |
| 审计写失败 | `authn.login`（尽力档） | 不挡登录；Error 日志与计数 | 放，审计是尽力档 |
| 事件发布失败 | `authn.login.succeeded` | Warn，不阻塞登录；事件是提示，不是保证 | 放 |
| 设置读取 | 调参类 | 读内存快照；读不出取声明的默认值并记 Error | — |
| 设置读取 | 保留用户名集合 `authn.account.reserved-usernames` | 读内存快照；读不出或没有声明覆盖时取声明缺省的空追加集合，内置清单照常生效，记一行 Error | — |
| 设置读取 | `authn.login.disabled-methods` 的原生值含 `password` | 读取侧过滤掉 `password`，口令方式照常可用，记一行 `Error` | 放：口令方式不在可关闭集合内 |

### 引擎处理的哨兵与应答

调用方用 `errors.Is` 判别，包裹一律 `%w`。引擎把别的功能返回的哨兵映射成应答时，沿用该功能的哨兵表给出的「对外」形状：账户、口令、退避、账户解析与自助类哨兵见 [authn-account](design-authn-account.md)，因素类与 `ErrStepUpRequired` 见 [authn-mfa](design-authn-mfa.md)，短信类见 [authn-sms](design-authn-sms.md)，全表见 [authn-failure](design-authn-failure.md)。本表只列登录引擎自己产生的哨兵：

| 哨兵 | 含义 | 对外 |
|---|---|---|
| `ErrFlowInvalid`、`ErrStepOutOfOrder` | 流程过期、已用、绑定不符、被并发消费、完成复核不过；步序不符 | 401 `flow-invalid` |
| `ErrMethodDisabled` | 方法被 `disabled-methods` 关闭 | 403 `method-disabled` |

启动期的 `ErrNoLoginMethod`（纳入 `authn.login` 而没有任何 `LoginMethod`）与 `ErrMethodConflict`（两个方法同名）终止启动并点名缺的或冲突的双方。

### 幂等与重试

- 引擎不重试：交换落空即按流程失效拒绝，方法超时与不可用不自动重发。
- `DELETE flows/current` 幂等；删除记录前先调用流程所有者的放弃回调，回调失败只记日志、记录照常删除；登录完成靠对流程记录的比较并交换保证只成功一次；回跳至多被处理一次。
- 短信发送结果不确定（超时，不知是否已发）时不盲目重发，用户走重发按钮，计入预算（[authn-sms](design-authn-sms.md)）。
- 邮箱验证码的发送失败按通道的分类处理：能证明消息未离开的失败退还本次占用的预算，结果不确定的失败（`ErrEmailUncertain`，或自建通道给出的任何未分类错误）保留预算，因为消息可能已经发出。无论哪一种，重发窗口都释放：没有待验证记录，用户本来就需要重发才能拿到可用的码。退还逐键用读加比较并交换，不用负增量，已过期的窗口因此不会被重新创建而凭空多出一个名额。
- 停在 `processing` 的记录不重试、不复用，用户重新开始流程。

### 依赖未核实行为的假设

下列断言依赖未经运行核实的行为，按假设处理，各配一条守住它的测试；测试失败即该断言的前提不成立。

| 假设 | 守住它的测试 |
|---|---|
| 每个 kv 实现对 `CompareAndSwap` 都满足「只在当前值等于旧值时写入」，包括并发写入者抢先时的重新判定 | 对每个 kv 实现并发提交同一次回跳，断言只有一个越过 `processing` |
| 通行密钥的仪式数据序列化后放得进 `FlowState.Private` 的上限 | 按允许的凭据清单增长测量序列化大小 |
| `SameSite=None` 的 `__Host-` cookie 随 IdP 页面发起的跨站 POST 送达回跳端点，且跨源检查的旁路只覆盖回跳路由；gin 的 `:provider` 写成 `ServeMux` 的 `{provider}` 后模式能命中 | 对回跳路由的跨站 POST 用例，同时断言其他流程路由的跨源请求仍被拒 |
| Google 的授权端点接受带 `code_challenge`（S256）的请求 | 用测试客户端对授权端点发带 `code_challenge` 的请求 |
| 微信：成功路径上 `POST` 是否被接受；`snsapi_login` 与 `snsapi_userinfo` 下 `unionid` 的下发条件；网页授权是否只能在微信内置浏览器发起 | 需要真实应用凭据，自动化只对模拟服务端测，真实行为在联调时核实 |
| 能读出 `tenant.resolve.endpoints` 的解码形状以核对挂载端不被解析层所守 | 解不开则只记 `Warn` 而不失败；用一个形状不符的配置读取面断言启动不终止 |

## 并发与事务边界

- **流程记录是唯一的并发裁判。** 每次迁移是 `CompareAndSwap(旧, 新)`，同一条流程上的并发请求至多一个胜出；败者按 `flow-invalid` 拒绝，不重试，也不做第二次读。
- **回跳的并发消费在方法之外裁决。** 两个并发回跳读到同一份旧记录时，只有一个能交换成 `processing`；败者在 `Answer` 被调用之前就被挡住，所以授权码至多换一次。
- **终态动作先交换后执行。** `ready` 交换成 `done` 的败者拒绝，胜者执行动作；动作失败时流程已消耗。这使「登录完成只成功一次」只依赖 kv 的原子性。
- **重置记录的并发裁判。** 重置的每次迁移同样是 `CompareAndSwap`：验证码通过时 `sent` 换成 `verified`，两个并发提交只有一个胜出；写口令时 `verified` 换成 `processing`，胜者才写审计与口令，并发败者按 `flow-invalid` 拒绝。策略拒绝或审计、依赖失败把 `processing` 换回 `verified`，用户可用同一个码重试；口令写入成功后删除记录。这使一次重置至多写一次口令、至多一条成功审计行。
- **重置绑定独立于登录绑定。** 重置用 `__Host-authn-reset` 与自己的 KV 记录，不复用登录流程的 cookie；同一浏览器里开始一次登录不会覆盖进行中的重置，反之亦然。
- **valid_after 与撤销同批。** 写口令、推进 `valid_after` 在账户模块的同一事务里完成；随后调用 `Sessions.RevokeAccount` 结束该账户的 browser 与 token 会话。其他副本在会话状态缓存过期后看到新的 `valid_after`，撤销语句清本副本缓存并发布 `authn.session.revoked`。
- **注册的一次建号在账户模块的事务里完成。** 注册处理器只做路由、请求校验与哨兵映射；账户、用户名标识、口令凭据与未证明预留由 `Signups.Register` 在同一事务写入，预留的插入是地址的唯一仲裁点。注册不建会话、不发邮件，因而没有跨模块的写。
- **跨模块的写不在一个事务里。** 账户解析、开户与绑定在 `authn.account` 自己的事务里完成；会话、因素、审计、事件各在自己的模块里写。引擎的一次完成因而是一串各自原子的调用：失败时可能留下已建而未交付的会话，它不可用于认证之外的任何事，随期限到期，用户重新登录。
- **自动并入不再发生。** 账户解析不按提供方声明的地址接管现有账户；显式关联在自己的流程里完成，成功之后按 A6 撤销发起会话之外的会话，不建新会话。
- **口令派生的并发约束归 [authn-account](design-authn-account.md)。** 登录路径只把 `ErrBusy` 映射成 503。
- **kv 不保证次序与持久。** 流程记录的丢失只使流程失败；`CompareAndSwap` 之外，引擎不依赖 kv 的任何其他一致性。

## 扩展点

- **第三方 `LoginMethod`。** 实现接缝并满足准入条件，登记为一个模块、非排他地交付 `LoginMethod`，即被引擎经 `ResolveAll` 取用；新增方法不新增路由，路由由 `flows` 与 `callback` 承载。名字全局唯一，重名即启动失败。社交类回跳方法的路径是 `callback/<方法名>`，企业 SSO 的路径是 `callback/<提供方标识>`。
- **`Provisioner`。** 根包的可选接口，非排他。联合登录或短信首登开户之前被调用，宿主可改写拟创建账户的显示名与属性，或返回 `ErrProvisionRefused` 拒绝开户；返回的 `CreateAccount` 只可改显示名与属性。
- **紧急开关与交付方式。** 设置 `authn.login.disabled-methods` 在运行期关闭某个方法（例如遭遇短信轰炸时关 `sms`），口令方式不在可关闭集合内，`delivery-modes` 限定允许的交付方式；`disabled-methods` 与 `delivery-modes` 是设置，不是配置。改到更宽松的取值时写一行 Warn 并经审计接缝记一条尽力档记录（[authn-config](design-authn-config.md)）。

## 实现状态

`pkg/authn` 目前含网关、口令表后端、身份与判决的类型、值类型、登录方法与因素的根包接缝、`Step`、路由共用部件 `internal/mount`、登录引擎 `authn.login`、口令方法 `authn.login.password`、短信方法 `authn.login.sms`，以及社交与微信登录方式 `authn.login.google`、`authn.login.github`、`authn.login.wechat-web`、`authn.login.wechat-mp`（微信两方式共用 `login/internal/wechat` 薄客户端）。引擎把两块内容放在内部包里：`login/internal/resolve` 把 `Proof` 解析成账户（开户、绑定与账户解析，交回一个 `Result`），`login/internal/selfservice` 挂账户凭据自助路由（改口令、手机号、加标识、设置登录方式与默认联系地址、删标识、解绑）；两个包都只被引擎构造与调用，别的模块够不到。

| 能力 | 状态 | 代码 |
|---|---|---|
| 根包登录接缝：`LoginMethod`、`Start`、`FlowState`、`StepInput`、`Step` 及构造器、`Proof`、`Field` | 已实现 | `pkg/authn/login.go` `LoginMethod`、`StepPrompt`；`pkg/authn/ids.go` `Proof`、`Field`、`Start`、`FlowState`、`StepInput`；`pkg/authn/internal/step/step.go` `Step`、`Prompt`、`KindOf`、`ProofOf`；`pkg/authn/internal/model/flow.go` `Start`、`FlowState`、`StepInput`；`pkg/authn/internal/model/login.go` `Proof`、`Field` |
| 登录引擎：流程记录、状态机、迁移、比较并交换 | 部分实现 | `pkg/authn/login/login.go` `Module`；`pkg/authn/login/engine.go`、`pkg/authn/login/flow.go` |
| 流程令牌的承载与绑定（cookie 与请求头载体） | 已实现 | `pkg/authn/login/flow.go`、`pkg/authn/login/callback.go` |
| 账户解析与策略判定的引擎侧编排 | 部分实现 | `pkg/authn/login/engine.go`、`pkg/authn/login/identity.go`；`pkg/authn/login/internal/resolve` `Resolver`、`Result`、`Outcome` |
| 首登开户建号前的一步（`signup` 状态、流程记录的 `pending`、用户名与口令答复及建号） | 已实现 | `pkg/authn/login/engine.go`、`pkg/authn/login/flow.go`、`pkg/authn/login/answer.go`、`pkg/authn/login/internal/resolve` |
| 终态动作（`login`、`link`、`step-up`、`enroll`）与完成复核 | 已实现 | `pkg/authn/login/action.go` |
| 在已登录身份上开始流程（`POST B/flows/authenticated`） | 已实现 | `pkg/authn/login/start.go` |
| 短信发送的每流程上限（`SendsLeft`、`Step.WithSends`） | 已实现 | `pkg/authn/login/engine.go`；`pkg/authn/internal/step/step.go` `(Step).WithSends`、`SendsOf`；`pkg/authn/internal/model/flow.go` `Start`、`FlowState` |
| IdP 发起的 SAML 的流程创建 | 已实现 | `pkg/authn/login/callback.go` `callbackIDPInitiated`；`pkg/authn/login/flow.go` `flowProvider`（社交方法的记录取方法名、企业 SSO 的记录取 `input.provider`；`FlowState.Provider` 同源交给方法） |
| 路由、请求与应答的形状、问题码 | 已实现 | `pkg/authn/login/routes.go`、`pkg/authn/login/respond.go`；`pkg/authn/login/answer.go`；`pkg/authn/internal/mount/problem.go` |
| 账户凭据自助路由与手机号验证 | 已实现 | `pkg/authn/login/internal/selfservice` `Service`、`Deps`；`pkg/authn/login/routes.go` |
| 登录标识的自助添加（用户名一步写入、邮箱经验证码证明） | 部分实现 | `pkg/authn/login/internal/selfservice/addidentifier.go` `(routes).addIdentifier`、`(routes).verifyIdentifier`；`pkg/authn/email.go` `EmailCodeSender`；`pkg/authn/login/login.go` `(*product).emailSender` |
| 标识的登录方式与默认联系地址的自助设置（`PUT B/identifiers/:id`） | 已实现 | `pkg/authn/login/internal/selfservice/identifiers.go`；`pkg/authn/account/admin.go` `AccountAdmin.SetIdentifierFlags`、`AccountAdmin.SetDefaultContact` |
| 邮箱验证码回路的各层预算、收件箱别名归一、失败退还与地址占用判定 | 已实现 | `pkg/authn/login/internal/selfservice/addidentifier.go` `(*routes).addEmail`、`reserveEmailBudgets`、`refundEmailBudgets`、`refundEmailBudget`、`emailCertifiesNotSent`、`untilNextEmailBucket`、`emailTakenByAnother`；`pkg/authn/internal/budgetalias` `Canonical`；`pkg/authn/emailcode` `(*product).SendCode`、`(*product).CertifiesNotSent`；`pkg/authn/email.go` `ErrEmailUncertain`；`(*routes).verifyIdentifier` 的 `email-taken` 映射；设置项声明于 `pkg/authn/login/config.go`，读取于 `pkg/authn/login/internal/selfservice/settings.go` `(*counts).get`；`pkg/authn/login/internal/selfservice/emailbudget_test.go`、`emailabuse_test.go` |
| 失败退避的流程侧、枚举防护与来源地址前置减负 | 已实现 | `pkg/authn/login/engine.go` `(*routes).recordSends`、`pkg/authn/login/start.go` `(*routes).admitSends`、`(*routes).chargeSends`、`pkg/authn/login/answer.go` `(*routes).answer` |
| 口令方法 `authn.login.password` | 已实现 | `pkg/authn/login/password/password.go` `Module`、`(method).Begin`、`(method).Answer`、`(method).Name` |
| 短信方法 `authn.login.sms` | 已实现 | `pkg/authn/login/sms/sms.go` `Module` |
| 请求来源地址传到短信引擎：方法取 `Start.Client` 与 `StepInput.Client`，因素取 `FlowState.Source`（只由 `challengeFactor` 填入），自助手机号路由取请求的 `Meta.ClientIP` | 已实现 | `pkg/authn/login/sms/sms.go` `(*method).Begin`、`Answer`、`phone`；`pkg/authn/login/answer.go` `challengeFactor`；`pkg/authn/login/internal/selfservice/call.go` `(*routes).call`、`phone.go` `(*routes).startPhone` |
| Google 方法 `authn.login.google` | 已实现 | `pkg/authn/login/google/google.go` `Module`；`pkg/authn/login/google/method.go` `Begin`、`Answer`、`exchange`；`pkg/authn/login/google/state.go` |
| GitHub 方法 `authn.login.github` | 已实现 | `pkg/authn/login/github/github.go` `Module`、`Begin`、`Answer`；`pkg/authn/login/github/api.go` `fetchUser`、`fetchPrimaryVerifiedEmail` |
| 微信方法 `authn.login.wechat-web`、`authn.login.wechat-mp` 与共用客户端 | 已实现 | `pkg/authn/login/wechatweb/wechatweb.go` `Module`、`Begin`、`Answer`；`pkg/authn/login/wechatmp/wechatmp.go` `Module`、`Begin`、`Answer`；`pkg/authn/login/internal/wechat/client.go` `Client`、`Exchange`、`AuthorizeURL`；`pkg/authn/login/internal/wechat/identity.go`、`state.go` |
| operation 声明与对外契约片段生成：`openapi/login.json` | 已实现 | `pkg/authn/login/operations.go`；`pkg/authn/login/openapi_generated_test.go`；片段 `pkg/authn/login/openapi/login.json` |
| 条件特性 `authn.phone`、`authn.identifiers.email` 的声明与报告 | 已实现 | `pkg/authn/login/login.go` `Module`、`takeBoard`、`postFeature`；`pkg/authn/login/routes.go` `mountRoutes`；`pkg/authn/login/internal/selfservice/selfservice.go` `FeaturePhone`、`FeatureEmail`、`Mount`；`pkg/authn/login/feature_test.go` `TestThePhoneSurfaceAndItsFeatureShareOneJudgement` |
| 匿名注册路由 `POST B/register` 与 `signup.password.enabled` 发现面 | 部分实现 | `pkg/authn/login/register.go` `(*routes).register`、`signupSourceAllow`、`signupGlobalAllow`、`failRegister`；`pkg/authn/login/operations.go` `registerOperation`、`AuthnLoginRegistration`、`AuthnLoginSignupPassword`；`pkg/authn/login/respond.go` `(*routes).methods`、`descriptorView`；`pkg/authn/login/config.go` `passwordSignupPath`、`signupIPLimitPath`、`signupGlobalHourlyPath`、`(*reader).passwordSignup`、`signupIPLimit`、`signupGlobalHourly`；`pkg/authn/login/login.go` `Module`（`authn.Signups`）、`diagnoseSignup` |
| 未验证账户的 `purpose=link` 门控（`SignupStateOf` 为 `SignupPending` 时 403 `contact-required`，经 `SignupVerification.SignupStateOf` 读取；没有邮箱的 `SignupNone` 不受限） | 已实现 | `pkg/authn/login/start.go` `(*routes).startAuthenticated` |
| 证明账户自己已持有的未证明邮箱标识（`POST B/identifiers` 重发到本账户地址、`POST B/identifiers/verify` 兑现后由 `AddIdentifier` 就地提升） | 已实现 | `pkg/authn/login/internal/selfservice/addidentifier.go` `(*routes).addEmail`、`(*routes).verifyIdentifier`；`pkg/authn/account/admin.go` `(*product).AddIdentifier` |
| 匿名口令重置的路由与状态机（`POST B/password-reset`、`GET\|DELETE B/password-reset/current`、`POST B/password-reset/verify`、`POST B/password-reset/complete`） | 已实现 | `pkg/authn/login/reset.go` `(*routes).passwordResetStart`、`passwordResetCurrent`、`passwordResetDelete`、`passwordResetVerify`、`passwordResetComplete`、`resetEligible`、`resetRecordEligible`、`resetOpen`、`resetChannelReady`、`resetChannelAvailable`；`pkg/authn/login/routes.go` `mountRoutes`；`pkg/authn/login/login.go` `(*product).emailReset`（字段）、`(*product).diagnoseReset`；`pkg/authn/login/reset_test.go` |
| 发现面的 `password_reset` 能力位 | 已实现 | `pkg/authn/login/respond.go` `(*routes).methods`；`pkg/authn/login/reset.go` `(*routes).passwordResetCapability`；`pkg/authn/login/operations.go` `passwordResetView`、`AuthnLoginPasswordReset` |
| 删除用户名标识答 409 `username-immutable` | 已实现 | `pkg/authn/login/internal/selfservice/identifiers.go` `(*routes).removeIdentifier`；`pkg/authn/login/internal/selfservice/call.go` `(*routes).fail`（`authn.ErrUsernameImmutable` 映射到 `mount.CodeUsernameImmutable`）；`pkg/authn/account/admin.go` `(*product).RemoveIdentifier` |
| `disabled-methods` 对 `password` 不生效：发现面照常列出，开始流程照常接受 | 已实现 | `pkg/authn/login/config.go` `disabledMethodsTextInDomain`、`withoutPassword`、`(*reader).disabledMethods`；`pkg/authn/login/respond.go` `(*routes).methods`；`pkg/authn/login/start.go` `(*routes).startFlow` |
