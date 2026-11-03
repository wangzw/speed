# 架构决策记录

本目录记录 speed 的架构决策：每条记录说明当时面临的约束、考虑过的选项、采纳的结论以及为此付出的代价。

记录只陈述当前生效的决策，修订原地进行：决策变了就改写记录本身，不另立新记录，也不保留旧版本。修订不留下痕迹——没有日期、没有变更说明；被替换的旧决策只作为「考虑过的选项」里的一个落选的选项存在。

引用形式为 `ADR-<slug>`，slug 命名决策本身。同一主题可以有多条决策，每条有能区分的 slug，并在下表中归到同一个主题之下。

## 模块机制

| 记录 |
|---|
| [core 驱动生命周期，但不编码应用形态](adr-core-scope.md) |
| [模块以 init 隐式注册到进程级注册表](adr-module-registration.md) |
| [模块之间按接口类型取用，依赖不满足即启动失败](adr-dependency-resolution.md) |
| [功能以类型化空指针指代](adr-capability-token.md) |
| [模块以统一的资源声明对外提供元数据](adr-module-resources.md) |
| [接口包暴露它所封装的第三方库](adr-module-packaging.md) |
| [资源声明的取材域由消费点裁定，判据是声明所述之物的生命周期](adr-module-resource-scope.md) |
| [两个实现子包共用一份第三方客户端构造与测试夹具](adr-shared-backend-unit.md) |
| [一个 Go module 承载全部功能，功能按包划分](adr-module-layout.md) |
| [预置实例是已登记模块的实例，归属按实例认出或由模块名指定](adr-supplied-instance.md) |
| [宿主辅助函数只抽第二个宿主真实复用的那一项进 pkg](adr-shared-helper-scope.md) |

## 生命周期

| 记录 |
|---|
| [生命周期分为八个阶段](adr-lifecycle-stages.md) |
| [配置在装配开始之前由宿主加载](adr-assembly-bootstrap.md) |
| [命令树由宿主静态定义，模块按层纳入](adr-command-tree.md) |
| [驱动深度由命令决定，释放责任不随之转移](adr-lifecycle-drive.md) |
| [纳入分为运行级与静态级，人机界面跟运行级走](adr-command-tree-inclusion.md) |
| [启用分两期，运行期的取舍归模块自己](adr-module-enablement.md) |
| [注册表有两个驱动入口，`Run` 是 `Drive` 的一种取值](adr-drive-entry.md) |
| [停机时入口先于其余模块停止](adr-lifecycle-shutdown-order.md) |

## 配置

| 记录 |
|---|
| [配置承载启动期的值，运行期参数归设置](adr-config-scope.md) |
| [配置是启动时的快照](adr-config-lifecycle.md) |
| [主配置源唯一，环境变量与命令行作为覆盖层](adr-config-source.md) |
| [命令行与配置的解析委托给成熟库](adr-config-parsing.md) |
| [默认值层可以取第二份来源：宿主编译进二进制的文档](adr-config-defaults.md) |

## 日志

| 记录 |
|---|
| [日志以标准库 slog 为调用接口](adr-log-api.md) |
| [上下文字段由产生方绑定到 logger，经 context 传递](adr-log-context.md) |
| [脱敏机制内置于日志根包且不可关闭，规则由登记提供](adr-log-redaction.md) |
| [持有秘密配置值的模块在构造期登记该值为日志的字面量匹配器，为此可以取用一次可选的日志功能](adr-log-secret-redaction.md) |

## 数据库

