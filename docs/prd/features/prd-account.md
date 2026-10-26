# 账户自助

## 问题陈述

已认证的账户持有人要在不借助管理员的前提下维护自己的账户：资料与偏好、登录名、邮箱与手机、口令、社交账号。邮箱尚未验证的账户还要在同一处完成验证，以解除账户上的限制。

## 范围

本人资料的读取与显示名修改，语言与时区偏好，登录标识的列出、添加与删除，邮箱与手机的添加、验证与更换，邮箱验证状态与验证节，改口令，社交账号的绑定与解绑，完成设置页，首页的一次性完成设置提示与应用栏之下的持续验证提示。

### 不在范围内

会话与设备的自助列出与撤销界面、两步验证的登记与移除、外观主题预设的选择、账户注册与登录方式的选择、管理面对他人账户的读写、账户的停用与删除。这些能力各有自己的需求文档。

## 参与者

- **账户持有人**：持有有效会话的终端用户。只能读改本人账户，不能经本能力的任何入口读写他人账户；安全类写的重新验证要求按 REQ-security-013 的规定。
- **邮箱未验证的账户持有人**：邮箱验证状态为未验证的账户持有人。在本能力内可读写本人资料、修改口令、添加并验证邮箱或手机，不因邮箱未验证被拒绝；其余动作的允许与禁止按 REQ-signup-024 与 REQ-signup-025 的规定。
- **冒充中的管理员**：经冒充到达本人资料面的发起者。对本能力的写一律被拒绝，读被映射到被冒充的账户。

## 功能需求

### REQ-account-001

系统必须允许已认证的账户持有人读取本人的账户资料，读取结果至少包含显示名、语言、时区、登录标识及其归属状态、已绑定的社交账号与邮箱验证状态；读取的账户只由发起者身份决定，不由请求携带的标识决定。

- **验收**：已登录用户打开账户页，读到的显示名、偏好与标识均属于本人；换另一个已登录账户读取，得到该账户自己的资料；请求中不携带账户标识时读取仍成功；请求携带另一个账户的标识时，返回的仍是发起者本人的资料。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-002

系统必须允许账户持有人修改本人的显示名。

- **验收**：提交新的显示名并保存后，重新读取账户资料得到新值。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-031

系统必须在显示名保存成功后让账户页与账户菜单反映新的显示名。

- **验收**：保存新的显示名后，账户页的显示名与账户菜单的触发标签都显示新值，无需重新登录。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-032

系统必须拒绝超过长度上界或不是合法文本的显示名，且拒绝时不改变已保存的显示名。

- **验收**：提交超过 100 字符的显示名得到拒绝提示，重新读取仍为旧值；提交不是合法文本的显示名同样被拒绝且不生效。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-003

系统必须允许账户持有人把语言保存为本人偏好；保存后界面按该语言呈现。

- **验收**：选择一种语言并保存后，重新读取账户资料得到该语言，同时界面语言切换为该语言。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-personalize-interface.spec.ts` `personalize-interface: the chosen language and time zone take effect and survive a reload`

### REQ-account-033

系统必须允许账户持有人把时区保存为本人偏好；账户内时间按保存的时区显示。

- **验收**：选择时区并保存后，重新读取账户资料得到该时区，账户内时间按该时区显示。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-personalize-interface.spec.ts` `personalize-interface: the chosen language and time zone take effect and survive a reload`

### REQ-account-034

系统必须允许账户持有人清空语言或时区偏好；清空某项偏好只清除账户偏好，不改变设备语言或设备时区。

- **验收**：清空语言偏好后账户侧为空而界面回落到设备语言；清空时区偏好后账户侧为空而时间显示按设备时区。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-035

请求未声明语言时，系统必须用账户保存的语言决定发给该账户的邮件及其它消息使用的语言；账户没有保存语言时使用部署的默认语言。

- **验收**：触发一条没有请求语言上下文的通知，其正文使用账户保存的语言；账户没有保存语言时该通知使用部署默认语言。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-004

除按 REQ-security-013 要求重新验证的拒绝外，账户偏好的写入失败时，系统必须提示保存失败并把控件退回已保存的值，不保留未保存的选择。

- **验收**：令一次偏好写入被服务端拒绝，界面出现失败提示，控件显示写入前的值，重新读取账户偏好与写入前一致。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-005

系统必须在冒充身份下拒绝修改被冒充账户的资料。

- **验收**：管理员经冒充进入账户页并提交显示名修改，得到拒绝，被冒充账户的显示名不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-036

系统必须在冒充身份下把资料读取映射到被冒充的账户，并让界面标明当前处于冒充状态。

- **验收**：管理员经冒充打开账户页，读到的显示名、偏好与标识属于被冒充账户；界面标明当前处于冒充状态。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-006

