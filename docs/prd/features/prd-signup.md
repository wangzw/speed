# 自助注册

## 问题陈述

新访客需要一个不依赖既有账户的建号入口，来源包括登录页的口令自助注册、短信首登开户、社交与企业登录首登开户；其中口令自助注册既不需要既有账户，也不以接收短信为前提。建号之后，账户在完成邮箱验证前可以使用一个封闭集内的能力，依赖可信邮箱的能力在本人证明邮箱之后放开。

## 范围

覆盖口令自助注册、短信首登开户、社交与企业登录首登开户、注册开关、用户名在建号前的取得与格式和保留字规则、注册成功后触发的自动登录与落点、注册后的邮箱验证门控与未验证账户的闲置清理，以及注册各入口对用户名与邮箱占用的披露规则。

### 不在范围内

登录方式的取舍、退避与限流、会话与令牌的期限与刷新等通用登录规则、账户资料的日常维护、成员与权限的日常管理、个人租户除注册落点与授予条件之外的规则、邮件与短信载体的实现。

## 参与者

- 新访客：没有会话，不能访问任何租户资源；可以到达登录与注册面、社交与企业回调落点、邀请落地。
- 已有账户持有人：持有会话或可以重新登录的账户持有人，邮箱可能已验证或未验证；可以用一个外部提供方发起社交登录。
- 管理员：在管理面预置或导入账户的账户持有人。
- 待验证账户：已登录的账户持有人，账户持有邮箱而没有任何一条已证明归属的邮箱；可以执行 REQ-signup-024 列明的动作，集合外的动作被拒绝。

## 功能需求

### REQ-signup-001

系统必须在匿名可读的登录方式发现面报告「开放注册」；「开放注册」在首登开户或口令注册任一开放时为真。

- **验收**：匿名读取发现面，首登开户或口令注册任一开放时「开放注册」为真，两者都关闭时为假。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-032

系统必须在同一匿名可读的登录方式发现面单独报告「开放口令注册」，其取值等于口令注册开关。

- **验收**：口令注册开关开放时「开放口令注册」为真，关闭时为假，且与「开放注册」的取值在同一应答中分别可见。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-002

登录面注册入口的呈现条件按 REQ-signin-005 的规定。

- **验收**：按 REQ-signin-005 的验收判定。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-003

口令注册开放时，注册模式必须在可开户的登录方式之外提供一张口令注册表单。

- **验收**：口令注册开放时，注册模式同时呈现口令注册表单与各可开户的登录方式；口令注册关闭时，注册模式不呈现口令注册表单。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-004

口令注册必须要求用户名、邮箱与口令同时提供；缺任一项的提交必须被拒绝且不建号。

- **验收**：分别缺用户名、缺邮箱、缺口令的提交各被拒绝，且没有账户产生；三者齐备、且用户名满足 REQ-signup-045 与 REQ-signup-046 的提交建号。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-045

用户名必须可安全用作 URL 路径段：经 NFKC（[REF-unicode-uax15](../../references.md)）规范化并转为小写后，长度为 3 到 64 个字符、只含 `[a-z0-9._-]`，首字符为字母或数字，不以静态凭据表的前缀 `static.` 起头，不得为 UUID 形状，并在全部账户间唯一；满足这些规则的用户名放入 URL 路径时不需要转义。

- **验收**：提交合形用户名建号后该用户名可用于登录；长度不在 3 到 64 之间、含 `[a-z0-9._-]` 以外的字符、首字符不是字母或数字、以 `static.` 起头或为 UUID 形状的用户名均被拒绝且不建号；合形用户名放入 URL 路径时无需转义。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-046

用户名不得与系统路径或系统名称冲突：内置保留字清单为 `api`、`console`、`account`、`admin`、`system`、`login`、`logout`、`signin`、`signup`、`static`、`assets`、`health`、`ready`、`metrics`、`oauth`、`callback`、`well-known`、`www`、`root`，部署可经设置 `authn.account.reserved-usernames` 追加保留字；写入用户名的每一条路径都必须拒绝保留字，保留字拒绝与用户名占用冲突（REQ-signup-008）是两种不同的结果。

- **验收**：以任一内置保留字或部署追加的保留字提交用户名，在匿名口令注册、首登开户的开户一步与账户面添加用户名三处均被拒绝：注册与首登开户不产生账户，账户面不写入该用户名；该拒绝不表现为用户名占用冲突。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-033