| 记录 |
|---|
| [数据库模块承担自足的横切能力，需要上层概念的经插件扩展点接入](adr-db-plugin-scope.md) |
| [模块机制只交付一个库，额外的连接由调用方经接口自建](adr-db-multiplicity.md) |
| [迁移以资源声明交付，按依赖序统一应用](adr-db-migration.md) |
| [约束拒绝以 GORM 的两个哨兵错误识别，驱动的原始错误留在错误链上](adr-db-errors.md) |
| [模块自己发出的语句经本模块在有界的等待内取连接，SQLite 的事务在开启时就等写锁](adr-db-waits.md) |
| [加密列按语句在所在引擎上实际读写的那一列认定](adr-db-encryption.md) |
| [数据库模块在 `Migrate` 拒收不出自它的 `New` 的预置实例](adr-db-supplied-instance-refusal.md) |
| [本模块的句柄装上自己的 GORM logger，经日志模块写出，内容里不带语句的值](adr-db-logging.md) |

## 入口

| 记录 |
|---|
| [入口模块的登记接口锚定 gin](adr-http-surface.md) |
| [路由经登记接口绑定，Spec 只承载元数据](adr-http-routing.md) |
| [一个入口模块承载多个具名端点](adr-http-endpoints.md) |
| [把监听与端点拆成两层](adr-http-listener-endpoint-split.md) |
| [监听按端点在配置里认领的前缀为请求选端，已登记的路由只用于核对归属](adr-http-dispatch.md) |
| [全部 HTTP API 路径收进 `/api/v1` 命名空间](adr-http-api-prefix.md) |
| [OpenAPI 片段以相对路径声明，端点前缀与版本段由 Spec 承载](adr-http-spec-path.md) |
| [operation 声明同时生成 OpenAPI 片段并绑定 handler](adr-http-openapi-generation.md) |
| [错误应答按公共集加覆盖声明，Fault 可返回也可 panic](adr-http-failure-responses.md) |
| [operation 声明覆盖完整的 HTTP 输入与应答形态](adr-http-request-response-kinds.md) |
| [管理面路径以作用域段开头，普通路由不带段](adr-http-api-path-scopes.md) |
| [契约文档承载分组、鉴权与顶层元数据，访问级别由 operation 声明](adr-http-contract-metadata.md) |
| [Stop 关闭监听器并等在途请求排空结束](adr-http-shutdown.md) |
| [请求路径的 panic 兜底由入口模块内建，每端唯一](adr-http-recovery.md) |
| [入口模块定义默认的错误响应结构，并经可选接口接受定制](adr-http-errors.md) |
| [入口模块的登记接口列出已声明的端名，`*` 保留为端名](adr-http-enumeration.md) |
| [跨源请求层桥接上游库，作为独立子包登记在防护阶段的最外侧](adr-http-cors.md) |
| [中间件的定位由固定的一组阶段给出，阶段内以约束与整数 Order 取舍](adr-http-stages.md) |
| [请求体上限是入口模块内建的一层，落在防护阶段之内](adr-http-body-limit.md) |
| [每请求一条访问记录由入口模块内建，位于全部模块层之外](adr-http-access-log.md) |
| [ops 读取 http.Spec：取可用集、不做减法、冲突即启动失败](adr-ops-spec-reader.md) |
| [入口模块交付请求体绑定入口，宿主的 handler 错误体统一到它的 RFC 9457 结构](adr-http-bind-and-error-shape.md) |
| [可信代理设置以模块级默认下发，端点可覆盖](adr-http-proxy-trust-defaults.md) |

## 特性

| 记录 |
|---|
| [特性文档发布本进程服务的特性名，名字由服务该接口的模块声明，只对已认证调用者可读](adr-feature-document.md) |
| [存在性进特性文档，取值与结构归拥有它的模块，条件特性由声明模块在 Init 中于决定该接口的判定处报告](adr-feature-ownership.md) |
| [特性名由二进制写出的 `contracts/features.json` 生成为 api-sdk 里的类型，前后端各有新鲜度测试](adr-feature-vocabulary.md) |

## 前端