系统必须允许邮箱未验证的账户持有人执行本能力内属于 REQ-signup-024 允许范围的动作——读取与修改本人资料、修改口令、添加并验证邮箱或手机——不得因邮箱未验证而拒绝这些动作。

- **验收**：邮箱未验证的账户在账户页读取本人资料、修改显示名、修改口令、添加并验证一条邮箱，均成功。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-007

系统必须列出账户持有的全部登录标识，并对每条邮箱与手机标明归属是否已证明，取值只有「已验证」与「未验证」两种；用户名不标归属。

- **验收**：列表同时出现用户名、邮箱与手机三类行；邮箱与手机行只显示两种归属标记之一，用户名行不带该标记；经管理员或导入写下而未证明的邮箱显示为未验证。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-security-hardening.spec.ts` `account-security-hardening: an account enrols two factors, ends another device and changes its password through a step-up`

### REQ-account-008

系统必须允许账户持有人添加一个用户名作为登录名，写入后立即可用于登录。

- **验收**：无用户名的账户添加合法用户名后，列表中立即出现该用户名且可用于口令登录。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-account-037

系统必须让一个账户至多持有一个用户名；账户已有用户名时，界面必须不再提供添加用户名的入口。

- **验收**：已有用户名的账户在账户页看不到添加用户名的入口；经直接请求再添加一个用户名同样被拒绝，列表不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-038

系统必须拒绝格式不合或被其他账户占用的用户名。

- **验收**：提交已被其他账户占用的用户名得到冲突提示，列表不变；提交格式不合的用户名同样被拒绝。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-009

系统必须让添加邮箱的第一步向目标地址发送验证码并应答为待验证。

- **验收**：未被占用的新地址使界面进入输入验证码一步并收到验证码。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-039

地址已被其他账户持有或留有未证明预留时，系统必须在发码之前拒绝并明确告知地址已被占用，且不发送验证码。

- **验收**：地址已被另一账户持有，或已被另一账户以未证明预留占用时，得到地址已被占用的提示，停在地址输入一步且收不到验证码。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-040

部署未装配邮件通道时，添加邮箱必须不可用并提示。

- **验收**：没有邮件通道的部署提交添加邮箱得到暂不可用的提示，且不进入验证码一步。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-010

系统必须只在提交的验证码正确时把该地址写为已证明的邮箱标识，或把账户已持有的同值地址就地提升为已证明。

- **验收**：提交正确验证码后邮箱行变为已验证；账户已持有同值未证明邮箱时，提交正确验证码后该行就地变为已验证，列表不新增一行。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-041

验证码错误或过期时系统必须拒绝，并允许重新提交。

- **验收**：提交错误验证码得到拒绝，字段清空且可再次提交；提交过期验证码同样被拒绝且可重新发码。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-042

系统必须对同一验证码记录的尝试次数设上限，用尽后该码作废。

- **验收**：连续错误达到上限后，即使输入正确验证码也不再通过，必须重新发码。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-011

系统必须允许账户持有人删除本人持有的标识。

- **验收**：删除一条多余标识后列表不再出现该条。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-043

系统必须拒绝删除不属于本人或不存在的标识。

- **验收**：删除另一账户的标识或不存在的标识得到拒绝，本人标识列表不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-044

系统必须拒绝带口令的账户删除其最后一个用户名或邮箱，且删除不生效。

- **验收**：带口令账户只剩一条用户名或邮箱时删除该条被拒绝并说明至少需要一个登录名，列表保持不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-045

系统不得把最后登录名保护适用于删除手机。

- **验收**：带口令账户同时持有用户名与手机时删除手机成功，即使它是账户唯一的手机。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-012

系统必须在账户没有任何用户名也没有邮箱标识时拒绝设置口令，并说明口令登录需要先有登录名。

- **验收**：仅有手机标识的账户提交设口令被拒绝，界面说明需先添加用户名或邮箱；添加用户名后再次提交设口令成功。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-account-013

系统必须在成功新增一条登录标识后，按 REQ-security-029 结束该账户在当前设备以外的会话。

- **验收**：在设备 A 新增一条登录名后，设备 B 的下一次请求被要求重新登录；设备 A 的当前会话仍然可用；删除标识不触发撤销。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-account-014

系统必须让添加手机经两步完成：账户先提交新号码并收到验证码，再提交该验证码即可完成，无需重填号码；通过后该手机成为已验证的登录方式。

- **验收**：未绑定手机的账户提交新号码后收到验证码，提交该验证码后列表出现该已验证手机；号码已属另一账户时发码阶段照常应答，验证阶段被拒绝且手机不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-046

系统必须允许账户通过同一两步流程把已有手机更换为新号码，通过后旧号码不再是该账户的登录方式。

- **验收**：已有手机的账户提交新号码并完成两步后，列表中的手机被替换为新号码，旧号码不再用于该账户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-015

系统必须把账户的邮箱验证状态读作下列值之一：已验证、未验证、不适用（账户没有任何邮箱）；判定与账户来源无关，预置、导入与自助注册的账户适用同一规则。

- **验收**：没有邮箱的账户读作不适用；持有未验证邮箱（含管理员写入）的账户读作未验证；持有至少一条已验证邮箱的账户读作已验证；任何来源的同状态账户得到相同结论。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`；`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens an account with the provider-verified email`

