# 自助注册

## 问题陈述

新访客需要一个不依赖既有账户的建号入口，来源包括登录页的口令自助注册、短信首登开户、社交与企业登录首登开户；其中口令自助注册既不需要既有账户，也不以接收短信为前提。建号之后，账户在完成邮箱验证前可以使用一个封闭集内的能力，依赖可信邮箱的能力在本人证明邮箱之后放开。

## 范围

覆盖口令自助注册、短信首登开户、社交与企业登录首登开户、注册开关、注册成功后触发的自动登录与落点、注册后的邮箱验证门控与未验证账户的闲置清理，以及注册各入口对用户名与邮箱占用的披露规则。

### 不在范围内

登录方式的取舍、退避与限流、会话与令牌的期限与刷新等通用登录规则、账户资料的日常维护、成员与权限的日常管理、个人租户除注册落点与授予条件之外的规则、邮件与短信载体的实现。

## 参与者

- 新访客：没有会话，不能访问任何租户资源；可以到达登录与注册面、社交与企业回调落点、邀请落地。
- 已有账户持有人：持有会话或可以重新登录的账户持有人，邮箱可能已验证或未验证；可以用一个提供方声明已验证的邮箱发起社交登录。
- 管理员：在管理面预置或导入账户的账户持有人。
- 待验证账户：已登录的账户持有人，账户持有邮箱而没有任何一条已证明归属的邮箱；可以执行 REQ-signup-024 列明的动作，集合外的动作被拒绝。

## 功能需求

### REQ-signup-001

系统必须在匿名可读的登录方式发现面报告「开放注册」；「开放注册」在首登开户或口令注册任一开放时为真。

- **验收**：匿名读取发现面，首登开户或口令注册任一开放时「开放注册」为真，两者都关闭时为假。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-032

系统必须在同一匿名可读的登录方式发现面单独报告「开放口令注册」，其取值等于口令注册开关。

- **验收**：口令注册开关开放时「开放口令注册」为真，关闭时为假，且与「开放注册」的取值在同一应答中分别可见。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-002

登录面注册入口的呈现条件按 REQ-signin-005 的规定。

- **验收**：按 REQ-signin-005 的验收判定。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-003

口令注册开放时，注册模式必须在可开户的登录方式之外提供一张口令注册表单。

- **验收**：口令注册开放时，注册模式同时呈现口令注册表单与各可开户的登录方式；口令注册关闭时，注册模式不呈现口令注册表单。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-004

口令注册必须要求用户名、邮箱与口令同时提供；缺任一项的提交必须被拒绝且不建号。

- **验收**：分别缺用户名、缺邮箱、缺口令的提交各被拒绝，且没有账户产生；三者齐备且合法的提交建号。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-033

口令注册必须接受显示名、语言与时区作为可选输入，并在提供时把它们保存为该账户的偏好；不提供时注册照常完成。

- **验收**：只提交用户名、邮箱与口令的注册成功；额外提交显示名、语言与时区的注册成功，读回的资料与偏好为提交的值。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-005

注册必须在一次提交内建立一个启用的账户。

- **验收**：注册成功后该账户存在且处于启用状态，可以用用户名与口令走登录流程并登录。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-034

注册应答不得建立会话。

- **验收**：注册成功后立即读取会话得到匿名，任何租户资源均不可达；此后以同一份凭据完成一次口令登录才会话建立。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-006

注册不得发送任何邮件。

- **验收**：在装配了可观察替身邮件通道的部署上完成一次注册，该替身收到的消息数为零；实现改为发信时该判据失败。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-007

注册提交的邮箱在本人证明之前不得成为登录名，也不得被另一个账户注册。

- **验收**：注册刚完成时以该邮箱作口令登录名登录失败；另一账户以同一地址注册得到邮箱占用冲突；本人完成邮箱验证后该邮箱方可作登录名。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-008

用户名已被占用时，注册必须以显式的用户名占用冲突拒绝，且不产生任何账户。

- **验收**：用一个已存在账户的用户名注册，得到用户名占用冲突；该用户名仍属于原账户，没有新账户产生。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-009

邮箱已被占用时，注册必须以显式的邮箱占用冲突拒绝，并整体回滚开户，不留下账户、用户名或口令凭据。

- **验收**：用一个已被其他账户持有、或正由其他账户待证明的邮箱注册，得到邮箱占用冲突；该次提交使用的用户名随后可以被另一次注册使用，且没有新账户产生。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-010

注册成功后，系统必须以同一份凭据自动发起一次口令登录。

- **验收**：注册成功且凭据可用时会话建立，进入登录态。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-035

自动登录被拒时，系统必须退回登录表单并预填本次注册的用户名。

- **验收**：令自动登录被拒，界面停在登录表单，用户名已预填为本次注册所用值。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-036

自动登录被拒后的回退路径不得重发注册请求。

- **验收**：退回登录表单后，本次提交只产生一次注册请求，账户仍然只有一份。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-011

口令注册开关关闭时，注册必须以「注册已关闭」拒绝，且不建号。

- **验收**：口令注册开关为关闭时提交注册得到「注册已关闭」拒绝；随后不存在该用户名对应的账户。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-012

短信首登开户开放时，未登记的手机号在通过短信验证后必须隐式建立一个账户并完成登录。

- **验收**：未登记的手机号走完短信登录即得到一个账户，且会话处于已登录状态。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-signup-013

短信首登开户关闭时，未登记的手机号不得建立账户，登录以失败应答；该应答与已登记号码的失败应答一致，泄露规则按 REQ-signin-014 的规定。