| 记录 |
|---|
| [静态托管与 SPA 回退独立成一个模块](adr-web-static-hosting.md) |
| [前端一次构建产出两件制品](adr-web-two-build-artifacts.md) |
| [API origin 在运行期注入，不在构建期烘焙](adr-web-runtime-config.md) |
| [路由与导航独立成包，壳不做导航](adr-web-route-seam.md) |
| [租户作用域的 query key 由框架提供工厂与逐出辅助函数](adr-web-tenant-query-key.md) |
| [现有前端原地复用，旧树不保留](adr-web-reuse.md) |
| [前端登录渠道的来源是后端发现端点，前端不写死集合](adr-web-auth-channel-discovery.md) |
| [账户自助写归前端的一组组件](adr-web-account-ui-selfservice.md) |
| [特性态由 auth-core 随会话提交持有，读取失败是独立状态，只有宿主读它](adr-web-feature-state.md) |
| [route-kit 按宿主给出的特性在场判定收窄注册接口，特性缺席的页面与未登记路径同走 `fallback`](adr-web-route-kit.md) |
| [管理后台列表页的行详情与创建改由侧边面板承载，面板是页内状态而非路由](adr-web-side-panel.md) |
| [写入的结果由每个反馈作用域的一个槽承载，同页至多一条结果、以最后落定的为准，成功确认为默认](adr-web-write-feedback.md) |
| [冒充期间由应用壳在应用栏之下显示可隐藏的横幅、外壳常驻 chrome 区里的身份指示并提供结束入口，身份变更按会话边界处理依赖身份的状态](adr-web-impersonation-shell.md) |
| [宿主可为平台命名空间补整套语言，注册期全覆盖规则不变](adr-web-host-language-sets.md) |
| [认证复用扩展点的归属](adr-web-auth-seams.md) |
| [界面表面由语义角色 token、主题映射与 SectionCard 原语共同承担](adr-web-surface-system.md) |
| [浅色模式的 warning、info 与 success 取 700 调，使 main 同时满足文字与填充两种读法](adr-web-light-contrast.md) |
| [应用外壳的形态在装配期选定并由一组内置变体表达，不交给宿主自由拼装](adr-web-shell-layout-variant.md) |
| [主题预设是纯数据的 token 覆写，效果由 ui-kit 映射，预设不承载组件覆盖](adr-web-theme-presets.md) |
| [颜色模式偏好跟随账户，系统偏好由主题 provider 解析](adr-web-color-mode-preference.md) |
| [语言选项按基础语言命名，同基础语言多变体时保留地区名](adr-web-language-display-name.md) |
| [管理面分两档：平台管理面在 /admin，工作区管理面在 /workspace/admin 的租户外壳内](adr-tenant-admin-surface.md) |

## 认证