### REQ-account-016

系统必须按邮箱验证状态渲染账户页的验证节：未验证且存在目标地址时显示目标地址、发送与重发验证码、验证码输入与更换地址入口；已证明时显示完成态；没有邮箱时不显示该节。

- **验收**：未验证账户看到目标地址与发送控件；已验证账户看到完成文案且没有验证码输入；没有邮箱的账户看不到该节。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`；`starters/demo/web/e2e/specs/journey-social-sign-in-and-linking.spec.ts` `social-sign-in: a first Google sign-in opens an account with the provider-verified email`

### REQ-account-017

系统必须让账户页按进入时携带的参数决定验证节的起始行为：验证参数下，未验证且存在目标邮箱时自动发送一次验证码且本次进入不重复发送；普通进入不发送验证码；自动发送被新鲜认证拒绝时不得自行打开重新认证对话框，必须保留手动发送并说明。

- **验收**：从「去验证」入口进入时目标邮箱收到一条验证码，页面停在验证码输入；直接打开账户页时不发码，需手动点击发送；自动发送被拒时出现手动发送按钮与说明，未出现重新认证对话框。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-018

系统必须允许未验证账户更换待验证的邮箱地址，进入更换入口时直接展开更换表单。

- **验收**：未验证账户从「更换邮箱」入口进入时表单已展开，无需再次点击。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-047

系统不得在账户进入更换邮箱入口时自动发送验证码。

- **验收**：从更换邮箱入口进入时，旧地址收不到验证码。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-048

提交新地址后，系统必须按发码与验证流程证明它；通过后新地址成为已证明的邮箱标识。

- **验收**：提交新地址并完成验证后，列表出现新地址的已验证行。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-operator-first-run.spec.ts` `operator-first-run: the operator proves the email and lands the first settings write`

### REQ-account-049

系统不得因这次更换释放原地址的未证明预留。

- **验收**：更换完成后原地址仍被占用，另一账户以该地址注册或添加邮箱被拒。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-019

系统必须在验证码发出后按服务端给出的间隔禁用重发并显示剩余等待时间；间隔内的重复发送必须被拒绝；服务端以限流拒绝时，等待时间按应答给出的时间重新起算。

- **验收**：发送成功后重发控件在间隔内不可用并显示剩余秒数；间隔结束后可再次发送；收到限流应答时倒计时按应答给出的时间重置。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-020

系统必须在应用栏之下为未验证账户持续显示验证提示，含目标地址、「去验证」与「更换邮箱」入口；提示不提供关闭，邮箱验证完成后消失；账户没有目标地址时提示改为提供添加邮箱的入口。

- **验收**：未验证账户在每个租户页看到该提示且没有关闭控件；完成验证后提示消失；在页面之间往返不使提示消失；没有目标地址时提示提供添加邮箱。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-021

系统必须在账户刚获得个人租户且账户设置未完成时，于首页显示一次性的「完成账户设置」提示，提示含进入完成设置页的链接与关闭控件。

- **验收**：刚获得个人租户且没有可作登录名的标识或没有口令的账户，在首页看到该提示，提示中有进入完成设置页的链接与关闭控件；账户本次并未新获得个人租户时不出现提示。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-055

设置完成后，系统必须按账户当前资料撤下已在首页显示的一次性提示，账户无需重新登录。

