# 账户安全

## 问题陈述

账户在第一因素之外还需要第二重证明，敏感操作前需要重新证明身份，账户主人需要看清哪些设备正在使用账户并能把它们终止，凭据丢失时需要一条可预期的找回路径。本能力规定这些行为对使用者的可见结果。

## 范围

本能力覆盖两步验证方式的登记、查看与移除，登录时的第二因素挑战，敏感操作的重新验证，会话的查看、逐条撤销与全部撤销，会话期限，撤销后的效果，以及凭据丢失的找回路径：经本人已证明邮箱或手机的匿名口令重置、管理员重置口令与运维接口。

### 不在范围内

- 第一因素登录方式本身的识别与流程（口令、短信登录、社交登录、企业登录、通行密钥登录），归登录能力。
- 口令、邮箱、手机号、本人资料与偏好的自助更改细节，归账户资料能力；本能力只规定这些动作在强度不足时如何被拦下。
- 平台运营者对账户、租户、授权与设置的其他业务管理面；本能力只规定运营者重置任意账户的两步验证方式，以及重置因素与发起冒充所需的强度与因素年龄要求。

## 参与者

- **账户主人**：已登录的账户持有人，可查看与撤销自己的会话、登记与移除自己的两步验证方式、发起需要重新验证的敏感操作。
- **平台运营者**：持管理面权限的员工，可重置任意账户的两步验证方式；发起重置与冒充前自身必须满足强度与因素年龄要求。
- **已完成第一因素的访客**：正在登录过程中、尚未建立已认证会话的账户持有人，需要完成第二因素或强制登记。

## 功能需求

### REQ-security-001

系统必须让账户查看自己已登记的两步验证方式清单，逐项给出种类、标签与状态；并列出本部署装配的每一种两步验证方式，未登记的方式给出登记入口。

- **验收**：账户页的两步验证节为账户已登记的每一种方式列出其状态（已启用、待确认、已停用），并按种类列出本部署装配的每一种方式，已登记的显示状态、未登记的给出登记入口；部署没有装配任何方式时才显示空态，而不是错误。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-security-002

系统必须要求身份验证器应用的登记在提交一个当前有效的一次性码之后才生效。

- **验收**：登记向导给出一次性设置密钥；在提交有效码之前，该方式保持待确认且不能用于登录；提交有效码后转为已启用；提交错误码时保持待确认并可重试。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-003

系统必须允许账户登记通行密钥作为两步验证方式。

- **验收**：在支持通行密钥的浏览器上完成设备仪式后，该方式出现在清单中且状态为已启用；浏览器不支持、用户取消或仪式失败时，清单保持不变并给出对应提示，可重试。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-security-004

系统必须允许账户登记短信验证码作为两步验证方式。

- **验收**：持有已验证手机号的账户看到短信入口，确认登记后该方式立即转为已启用；没有已验证手机号时登记被拒绝并提示先添加并验证手机号。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-005

系统必须只把部署实际装配的两步验证种类作为登记入口提供。

- **验收**：部署没有装配的种类不在账户页出现登记入口，账户也无法通过界面发起该种类的登记；装配的种类各自出现一个入口。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-006

系统必须允许账户移除自己已启用的两步验证方式。

- **验收**：确认移除后，该方式不再出现在清单中，也不再被登录或重新验证使用。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-007

当账户被要求必须使用多因素时，系统必须拒绝移除其最后一个已启用的两步验证方式。

- **验收**：对这类账户移除唯一方式被拒绝并给出说明，清单保持不变；未被要求必须使用多因素的账户不受这一限制。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-008

登录时，账户已登记至少一个已启用的两步验证方式，系统必须在第一因素通过后要求再通过其中一个方式。

- **验收**：这类账户的登录停在第二因素步骤，未通过时不建立已认证会话；通过后进入应用；可选方式是该账户已登记且已启用、本部署有实现、且通道不同于第一因素的那一组。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-security-009

系统必须要求第二因素使用的通道与本次登录第一因素已使用的通道不同。

- **验收**：与第一因素同通道的两步验证方式不出现在可选清单中；可选清单为空时该次登录无法完成，并提示登录失败，而不是放行。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-010

系统必须让登录期间登记的两步验证方式不计入本次登录会话的强度。

- **验收**：在一次会话内刚完成登记后，该会话仍按单因素处理，敏感操作仍要求重新验证；使用该方式进行下一次登录之后才得到多因素会话。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-011