- **验收**：关闭时未登记号码完成短信验证后没有账户产生，且得到与已登记号码的错误验证码相同的失败应答。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-014

短信隐式开户产生的账户必须没有用户名也没有口令，并必须被引导到完成账户设置，补上用户名与口令。

- **验收**：开户后该账户没有任何可作登录名的标识，也没有口令；完成设置页提供补上用户名与口令的入口，登录名补齐之前口令设置不可用；补齐之后用户名与口令都能用于口令登录。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

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

社交登录首登开户开放、且提供方声明已验证的邮箱不属于任何现有账户时，首次社交登录必须建立一个账户，并把该邮箱作为该账户已验证的邮箱标识。

- **验收**：用一个可开户的社交方式首次登录后账户存在；提供方声明该邮箱已验证时，账户的邮箱标识为提供方声明的地址、证明渠道为对应提供方；提供方未声明邮箱已验证时不写入邮箱标识。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens an account with the provider-verified email`

### REQ-signup-043

每一个账户必须持有系统分配的账户标识：它在账户建立时生成，与登录标识、外部身份标识都无关，此后不因账户资料或外部账号资料的变化而改变。

- **验收**：分别经口令自助注册、短信首登、社交首登、企业即时开通与邀请接受建立账户，每个账户都得到非空、两两不同、且不等于本次开户所用外部身份标识的账户标识；更改显示名、邮箱或外部账号资料后，账户标识保持不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-044

使用社交账户创建账户时，系统必须补齐账户建立所必需的内容，不得留下以外部身份标识充当账户标识、或缺少内容的记录；提供方没有给出已验证邮箱时，账户仍以系统标识完整存在。

- **验收**：首次社交登录建出的账户持有非空、且不同于该外部身份标识的账户标识；提供方未声明已验证邮箱时，账户没有邮箱标识，但仍能经该社交身份登录并在账户页读到本人资料；提供方声明已验证邮箱的情形按 REQ-signup-017 的规定。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-018

企业 SSO 首登开户必须按提供方声明的开通方式处理：预置开通按 REQ-signin-021 的规定，即时开通按 REQ-signin-022 的规定。

- **验收**：预置开通按 REQ-signin-021 的验收判定；即时开通按 REQ-signin-022 的验收判定；提供方声明的开通方式不是这两种之一时登录失败且不建户。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`（仅驱动预置开通；即时开通与非法开通方式由后端测试覆盖）

### REQ-signup-019

自动绑定关闭时，已存在的账户用同一邮箱社交登录必须按 REQ-signin-017 的规定拒绝，不得接管或合并该账户。

- **验收**：自动绑定为关闭时，该场景得到「请先绑定」的拒绝；原账户的登录方式与凭据保持不变，没有新账户产生。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a Google email already on an account is refused with link-required`

### REQ-signup-020

只有当提供方证明邮箱归属、该邮箱命中现有账户、且自动绑定关闭时，系统才给出「请先绑定」的拒绝；提供方自称未验证的邮箱不得触发该拒绝。

- **验收**：
  - 提供方声明已验证、该邮箱命中现有账户、自动绑定关闭：得到「请先绑定」拒绝，不登录该账户，不建新账户。
  - 提供方声明已验证、该邮箱不命中任何账户、注册开放：建立并登录一个新账户。
  - 提供方声明已验证、该邮箱不命中任何账户、注册关闭：登录失败，不建账户。
  - 提供方声明未验证：任何情况下都不出现「请先绑定」拒绝；注册开放时按无邮箱线索建户并登录，注册关闭时登录失败。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-042

自动绑定取「已验证邮箱」时，提供方声明已验证的邮箱属于现有账户、该邮箱由提供方证明归属、账户处于启用状态、且该邮箱所在域没有租户级的登录提供方时，系统必须把该社交身份绑定到该账户并以其登录，不新建账户；任一条件不满足时按 REQ-signin-017 的规定拒绝；微信身份一律不自动绑定。

- **验收**：自动绑定为「已验证邮箱」时，满足上述条件的首次社交登录在该账户上完成，账户数不变；任一条件不满足时得到「请先绑定」拒绝；微信身份即使满足其余条件也不自动绑定。
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
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-038

落在邮箱验证节时，系统必须自动发送一次验证码，且本次进入不重复发送。

- **验收**：落点出现时目标邮箱收到一条验证码；停留在该页不触发第二次发送，重发需手动触发。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-039

注册完成后的落点与个人租户特性是否启用无关。

- **验收**：启用与不启用个人租户的两种装配下，注册完成后的落点都是账户页的邮箱验证节。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-signup-029

启用个人租户的部署中，没有任何其他成员关系的账户必须得到一个个人租户。

- **验收**：在启用个人租户的部署中，没有任何成员关系的账户得到一个个人租户，并持有其所有者角色；邮箱是否已证明不影响这一结果。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-040

已有其他成员关系的账户不得得到个人租户。

- **验收**：一个已是某一租户成员的账户请求个人租户时，没有新的租户产生，其成员关系不变。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-signup-031

系统必须只对最后一次登录（从未登录时取建立时间）距今超过部署设定的闲置期限、未验证且没有任何已证明邮箱的自助注册账户执行清理；闲置期限缺省为 720 小时，取 0 时关闭清理；管理员预置与导入的账户永不因未验证被清理。

- **验收**：让未验证自助注册账户的最后登录时间或建立时间距今超过闲置期限，该账户被清理；刚刚活动过的同类账户不被清理；管理员预置或导入的账户即使未验证也不在清理范围内；闲置期限取 0 时没有账户因未验证被清理。
- **状态**：已实现
- **端到端测试**：未实现

