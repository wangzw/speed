# 登录

## 问题陈述

匿名访客要以本部署实际提供的任一方式证明身份并取得会话。启用了哪些登录方式、是否开放开户、外部提供方回跳落在哪里，只由部署决定，界面与登录入口不能把这些事实写死。

## 范围

登录方式的匿名发现，口令登录，短信验证码登录，社交登录（Google、GitHub、微信），企业单点登录（OIDC、SAML），通行密钥登录，外部提供方回跳的落点与失败落地，登录失败的可见结果与退避，以及认证守卫对未登录者的拒绝。

### 不在范围内

自助注册的建号规则与开户后的邮箱验证回路；第二因素的校验与因素登记；会话与访问令牌的期限、撤销与刷新；账户自助中的改口令、添加登录标识与解绑；授权判定与租户准入。

## 参与者

- **匿名访客**：不持有会话，可读匿名发现文档，可发起任一已启用方式的登录，可到达外部提供方的回跳落点。
- **会话已失效的用户**：曾持有会话而当前不被认可，看到登录面并重新登录。
- **部署方**：决定装配哪些登录模块、开放哪一类开户、关闭哪些方式；这些决定经发现文档与登录应答呈现给匿名访客。

## 功能需求

### REQ-signin-001

系统必须提供一份匿名可读的登录方式发现文档，其中只列出本部署已装配且未被关闭的登录方式。

- **验收**：未认证调用读取该文档得到成功应答；增删一个登录模块或在运行期关闭一个方式后，同一应答里对应条目随之出现或消失。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-deploy-and-operate.spec.ts` `deploy-and-operate: a deployment serves, seeds, and refuses an unproved operator write`；`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`

### REQ-signin-002

发现文档中的每个登录方式必须自报其呈现类别，取值限于表单、跳转、企业与通行密钥。

- **验收**：每个条目带一个呈现类别，取值限于表单、跳转、企业、通行密钥。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-deploy-and-operate.spec.ts` `deploy-and-operate: a deployment serves, seeds, and refuses an unproved operator write`

### REQ-signin-003

登录方式的匿名发现文档不得携带任何关于某个账户或某个地址是否存在的判断，也不得携带企业身份提供方的清单。

- **验收**：同一部署对任意匿名调用者返回相同的登录方式条目集合，不随提交的邮箱或账户变化；企业提供方只能经按邮箱域的发现请求取得。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`

### REQ-signin-004

登录面呈现的登录方式集合必须由发现文档决定，界面不得内置或另行维护一份渠道清单；宿主可以在此基础上收窄或重排。

- **验收**：只改部署装配或方式开关、不改前端产物，刷新登录面后渠道集合随之变化。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`

### REQ-signin-005

登录面呈现注册入口的条件按 REQ-signup-002 的规定。

- **验收**：首登开户与口令注册都关闭时登录面无注册入口；仅口令注册开启时出现注册入口；仅首登开户开启、且有可见的短信或社交方式能为未知标识开户时出现注册入口。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-006

系统必须以用户名或邮箱标识连同口令作为登录方式。

- **验收**：持有口令凭据且仍有用户名或邮箱标识的账户，以正确的标识与口令提交后登录完成；只存在于未证明预留中的邮箱不能作为登录名。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signin-007

口令登录失败必须以单一结果呈现，不区分标识不存在、口令不符、账户停用与账户没有口令。

- **验收**：上述任一情形得到同一状态码与同一错误码；界面在口令字段给出同一提示，且不陈述具体成因。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-offboarding.spec.ts` `account-offboarding: an operator disables a member and the member returns after the restore`

### REQ-signin-008

标识级退避生效时，系统必须拒绝该标识的口令校验，并告知可以重试的等待时长。

- **验收**：同一标识的失败次数超过自由次数后，等待期内的尝试得到限流拒绝并给出等待时长；等待期内不比对口令。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-offboarding.spec.ts` `account-offboarding: an operator disables a member and the member returns after the restore`

### REQ-signin-009

账户级退避生效时，系统必须使应答与普通凭据失败同形，不得让调用方看出退避本身。

- **验收**：同一账户的失败达到阈值后，即使提交正确口令也得到与口令错误相同的应答，而不是限流应答。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-010

被要求更换口令的账户必须先完成更换口令，登录才可完成。

- **验收**：这类账户提交正确口令后流程停在设置新口令的步骤；新口令不合策略或与当前口令相同时流程不完成；更换成功后登录完成。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-011

系统必须以短信验证码作为登录方式：先向提交的手机号发送验证码，再以该验证码完成登录。

- **验收**：号码被接受后界面报告验证码已发送；提交正确且未使用的验证码后登录完成；提交错误或已使用的验证码不完成登录。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-signin-012

短信登录必须只对允许的国家或地区号码前缀开放。

- **验收**：号码前缀不在放行表内时得到「地区不支持」的拒绝；放行表为空时全部号码被拒；被拒时不发送验证码。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-013

一次登录流程中可发送的短信条数必须有上限，达到上限后不再发送。

- **验收**：同一流程的发送次数达到上限后，再次请求发送得到限流拒绝，且不发出短信。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-014

系统在短信登录路径上不得泄露一个手机号是否已登记。

- **验收**：未登记号码在开户关闭时得到的发送应答与已登记号码相同，其后的验证码答复一律为统一的登录失败。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-015

系统必须以跳转到外部提供方的方式支持社交登录，覆盖 Google、GitHub 与微信。