系统必须限制对第二因素的连续错误尝试。

- **验收**：连续提交错误码达到阈值后，退避期内的提交不再被校验并返回稍后重试的提示；退避结束后恢复接受提交；退避期内提交正确的码也不被接受。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-012

系统必须拒绝重复使用同一个有效窗口内已经被接受过的身份验证器一次性码。

- **验收**：同一个六位码在有效窗口内第二次提交被拒绝；等待进入下一个窗口后，新生成的码可以正常使用。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-013

会话不具备该账户要求的认证新鲜度时，系统必须拒绝以下写操作并要求重新验证身份：更改口令、选作或取消登录方式、设置默认联系地址、删除标识、增删两步验证方式、绑定或解绑外部身份。添加用户名是新增一条登录名，同样要求新鲜认证并在成功后结束该账户的其他会话。登记一条联系方式（发送验证码）与完成该联系方式的验证本身不要求新鲜认证，也不轮换会话或结束其他会话；但验证改变账户据以证明或找回自己的事实时要求新鲜认证：一条会取代账户现有默认联系地址、或成为其唯一邮箱地址（因而成为自动登录方式）的邮箱被证明，以及一个会取代某个已启用短信两步验证方式发码目标的新手机号被证明，都要求新鲜认证，会话不具备时被拒。账户有已启用的两步验证方式时，要求的认证新鲜度是新鲜的本地多因素；没有已启用的两步验证方式时，要求的认证新鲜度是以本会话建立时使用的第一因素通道完成的新鲜证明。

- **验收**：改口令、添加用户名、把邮箱或手机选作或取消登录方式、设置默认联系地址、删除标识、新增或移除两步验证方式、绑定或解绑外部身份等操作，在会话不满足新鲜度时被拒绝并显示重新验证入口；满足时操作成功，例如改口令后新口令可用于下一次登录而旧口令被拒绝。会话不满足新鲜度时，验证一条不会取代默认联系地址、不是账户唯一邮箱地址、也不取代已启用短信因素目标的邮箱或手机仍然成功，当前会话不轮换，账户的其他会话不被结束；验证一条将成为默认联系地址或账户唯一邮箱地址的邮箱、或一个将取代已启用短信因素目标的新手机号时，被拒绝并显示重新验证入口。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-security-014

重新验证成功后，系统必须自动重放此前被拒的那次操作恰好一次。

- **验收**：重新验证成功后，原先被拒的操作执行并得到成功反馈；取消重新验证时原操作保持未执行，且之后不自动重放。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-security-015

重新验证必须接受一种在本会话认证之前已启用、且通道不同于本会话已证明通道的两步验证方式。

- **验收**：在本会话期间刚登记的方式不出现在重新验证的可选清单中；在本会话认证之前启用的另一通道方式可以被选中并完成升级。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-impersonation-for-support.spec.ts` `impersonation-for-support: the operator acts as a member and the record and audit trail survive the end`

### REQ-security-016

账户没有任何已启用的两步验证方式时，系统必须允许以本会话建立时使用的第一因素通道当场重新证明身份。

- **验收**：口令登录建立的会话可以用口令重新证明，短信登录建立的会话可以用短信码重新证明；重新证明成功后原先被拒的操作可以重放。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-017

重新验证无法完成时，系统必须给出重新登录的出口，而不是停在无法推进的步骤。

- **验收**：账户有因素但没有能强化本会话的方式时，界面提示无法完成升级并提供重新登录；会话由社交或企业登录建立、第一因素通道无法在本会话重新证明时，同样给出重新登录出口且不发起验证步骤。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-018

系统必须要求以下最高权限动作的发起者名下存在一个已启用达到部署在运行期设置里声明的因素成熟时长的非短信两步验证方式：重置他人两步验证方式、重置他人口令、发起冒充。

- **验收**：把因素成熟时长设为一个正时长后，启用时长不足该时长的账户发起冒充、重置他人因素或重置他人口令时被拒绝并提示需要等待；启用时长超过该时长后可以执行；重新验证不能缩短这一等待。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-019

系统不得提供恢复码作为两步验证的找回凭据。

- **验收**：账户设置与登录面都没有生成、查看或使用恢复码的入口；登记两步验证方式不产生任何一次性恢复码。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-020

系统必须让忘记口令的账户本人在未登录状态下，凭一条本人已用验证码证明过所有权的邮箱或手机收到的验证码设定新口令；本人选择用哪一种。该选择只看所有权，与该值是否为默认联系地址或是否作登录方式无关。

- **验收**：账户同时有已证明邮箱与已证明手机时，口令重置面提供这两种目标供选择；只有一种时只提供那一种；提交其中一种并提交正确的验证码后，可以设定新口令；随后用新口令登录成功，旧口令不再可用。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-password-reset-email.spec.ts` `password-reset-email: an addressed code resets the password and ends the old sessions`；`starters/demo/web/e2e/specs/journey-password-reset-sms.spec.ts` `password-reset-sms: a phone-code reset writes a new password and ends the old sessions`

