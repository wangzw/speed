# starters/demo — 需求文档

这套文档规定 starters/demo 对使用者的行为义务。demo 是本仓库可运行、可拷走的 SaaS 骨架，把 `pkg/` 的模块装配成一个可部署给终端用户的程序；文档从系统外部描述它必须做到什么，覆盖登录与注册、账户与安全、租户与成员、平台运营、通知、文件、界面偏好、部署运维。

## 索引

- [总览](overview.md) — 系统是什么、对外提供哪些能力、整体范围边界
- [术语表](../glossary.md) — 全项目共用、各文档统一的术语

### 旅程

| 旅程 | 文档 |
|---|---|
| 口令注册到个人空间与第一个文件 | [口令注册到个人空间与第一个文件](journeys/journey-first-registration-to-personal-workspace.md) |
| 短信首登开户到改用口令登录 | [短信首登开户到改用口令登录](journeys/journey-sms-signup-to-password-login.md) |
| 社交登录开户与已有账户的绑定 | [社交登录开户与已有账户的绑定](journeys/journey-social-sign-in-and-linking.md) |
| 邀请一位成员进入租户 | [邀请一位成员进入租户](journeys/journey-invitation-to-tenant.md) |
| 停用账户并让它的会话失效 | [停用账户并让它的会话失效](journeys/journey-account-offboarding.md) |
| 登记第二因素并执行一次敏感操作 | [登记第二因素并执行一次敏感操作](journeys/journey-account-security-hardening.md) |
| 成员在租户里使用文件与通知 | [成员在租户里使用文件与通知](journeys/journey-member-workspace.md) |
| 开通一个租户并让首位成员进入 | [开通一个租户并让首位成员进入](journeys/journey-operator-onboards-tenant.md) |
| 为租户接入企业身份提供方 | [为租户接入企业身份提供方](journeys/journey-enterprise-sso-onboarding.md) |
| 冒充一位成员排障后结束冒充 | [冒充一位成员排障后结束冒充](journeys/journey-impersonation-for-support.md) |
| 运营者首次登录并解除管理写入的限制 | [运营者首次登录并解除管理写入的限制](journeys/journey-operator-first-run.md) |
| 调整运行期设置并在出错时回退 | [调整运行期设置并在出错时回退](journeys/journey-operator-configuration.md) |
| 上线一条短信模板并投入发送 | [上线一条短信模板并投入发送](journeys/journey-sms-template-rollout.md) |
| 按偏好调整界面 | [按偏好调整界面](journeys/journey-personalize-interface.md) |
| 把 demo 部署到云主机并让运营者能登录 | [把 demo 部署到云主机并让运营者能登录](journeys/journey-deploy-and-operate.md) |

### 能力

| 能力 | 文档 |
|---|---|
| 自助注册 | [自助注册](features/prd-signup.md) |
| 登录 | [登录](features/prd-signin.md) |
| 账户安全 | [账户安全](features/prd-security.md) |
| 账户自助 | [账户自助](features/prd-account.md) |
| 组织与成员 | [组织与成员](features/prd-tenancy.md) |
| 平台运营 | [平台运营](features/prd-platform-admin.md) |
| 平台配置 | [平台配置](features/prd-platform-config.md) |
| 通知 | [通知](features/prd-notifications.md) |
| 文件 | [文件](features/prd-objects.md) |
| 界面偏好 | [界面偏好](features/prd-presentation.md) |
| 部署与运维 | [部署与运维](features/prd-deployment.md) |