| 记录 |
|---|
| [认证模块的范围：交付身份系统，授权另立](adr-authn-scope.md) |
| [包与模块的打包：根包只放接口，一个功能一个叶子包一个模块](adr-authn-packaging.md) |
| [Requires 只写排他功能，并立功能一律在 Init 里 ResolveAll](adr-authn-wiring.md) |
| [身份以账户为中心：Account、Actor、责任键 Key、类型落点](adr-authn-identity.md) |
| [判决带事实、不可字面构造，网关校验后盖章](adr-authn-verdict.md) |
| [路由登记按已匹配的路由逐字比对，认证层不自持匹配器](adr-authn-exemption.md) |
| [不命中匿名登记的未认证请求，网关的应答与匹配到的路由无关](adr-authn-unmatched-request.md) |
| [校验后端按凭据载体整条认领，账户命名空间声明取代主体归属](adr-authn-credential-dispatch.md) |
| [状态按丢失的方向放置：丢失会放宽校验的进 db，丢失只会让流程失败的进 kv](adr-authn-state-placement.md) |
| [失败语义：依赖不可用时拒绝，有界放宽逐项列出](adr-authn-failure.md) |
| [装配层面的性质写成非保证，不为它们改动别的模块](adr-authn-guarantees.md) |
| [判定接口 Decider：管理面与冒充缺席即全拒，自助范围不过判定接口](adr-authn-decision-seam.md) |
| [配置与设置的分界、放宽开关、秘密的存放](adr-authn-settings-and-secrets.md) |
| [事件、审计、日志与指标的契约：事件是提示，审计分强制与尽力，日志与指标不带高基数值](adr-authn-events-and-audit.md) |
| [用户档案的自助功能与管理面分立](adr-authn-selfservice-profile.md) |
| [登录标识的自助添加：用户名直接写，邮箱经验证码证明所有权](adr-authn-selfservice-identifier.md) |
| [自助注册即建号，账户的邮箱只有已验证与未验证两种状态](adr-authn-signup.md) |
| [每个用户账户在建号之前取得用户名，用户名须 URL 安全并排除保留用户名](adr-authn-username-required.md) |
| [用户名一经写入不可删除、不可更换](adr-authn-username-permanent.md) |
| [口令账户必须保留一个登录名，最后一个登录名保护只约束邮箱](adr-authn-login-identifier.md) |
| [口令登录方式不可被关闭](adr-authn-password-method-required.md) |
| [邮箱地址的占用由联系方式预留的唯一约束原子仲裁，未验证预占被接受](adr-authn-contact-claim.md) |
| [回跳失败时把用户代理落回产品，机器码经 URL 片段携带](adr-authn-callback-failure-landing.md) |
| [人机验证是缺省开启为工作量证明的发送前闸，机制由部署选定](adr-authn-human-verification.md) |
| [成员关系删除的清理是同步接口，事件只作崩溃窗口补偿](adr-authn-membership-cleanup.md) |

## 邀请

| 记录 |
|---|
| [邀请以自持状态与不透明令牌成立](adr-authn-invitation.md) |
| [只做邀请，不做组织树](adr-org-invitation.md) |

## 账户与凭据

| 记录 |
|---|
| [账户模型：全局账户、已验证标识、成员关系表、墓碑删除、无外键、UTC](adr-authn-accounts.md) |
| [账户停用、删除与密钥撤销的清理：本模块事务加钩子加事件补偿，`valid_after` 兜底](adr-authn-account-lifecycle.md) |
| [账户恢复经 `AccountEnabled` 钩子观察，与停用同一条提交后同步调用](adr-authn-account-restoration.md) |
| [外部身份绑定、显式关联、首登开户与企业预置账户的规则](adr-authn-external-binding.md) |
| [账户登记的邮箱与手机恒为联系方式，登录方式与默认联系地址由持有人选择，账户只有一条邮箱时它一经验证即成为登录方式](adr-authn-contact-and-login-ways.md) |
| [口令条目用 PHC 字符串：Argon2id 缺省，PBKDF2-SHA256 可用，旧写法只读](adr-authn-password-hash.md) |
| [认证前的失败退避与枚举防护：db 的 Throttle，账户级静默，口令表进程内](adr-authn-throttling.md) |

## 登录与多因素

| 记录 |
|---|
| [登录引擎：方法只交出事实，流程记录放 kv，比较并交换由引擎做](adr-authn-login-flow.md) |
| [登录方式的发现：渠道来源是后端发现端点，方法自带描述符](adr-authn-login-method-discovery.md) |
| [短信验证码：挑战状态机归 SMSCodes，发送归顶层能力 sms.Sender，预算归共享台账](adr-authn-sms.md) |
| [微信登录自写薄客户端，网站应用与公众号拆成两个模块](adr-authn-wechat.md) |
| [社交与企业 OIDC 的校验桥：go-oidc 加 x/oauth2，state、PKCE、nonce 自做](adr-authn-oauth-flow.md) |
| [社交登录提供方的端点与出站信任作为部署配置，缺省不变](adr-authn-social-endpoints.md) |
| [多因素：因素是并立的接口，编排与保障等级只在 authn.mfa 一处合成](adr-authn-mfa.md) |
| [没有恢复码时的找回途径与接受的代价](adr-authn-mfa-recovery.md) |

## 短信