口令注册必须接受显示名、语言与时区作为可选输入，并在提供时把它们保存为该账户的偏好；不提供时注册照常完成。

- **验收**：只提交用户名、邮箱与口令的注册成功；额外提交显示名、语言与时区的注册成功，读回的资料与偏好为提交的值。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-005

注册必须在一次提交内建立一个启用的账户。

- **验收**：注册成功后该账户存在且处于启用状态，可以用用户名与口令走登录流程并登录。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-034

注册应答不得建立会话。

- **验收**：注册成功后立即读取会话得到匿名，任何租户资源均不可达；此后以同一份凭据完成一次口令登录才会话建立。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-006

注册不得发送任何邮件。

- **验收**：在装配了可观察替身邮件通道的部署上完成一次注册，该替身收到的消息数为零；实现改为发信时该判据失败。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-007

注册提交的邮箱在本人证明之前不得成为登录方式，也不得被另一个账户注册；证明之后，只有持有人把它选作登录方式时，它才可用于口令登录。

- **验收**：注册刚完成时以该邮箱作口令登录名登录失败；另一账户以同一地址注册得到邮箱占用冲突；本人完成验证并把它选作登录方式后才可作为口令登录名。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-008

用户名已被占用时，注册必须以显式的用户名占用冲突拒绝，且不产生任何账户。

- **验收**：用一个已存在账户的用户名注册，得到用户名占用冲突；该用户名仍属于原账户，没有新账户产生。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-009

邮箱已被占用时，注册必须以显式的邮箱占用冲突拒绝，并整体回滚开户，不留下账户、用户名或口令凭据。

- **验收**：用一个已被其他账户持有、或正由其他账户待证明的邮箱注册，得到邮箱占用冲突；该次提交使用的用户名随后可以被另一次注册使用，且没有新账户产生。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-010

注册成功后，系统必须以同一份凭据自动发起一次口令登录。

- **验收**：注册成功且凭据可用时会话建立，进入登录态。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-035

自动登录被拒时，系统必须退回登录表单并预填本次注册的用户名。

- **验收**：令自动登录被拒，界面停在登录表单，用户名已预填为本次注册所用值。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-036

自动登录被拒后的回退路径不得重发注册请求。

- **验收**：退回登录表单后，本次提交只产生一次注册请求，账户仍然只有一份。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-011

口令注册开关关闭时，注册必须以「注册已关闭」拒绝，且不建号。

- **验收**：口令注册开关为关闭时提交注册得到「注册已关闭」拒绝；随后不存在该用户名对应的账户。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-012

短信首登开户开放时，未登记的手机号在通过短信验证后、建立账户之前必须在同一步取得一个合法用户名、一个可选的显示名、一个合法口令与确认口令，判据按 REQ-signup-047 与 REQ-signup-051 的规定；账户建立时即持有该用户名与口令，显示名按 REQ-signup-051 取得，并完成登录；开户成功后的落点按 REQ-signup-052 的规定。

- **验收**：未登记的手机号走完短信登录并提交合法用户名、可选显示名、口令与确认口令后得到一个持有该用户名与口令的账户，且会话处于已登录状态；选填的显示名被保存为该账户的显示名；未提交或提交不合法用户名、口令不符合策略或两次口令不一致时没有账户产生；落点按 REQ-signup-052 的验收判定。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-signup-013

短信首登开户关闭时，未登记的手机号不得建立账户，登录以失败应答；该应答与已登记号码的失败应答一致，泄露规则按 REQ-signin-014 的规定。

- **验收**：关闭时未登记号码完成短信验证后没有账户产生，且得到与已登记号码的错误验证码相同的失败应答。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-014

开户规则约束每一个新建的用户账户：自助注册、短信与社交首登开户、企业即时开通、邀请受理、管理面预置、初始装配与导入，每条写入路径新建的用户账户都必须在建立时持有一个合法用户名，未取得合法用户名时不得建立账户；服务账户以服务密钥登录，不是用户账户，不受这条约束。用户名判据按 REQ-signup-045 与 REQ-signup-046 的规定。

- **验收**：分别经上述每条写入路径开户，持有合法用户名时新建的用户账户读到该用户名；缺少用户名或用户名不合法时不产生用户账户；服务账户不因缺少用户名被拒绝。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-047