- **验收**：看到提示后补齐用户名与口令，提示消失且无需重新登录；在账户设置页完成设置后返回首页，提示同样不再出现。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-workspace.spec.ts` `first-registration: a new visitor reaches a personal workspace, and occupancy is refused`

### REQ-account-056

一次性提示的关闭必须只在本次身份会话内生效；同一身份会话内离开并返回首页不再显示，换身份后重新按触发判据判定。

- **验收**：关闭提示后在页面之间往返均不再出现；在同一浏览器切换到另一个账户的身份会话后，触发判据满足时提示重新出现。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-account-057

一次性提示不得由注册动作本身触发；触发判据是账户刚获得个人租户且设置未完成，与账户是否经登录面的注册入口到达无关。

- **验收**：不经注册入口、以新号码走普通短信登录而隐式开户的账户，在个人租户刚创建且设置未完成时同样看到提示。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-022

系统必须提供完成设置页，并要求没有登录名的账户先添加用户名，再设置口令。

- **验收**：仅有手机的账户在页面先看到添加用户名，添加后出现口令表单。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-account-050

账户没有可作登录名的标识时，完成设置页必须不显示口令表单，并说明需要先添加用户名。

- **验收**：只有手机标识的账户在完成设置页看不到口令表单，看到需要先添加用户名的说明。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-051

账户已有登录名与口令时，完成设置页必须陈述设置已完成并给出回到首页的入口。

- **验收**：补齐用户名与口令后页面变为完成态并给出回到首页的链接。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-052

完成设置页必须提供稍后设置的出口；跳过不撤销账户。

- **验收**：点击「稍后设置」离开后账户仍可用短信登录。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-023

系统必须在账户已有口令时要求提交正确的当前口令才能修改；没有口令的账户不要求当前口令。

- **验收**：有口令账户提交错误的当前口令被拒绝且口令不变；提交正确的当前口令与合格新口令后新口令生效；没有口令的账户在新口令合格时直接生效。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-sms-signup-to-password-login.spec.ts` `sms-signup: a phone-code first sign-in becomes a username-and-password login`

### REQ-account-024

系统必须拒绝不满足口令策略的新口令：长度越界、与账户的用户名、邮箱或手机相同、或与当前口令相同，并说明不满足策略而不写出具体数值。

- **验收**：命中任一不合格条件（长度越界、与本人的用户名、邮箱或手机相同、与当前口令相同）时被拒绝且口令不变，界面出现策略说明且不含长度数字；合格的新口令成功。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-025

系统必须在当前口令不符时使用本界面自己的失败说明，不点名失败成因，也不复用登录面的文案。

- **验收**：输入错误的当前口令后，账户页出现本节说明，文案不点名「口令不正确」一类成因，也不出现登录面点名邮箱、手机号或验证码的句子。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-026

系统必须在改口令成功后轮换当前会话，并按 REQ-security-029 结束账户在其他设备上的会话。

- **验收**：设备 A 改口令后仍可继续使用，设备 B 的下一次请求被要求重新登录；会话列表中不再出现设备 B 的会话。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-027

系统必须列出账户已绑定的社交账号；可添加的渠道必须只取部署启用的重定向登录方法中尚未绑定的那些，已绑定的渠道不再出现在添加区。

- **验收**：已绑定某一提供方的账户在列表看到该行，添加区不再提供该渠道；部署未启用的方法既不出现在列表也不出现在添加区。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-account-linking.spec.ts` `social-account-linking: a password account links Google, then signs in with it`

### REQ-account-028

系统必须允许账户持有人发起社交绑定，经提供方跳转后回到本应用即完成绑定。

- **验收**：已验证账户绑定某一提供方后回到本应用，列表出现该行。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-account-linking.spec.ts` `social-account-linking: a password account links Google, then signs in with it`

### REQ-account-053

系统不得在社交绑定完成时建立新的登录会话；账户原有会话保持不变。

- **验收**：绑定完成后回到账户页，登录状态与绑定前相同，原有会话未被替换。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-social-account-linking.spec.ts` `social-account-linking: a password account links Google, then signs in with it`

### REQ-account-054

系统必须按 REQ-signup-025 拒绝邮箱未验证账户发起社交绑定，并提示先验证邮箱。

- **验收**：邮箱未验证的账户发起绑定得到「先验证邮箱」的拒绝，未跳转到提供方。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-029

系统必须允许账户持有人解绑已绑定的社交账号，但解绑后账户必须仍保留至少一种可用登录方式，否则必须拒绝解绑且绑定不变。

- **验收**：账户另有口令或其它登录方式时解绑成功、列表不再出现该行；账户仅剩该绑定时解绑被拒绝，列表保持不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-030

系统必须按 REQ-security-013 与 REQ-security-014 对安全类写要求重新验证与重放；界面对当前口令不符与新鲜认证不足给出彼此不同的结果，当前口令不符显示口令失败说明，新鲜认证不足显示重新验证入口。

- **验收**：输入错误的当前口令时显示口令失败说明且不出现重新验证入口；新鲜认证不足时出现重新验证入口且不显示口令失败说明。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-account-058

保存本人资料与偏好（显示名、语言与时区）的写入必须按 REQ-security-013 的重新验证要求执行，被拒绝的这一次写入必须按 REQ-security-014 重放。

- **验收**：会话不满足 REQ-security-013 的要求时保存被拒并出现重新验证入口；重新验证成功后，原保存按 REQ-security-014 重放一次并得到与直接保存相同的成功确认；取消重新验证时本次保存不写入，控件保持已保存的值。
- **状态**：已实现
- **端到端测试**：未实现