### REQ-security-021

平台运营者必须能重置任意账户的全部两步验证方式。

- **验收**：重置后该账户的全部方式失效且其会话全部结束；该账户的下一次登录仍须先通过第一因素，然后只能登记新的方式，重置本身不产生已认证会话。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-022

账户必须能查看自己的活跃会话列表，含设备摘要、地址、最近活动时间与当前会话标记。

- **验收**：会话节逐行显示设备摘要、地址、最近活动与操作，当前会话被标记；没有其他会话时显示空态；读取失败时显示错误与重试。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`（该 spec 驱动设备摘要与当前会话标记）

### REQ-security-023

账户必须能撤销自己的单个会话。

- **验收**：撤销自己的某条会话后，该会话不再可用，列表随之收敛。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-security-031

账户不得能撤销不属于自己的会话。

- **验收**：尝试撤销不属于自己的会话被拒绝，且应答不泄露该会话是否存在。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-024

账户必须能一次撤销除当前会话之外的全部其他会话。

- **验收**：确认后，该账户在其他设备上的会话全部结束，当前会话保持可用，列表只余当前会话。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-025

被撤销的会话在任何需要登录的请求上必须被拒绝；匿名面的请求不受影响。

- **验收**：在被撤销的设备上发起任何需要登录的请求得到未认证应答；匿名面请求不因该撤销被拒。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-offboarding.spec.ts` `account-offboarding: an operator disables a member and the member returns after the restore`

### REQ-security-032

被撤销会话的当事人必须被引导重新登录。

- **验收**：被撤销的设备上，界面进入会话结束屏并提供重新登录入口；当前设备不受影响。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-026

会话必须同时受空闲期限与绝对期限约束，任一到期后不得再用于请求。

- **验收**：空闲超过空闲期限，或自登录起超过绝对期限后，下一次请求被拒绝并引导重新登录；空闲期限与绝对期限由部署调整，用户无需逐个管理。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-027

浏览器会话必须在整页刷新后保持。

- **验收**：在已登录状态下刷新任意页面，仍处于已登录状态，不出现登录面。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-028

系统不得提供跨浏览器关闭保持登录的「记住我」。

- **验收**：关闭并重新打开浏览器后需要重新登录；界面没有「记住我」选项。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-029

改口令、新增登录方式、登记两步验证方式、绑定或解绑外部身份成功后，系统必须结束该账户的其他会话。登记并验证一条联系方式（邮箱或手机）不在此列：它不新增登录方式，也不结束会话。

- **验收**：改口令、新增登录方式、登记两步验证方式、绑定或解绑外部身份这几种操作中任一成功后，该账户在其他设备上的会话在下一次请求被拒绝，当前设备保持登录；登记并验证一条邮箱或手机成功后，账户在其他设备上的会话保持有效。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-security-033

移除两步验证方式成功后，系统必须结束该账户的其他会话。

- **验收**：移除任一已启用的两步验证方式成功后，该账户在其他设备上的会话在下一次请求被拒绝，当前设备保持登录。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-030

账户必须能从当前设备退出登录。

- **验收**：退出后当前会话的下一次请求被拒绝，界面回到未登录状态。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-security-034

系统必须只接受账户本人已用验证码证明过的邮箱或手机作为口令重置的依据，并且不因提交的邮箱或手机是否属于一个账户而改变界面的可见应答。