首登开户必须在建立账户之前取得一个合法用户名：短信首登开户、社交登录首登开户与企业即时开通三条路径一律适用，用户名判据按 REQ-signup-045 与 REQ-signup-046 的规定；没有取得合法用户名时不得建立账户。**短信首登开户与社交首登开户的同一步还必须取得一个合法口令**：口令与用户名在同一次提交里给出，判据按账户的口令策略，不合格时不建账户。短信首登的这一步依次取用户名、可选的显示名、口令与确认口令；企业即时开通的首登一步只取用户名，不取口令。显示名的取得与回退按 REQ-signup-051 的规定。

- **验收**：分别经短信首登、社交首登与企业即时开通发起开户，流程在建立账户之前呈现开户一步。账户仅在用户名合法、且对短信首登与社交首登在同一提交里给出符合口令策略的口令与确认口令时建立：只提交用户名、只提交用户名与显示名、口令不符合策略或两次口令不一致时均不产生账户；用户名、口令与确认口令都合法时账户建立并立即可以口令登录；企业即时开通只提交合法用户名即建立账户。短信首登的这一步同时接受可选的显示名，显示名的结果按 REQ-signup-051 的验收判定。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`；`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens the account face and keeps the provider address as a contact`；`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: a just-in-time provider asks for a username before it opens an account`

### REQ-signup-015

登录模式下存在可开户的短信方式时，界面必须在短信渠道下给出一句开户说明，且该说明对每个号码保持一致。

- **验收**：该说明在可开户的短信渠道下出现，其文本不随号码是否已登记而变化。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-signup-016

短信首登开户与社交首登开户产生的账户不得建立任何成员关系；企业即时开通产生的成员关系按 REQ-signin-022 的规定。

- **验收**：新开户的短信或社交账户访问任何租户资源时按无成员关系处理。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-017

旧行为下由社交首登开户写入账户的、提供方声明已验证的邮箱标识保留在账户上，按其写入时的方式继续工作；系统不得回溯改写或删除这类标识。新的社交首登开户的地址处理见 REQ-signup-048。

- **验收**：旧行为下写入的邮箱标识仍在账户上，并可用于其原本可用的登录；该标识不被批量改写或删除。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-043

每一个账户必须持有一个账户标识：它不是用户名、邮箱或手机这类登录标识或联系方式，也不是绑定在该账户上的外部身份标识；账户建立之后，该标识不因账户资料或外部账号资料的变化而改变。

- **验收**：分别经口令自助注册、短信首登、社交首登、企业即时开通与邀请接受建立账户，每个账户都得到非空、两两不同、且不等于本次开户所用外部身份标识的账户标识；宿主导入的账户带入的标识非空且格式合法；更改显示名、邮箱或外部账号资料后，上述标识都不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-044

使用社交账户创建账户时，系统必须补齐账户建立所必需的内容，其中包括一个合法用户名与一个合法口令，不得留下以外部身份标识充当账户标识、或缺少内容的记录；提供方声明为已验证的地址写为联系方式（默认通讯邮箱），并按 REQ-signup-048 的规定处置登录方式。

- **验收**：首次社交登录建出的账户持有非空、且不同于该外部身份标识的账户标识，并持有建号前取得的合法用户名与口令；账户以联系方式持有提供方声明为已验证的那个地址，默认通讯邮箱是它，并按 REQ-account-068 同时被选作登录方式；未取得合法用户名或口令不合策略时不建账户。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens the account face and keeps the provider address as a contact`

### REQ-signup-018

企业 SSO 首登开户必须按提供方声明的开通方式处理：预置开通按 REQ-signin-021 的规定，即时开通按 REQ-signin-022 的规定；即时开通在建立账户之前必须先取得一个合法用户名（判据按 REQ-signup-047 的规定）。

