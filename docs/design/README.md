# 设计文档

speed 是一个配置驱动的模块化应用程序框架，以 Go 库的形式交付。它为 Go 应用程序提供模块化的骨架与一批通用模块。

本目录只记录设计结论。决策的理由、考虑过的选项与付出的代价记录在[架构决策记录](../adr/README.md)中；术语见[术语表](../glossary.md)。

## 索引

| 文档 | 内容 |
|---|---|
| [架构](architecture.md) | 系统形状、模块依赖方向、跨切面设计、架构不变量 |
| [core](modules/design-core.md) | 模块化基础设施：描述符、注册、预置、查询、生命周期驱动 |
| [config](modules/design-config.md) | 启动必须项的加载：输入项收集、传输与格式扩展点、各来源的覆盖 |
| [log](modules/design-log.md) | 结构化日志：处理链、内置的格式与目的地、上下文传递、脱敏、文件滚动与清理 |
| [db](modules/design-db.md) | 数据库访问：连接的建立与关闭、迁移的收齐与应用、插件扩展点 |
| [http](modules/design-http.md) | HTTP 入口：监听端、路由与中间件的登记、中间件排序、请求上下文、panic 兜底、停机排空 |
| [http-cors](modules/design-http-cors.md) | 入口的跨源层：桥接 gin-contrib/cors，逐监听端策略，登记在防护阶段最外侧 |
| [authn](modules/design-authn.md) | 认证：身份系统的职责边界、模块清单与依赖、身份与判决的形状、状态放置、链上次序、装配层面的非保证 |
| [authn-gate](modules/design-authn-gate.md) | 认证网关：凭据的取出与整条认领、校验后端的接缝、路由访问级别的登记、判决的复核与盖章、口令表 |
| [authn-account](modules/design-authn-account.md) | 账户存储：账户、已验证标识、外部身份绑定、成员关系、口令凭据、服务密钥、失败退避 |
| [authn-login](modules/design-authn-login.md) | 登录：引擎与流程记录、登录方式的接缝、口令、社交与微信方式、认证前的失败退避 |
| [authn-sms](modules/design-authn-sms.md) | 短信验证码：挑战状态机、发送预算与尝试上限、发送方接缝 |
| [authn-mfa](modules/design-authn-mfa.md) | 多因素：因素登记簿、第二因素的策略判定、因素的登记与校验编排、保障等级的合成 |
| [authn-session](modules/design-authn-session.md) | 登录会话：浏览器、令牌、服务三种种类的存放与期限、cookie 与 CSRF、撤销面 |
| [authn-token](modules/design-authn-token.md) | 令牌与服务账户：JWT 访问令牌、不透明刷新令牌、签名密钥与 JWKS、令牌端点 |
| [authn-sso](modules/design-authn-sso.md) | 企业单点登录：租户自有身份提供方的登记、邮箱域所有权与发现、SAML 与企业 OIDC 的校验 |
| [authn-impersonation](modules/design-authn-impersonation.md) | 冒充：有期限的冒充记录、结构性限制、有效性判定、审计先于动作 |
| [authn-admin](modules/design-authn-admin.md) | 对授权的承诺：判定接缝、管理面模块、自助范围的判据、403 的收窄 |
| [authn-config](modules/design-authn-config.md) | 认证的配置键、设置键与秘密的存放、放宽开关、启动期自检 |
| [authn-events](modules/design-authn-events.md) | 认证的事件、审计、日志与指标：名字、载荷与属性的全集 |
| [authn-failure](modules/design-authn-failure.md) | 认证的错误与失败语义：哨兵表、依赖不可用时每条路径的方向、有界放宽、时间的使用 |
| [authn-threats](modules/design-authn-threats.md) | 认证的威胁模型：按类别的威胁与对策、部署层前提、接受的代价、未覆盖的威胁 |
| [tenant](modules/design-tenant.md) | 租户：请求面的租户解析、名册与生命周期、共享表加租户列的语句过滤 |
| [tenant-admission](modules/design-tenant-admission.md) | 租户准入层：身份是否被准入已解析的租户、租户级认证强度要求、冒充的租户范围 |
| [authz](modules/design-authz.md) | 授权：职责边界、模块清单与依赖、判定引擎与策略存储、中间件与路由声明、管理面、承接的权限点 |
| [kv](modules/design-kv.md) | 键值原语：不透明字节的存取与过期、原子自增与比较并交换、按最弱后端设计的接口面 |
| [sms](modules/design-sms.md) | 短信发送：按模板发送、模板一等声明、载体接缝与厂商适配 |
| [event](modules/design-event.md) | 事件总线：按类型发布与订阅、载荷的结构化读取、投递承诺的下界 |
| [outbox](modules/design-outbox.md) | 事务发件箱：在写入的事务里暂存事件、提交之后中继到事件总线、拒收后重发、多副本的有时限认领 |
| [audit](modules/design-audit.md) | 审计：语句、事件、请求与记录接缝的捕获通路、审计表、明细的脱敏 |
| [settings](modules/design-settings.md) | 设置：模块声明的运行期参数、三级回落、以快照读取、变更事件的发布与订阅 |
| [ratelimit](modules/design-ratelimit.md) | 限流：租户内按责任键的请求速率限制、以设置读取额度与窗口、经键值原语按窗口计数 |
| [observability](modules/design-observability.md) | 运行期遥测：上游 SDK 的 provider、请求路径的采集、出站注入、导出后端 |