- **验收**：装配对应方式后，选择该方式得到提供方的授权地址；在提供方完成授权并回跳后，无后续步骤的登录直接完成并回到发起页。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens an account with the provider-verified email`（仅驱动 Google 跳转；GitHub 与微信未在浏览器层驱动）

### REQ-signin-016

社交方式为未知外部身份开户必须受开户开关控制。

- **验收**：开户开关关闭且该外部身份没有任何绑定时，登录以统一的登录失败结束且不建立账户；开关开启时建立启用的账户并完成登录。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-017

当社交登录的外部身份没有绑定、提供方声明的已验证邮箱属于一个现有账户、且本次登录不被自动绑定接管时，系统必须要求先绑定，而不是登录该账户或另开户；自动绑定接管本次登录的条件按 REQ-signup-042 的规定。

- **验收**：自动绑定未接管本次登录时，该情形得到「需先绑定」的专用拒绝；自动绑定接管时登录完成，且不出现该拒绝。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a Google email already on an account is refused with link-required`

### REQ-signin-018

外部身份已被另一账户占用时，登录必须失败并报告冲突，任何一方账户与绑定都不被改动。

- **验收**：同一（提供方，主体）已绑定在一个账户上而本次断言落到另一个账户的处理时，应答为冲突拒绝，两个账户及其绑定均不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-033

系统必须以提供方给出的稳定身份标识识别一个外部身份：键是（提供方，稳定标识）。提供方声明的邮箱只作为该身份的资料，不参与识别；提供方侧更改邮箱之后再次登录，必须仍登录原账户，不新建账户。

- **验收**：同一提供方下，同一稳定身份标识在提供方侧更改邮箱后再次登录，落回原账户，账户数与绑定数不变；同一邮箱分别出现在两个不同提供方下时，按两个互不相同的身份处理，各自可以指向不同账户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-019

微信登录在提供方给出 unionid 时，必须把同一人的不同应用归到同一账户，并升级此前按应用保存的绑定。

- **验收**：一个账户先按应用内 openid 建立绑定，随后同一人的登录带来 unionid 时，该账户补上 unionid 绑定；该 unionid 已属于另一账户时以冲突拒绝结束且两个账户都不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-020

企业单点登录必须按邮箱域发现提供方；未配置、未验证或所属提供方已停用的域得到空结果。

- **验收**：提交未配置或未验证域的地址得到空列表；提交已配置、已验证且所属提供方已启用的域的地址得到该租户的提供方；两种情形的应答同形同时，耗时一致而不随是否有匹配变化。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`

### REQ-signin-021

采用预置开户的企业提供方必须只允许登录到管理员预先建好并带该邮箱的账户。

- **验收**：提供方为预置模式时，匹配不到预置账户的登录以统一的登录失败结束，且不新建账户。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`（仅驱动预置账户命中的正向分支；匹配不到预置账户时拒绝由后端测试覆盖）

### REQ-signin-022

采用即时开通的企业提供方必须在首次登录时建立账户，并建立该租户的成员关系。

- **验收**：提供方为即时开通模式且断言邮箱域属于其已验证域时，首次登录得到一个新建账户与一条该租户的成员关系。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-023

企业登录断言的邮箱域不属于该提供方所属租户已验证的域时，登录必须失败。

- **验收**：域不匹配的断言得到统一的登录失败，不建账户，也不写成员关系。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-024

系统必须以通行密钥完成登录：用户在浏览器完成凭据仪式后即登录。

- **验收**：已登记可发现凭据的用户选择通行密钥登录并在浏览器完成仪式后，登录完成。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-025

通行密钥仪式进行期间，界面必须显示等待状态与取消入口，且取消立即生效。

- **验收**：仪式挂起时显示等待提示与取消控件；取消后不等仪式落定即显示已取消并给出重试；取消之后的迟到凭据不被采用。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-026

登录流程必须有时效与一次性：过期、已使用、被并发消费或绑定不符的答复一律以流程失效拒绝。

- **验收**：重复提交同一回跳、使用过期流程或携带错误绑定的答复均得到流程失效拒绝；界面显示失败提示并退回该渠道的第一步。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-027

跳转类登录在没有后续步骤时，必须于回跳应答内建立会话并把用户送回发起登录的页面。

- **验收**：会话交付、无后续步骤的回跳应答为重定向到发起页，且同一次应答设置会话凭据。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens an account with the provider-verified email`

### REQ-signin-028

跳转类登录被拒且回跳本身是顶层导航时，系统必须把用户送回一个产品内页面，并在该页面显示这次拒绝对应的文案。

- **验收**：该情形下浏览器落在一个产品内页面，页面显示这次拒绝对应的文案，而不是原始拒绝码。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a Google email already on an account is refused with link-required`

### REQ-signin-029

受认证守卫保护的端点对未认证请求必须回答未认证。

- **验收**：不带凭据访问受保护端点得到未认证应答，且应答不包含受保护内容。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-030

未认证访问受保护页面时，界面必须渲染登录面而不是错误页，并在登录完成后就地继续。

- **验收**：未登录打开受保护页面看到登录面；在同一页面完成登录后，原页面渲染出内容而不是停在登录面。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-offboarding.spec.ts` `account-offboarding: an operator disables a member and the member returns after the restore`

### REQ-signin-031

跳转类与企业类登录方式必须自报所用的线协议；表单类与通行密钥类方式不得携带线协议。

- **验收**：跳转类与企业类条目带线协议，表单类与通行密钥类条目不携带线协议。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signin-032

受认证守卫保护的端点的未认证应答必须带认证挑战头，且挑战头陈述的凭据方式只包含装配中在场的凭据载体；装配不含口令表时不得列出 `Basic`。

- **验收**：不带凭据访问受保护端点得到未认证应答并带认证挑战头；装配不含口令表时挑战头不含 `Basic`。
- **状态**：已实现
- **端到端测试**：未实现