- **验收**：预置开通按 REQ-signin-021 的验收判定；即时开通按 REQ-signin-022 的验收判定；提供方声明的开通方式不是这两种之一时登录失败且不建户；即时开通未取得合法用户名时不建账户。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`；`enterprise-sso-onboarding: a just-in-time provider asks for a username before it opens an account`

### REQ-signup-019

旧行为下在自动绑定关闭时写下的账户与其外部身份绑定保持不变：这些账户的登录方式与凭据不被新的关联规则改写。

- **验收**：旧账户仍以原有的登录方式登录，凭据不变；外部身份绑定不被新的写入改写或删除。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-020

旧行为下未命中现有账户而开立的账户保持不变：其登录方式与资料不被新的关联规则改写；旧行为下由提供方声明地址触发的需先绑定拒绝所作用的账户同样保持不变。

- **验收**：旧行为下开立的这类账户仍以原有登录方式登录，账户资料不变；这些账户不被新的写入改写或删除。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-042

旧行为下由自动绑定建立的外部身份绑定保留在账户上，继续按其写入时的方式用于登录；系统不得回溯改写或删除它们。

- **验收**：旧行为下自动绑定产生的外部身份绑定仍可用于登录，账户数不变；这些绑定不被批量改写或删除。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-021

首登开户开关与口令注册开关必须相互独立：改变其中一个不改变另一个的取值与入口。

- **验收**：单独改变任一开关后，另一个开关的取值与登录面入口保持不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-037

首登开户开关与口令注册开关在未配置时都必须按关闭处理。

- **验收**：在没有相关配置的部署上「开放注册」与「开放口令注册」都为假，登录页没有注册入口，口令注册请求得到「注册已关闭」拒绝。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-022

注册的可用性必须随装配：出厂缺省配置下，没有邮箱验证码投递通道的部署把口令注册取为关闭，登录页不呈现注册入口；开放口令注册的部署必须包含邮箱验证码投递通道。

- **验收**：未配置注册开关的部署口令注册为关闭，登录页无注册入口，注册请求得到「注册已关闭」拒绝；开放口令注册的部署可以把验证码送达邮箱。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-023

本文的验证门控以账户的邮箱验证状态为依据，该状态读作「已验证」「未验证」「无邮箱」三种之一，判定按 REQ-account-015 的规定，与账户来源无关。

- **验收**：没有邮箱的账户读作「无邮箱」，持有未证明邮箱的账户读作「未验证」，持有至少一条已证明邮箱的账户读作「已验证」；来源不同而邮箱证明情况相同的账户得到相同结论。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-024

未验证账户必须允许登录，并允许下列动作：账户页的自助动作按 REQ-account-006 的规定；登记多因素与通行密钥；接受他人发来的邀请。

- **验收**：未验证账户执行 REQ-account-006 列明的每个动作、登记多因素、登记通行密钥、接受他人发来的邀请均成功；执行 REQ-signup-025 列明的动作被拒绝。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-025

未验证账户必须禁止一组封闭动作：签发或撤销邀请、创建或变更租户、管理面的人类写、授权与设置的写面、创建服务密钥、绑定外部身份、发起冒充。

- **验收**：未验证账户执行上述任一动作被拒绝；拒绝结果不因动作经由界面还是直接请求而不同。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-026

验证手机不得改变邮箱验证状态；只有邮箱被证明才解除未验证账户的限制。

- **验收**：未验证账户完成手机验证后仍读作未验证、限制不变；完成邮箱验证后转为已验证、限制解除。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-027

管理员预置或导入的账户在本人证明邮箱之前与自助注册账户受同一组未验证限制；其中管理面人类写的拒绝按 REQ-platform-admin-017 的规定。

- **验收**：管理员预置的账户首次登录后，未证明邮箱期间执行 REQ-signup-025 列明的动作被拒绝；本人证明邮箱后放行。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-deploy-and-operate.spec.ts` `deploy-and-operate: a deployment serves, seeds, and refuses an unproved operator write`

### REQ-signup-028

口令注册成功并自动登录后，系统必须把该账户落在账户页的邮箱验证节。

- **验收**：注册完成后的落点是账户页的邮箱验证节，而不是账户页的其它节或站内首页。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-038

落在邮箱验证节时，系统必须自动发送一次验证码，且本次进入不重复发送。

- **验收**：落点出现时目标邮箱收到一条验证码；停留在该页不触发第二次发送，重发需手动触发。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-signup-039

口令注册完成后的落点是账户页的邮箱验证节；短信首登的落点按 REQ-signup-052 的规定，社交首登与企业即时开通的落点按 REQ-signup-053 的规定。

- **验收**：口令注册完成后的落点是账户页的邮箱验证节。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-052

短信首登开户成功后，系统必须把当事人落在租户区首页，当前组织是个人组织；从邀请链接发起的短信首登开户不得导航离开该链接的接受页，账户建立后停留在该页。

- **验收**：经短信首登完成开户后页面落在租户区首页，当前组织是个人组织；从邀请链接发起的短信首登开户完成后仍停留在该邀请的接受页，不发生导航。
- **状态**：未实现
- **端到端测试**：未实现