| 记录 |
|---|
| [短信能力由发送接口、模板注册表与管理面组成，载体不感知模板](adr-sms-capability.md) |
| [逐模板的厂商审核是载体的能力，不是能力的普遍不变量](adr-sms-template-review-is-carrier-capability.md) |
| [发送身份的登记在带外完成，一个进程只挂一个载体且不做跨厂商路由](adr-sms-registration-out-of-scope.md) |
| [Twilio 载体绑定官方 SDK，不自写 HTTP 客户端](adr-sms-twilio-sdk.md) |

## 邮件

| 记录 |
|---|
| [邮件发送能力为顶层能力模块，投递载体为 SMTP，内容不模板化](adr-mail-capability.md) |

## 发送载体

| 记录 |
|---|
| [发送能力的载体由运行期选择项决定，日志载体是保底](adr-carrier-selection.md) |

## 会话、令牌与冒充

| 记录 |
|---|
| [登录会话：db 权威存放，scs 只作令牌与 cookie 的桥，cookie 缺省仅主机会话期，跨源由网关检查](adr-authn-session.md) |
| [令牌：JWT 访问令牌与不透明刷新令牌，签名密钥放文件，服务账户走 client_credentials](adr-authn-token.md) |
| [冒充：有期限的记录、能力取自被冒充者、结构性限制、租户范围的联合执行、审计先于动作](adr-authn-impersonation.md) |

## 企业 SSO

| 记录 |
|---|
| [企业 SSO：IdP 作表里的数据，SAML 用 gosaml2 加补充校验，企业 OIDC 共用桥](adr-authn-sso.md) |
| [企业 SSO 的解析器与出站信任根可配置](adr-authn-sso-trust-root.md) |

## 租户

| 记录 |
|---|
| [租户解析、租户名册与数据隔离同属一个功能目录，设置与授权另立](adr-tenant-scope.md) |
| [根包不登记模块，解析与名册各登记一个模块并各自排他交付](adr-tenant-packaging.md) |
| [租户标识由请求侧解析得到，取法由配置选定，不从认证身份派生](adr-tenant-resolution.md) |
| [租户解析以认证为锚点排在认证之内，未认证的请求不进入解析层](adr-tenant-ordering.md) |
| [租户列存不可变标识，请求侧出现的名字是名册上的可改别名](adr-tenant-identifier.md) |
| [租户名册由本模块自带表承载，状态是一个封闭集合，停用不删数据](adr-tenant-directory.md) |
| [名册自己的语句经数据库模块的 Connection 取交付连接，取不到、表不在与其余失败分开作答](adr-tenant-directory-connection.md) |
| [已删除的租户是名册上的终态行，标识与别名不再复用](adr-tenant-deletion.md) |
| [隔离落在共享表加租户列，过滤经 GORM 插件，模型按约定声明自己受约束](adr-tenant-isolation.md) |
| [条件里以表名限定各列的整键元组按上下文填租户那一半，多对多关联的连接表按所连模型写进它的租户列判定](adr-tenant-isolation-joins.md) |
| [上下文无租户时受约束模型的语句拒绝执行，跨租户必须在调用点字面写出](adr-tenant-scoping.md) |
| [不经请求侧的路径经名册进入单个租户，标识的写入仍只在本功能目录](adr-tenant-scoping-jobs.md) |
| [停用期间仍属于一个租户的工作，经名册的另一个进入方法进入那个租户](adr-tenant-suspension-access.md) |
| [装配层面的性质写成非保证，租户隔离不由框架承诺](adr-tenant-guarantees.md) |
| [租户准入层放在 tenant 目录：成员关系、租户级强度策略与冒充范围](adr-tenant-admission.md) |
| [租户名册的管理面由框架提供一个平台域的模块](adr-admin-tenant.md) |
| [租户的停用来源记在名册行上，账户联动只撤销它自己造成的那一次](adr-tenant-suspension.md) |
| [管理员身份由租户成员关系承载，system 是名册中的真实租户](adr-tenant-administration.md) |
| [调用方本人的租户读取由 tenant 目录新增的只挂路由的模块承担，挂在账户端的普通接口](adr-tenant-self.md) |

