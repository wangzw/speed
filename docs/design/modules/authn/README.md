# 认证

认证功能的文档索引。主文档给出职责边界、模块清单与依赖、身份与判决的形状、状态放置与链上次序；网关、账户、登录、会话、令牌、多因素、短信、企业 SSO、冒充、管理面、邀请、用户档案及各项横切主题各一份子模块文档。

## 索引

| 文档 | 内容 |
|---|---|
| [authn](design-authn.md) | 认证：职责边界、模块清单与依赖、身份与判决的形状、状态放置、链上次序、装配层面的非保证 |
| [authn-gate](design-authn-gate.md) | 认证网关：凭据的取出与整条认领、校验后端的接缝、路由访问级别的登记、判决的复核与盖章、口令表 |
| [authn-account](design-authn-account.md) | 账户存储：账户、已验证标识、外部身份绑定、成员关系、口令凭据、服务密钥、失败退避 |
| [authn-login](design-authn-login.md) | 登录：引擎与流程记录、登录方式的接缝、口令、社交与微信方式、认证前的失败退避 |
| [authn-sms](design-authn-sms.md) | 短信验证码：挑战状态机、发送预算与尝试上限、发送方接缝 |
| [authn-mfa](design-authn-mfa.md) | 多因素：因素登记簿、第二因素的策略判定、因素的登记与校验编排、保障等级的合成 |
| [authn-session](design-authn-session.md) | 登录会话：浏览器、令牌、服务三种种类的存放与期限、cookie 与 CSRF、撤销面 |
| [authn-token](design-authn-token.md) | 令牌与服务账户：JWT 访问令牌、不透明刷新令牌、签名密钥与 JWKS、令牌端点 |
| [authn-sso](design-authn-sso.md) | 企业单点登录：租户自有身份提供方的登记、邮箱域所有权与发现、SAML 与企业 OIDC 的校验 |
| [authn-impersonation](design-authn-impersonation.md) | 冒充：有期限的冒充记录、结构性限制、有效性判定、审计先于动作 |
| [authn-admin](design-authn-admin.md) | 对授权的承诺：判定接缝、管理面模块、自助范围的判据、403 的收窄 |
| [authn-invite](design-authn-invite.md) | 邀请：自持状态机与不透明令牌、接受即兑现的账户与成员关系创建 |
| [authn-profile](design-authn-profile.md) | 用户档案自服务面：身份取自上下文、路径不带账户标识的读改路由 |
| [authn-config](design-authn-config.md) | 认证的配置键、设置键与秘密的存放、放宽开关、启动期自检 |
| [authn-events](design-authn-events.md) | 认证的事件、审计、日志与指标：名字、载荷与属性的全集 |
| [authn-failure](design-authn-failure.md) | 认证的错误与失败语义：哨兵表、依赖不可用时每条路径的方向、有界放宽、时间的使用 |
| [authn-threats](design-authn-threats.md) | 认证的威胁模型：按类别的威胁与对策、部署层前提、接受的代价、未覆盖的威胁 |

## 实现状态

| 子模块 | 状态 |
|---|---|
| [authn-gate](design-authn-gate.md) | 已实现 |
| [authn-account](design-authn-account.md) | 已实现 |
| [authn-login](design-authn-login.md) | 已实现 |
| [authn-sms](design-authn-sms.md) | 已实现 |
| [authn-mfa](design-authn-mfa.md) | 已实现 |
| [authn-session](design-authn-session.md) | 已实现 |
| [authn-token](design-authn-token.md) | 已实现 |
| [authn-sso](design-authn-sso.md) | 已实现 |
| [authn-impersonation](design-authn-impersonation.md) | 已实现 |
| [authn-admin](design-authn-admin.md) | 已实现 |
| [authn-config](design-authn-config.md) | 已实现 |
| [authn-events](design-authn-events.md) | 已实现 |
| [authn-failure](design-authn-failure.md) | 已实现 |
| [authn-threats](design-authn-threats.md) | 已实现 |
| [authn-invite](design-authn-invite.md) | 已实现 |
| [authn-profile](design-authn-profile.md) | 已实现 |