### REQ-signup-053

社交首登开户与企业即时开通经提供方回调完成后，系统必须把当事人落在租户区首页；从邀请链接发起的回调必须回到该邀请的路径，不落在租户区首页。

- **验收**：经社交提供方或企业提供方完成开户后页面落在租户区首页；企业即时开通建立的账户与社交开户落在同一处；从邀请链接发起的回调完成后回到该邀请的路径。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens the account face and keeps the provider address as a contact`（社交首登开户落在租户区首页）；`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: a just-in-time provider asks for a username before it opens an account`（企业即时开通开户落在租户区首页）；`starters/demo/web/src/pages/auth-callback-page.test.tsx` `lands a viewer home when the provider detour started elsewhere`（回调落在租户区首页）、`returns a viewer who left an invitation link to that landing`（从邀请链接发起的回调回到该链接）

### REQ-signup-054

配置为即时开户的企业身份提供方必须声明确定用户名的策略：从提供方声明映射，或在开户前由用户确认；系统不得在用户名未确定时建立账户。

- **验收**：声明映射的提供方从允许的提供方声明取值并规范化为合法用户名，缺失、不合规或与已用名称冲突时拒绝登录并提示改用开户前确认；声明开户前确认的提供方在账户建立前要求用户确认用户名，确认后不可变；预置账户提供方不需要该策略；未声明策略的即时开户提供方不能被启用。
- **状态**：未实现
- **端到端测试**：未实现

### REQ-signup-029

每个用户账户在账户建立时必须得到一个个人租户。

- **验收**：用户账户建立后立即得到一个个人租户，并持有其非内建 `tenant-admin` 角色；邮箱是否已证明、是否已属于其他组织都不影响这一结果。
- **状态**：未实现
- **端到端测试**：未实现

### REQ-signup-040

已有其他成员关系不得阻止个人租户的建立。

- **验收**：一个已是某一租户成员的账户在建立时同样得到一个个人租户，其原有成员关系不变。
- **状态**：未实现
- **端到端测试**：未实现

### REQ-signup-031

系统必须只对最后一次登录（从未登录时取建立时间）距今超过部署设定的闲置期限、未验证且没有任何已证明邮箱的自助注册账户执行清理；闲置期限缺省为 720 小时，取 0 时关闭清理；管理员预置与导入的账户永不因未验证被清理。

- **验收**：让未验证自助注册账户的最后登录时间或建立时间距今超过闲置期限，该账户被清理；刚刚活动过的同类账户不被清理；管理员预置或导入的账户即使未验证也不在清理范围内；闲置期限取 0 时没有账户因未验证被清理。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-048

社交首登开户把提供方声明为已验证的地址写成该账户的**联系方式**（默认通讯邮箱）**与登录方式**：建号时账户只有这一条邮箱地址，按 REQ-account-068 它同时被选作登录方式，因此该地址可以作口令登录名。该地址在开户页面上只读展示，持有人不编辑它。企业即时开通不写入提供方声明的地址：企业预置账户是运营者建好的，即时开通没有展示该地址的页面。两种路径下账户取自提供方的资料都只有显示名与该地址的上述处置。提供方没有给出显示名时，账户的显示名取用户名（REQ-signup-051）。开户成功后的落点按 REQ-signup-053 的规定。

- **验收**：经社交首登新开立的账户持有提供方声明为已验证的那个地址作为联系方式，默认通讯邮箱是它，登录方式中也有它，且它可用作口令登录名；开户页面上该地址只读，无法编辑；企业即时开通新开立的账户不持有提供方声明的地址；两种路径的显示名都等于提供方给出的显示名；提供方未给出显示名时账户仍建立，显示名按 REQ-signup-051 的规定取用户名。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens the account face and keeps the provider address as a contact`

### REQ-signup-051

首登开户的显示名必须只来自一个来源：短信首登开户的开户一步必须接受当事人选填的显示名，社交首登开户与企业即时开通必须取提供方给出的显示名；任何一条首登开户路径没有给出显示名时，账户的显示名必须取用户名。显示名的长度上界为 100 字符：首登开户路径对超过上界的值截断到 100 字符而不是拒绝，账户资料的显示名改名则按 REQ-account-032 拒绝超过上界的值。空值取用户名这一回退同样适用于没有给出显示名的社交首登开户与企业即时开通。