- **验收**：未证明的邮箱或手机、从未与本产品关联的邮箱或手机、已停用或已删除账户的邮箱或手机、以及没有口令的账户，提交后都不会收到消息；界面给出的状态、提示与「验证码已发送」的合格情形相同；提交任意错误的验证码得到同一条拒绝提示。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-password-reset-email.spec.ts` `password-reset-email: an addressed code resets the password and ends the old sessions`；`starters/demo/web/e2e/specs/journey-password-reset-sms.spec.ts` `password-reset-sms: a phone-code reset writes a new password and ends the old sessions`

### REQ-security-035

系统必须在口令重置完成后不建立已认证会话，并且不改变账户已登记的第二因素；下一次登录仍要求先通过第一因素、再通过该账户原本要求的第二因素。

- **验收**：重置成功后回到登录面而不是进入账户区；用新口令登录时，登记了第二因素的账户仍停在第二因素；重置前后，账户页列出的第二因素清单不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-036

系统必须在口令重置成功后结束该账户已有的全部浏览器会话与令牌会话。

- **验收**：重置前在另一台设备上保持登录，重置后该设备的下一次请求被要求重新登录；重置完成后不存在由重置本身建立的会话。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-password-reset-email.spec.ts` `password-reset-email: an addressed code resets the password and ends the old sessions`；`starters/demo/web/e2e/specs/journey-password-reset-sms.spec.ts` `password-reset-sms: a phone-code reset writes a new password and ends the old sessions`

### REQ-security-037

系统必须在部署未显式提供口令重置能力时，不向任何访客提供该能力；提供与否只由部署决定。

- **验收**：缺省部署的登录面没有「忘记口令」入口，直接进入口令重置面得到「本部署不提供该方式」的可见结果；部署显式提供后，入口与重置面出现。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-security-038

租户要求成员经该租户的企业单点登录认证时，系统必须拒绝未满足该要求的会话在目标租户内发起的、经租户准入的操作，并引导成员经该租户的企业单点登录重新认证；该拒绝必须与本地两步验证方式的加强认证分开呈现。

- **验收**：租户启用该要求后，以本地第一因素建立会话的租户管理员在目标租户的邀请与成员页发起一次邀请时，界面显示点名企业单点登录的拒绝与重新登录出口，不打开本地加强认证对话框、不列出本地两步验证方式；该管理员经该租户的企业身份提供方重新登录后重做这次邀请成功，被邀请地址收到邀请。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-sso-required.spec.ts` `tenant-sso-required: a member re-invites a colleague through the tenant's enterprise SSO after the tenant demands it`

### REQ-security-039

第一因素本身已证明两类因素（带用户验证的通行密钥断言）时，视为已满足 `REQ-security-008` 的「第一因素通过后再通过一个两步验证方式」要求，不再要求第二因素；登录断言未带用户验证的通行密钥不得作为第一因素完成登录。

- **验收**：带用户验证的通行密钥断言完成登录时，登录不再要求第二因素，直接进入应用；未带用户验证的通行密钥断言被拒绝，登录不成功。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-passkey-user-verification.spec.ts` `passkey-user-verification: a user-verifying passkey signs in without a second factor`

### REQ-security-040

账户在任何时刻都必须保留至少一个非通行密钥的登录方式：可用的口令登录名、被选作登录方式的已验证手机号或外部身份之一。删除会让账户不再持有其中任何一种的操作必须被拒绝。用户名不可删除、不可更换（REQ-account-070），口令登录名中可被删除的只有被选作登录方式的已验证邮箱。

- **验收**：删除最后一个被选作登录方式的已验证邮箱、被选作登录方式的手机号或外部身份，或管理员重置因素，会使账户不再持有可用的口令登录名、被选作登录方式的已验证手机号与外部身份中的任何一种时，操作被拒绝并说明需要先补一种登录方式；账户仍持有其中任何一种时操作成功。删除用户名不在本规则范围内，一律被拒绝（REQ-account-070）。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-last-login-way.spec.ts` `last-login-way: removing the last way that is not a passkey is refused`

### REQ-security-041

系统在口令重置中必须只以账户对邮箱或手机的所有权为依据；一个值是否为默认联系地址或是否被选作登录方式，既不放宽也不收紧它作为重置目标的资格。

- **验收**：一条已验证但未被选作登录方式、也未被设为默认联系地址的邮箱仍能收到重置验证码并完成重置；一条未验证的邮箱或手机即使被列为联系方式也不能作为重置目标。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-password-reset-email.spec.ts` `password-reset-email: an addressed code resets the password and ends the old sessions`