## 授权

| 记录 |
|---|
| [判定引擎桥接 Casbin v3，模型文本固定在代码里](adr-authz-engine.md) |
| [权限模型：角色到权限，权限按（资源，动作），租户即策略域](adr-authz-model.md) |
| [策略存储：自写薄适配器加真实租户列，取代 Casbin 的默认存储](adr-authz-store.md) |
| [承接 Decider 接口：Decision 结构不变，资源属性在模块内合成](adr-authz-decision-seam.md) |
| [强制点：端点级中间件加路由声明，未声明默认拒绝](adr-authz-enforcement.md) |
| [打包：根包只放接口与常量，策略、强制点、管理面各为一个叶子模块](adr-authz-packaging.md) |
| [策略常驻进程内不可变快照，事件失效、周期重载兜底](adr-authz-cache.md) |
| [反转旧树「不用 Casbin」的结论：被否的是默认存储，不是引擎](adr-authz-reversal.md) |
| [种入接口：独立于 Manager 的 Seeder 与 Reconciler 能力](adr-authz-seeding.md) |
| [租户域的管理端点：治理权限元数据、租户管理员允许集与防提权](adr-authz-tenant-management.md) |
| [主体持有的权限由策略模块读出，并经一条自身路由回答](adr-authz-subject-permissions.md) |

## 键值

| 记录 |
|---|
| [键值模块交付原语，不交付缓存语义](adr-kv-scope.md) |
| [部署前提由各实现自己声明，不满足即启动失败](adr-kv-implementations.md) |

## 事件

| 记录 |
|---|
| [事件载荷结构化读取，总线内置租户标识](adr-event-scope.md) |
| [投递承诺取所有实现都能满足的下界](adr-event-delivery.md) |
| [事件携带发布时赋予的标识，作为使用方去重的键](adr-event-identity.md) |
| [分发层为每个订阅者记一个跨度，事件跨进程携带追踪上下文](adr-event-tracing.md) |

## 事务发件箱

| 记录 |
|---|
| [已提交写入的宣告在写入的事务里暂存，提交之后由中继发布](adr-outbox-scope.md) |
| [事务发件箱是事件功能目录里的另一项功能，接口包是子包](adr-outbox-packaging.md) |
| [各副本的中继以有时限的认领分摊一张暂存表，发布在事务之外](adr-outbox-replicas.md) |

## 审计

| 记录 |
|---|
| [审计交付捕获，不交付查询、自证与归责判定](adr-audit-scope.md) |
| [捕获由语句、事件、请求与记录接口构成](adr-audit-capture.md) |
| [捕获的覆盖面由配置点名，审计不推断也不通配](adr-audit-reach.md) |
| [审计自持一张表，不经事件总线中转、不做多后端](adr-audit-sink.md) |
| [语句级的记录跟着那条语句成败，请求级与事件级按 fail-open 处置，记录接口返回错误](adr-audit-failure.md) |
| [审计的敏感键名取一个闭集，与日志共用一份登记](adr-audit-redaction.md) |
| [一个登记单元承载各条捕获通路](adr-audit-packaging.md) |
| [链上锚点用审计自己的接口类型，锚定方 import 接口包](adr-audit-anchor.md) |
| [审计的存在与覆盖面是装配的结果，不是框架的承诺](adr-audit-guarantees.md) |
| [审计记录的导出由检索接口的一条路由承载，判定复用检索权限](adr-audit-export.md) |
| [事件级审计行按事件标识去重，由表上的唯一约束裁定](adr-audit-replicas.md) |
| [审计表的租户列、行键与唯一写入方属于对外接口](adr-audit-table-contract.md) |
| [冒充的明细键只由审计模块写入，非冒充行上的同名键被剥掉](adr-audit-impersonation.md) |
| [审计的检索接口由一个独立的 audit.admin 模块承载](adr-audit-admin.md) |