- **验收**：短信首登的这一步按用户名、可选显示名、口令与确认口令的顺序呈现；填写显示名时建立的账户持有填写的值，超过 100 字符时持有前 100 字符；留空时建立的账户显示名等于本次选定的用户名。社交首登与企业即时开通在提供方给出显示名时账户持有该值，超过 100 字符时持有前 100 字符；提供方未给出显示名时账户仍建立，显示名等于用户名。同一条路径不同时采用当事人填写的值与提供方的值。超过 100 字符的显示名在首登开户中不导致建号失败，而在账户资料改名时按 REQ-account-032 被拒绝。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`（填写显示名）、`sms-signup: a phone-code first sign-in with no display name takes the username`（留空取用户名）；`pkg/authn/login/internal/resolve/display_name_test.go` `TestAPhoneSignupDisplayNameIsMadeFitToStore`（截断到 100 字符）、`TestAFirstLoginWithoutADisplayNameIsNamedAfterItsUsername`（短信、社交与企业即时开通三条路径的回退）；`pkg/authn/login/internal/resolve/social_test.go` `TestSocialOpenSignupCreatesAnEnabledUserWithTheDeclaredAddressAsAContact`（社交提供方的显示名截断到 100 字符）；`pkg/authn/login/internal/resolve/provision_test.go` `TestProvisionerNameIsCutLikeTheProvidersOwn`（企业即时开通的显示名截断到 100 字符）；`pkg/authn/account/accounts_test.go` `TestAccountFieldsAreWrittenOnTheAccountThatIsThere`（账户改名的超界值被拒绝）

### REQ-signup-049

自动绑定不再作为系统行为存在：设置 `authn.account.auto-link` 不再存在，社交登录与企业即时开通不得按提供方声明的地址接管一个现有账户或把该外部身份并入该账户。提供方声明为已验证的地址（企业提供方以其已验证域内的地址替代该声明）命中现有账户已登记的邮箱地址时，社交登录与企业即时开通按 REQ-signin-017 报告需先绑定；不命中且开户开放时，按 REQ-signup-048 建立账户：社交首登写入提供方显示名、把该地址写为联系方式并取得用户名与口令，企业即时开通写入提供方显示名。提供方未声明该地址已验证、且其域也不属于已验证域时，按不命中处理。

- **验收**：配置中不再有自动绑定项；命中现有账户时登录不进入该账户、不建立绑定、不另开账户，得到需先绑定的拒绝；不命中且开户开放时先取得合法用户名（社交首登另取口令），再建立并登录新账户：社交首登的账户以联系方式持有该地址，并按 REQ-account-068 同时把它选作登录方式，企业即时开通的账户不持有该地址；当事人以自己的登录方式登录并在账户面显式绑定后，绑定才建立。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens the account face and keeps the provider address as a contact`、`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a Google email already on an account is refused with link-required`

### REQ-signup-050

系统不得以迁移改变既有数据的行为：旧行为写下的账户、邮箱标识、手机标识与外部身份绑定继续按写入时的方式工作，任何既有行的可见行为不变。承载登录方式、联系方式与默认联系地址的新列由一次性回填按旧行为补齐——旧行为写下的标识置为可登录，邮箱标识另置为联系方式，每个已有已证明邮箱的账户把最早证明的一封置为默认联系地址；回填只写这些新列，不改写验证时刻、验证渠道与取值等既有事实列。导入按源记录设置登录方式与默认联系地址，登记的邮箱与手机一律读作联系方式。本文与相关能力的新规则只作用于新的登录与新的写入；联系方式、登录方式与默认联系地址的规则见 REQ-account-061 至 REQ-account-066。

- **验收**：旧行为写下的邮箱标识、手机标识与外部身份绑定在新规则下仍可用于其原本可用的登录，并仍按旧行为读作联系方式、持有原有的默认联系地址；一次性回填只写新增的标记列，既有行的验证时刻、验证渠道与取值保持不变，行本身不被删除，可见行为不变；导入的账户按源记录取得登录方式与默认联系地址，登记的邮箱与手机读作联系方式；新开立的账户与新的写入遵守新规则。
- **状态**：已实现
- **端到端测试**：未实现（迁移回填与导入由 `pkg/authn/account/identifier_axes_test.go` `TestTheIdentifierAxesMigrationBackfillsExistingRows`、`TestAnImportWritesTheAxesItsSourceRecordHad` 覆盖）