## 设置

| 记录 |
|---|
| [设置以一个模块交付读写接口，租户标识以字符串表达](adr-settings-scope.md) |
| [设置项由模块声明，读取与写入按声明校验](adr-settings-declaration.md) |
| [设置的层由显式的层选指代，零值不是一个层](adr-settings-layer.md) |
| [设置的生效靠内存快照，事件广播加速、周期刷新兜底](adr-settings-propagation.md) |
| [设置的变更审计只发事件，历史归审计模块](adr-settings-audit.md) |
| [键表经 Store.Declared 只读列出，敏感项不带默认值](adr-settings-key-listing.md) |
| [设置项的取值域由声明方给出，写入时判定](adr-settings-domain.md) |
| [设置的写入、删除与列举各经数据库模块的 Connection 取一条自己的连接，取连接的等待有界](adr-settings-connection.md) |
| [运行期设置的管理面由框架提供一个按层判定的模块](adr-admin-settings.md) |

## 限流

| 记录 |
|---|
| [限流交付速率限制，不承载配额](adr-ratelimit-scope.md) |
| [限流不算架构的安全边界](adr-ratelimit-boundary.md) |
| [速率限制按责任键计数，取不到身份即放行](adr-ratelimit-dimension.md) |
| [限流以两个固定窗口加权近似滑动窗口](adr-ratelimit-algorithm.md) |
| [超限以 429 与 Retry-After 收口](adr-ratelimit-verdict.md) |
| [计数后端不可达时限流放行](adr-ratelimit-failure.md) |
| [限流根包即实现，认证与租户只按上下文读](adr-ratelimit-packaging.md) |
| [限流的装配级性质写成非保证](adr-ratelimit-guarantees.md) |

## 遥测

| 记录 |
|---|
| [遥测模块只装配上游 SDK 的 provider，不定义信号语义](adr-observability-scope.md) |
| [选库为 OpenTelemetry](adr-observability-library.md) |
| [根包是接口包且不登记模块，装配器排他、导出后端非排他](adr-observability-packaging.md) |
| [provider 经注册表交付，不装成上游的全局，也不装进程级的错误处理器](adr-observability-provider.md) |
| [采样、批处理、导出周期、停机上界与采集面清单归配置，输入项不接受命令行来源](adr-observability-config.md) |
| [请求计量与服务端跨度取上游采集库的完整属性面，本模块只补端名；服务端跨度以方法与路由模板定名](adr-observability-instruments.md) |
| [不订阅事件总线，领域事件不转成跨度](adr-observability-events.md) |
| [传播器单点定义，入站提取在采集层、出站注入由调用点选用的工具承载](adr-observability-propagation.md) |
| [端点不可达默认是运行期事实，探测是配置开关，导出失败不暴露为观测面](adr-observability-failure.md) |
| [刷出落在关闭阶段，以配置的上界为界](adr-observability-shutdown.md) |
| [采集层的锚点用本模块交付的功能，本层自带记录后重抛](adr-observability-anchor.md) |
| [存在与覆盖面是装配与配置的结果，不是框架的承诺](adr-observability-coverage.md) |
| [请求路径的采集以上游 gin 采集库为基座，本模块只补日志关联与端名](adr-observability-request-layer.md) |

## 国际化

| 记录 |
|---|
| [国际化只覆盖服务端生成的用户可见文案，机器码契约不变](adr-i18n-scope.md) |
| [跨模块边界的语言用 BCP 47 字符串，语言匹配库关在实现子包内](adr-i18n-locale-type.md) |
| [请求的语言按 context、账户偏好、默认三级解析，两层中间件分居观测与鉴别阶段](adr-i18n-locale-resolution.md) |
| [各模块以内嵌资源声明消息目录，装配期再并入一个可配置的覆盖目录](adr-i18n-catalogs.md) |
| [渲染责任随载荷形态划分，复合消息由后端渲染、类型化的值交给客户端](adr-i18n-rendering-ownership.md) |
| [错误响应只承载成品文本，结构扩展点不带语言](adr-i18n-error-rendering.md) |
| [语言与时区偏好存账户属性集，不改 authn 表结构](adr-i18n-preferences-storage.md) |
| [默认语言是配置项 i18n.default-locale，i18n 与 authn 取同一个](adr-i18n-default-locale.md) |
| [受支持语言集由目录派生，默认语言是配置而非常量](adr-i18n-language-set.md) |
| [消息按语言整体渲染，复数与时间段用自写语法，内嵌的日期时间按语言与时区渲染](adr-i18n-message-rendering.md) |

## 作业

| 记录 |
|---|
| [作业能力只承载必须跨副本收敛的触发，进程内循环归各模块自己的 ticker](adr-jobs-scope.md) |
| [作业能力根包是接口包，队列与登记接口由 jobs.db 排他交付，调度器单列一个模块](adr-jobs-packaging.md) |
| [多副本靠有时限的认领分摊一张作业表，至少一次、不选主、只做数据库后端](adr-jobs-claim.md) |
| [周期调度靠窗口幂等键收敛，声明按租户经名册展开，取材域取参与集](adr-jobs-schedule.md) |
| [作业进入终态时发事件，发布义务记在作业行自己身上，事件总线因此是可选依赖](adr-jobs-terminal-event.md) |

## 通知

| 记录 |
|---|
| [通知能力做站内信收件箱、渠道偏好矩阵、类型分类法登记接口、出站投递流水线与实时推送，同意台账刻意后置且不声明权限点](adr-notification-scope.md) |
| [通知类型是发出它的业务模块的资源声明，通知模块是消费点，取材域取可用集](adr-notification-taxonomy.md) |
| [渠道是封闭的平台词汇，可用性由装配决定，接口缺席时跳过而不失败](adr-notification-channels.md) |
| [出站投递经持久队列异步进行，逐渠道一个去重键，notification_sends 是幂等与结果账，短信预算与认证共用台账，邮件预算留在键值计数器](adr-notification-delivery.md) |
| [实时推送每副本一个 Hub，行先提交、事件后发，Hub 只值延迟](adr-notification-stream.md) |
| [通知偏好路由按端名清单挂载，不挂在入口模块声明的每一个端上](adr-notification-endpoints.md) |

## 对象存储

| 记录 |
|---|
| [对象存储本轮交付核心功能、字节存储接口与 `fs`/`s3` 后端，复验净化与缩略图派生后置](adr-objectstore-scope.md) |
| [对象存储的字节存储接口由根包声明，元数据接口与 `fs`/`s3` 后端各为一个排他模块](adr-objectstore-packaging.md) |
| [对象以声明、上传、完成三步写入，内部键从不经 API 暴露](adr-objectstore-lifecycle.md) |
| [对象存储记下调用方给的名字，入口规整一次后回显](adr-objectstore-object-name.md) |
| [业务前端经直连地址读写对象字节，铸址归元数据面，后端把能否铸址声明为可选能力](adr-objectstore-direct-access.md) |

## 宿主

| 记录 |
|---|
| [用顶层 starters/ 提供可运行的示范骨架](adr-starters-skeleton.md) |
| [每个账户创建即拥有一个个人租户，个人租户是单人的受保护租户](adr-starters-personal-tenant.md) |

## 测试

| 记录 |
|---|
| [外部测试后端由 compose 提供，测试按是否依赖它分为单元与集成](adr-test-tiers.md) |
| [前端的版面与可聚焦性由真实浏览器层的用例守住，不进提交门禁](adr-web-browser-tier.md) |

## 兼容性

| 记录 |
|---|
| [早期阶段不做稳定性承诺，破坏性改动仍逐次标注](adr-no-stability-promise.md) |
