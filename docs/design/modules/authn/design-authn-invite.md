# authn/invite

本模块持有邀请的状态机与 `authn_invitations` 表：一张邀请点名一个地址与一个[租户](../../../glossary.md)，在被接受之前不产生成员行，也不产生账户。接受令牌授予「加入这个租户」的能力，不证明被邀请地址的归属——签发者手里也有这枚令牌。接受要求一个已登录的账户，并且该账户自己持有与被邀请地址相同的已证明邮箱；地址不必是该账户的登录方式。签发成功后，本模块经可选接口 `authn.InviteNotifier` 请一次接受通知，投递成功时签发应答不再交出令牌；成员被停用或移除时，本模块撤销该账户在该租户下的待接受邀请，组织被删除时撤销该租户的全部待接受邀请。接受还按可选的 `authn.TenantStateResolver` 判定租户是否仍在服务：已删除的租户令该行作废，停用的租户令该行保持待接受。签发与接受的两档预算把签发与探测都限在固定窗口内。它不渲染通知、不持邮件载体、不持账户与成员关系的表与规则，也不判定租户成员资格。读职责清单前应持有的一句话心智模型：邀请是一条待兑现的成员关系授予，令牌只兑现加入，邮箱归属由接受人自己证明。

第一遍按顺序读的节是职责与非职责、术语表、外部接口契约、依赖、UML 图、核心数据结构、核心算法与流程；术语表在遇到邀请、接受令牌、待接受态、投递结果时回看，UML 图的状态机给出邀请的去向、类型图给出通知接口的位置，核心数据结构的状态一览说明每个状态在做什么，核心算法与流程的走查、签发、接受与通知给出一次兑现与一次送达的落库与外部调用过程。错误与失败语义与并发与事务边界是最后自检的节；扩展点与实现状态在主体之后回看。

## 职责

- **驱动邀请的状态机。** 经一张 `authn_invitations` 表驱动邀请从「待接受」到终态的每一次迁移，签发、列表、撤销与接受都经它。邀请是一次**待兑现的成员关系授予**：它点名一个地址与一个租户，在被接受之前没有任何成员行存在，也没有任何账户由它建立。
- **只在目标可邀请、期限有界时签发。** 签发在写行之前拒绝三类目标：被邀请地址已经属于该租户的活跃成员或停用成员，被邀请地址属于服务账户，或目标租户是系统租户。显式期限必须晚于当前时刻，且不得晚于签发时刻加 90 天；不带期限时取设置时长，设置本身的取值域同样是 1 小时到 90 天。同一（租户，地址）下至多一条待接受邀请，并发的签发在插入处由部分唯一索引仲裁，输者得到 `ErrInvitationPending`；一次新的签发只在状态仍为待接受时取代旧行，已接受与已撤销的行不被改写。
- **接受即验证并兑现。** 一次接受消费一个令牌，并把一条来源为 `invite` 的成员行写给**已登录的调用者账户**——这是 `Membership.Source` 早已声明的取值之一（`pkg/authn/internal/model/directory.go` `Membership.Source`）。调用者必须持有与被邀请地址相同的已证明邮箱标识（规范化后比较）；该地址可以是、也可以不是调用者的登录方式。没有这条证明的账户被拒绝，令牌保持可兑现。接受从不新建账户，账户规则仍归 `authn.account`。
- **令牌授予加入，不证明地址归属。** 令牌只以摘要留存，明文在签发应答里至多出现一次（投递成功时连这一次也不出现，链接已由通知送达）；撤销因此是改状态，不是撤销一个签名。签发者手里也有令牌，所以仅凭令牌出示不能成立一次接受，候选账户必须自己证明被邀请地址。
- **签发之后请一次通知，并决定令牌是否随应答交出。** 签发成功后，本模块经可选接口 `authn.InviteNotifier` 请一次接受通知，限时五秒，把投递结果作为 `sent`、`failed` 或 `not_configured` 写进签发应答。投递成功说明收件地址已经持有链接，应答不再交出令牌；投递失败或未配置时，签发者仍需转交链接，应答交出令牌。通知失败不撤销邀请，HTTP 仍为 201；失败记一行 `Error` 日志与一条尽力档 `authn.invitation.deliver` 审计行。通知的正文、语言与投递归通知接口的实现（`authn.invitemail` 是仓库提供的那一份），本模块只交出通知所需的租户、地址、令牌、期限与请求语言。
- **成员退出时撤销他的待接受邀请。** 本模块订阅 `authn.membership.changed`：成员被停用或移除时，把这个账户的邮箱在该租户下的待接受邀请置为已撤销。邀请行仍是权威，事件只是提示，撤销以「状态仍为待接受」为条件。
- **组织删除时收回该租户的全部待接受邀请。** 本模块排他交付根包的 `authn.InvitationRevoker`；租户归属模块在删除提交之后调用它，把该租户状态仍为待接受的邀请置为已撤销。调用是尽力而为的，失败只留警告；接受本身还会因租户不再服务而拒绝。
- **接受按租户状态决定拒绝方向。** 接受读取可选的 `authn.TenantStateResolver`：租户在服务时照常兑现，已停用时答 409 `conflict` 且邀请保持待接受（重新启用后同一令牌可再兑现），已删除时撤销该行并答 404。没有解析器的装配没有租户状态可判，接受照常进行。
- **以模块 `authn.invite` 排他交付接口 `Invites` 与 `authn.InvitationRevoker`。** 邀请的签发、列表、撤销与接受都由本模块经 `Invites` 作答，别的模块取用邀请能力没有第二条入口；接口与数据类型随它同处本子包（[design-authn](design-authn.md)）。组织删除时的收回经 `InvitationRevoker` 交付。

### 非职责

- **不做组织树。** 没有节点、没有层级、没有子树数据范围，接受只落一条成员关系，不带任何位置。早期实现有一棵多级组织树，本方案刻意不做它（[ADR-org-invitation](../../../adr/adr-org-invitation.md)）。
- **不渲染通知，不排队，不重试。** 本模块不构造邮件正文、不选语言、不触达邮件载体；它只把 `InviteNotice` 交给 `authn.InviteNotifier` 的实现，由后者渲染并投递。通知在签发路径上同步调用一次，没有提醒、没有自动重发、没有后台重试：一条新的待接受邀请只能来自一次新的签发调用，一次重发就是一次新的签发。
- **不持账户、成员关系、标识的表与规则。** 接受只读调用者的标识、写一条成员行（经 `Memberships.PutJoining`），从不建账户、不写标识、不证明邮箱；这些写入归 `authn.account`，账户与成员关系的规则与哨兵也归 [authn-account](design-authn-account.md)。
- **不做邮件的送达证明。** `sent` 只表示载体接受了这条消息，不证明地址可达、收件箱收到或被打开。地址可达性仍由投递渠道与收件方承担，签发者始终可以复制链接另交。
- **不判定租户成员资格，不 import `pkg/tenant`。** 租户标识在本模块里是字符串；请求期判定归 `tenant.admission`（[design-tenant-admission](../tenant/design-tenant-admission.md)）。租户是否存在与是否在服务也不由本模块判定：它只经可选的 `authn.TenantStateResolver` 读取，按读到的状态决定拒绝或撤销。
- **不承载角色。** 接受只落成员关系，角色按（账户，租户）绑定归 `authz.policy`（[design-authz](../design-authz.md)）。
- **不代替签发者决定把谁拉进租户。** 管理面的判定接口回答「这个发起者能不能签发」，本模块保证签发者的身份与租户范围，不保证他选的人对不对。

## 术语表

以下术语的含义限于本文档与邀请主题的决策记录；它们不进项目术语表。

| 术语 | 英文 | 适用范围 | 定义 |
|---|---|---|---|
| 邀请 | invitation | 本文档与邀请主题的决策记录 | 一次待兑现的成员关系授予：点名一个地址与一个租户，带状态与期限。接受之前不产生成员行，也不产生账户。 |
| 接受令牌 | acceptance token | 本文档与邀请主题的决策记录 | 接受所凭的凭据，签发应答至多交出一次，只以摘要留存的 256 位随机串。它授予加入该租户的能力，不证明被邀请地址的归属。 |
| 待接受态 | pending | 本文档与邀请主题的决策记录 | 邀请已签发、未接受、未撤销且未过期的状态。它属于邀请自己的一行，不属于账户表——账户表没有可放待接受行的行态。 |
| 投递结果 | delivery outcome | 本文档与邀请主题的决策记录 | 一次接受通知的结局，封闭集为 `sent`、`failed`、`not_configured`。它只说明这次通知有没有被载体接受，不说明地址可达或消息被阅读。 |

## 外部接口契约

### 交付的功能

本模块排他交付接口 `Invites`，由本模块回答。接口、值类型与哨兵一并定义在 `pkg/authn/invite`，不随其余认证接口放进根包 `pkg/authn`（[design-authn](design-authn.md)；理由见 [ADR-authn-invitation](../../../adr/adr-authn-invitation.md)）。

```go
// 邀请接口：持有邀请的状态机，决定一条邀请能不能签发、能不能被一个令牌兑现。
type Invites interface {
    // Issue 签发一条邀请。同一（租户，地址）下已有的待接受邀请被取代。
    // 返回值里的令牌只在此处交出一次。
    Issue(ctx context.Context, in IssueInvite) (Issued, error)
    // List 列出某租户的邀请，一页一条游标。
    List(ctx context.Context, tenant string, page Page) ([]Invitation, string, error)
    // Revoke 撤销一条邀请，使其令牌失效。撤销已接受的邀请是 ErrInvitationAlreadyAccepted。
    Revoke(ctx context.Context, tenant, id string) error
    // Accept 用令牌兑现一条邀请，为已登录且持有被邀请地址已证明邮箱的
    // 调用者写一条成员行（source = invite）。它从不建账户，也从不证明邮箱。
    Accept(ctx context.Context, in AcceptInvite) (Membership, error)
}

type IssueInvite struct {
    Tenant    string    // 字符串形式的租户标识
    Email     string    // 被邀请人的地址，规范化前的原值
    ExpiresAt time.Time // 绝对期限；零值取设置的时长
}

// Issued 是一次签发的领域结果。投递结果不在这里：通知在管理面路由上
// 请取，由路由把它与令牌是否随应答交出一起决定。
type Issued struct {
    Invitation Invitation
    Token      string // 接受令牌，只在此处交出一次；不得写日志、事件或追踪属性
}

type Invitation struct {
    ID         string
    Tenant     string
    Email      string // 规范化后的地址
    Status     InvitationStatus
    InvitedBy  AccountID
    ExpiresAt  time.Time
    AcceptedAt time.Time // 零值：尚未接受
    CreatedAt  time.Time
}

type AcceptInvite struct {
    Token       string // 接受令牌
    Username    string // 忽略：接受从不建账户
    DisplayName string // 忽略：接受从不建账户
    Password    string // 忽略：接受从不建账户
}

// InvitationStatus 是邀请状态的封闭集。零值 Pending 与存量列一致。
type InvitationStatus uint8

const (
    InvitationPending  InvitationStatus = iota // 待接受（零值）
    InvitationAccepted                         // 已接受
    InvitationRevoked                          // 已撤销
)

// Valid 报告 s 是否在存量列里。已过期的待接受邀请仍是 InvitationPending：
// 过期由时钟与 ExpiresAt 推出，不落列。
func (s InvitationStatus) Valid() bool { return s <= InvitationRevoked }

// InviteNotifier 是接受通知的投递接口，可选。定义在根包
// pkg/authn/invite_notice.go，由宿主或 authn.invitemail 交付。
type InviteNotifier interface {
    // Notify 按 notice 的地址与语言送出一次通知，并报告投递结果。
    // 失败不回滚邀请；调用方仍以 201 作答，并按结果决定应答是否交出令牌。
    Notify(ctx context.Context, notice InviteNotice) (InviteDelivery, error)
}

type InviteNotice struct {
    Tenant    string    // 租户名称；通知里不出现内部标识
    Email     string    // 规范化后的被邀请地址
    Token     string    // 接受令牌；不得写日志、追踪、事件或错误文本
    ExpiresAt time.Time // 邀请的绝对期限；零值表示调用方不知道
    Locale    string    // 请求点名的 BCP 47 语言，空串表示没有点名
}

// InviteDelivery 是一次通知的结果。零值是 InviteNotConfigured。
type InviteDelivery uint8

const (
    InviteNotConfigured InviteDelivery = iota // 本部署没有配置通知通道，什么也没发
    InviteSent                                // 载体接受了这条消息
    InviteFailed                              // 载体拒绝，或结果未知
)
```

**`Invitation` 不含令牌。** 令牌明文在 `Issued` 里出现一次，此后任何视图、列表、事件与诊断都不带它；`Invitation` 是给管理面读的只读视图。管理面签发应答只在投递没有成功时带一次令牌：投递成功说明收件地址已经握有链接，应答再交出明文就多了一份持有者；投递失败或未配置时签发者必须自己转交，应答照常交出。

**组织删除收走待接受邀请。** 本模块排他交付根包的 `authn.InvitationRevoker`：

```go
// InvitationRevoker 在租户被删除后收回它还能发出的全部授予。
type InvitationRevoker interface {
    // RevokeTenantInvitations 把该租户的全部待接受邀请置为已撤销。
    // 空租户或没有待接受行返回 nil；已接受与已撤销的行不被改写。
    RevokeTenantInvitations(ctx context.Context, tenant string) error
}
```

租户归属模块在删除提交之后调用它，失败只记一行警告而不让删除失败；接受本身还会按租户状态拒绝，所以调用没到达时令牌也不能兑现。撤销写以「状态仍为待接受」为条件，重复调用不改变结果。

**接受按租户状态分流。** 接受读取可选的 `authn.TenantStateResolver`（`TenantStateOf` 返回 `TenantServing`、`TenantDisabled` 或 `TenantDeleted`）：租户在服务时继续核对；已停用时答 409 `conflict`（`ErrInvitationTenantInactive`）且邀请保持待接受，所以重新启用后同一令牌还能兑现；已删除、或解析器回答闭集之外的状态时，撤销该行并答 404 `ErrInvitationNotFound`。装配没有解析器时没有租户状态可判，接受照常进行。读租户状态失败是 503，不按可接受处理。

**投递结果是通知的结果，不是邀请的状态。** 它取 `sent`、`failed`、`not_configured` 之一；`not_configured` 表示没有通知接口、部署选择不发邮件，或所选载体（日志载体）不把消息交给收件人，`failed` 表示载体拒绝或结果未知。三者都不改变邀请行与 HTTP 状态；只有 `sent` 让应答不交出令牌。`InviteDelivery` 的零值就是 `not_configured`，所以一个从未设置过的结果读起来是「没有发」，而不是「发失败了」。

**`InvitationStatus` 的存量列是 `{待接受，已接受，已撤销}`，观察态是它加「已过期」。** 已过期不是一次写出的迁移，而是「状态为待接受，且当前时刻不早于 `ExpiresAt`」的推导；接受与列表都按当前时刻判定，不靠清扫器翻转状态。判据是状态放置的方向：一次丢失的过期翻转只让一条已经不能接受的邀请看起来还能接受，而接受路径自己会拒绝它，安全性不依赖这次翻转（[ADR-authn-state-placement](../../../adr/adr-authn-state-placement.md)）。

**`IssueInvite` 与 `AcceptInvite` 的字段语义是承诺**：`IssueInvite.Email` 是未规范化的原值，规范化归本模块；`Issued.Token` 是只交出一次的凭据；`AcceptInvite` 的 `Username`、`DisplayName` 与 `Password` 被忽略——接受从不建账户，保留它们只是让旧客户端仍然可以发送而不被拒绝。

**签发有资格与期限两条边界。** 被邀请地址已经属于该租户的活跃成员或停用成员时，再签发一条邀请没有可授予的东西，签发被拒绝且不写行；被邀请地址属于服务账户时同样拒绝，因为服务账户以服务密钥登录，没有可接受的成员关系；目标租户是系统租户时也拒绝，系统租户不接受邀请加入。显式期限必须晚于当前时刻且不晚于签发时刻加 90 天；不带期限时取设置时长，设置的取值域为 1 小时到 90 天，越出时取声明默认值。两个边界都在写任何行之前判定。

**接受的前提是身份与地址归属，二者缺一不可。** 令牌只授予加入，签发者持有同一枚令牌，所以令牌本身不能成立一次接受。调用者必须是已登录账户；该账户必须持有一条与被邀请地址规范化后相等的邮箱标识，且这条标识的验证渠道属于 `Verification.Proves` 为真的那一类（`email-code`、`sms-code`、`idp:<提供方标识>`）。满足条件后，`Memberships.PutJoining` 为调用者写一条来源 `invite` 的成员行。接受从不新建账户、从不写标识、从不证明邮箱；证明由账户自己的邮箱验证路径完成。

### 接受的前提与成员行

- **没有身份**：`401`，带该能力的强认证挑战；不询问令牌是否真实。`ErrInvitationUnauthenticated`。
- **没有证明被邀请地址**：`403 invitation-email-unproved`；邀请保持待接受，客户端据此展示「先证明地址」的步骤。`ErrInvitationEmailUnproved`。地址的证明渠道必须是证据一类；`invite`、`admin`、`import-unverified` 与 `self` 都不算。
- **地址不必是登录方式**：成为被邀请地址的已证明邮箱不把该地址选作登录标识，也不把它设为默认联系地址；这两项分别按 REQ-account-064 与 REQ-account-063 由持有人选择、由账户模块维护（[ADR-authn-contact-and-login-ways](../../../adr/adr-authn-contact-and-login-ways.md)）。
- **地址尚无账户**：接受不替当事人开户；没有已登录账户就没有可写的成员行。当事人先在装配开放注册（或在别处已有账户）的前提下登录并证明地址，再回到链接接受（REQ-tenancy-018）。
- **已有成员关系且仍启用**：直接兑现，保留其来源与 SSO 豁免；一次接受只是补上成员行，不改写当事人如何成为成员。
- **已有成员关系且已停用**：`409 membership-inactive`，并撤销这条待接受邀请；停用的成员不能借一枚仍有效的令牌复活。`ErrInvitationMembershipInactive`。
- **租户已停用**：`409 conflict`，邀请保持待接受；重新启用后同一令牌可以再兑现。`ErrInvitationTenantInactive`。
- **租户已删除**：`404`，并撤销这条邀请；一个不再存在的租户不再有任何可授予的成员关系。
- **成员行的来源写 `invite`**：本模块落下的每一条新成员行，`Membership.Source` 都取 `invite`。`Memberships.PutJoining` 的成员关系变更事件（`authn.membership.changed`）随带该来源，订阅方据此分清邀请加入与管理员直加（[design-authn-events](design-authn-events.md)）。
- **令牌只能兑现一次**：条件写把行从待接受翻到已接受，条件是状态仍为待接受且未过期；两个并发接受至多一个赢家，输者按终态报告，不写成员行。

**接受不写验证渠道 `invite`。** `Verification` 的封闭集仍含 `invite`（`pkg/authn/internal/model/directory.go` `VerifiedViaInvite`），但它不再证明地址归属：`Verification.Proves` 对 `invite`、`admin`、`import-unverified` 与 `self` 都为假。令牌被送达并出示只说明持有者收到了链接，与短信验证码同类的是「一个载荷被出示」，而签发者也持有这枚载荷，所以它不足以证明邮箱属于接受人。

### 登记单元与名字

| 包 | 模块名 | 交付 | 排他 |
|---|---|---|---|
| `pkg/authn/invite` | `authn.invite` | `Invites` 与 `authn.InvitationRevoker`，以及 `Invitation`、`InvitationStatus`、`Issued`、`IssueInvite`、`AcceptInvite` 与哨兵 | 排他 |
| `pkg/authn/invitemail` | `authn.invitemail` | `InviteNotifier` 的邮件实现 | 排他 |

模块名照顶层能力：嵌套子包取 `authn.<功能>`，本模块取 `authn.invite`（[design-authn](design-authn.md)）。设置命名空间取同一取值；模块仍声明一个没有输入项的配置节，使旧配置里的 `authn.invite.ttl` 按完整路径报未知键。`InviteNotifier`、`InviteNotice` 与 `InviteDelivery` 定义在根包 `pkg/authn/invite_notice.go`，不在任何一个实现子包。

### 通知的邮件实现 `authn.invitemail`

`authn.invitemail` 是仓库提供的通知实现：它把一次 `InviteNotice` 渲染成邮件并交给 `pkg/mail.Sender` 的载体，排他交付 `InviteNotifier`。一条命令可以点名它，也可以换成宿主自己的实现；两个实现同时在场在消解阶段失败。

- **配置项 `authn.invitemail.accept-base-url` 给出接受页所在的绝对来源。** 取值须是绝对 http/https URL、带主机、无查询与片段；末尾的 `/` 归一化掉。通知的链接是 `<accept-base-url>/invitations/<令牌>`，路径由模块拼接、令牌按单个路径段转义，配置改不了接受路由的形状。值不合法时在 `Prepare` 阶段终止启动，点名输入项且不回显取值。
- **通知里的租户名取名称。** `InviteNotice` 携带租户的内部标识，渲染前经可选的 `authn.TenantNameResolver` 换成名称；主题与正文都写名称，接缝缺席、解析失败或解析不到时写空串，不回退成内部标识。

**空值是合法的未配置部署。** 模块保持启用，不触达载体，答 `InviteNotConfigured` 与 nil 错误。选 `mail.carrier: none` 的运行里没有任何发送载体，模块随邮件能力一并 `StateDisabled`，不因一个缺席的依赖让整个装配起不来；这样的运行不校验接受来源。
- **语言取自请求，缺目录时回退。** 通知按 `InviteNotice.Locale` 选择消息：先按原标签，再按主语言子标签，最后英文。模板在模块的 `locales/zh-CN.json` 与 `locales/en-US.json`，包含主题、纯文本与 HTML；`New` 在启动时渲染每个目录的每条消息，缺键即启动失败，不等到第一次发送才发出一条只有键的消息。
- **期限按 UTC 写。** `InviteNotice.ExpiresAt` 是邀请的唯一期限，按 RFC 3339 的 UTC 时刻插入正文；零值表示调用方不知道，渲染为空，不编造一个时刻。
- **令牌、收件人与正文不写日志、不进错误文本。** 每次发送只记一行结果与耗时，行里没有令牌、地址、租户或正文；实现方返回给调用方的错误是载体自己的错误，不含令牌。
- **载体错误按原分类透传。** `mail.ErrThrottled`、`mail.ErrRejected`、`mail.ErrUncertain` 原样返回，由邀请模块决定怎么记；本模块不加自己的分类，也不重试。
- **消息不带去重标识。** 每次签发都画一个新令牌，重发是一条取代旧行的新邀请；一个稳定的 `DedupeID` 会压掉一条合法的通知，而载体本身对每次调用只投递一次。

装配上，`starters/demo/server/capabilities.go` 把本模块编入每次构建并交给 `serve` 点名；审查与本地配置给它一个接受来源并选 `mail.carrier: log`。日志载体只写进程日志、不把消息交给收件人，`authn.invitemail` 因此把邀请的投递结果报为 `not_configured`：每次签发都由管理面的一次性面板交出接受链接，通知不写进进程日志；验证码等其余邮件仍按日志载体自己的行为处理（[design-starters](../design-starters.md)）。

### 路由

本模块经 `mount.Mounter` 挂管理面与 account 侧的路由。管理面路由登记为 `Guarded`，并登记为 `Impersonable`：它们按路径里的租户域判定，目标是该租户管理员时，冒充身份持有它在该租户的管理权限，邀请路由照常可用（[design-authn-impersonation](design-authn-impersonation.md)）。接受的读取路由登记为 `Public`：令牌是它展示邀请摘要的全部凭据。接受的兑现路由登记为 `Optional`：请求带凭据时网关给出身份，处理器拒绝没有身份的请求。管理面按租户寻址且在该租户的策略域判定，因此落在 `tenants` 作用域与受租户两层守护的端上；接受路由按令牌寻址、没有租户，落在普通路由（[ADR-http-api-path-scopes](../../../adr/adr-http-api-path-scopes.md)）。

| 面 | 方法 路径 | 登记 | 授权 |
|---|---|---|---|
| 管理面 | `POST Bt/:tenant/invitations` | `Guarded`，`Impersonable` | 判定接口 + 强制档审计；签发后尽力通知，并按下述规则决定应答是否带令牌 |
| 管理面 | `GET Bt/:tenant/invitations` | `Guarded`，`Impersonable` | 判定接口；读不写审计；可按 `status` 收窄 |
| 管理面 | `DELETE Bt/:tenant/invitations/:id` | `Guarded`，`Impersonable` | 判定接口 + 强制档审计 |
| account 侧 | `GET B/invitations/:token` | `Public` | 令牌 |
| account 侧 | `POST B/invitations/:token` | `Optional` | 已登录的调用者 + 被邀请地址的已证明邮箱 + 令牌 |

`B` 是普通路由的基路径，`Bt` 是 `tenants` 作用域的基路径（`authn.gate.mount.bases`，见 [authn-gate](design-authn-gate.md)）。接受的读取路由对仍可兑现的令牌返回邀请摘要；已过期、已接受与已撤销的令牌只返回 `state`，不返回地址、租户名与有效期——链接不再授予任何东西时，摘要里的字段也一并消失。兑现路由在核对令牌之前先读租户状态，停用与删除的拒绝方向见上一节。

基路径 `B` 由 [authn-gate](design-authn-gate.md) 声明。管理面路由按路径里的租户标识寻址，不按名称表，所以本模块不依赖租户名册；接受路由按令牌寻址，路径里不出现租户。`POST B/invitations/:token` 读调用者的身份与其邮箱标识，不读正文里的用户名、显示名或口令；没有身份答 401，没有证明被邀请地址答 403。请求声明长度为零时不绑定正文并按错误请求拒绝；`{}` 是一段正文，照常接受。

**接受的读取与兑现共用一组探测预算。** 两条路由在查令牌之前按令牌摘要与客户端地址计数（`authn.invite.probe-*`），超限答 429 并带 `Retry-After`；令牌键是摘要，从不是明文。计数键在查令牌之前写入，所以 429 不透露令牌是否存在。

**列表的 `status` 查询把一页收窄到一种观察态。** 取值是封闭集 `pending`、`expired`、`accepted`、`revoked`；缺省列出全部，集合外的值答 400，不当作「全部」。`pending` 是状态为待接受且 `expires_at` 晚于当前时刻的行，`expired` 是状态仍为待接受而 `expires_at` 不晚于当前时刻的行，`accepted` 与 `revoked` 是两种终态。过期仍不落列：两个 `pending` 分支读的是同一列状态，差别只在时钟与期限的比较。收窄只发生在管理面路由，`Invites.List` 本身仍列出全部。

### 管理面的判定顺序

管理面路由的判定在 handler 内做出，不经请求中间件的声明判定，与 `authn.admin` 一致（[ADR-authn-invitation](../../../adr/adr-authn-invitation.md)）。顺序固定：

1. **读发起者。** 请求没有身份是 401 加强认证挑战，不询问接口。
2. **注册验证状态的门控。** 发起者是用户账户且视为未验证（`SignupPending`：有邮箱而没有一条已证明的）时，签发（含取代旧待接受邀请的那次重发）与撤销答 403 `contact-required`；列表是读，不受影响。门控在身份确定之后、判定接口与强制审计之前，只收紧不放行，被拦下的请求不询问接口、不写审计。状态经 `SignupVerification.SignupStateOf` 读取，读失败答 503，不按放行处理；服务账户与视为 `SignupNone`（没有任何邮箱）的发起者豁免。
3. **询问判定接口。** 签发、列表与撤销都以 `Action authn.membership.manage` 询问 `Decider`——邀请授予的是一条成员关系，动作与成员关系的写同属一个动作（`pkg/authn/decide.go` `ActionMembershipManage`）。`Decision.Tenant` 取路径里的租户标识，`Target` 在撤销时取邀请标识、其余为空。接口缺席即全拒，与其余管理面一致（[design-authn-admin](design-authn-admin.md)）。接口拒绝时答 403，故障答 503。
4. **分支。** 门控与判定之后：写路由先解析正文、跑结构校验，再落强制档审计（落不进即 503、动作不执行），再调 `Invites`，把回绝映射成应答；列表调 `Invites.List` 直接应答。

**判定接口由本模块取用，不由 `authn.admin` 代挂。** 邀请有独立状态机与自己的表，路由因此挂在本模块；授权依据与成员关系的写一致，所以复用同一个动作，不新增动作常量。

### 声明的特性

**`authn.invite` 以[静态特性](../../../glossary.md) `authn.invitations` 声明邀请路由。** 描述符的 `Resources` 带 `feature.Spec{Name: "authn.invitations"}`（[feature](../design-feature.md)）。这个特性在场，表示本进程挂出「路由」一节的全部路由：管理面的签发、列表与撤销，以及接受路由的读取与接受。路由无条件挂出：`mount.Mounter` 是必需依赖，模块进入启用集即挂出它们，挂与不挂不另取决于配置或装配，所以声明为静态特性符合架构不变量 18（[architecture](../../architecture.md#架构不变量)）。邀请的存活期 `authn.invite.ttl` 是设置项，不发布。

特性文档只对已认证调用者可读；接受的读取路由是 `Public`，兑现路由是 `Optional`，邀请是否装配不经任何匿名发现接口发布。

### 预算

- **签发预算**：按被邀请地址、发起账户与整个部署分别计数，三者在同一个窗口内依次检查，任一超限即拒绝签发并在写行之前返回 429。地址按规范化后的收件箱形态计数，`+` 后缀与 Gmail 的局部点号不能放大额度。没有身份的宿主命令跳过发起账户这一档，地址与部署两档仍生效。
- **探测预算**：接受的两条路由按令牌摘要与客户端地址计数，令牌档与客户端档都在查令牌之前写入。IPv6 客户端按 /64 计数。计数后端读不到时答 503，不按放行处理。
- **两档预算的窗口与上限都是设置**：`authn.invite.issue-window`、`authn.invite.issue-address-limit`、`authn.invite.issue-issuer-limit`、`authn.invite.issue-global-limit`、`authn.invite.probe-window`、`authn.invite.probe-token-limit`、`authn.invite.probe-client-limit`。读取失败或取值越界时取声明默认值并记一行 `Error`，不把预算静默关掉。
- **增量先于判定，被拒绝的请求仍然消耗额度。** 每次检查先把计数加一，再与上限比较；被拒绝的签发或探测仍计入它被计到的那一档，所以同一窗口内的重复尝试不能无限得到额度。签发的缺省窗口是一小时，地址、发起账户与部署的缺省上限是 5、50、500；探测的缺省窗口是一分钟，令牌与客户端的缺省上限是 10、30。计数后端缺席或读不到时答 503，不按放行处理。

### 宿主可见的接口

- **模块名**：`authn.invite`。宿主的命令树点模块名纳入，设置命名空间取同一取值。
- **设置输入项**：`authn.invite.ttl`（时长，缺省 `168h`，取值域 1h 到 2160h）。每次不带期限的签发读它；设置缺席时取声明默认值，读取失败或取值越界时取声明默认值并记一行 `Error`，签发照常。存活期变长即放宽：本模块订阅 `settings.changed`，改到更长时写一行 `Warn` 并经审计接口记 `authn.settings.relaxed`，方向 `increase`。键的名字、取值域与缺省在 [design-authn-config](design-authn-config.md)。
- **路由**：`Bt/:tenant/invitations` 下的签发、列表与撤销，以及 `B/invitations/:token` 下的读取与接受；列表的 `status` 查询与签发应答的 `email_delivery` 字段；询问的动作 `authn.membership.manage`。签发应答在 `email_delivery` 为 `sent` 时不带 `token`，其余两个取值时带一次。
- **预算设置项**：`authn.invite.issue-window`、`authn.invite.issue-address-limit`、`authn.invite.issue-issuer-limit`、`authn.invite.issue-global-limit`、`authn.invite.probe-window`、`authn.invite.probe-token-limit`、`authn.invite.probe-client-limit`；取值与缺省在 [design-authn-config](design-authn-config.md) 与 `pkg/authn/invite` 的 `AGENTS.md`。
- **通知接口**：`InviteNotifier`、`InviteNotice`、`InviteDelivery` 与三个结果取值；本模块只取用接口，宿主或 `authn.invitemail` 交付实现。
- **组织删除的收回接口**：`authn.InvitationRevoker` 排他交付，`RevokeTenantInvitations` 收回一个租户的全部待接受邀请；租户归属模块在删除提交之后取用。
- **租户状态读取**：可选取用 `authn.TenantStateResolver`；缺席时接受没有租户状态可判。
- **令牌前缀**：接受令牌以 `spd_inv_` 起头，供日志脱敏识别（对照 `spd_rt_`、`spd_cs_` 两条既有前缀，见 [design-authn-token](design-authn-token.md)）。
- **审计动作** `authn.invitation.deliver`：一次签发通知失败时写的尽力档行，明细带 `action_id` 与 `tenant`，不带地址与令牌。
- **特性名**：`authn.invitations`，静态特性（见「声明的特性」）。

### 对外接口

- `Invites` 的方法集，`Invitation`、`InvitationStatus`、`Issued`、`IssueInvite`、`AcceptInvite` 的字段与方法集，`InviteNotifier`、`InviteNotice`、`InviteDelivery` 与三个结果取值，以及本模块的全部哨兵，属于对外接口。
- 路由路径与每条管理面路由询问的动作属于对外接口；`InvitationStatus` 的存量列、`status` 查询的四个取值、「已过期由时钟推出、不落列」、签发应答的 `email_delivery` 取值、「投递成功时应答不带令牌」、接受的读取对终态令牌只返回 `state`，同属接口。
- 成员行的 `Source = invite`、令牌前缀 `spd_inv_`、设置键 `authn.invite.ttl` 与特性名 `authn.invitations` 是宿主可见的接口的一部分，改名即破坏性变更；前端经生成的特性名类型引用特性名，撤去它或改变它所指的接口同样是破坏性变更。配置不再承载 `authn.invite.ttl`：旧配置里仍写着它的宿主在启动时按未知键失败。

### 哨兵错误

| 哨兵 | 定义处 | 含义 |
|---|---|---|
| `ErrInvitationNotFound` | `pkg/authn/invite` | 令牌或邀请标识不指向任何一条邀请 |
| `ErrInvitationExpired` | `pkg/authn/invite` | 令牌属于一条已过期的待接受邀请 |
| `ErrInvitationRevoked` | `pkg/authn/invite` | 令牌属于一条已撤销的邀请 |
| `ErrInvitationAlreadyAccepted` | `pkg/authn/invite` | 令牌属于一条已接受的邀请，或撤销一条已接受的邀请 |
| `ErrInvitationPending` | `pkg/authn/invite` | 该（租户，地址）已有一条待接受邀请，签发的取代竞态输掉时报告 |
| `ErrInvitationUnauthenticated` | `pkg/authn/invite` | 接受请求没有已登录的调用者；令牌单独不成立 |
| `ErrInvitationEmailUnproved` | `pkg/authn/invite` | 调用者没有持有被邀请地址的已证明邮箱标识 |
| `ErrInvitationMembershipInactive` | `pkg/authn/invite` | 调用者在该租户的成员关系已停用；邀请被撤销 |
| `ErrInvitationTenantInactive` | `pkg/authn/invite` | 邀请所属的租户已停用；邀请保持待接受 |
| `ErrInvalidEmail` | `pkg/authn/invite` | 被邀请的地址不成形 |
| `ErrInvite` | `pkg/authn/invite` | 本模块自己的构造错误（令牌熵源失败一类），归 503 |

调用方以 `errors.Is` 判别，包裹一律用 `%w`。错误文本不携带地址或令牌。

## 依赖

| 依赖 | 用途 |
|---|---|
| `pkg/core` | 模块描述符与交付功能 |
| `pkg/config` | 一个没有输入项的配置节：旧配置里的 `authn.invite.ttl` 因而按完整路径报未知键 |
| `pkg/settings` | `Optional`：声明并读取设置项 `authn.invite.ttl` 与签发、探测两档预算，订阅 `settings.changed` 监视放宽方向 |
| `pkg/db` | `authn_invitations` 表与迁移 |
| `pkg/kv` | 签发与探测预算的计数后端；必需，缺席即启动失败 |
| `pkg/authn`（根包） | `Membership`、`AccountID`、`Member`；`Invites` 与值类型取自本子包 |
| `Memberships`（根包声明，由 `authn.account` 交付） | 读调用者在该租户的成员关系、以 `PutJoining` 的条件写为调用者写来源为 `invite` 的成员行 |
| `Accounts`（根包声明，由 `authn.account` 交付） | 读调用者的标识，用来核对被邀请地址是否已证明 |
| `mount.Mounter`（`pkg/authn/internal/mount`） | 挂管理面与接受路由 |
| `Decider`（根包） | 管理面判定；可选取用，缺席即全拒 |
| `SignupVerification`（根包声明，由 `authn.account` 交付） | 管理面由人发起的写经 `SignupStateOf` 读发起者的注册验证状态；必需，读失败答 503 |
| `audit.Audit` | 管理面写路由的强制档审计，加通知失败的尽力档行；必需 |
| `InviteNotifier`（根包） | 可选：签发成功后请求一次接受通知；缺席时结果答 `not_configured` |
| `pkg/i18n` | 从请求上下文取通知语言；没有点名语言时 `InviteNotice.Locale` 为空 |
| `event.Bus` | 可选：签发与生命周期事件；成员关系变更经它送达撤销订阅 |
| `authn.TenantStateResolver`（根包） | 可选：接受判定租户是否在服务；缺席时没有租户状态可判 |
| `log.Logger` | 可选：诊断 |
| `pkg/feature`（根包） | 以 `feature.Spec` 声明静态特性 `authn.invitations`；不取用 `feature.Board` |

**方向核对。** `authn.invite` 位于认证功能目录内，不 import `pkg/tenant`、授权功能目录、宿主、`pkg/mail` 与 `pkg/feature/board`，也不 import 认证功能目录里别的实现子包。通知经由根包接口取用，渲染与邮件投递在 `authn.invitemail` 里，故邀请模块的依赖闭包不出现任何邮件载体。`pkg/feature` 根包只依赖标准库，为声明特性 import 它不扩大本模块的闭包（[feature](../design-feature.md)）。管理面判定经根包的 `Decider`，与 `authn.admin` 同一个接口（[design-authn-admin](design-authn-admin.md)）。

## UML 图

### 状态机

下图固定邀请的去向。存量列只写三态；「已过期」由时钟越过 `ExpiresAt` 推出，是观察态而不是一列，所以图上它是从待接受出发的一条迁移，注明了它不落列。终态不再有出边：一条已接受、已撤销或已过期的邀请不能回到待接受。

```mermaid
stateDiagram-v2
    [*] --> 待接受
    待接受 --> 已接受 : 接受令牌并赢得条件写
    待接受 --> 已撤销 : 管理面撤销／签发被取代／永久失败结算／成员停用或移除／组织删除
    待接受 --> 已过期 : 时钟越过 ExpiresAt（推导，不落列）
    已接受 --> [*]
    已撤销 --> [*]
    已过期 --> [*]
```

### 类型与接口

下图固定本模块交付什么、取用什么。`Invites` 与 `inviteProduct` 同处 `pkg/authn/invite`，后者排他实现前者；接受路由按令牌兑现，管理面经 `Decider` 判定。图上看不出的是：同一（租户，地址）下最多一条待接受邀请，由部分唯一索引与签发的取代路径共同保证。

```mermaid
classDiagram
    class Invites {
        <<interface>>
        +Issue(ctx, in) Issued
        +List(ctx, tenant, page) []Invitation, string
        +Revoke(ctx, tenant, id)
        +Accept(ctx, in) Membership
    }
    class IssueInvite
    class AcceptInvite
    class Issued
    class Invitation
    class InvitationStatus
    class InviteNotifier {
        <<interface>>
        +Notify(ctx, notice) InviteDelivery, error
    }
    class InviteNotice
    class InviteDelivery
    class InvitationRevoker {
        <<interface>>
        +RevokeTenantInvitations(ctx, tenant)
    }
    class TenantStateResolver {
        <<interface>>
        +TenantStateOf(ctx, id) TenantState, error
    }
    class inviteProduct
    class Decider {
        <<interface>>
        +Decide(ctx, d) bool, error
    }
    class Memberships {
        <<interface>>
    }
    class Accounts {
        <<interface>>
    }
    inviteProduct ..|> Invites
    inviteProduct ..|> InvitationRevoker
    inviteProduct ..> TenantStateResolver : 可选取用
    inviteProduct ..> Decider : 可选取用
    inviteProduct ..> InviteNotifier : 可选取用，签发后一次
    inviteProduct ..> Memberships : 读成员关系、写成员行
    inviteProduct ..> Accounts : 读调用者的标识
    Invites ..> IssueInvite
    Invites ..> AcceptInvite
    Invites ..> Issued
    Invites ..> Invitation
    Invites ..> InviteDelivery
    Invitation ..> InvitationStatus
    InviteNotifier ..> InviteNotice : 入参
    InviteNotifier ..> InviteDelivery : 结果
```

**通知接口在根包里，渲染与投递在实现方。** `inviteProduct` 只取用 `InviteNotifier`，从不 import 邮件包；`authn.invitemail` 是仓库提供的实现，它把 `InviteNotice` 渲染成邮件并交给 `pkg/mail.Sender`。`InviteNotice` 是本模块交给通知实现、携带接受令牌的唯一值，实现方必须把它挡在日志、追踪、事件与错误文本之外。

## 核心数据结构

| 概念 | 类型 | 说明 |
|---|---|---|
| 邀请 | `Invitation` | 一条邀请的只读视图：标识、租户、地址、状态、签发者、期限、接受时刻、创建时刻；不含令牌 |
| 状态 | `InvitationStatus` | `待接受`（零值）／`已接受`／`已撤销` 的存量封闭集；「已过期」由时钟推出 |
| 签发结果 | `Issued` | 邀请视图与一次交出的令牌 |
| 通知输入 | `InviteNotice` | 租户、规范化地址、令牌、期限与请求语言；唯一携带令牌的通知值 |
| 投递结果 | `InviteDelivery` | `not_configured`（零值）／`sent`／`failed`；只描述通知，不描述邀请；`sent` 时应答不带令牌 |
| 签发入参 | `IssueInvite` | 租户、地址、可选期限 |
| 接受入参 | `AcceptInvite` | 令牌；用户名、显示名与口令被忽略，保留只为旧客户端可发送 |

**`authn_invitations` 表**，在 `pkg/authn/invite` 的一份迁移集里，`migrations/postgres/` 与 `migrations/sqlite/` 各一份同名文件，序号自 `0001_` 起。地址与 `authn_identifiers.value` 一样按规范化后的明文存，不加密；令牌只存摘要。下面写 PostgreSQL 的形态；SQLite 的差别只有类型与部分索引的写法。

```sql
CREATE TABLE authn_invitations (
    id           TEXT        NOT NULL PRIMARY KEY,   -- 邀请标识，随机、不可推测
    tenant_id    TEXT        NOT NULL CHECK (tenant_id <> ''),
    email        TEXT        NOT NULL,               -- 规范化后的地址
    token_digest TEXT        NOT NULL,               -- 令牌的 SHA-256 十六进制
    status       SMALLINT    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)), -- 0 待接受、1 已接受、2 已撤销
    invited_by   TEXT        NOT NULL,               -- 签发者的账户标识，无外键
    expires_at   TIMESTAMPTZ NOT NULL,
    accepted_at  TIMESTAMPTZ NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (token_digest)
);
CREATE INDEX authn_invitations_tenant ON authn_invitations (tenant_id, created_at);
CREATE UNIQUE INDEX authn_invitations_one_pending ON authn_invitations (tenant_id, email) WHERE status = 0;
```

- **`token_digest` 全局唯一**：一个令牌映射到一条邀请，接受按摘要查行，查询不带租户谓词——接受者还没有该租户的成员归属，链接也不含租户。这是本模块唯一不按租户过滤的查询：兑现前不存在可依附的租户，令牌是唯一的定位键。
- **部分唯一索引 `authn_invitations_one_pending`** 仲裁同一（租户，地址）下至多一条待接受邀请：一条新签发在插入处与并发的签发相撞即 `ErrInvitationPending`，而不是留下两条并存的活令牌。
- **`invited_by` 是账户标识，不建外键**：与认证其余表一样，表之间不建外键，完整性由写入方在自己的事务里保证（[design-authn-account](design-authn-account.md)）。
- **状态筛选索引 `authn_invitations_tenant_status_created`** 由 `0002_authn_invitations_status_index.sql` 建立，列为 `(tenant_id, status, created_at)`：租户与存量状态先收窄，创建时刻再排序。两个待接受观察态共用 `status = 0`，差别在 `expires_at` 的比较，因此都走这一条索引。

### 状态一览

存量状态是「待接受」「已接受」「已撤销」，加上由时钟推出的观察态「已过期」；转换见 UML 图的状态机。

| 状态 | 一句话含义 | 当前动作（邀请） | 直觉画面 |
|---|---|---|---|
| 待接受 | 已签发、未接受、未撤销且未过期 | 等待持有者出示令牌兑现 | 一张还没打孔的入场券 |
| 已接受 | 令牌已被一次兑现，终态 | 不再参与兑现，成员关系已由账户模块写下 | 已检票入场的票根 |
| 已撤销 | 管理面撤销、签发被取代、永久失败结算、成员停用／移除或组织删除，终态 | 拒绝该令牌的后续出示 | 作废的票 |
| 已过期 | 待接受而时钟已越过 `ExpiresAt`，由推导得出，不落列 | 仍列在待接受里，接受路径按当前时刻拒绝它 | 过期未用的票 |

## 核心算法与流程

### 走查

初始状态：`authn_invitations` 里有一条待接受的邀请；受邀人已经登录，并在自己的账户上证明了被邀请的地址。

1. 计入探测预算；超限即 429，不查令牌。
2. 按令牌摘要查行：命中，状态为待接受，时钟未越过 `ExpiresAt`。持久状态不变。
3. 核对租户状态：装配有 `authn.TenantStateResolver` 时读它；租户停用即 409、邀请保持待接受，租户已删除即 404、邀请被撤销。装配没有解析器时跳过。持久状态不变。
4. 核对调用者：请求有身份，账户的标识里有一条与邀请地址相等的已证明邮箱。没有身份即 401；没有这条证明即 403，邀请保持待接受。
5. 核对成员关系：已有且已停用即 409，并撤销这条邀请；已有且启用则记下它的来源与 SSO 豁免。持久状态不变。
6. 抢先兑现：条件写把该行从待接受翻到已接受。持久状态是这一行 `status` 变为已接受、`accepted_at` 非空。
7. `Memberships.PutJoining` 为调用者写成员行：新建的来源为 `invite`，已有的保留来源与 SSO 豁免。持久状态是新增或补上的成员行。
8. 返回成员关系。邀请行保持已接受，令牌不再可用。

写成员行若是临时失败，邀请退回待接受，同一令牌可以重试；永久失败则结算为已撤销。

### 签发

1. 校验租户标识：空、超过 255 字节或含空白与控制字符即 `ErrInvalidArgument`，在写任何行之前拒绝。
2. 确定期限：显式期限必须晚于当前时刻且不晚于签发时刻加 90 天，否则 `ErrInvalidArgument`；不带期限时取设置时长，设置读取失败或越出 1 小时到 90 天的取值域时取声明默认值并记一行 `Error`。
3. 依次检查三档签发预算：被邀请地址、发起账户、整个部署；任一超限即 429，在写任何行之前拒绝。地址按规范化后的收件箱形态计数。
4. 规范化地址；不成形即 `ErrInvalidEmail`，在写任何行之前拒绝。
5. 拒绝不合格目标：被邀请地址已属于该租户的活跃成员或停用成员、该地址属于服务账户、或目标租户是系统租户时，拒绝这次签发；判据在读标识与成员关系之后、写行之前。
6. 生成令牌（256 位随机、`spd_inv_` 前缀、base64url）与它的 SHA-256。
7. 一个事务里：把同一（租户，地址）下仍为待接受的旧邀请置为已撤销（按部分唯一索引筛选，条件写），插入新邀请行。
8. 发布事件。
9. 若装配交付了 `InviteNotifier`，以五秒上限请求一次通知；结果归入 `InviteDelivery`。没有通知接口、或实现答「未配置」时是 `not_configured`。
10. 返回签发应答：邀请视图、投递结果，以及只在投递不是 `sent` 时出现一次的令牌明文。

签发使旧链接立即失效：旧行转已撤销，它的令牌此后一律 `ErrInvitationRevoked`。重发是一次新的签发：它只取代待接受的旧行，已接受与已撤销的行不被改写，新行是新的一条待接受邀请。

### 通知

1. 通知只在签发成功之后请求，且只请求一次；它不在邀请的事务里，也不回写邀请行。
2. 请求把一个 `InviteNotice` 交给实现：租户、规范化地址、令牌、绝对期限与请求语言。语言取自请求上下文，没有被点名时是空串，由实现方按自己的目录回退。
3. 调用带五秒上限。实现返回 `InviteSent` 且无错误即 `sent`；实现返回 `InviteNotConfigured` 且无错误即 `not_configured`，不记日志、不写审计；其余结果（含超时、拒绝、未知结果与 panic）一律 `failed`。
4. `failed` 写一行 `Error` 日志（租户、邀请标识、结果与成因，不含地址与令牌），并写一条尽力档 `authn.invitation.deliver` 审计行，以 `action_id` 关联同一次签发。
5. 通知的结果不改变邀请行与 HTTP 状态，只决定应答是否带令牌：`sent` 不带，`failed` 与 `not_configured` 带一次。实现方自身负责渲染与投递，本模块不猜测它说了什么、经哪条通道送出。

### 接受

1. **要求身份。** 请求没有已登录调用者即 `ErrInvitationUnauthenticated`，答 401 与强认证挑战；不查令牌也读不到邀请。
2. **计入探测预算。** 按令牌摘要与客户端地址在查令牌之前计数；超限答 429，不透露令牌是否存在。
3. **按摘要查行。** 令牌不成形或查不到行即 `ErrInvitationNotFound`；查到但状态非待接受，按状态答 `ErrInvitationAlreadyAccepted` 或 `ErrInvitationRevoked`；时钟不早于 `ExpiresAt` 即 `ErrInvitationExpired`。这三种终态只按状态报告，不读地址与租户名。
4. **核对租户状态。** 有 `authn.TenantStateResolver` 时读租户状态：停用即 `ErrInvitationTenantInactive`，邀请保持待接受；已删除或闭集之外即撤销该行并答 `ErrInvitationNotFound`；读失败答 503。
5. **核对被邀请地址的证明。** 读调用者账户的标识，找一条 `kind` 为邮箱、值与邀请地址（规范化后）相等且 `Verification.Proves` 为真的标识。没有这条标识即 `ErrInvitationEmailUnproved`，邀请保持待接受；读标识失败不是拒绝，答 503。地址不需要是该账户的登录方式。
6. **核对现有成员关系。** 已有且已停用即 `ErrInvitationMembershipInactive`，撤销这条待接受邀请后答 409；已有且启用则记下它的来源与 SSO 豁免，供第 8 步保留。
7. **抢先兑现。** 条件写把该行从待接受翻到已接受，条件是状态仍为待接受且未过期；两个并发接受同一令牌至多一个赢得这次翻转，输者按终态报告，不写任何行。未赢得翻转时在至多三次尝试内重读并重试，最终仍按终态报告；三次之后仍未赢得翻转答 503。
8. **兑现。** `Memberships.PutJoining` 为调用者写成员行：新建的来源 `invite`，已有的保留来源与 SSO 豁免；写是条件写，读到活跃而在写入时已被停用或移除的成员关系不会被复活，此时答 409 并撤销邀请。接受不建账户、不写标识、不证明邮箱。
9. **失败结算。** 第 8 步跨模块，在自己的事务里，与第 7 步的翻转不在一条事务里。它失败时按失败性质结算这次抢先：临时失败把行还给待接受（重试不被锁死），永久失败（标识或绑定冲突一类）结算为已撤销（一条再也兑现不了的邀请不该把令牌留到 TTL 结束）。结算是「状态仍为已接受」的条件写，绝不无条件覆盖；结算本身的失败只记 Error，不改变返回给调用方的错误。

### 列表与撤销

- **列表**按租户取一页，待接受的在前（按创建时刻倒序），游标是上一页末条的创建时刻与标识。已过期的待接受行照常列出（REQ-tenancy-015）——它仍是一行待接受，调用方按 `ExpiresAt` 读得出它已经陈旧；隐藏它会把一条已送出的邀请显示成一条从未发出的邀请。管理面路由另按 `status` 收窄：`pending` 只看未过期的待接受行，`expired` 只看已过期的待接受行，`accepted` 与 `revoked` 各看一种终态；缺省列出全部。收窄谓词与分页谓词叠在同一次查询上，游标仍按同一次排序推进。
- **撤销**把一条待接受邀请转已撤销，条件是状态仍为待接受。已接受的邀请不静默退群，答 `ErrInvitationAlreadyAccepted`（移除成员是 `Memberships.Remove` 与成员管理路由的事）；已撤销的邀请再撤销是幂等成功。撤销从不改写一条已接受或已撤销的行。
- **重发只新增，不改写终态。** 对一条已接受或已撤销的邀请重发，只是以同一地址签发一条新的待接受邀请；原邀请保持它的终态，历史与审计都指向原行。对一条待接受的邀请重发，旧行在同一事务里转已撤销，新行是唯一可兑现的一条。

### 复杂度

- 签发、接受与撤销按唯一索引（`token_digest`、主键、部分唯一索引）定位，单行读写。
- 列表按 `authn_invitations_tenant` 或 `authn_invitations_tenant_status_created` 索引走一页，条数由 `Page.EffectiveLimit` 界定。带 `status` 的读多一个状态谓词，仍是一页的范围。
- 通知是一次有界外部调用，复杂度由实现方决定；本模块只加一次五秒上限与一次可能的日志、审计写入。

## 错误与失败语义

| 情形 | 表现 |
|---|---|
| 地址不成形 | 签发 `ErrInvalidEmail`，在写行之前拒绝 |
| 租户标识不成形，或显式期限不晚于当前时刻、晚于 90 天 | 签发 `ErrInvalidArgument`，在写行之前拒绝 |
| 被邀请地址已属于该租户的活跃或停用成员、属于服务账户，或目标是系统租户 | 拒绝签发，在写行之前拒绝 |
| 同一（租户，地址）已有待接受邀请且并发的签发赢在插入处 | `ErrInvitationPending` |
| 令牌不成形、无对应行 | `ErrInvitationNotFound` |
| 令牌属于已过期的待接受邀请 | `ErrInvitationExpired` |
| 令牌属于已撤销的邀请 | `ErrInvitationRevoked` |
| 令牌属于已接受的邀请 | `ErrInvitationAlreadyAccepted` |
| 撤销一条已接受的邀请 | `ErrInvitationAlreadyAccepted` |
| 撤销一条已撤销的邀请 | 幂等成功 |
| 接受请求没有身份 | 401 与强认证挑战；不查令牌 |
| 接受者没有证明被邀请地址 | 403 `invitation-email-unproved`，邀请保持待接受 |
| 接受者在该租户的成员关系已停用 | 409 `membership-inactive`，并撤销该待接受邀请 |
| 接受者面对的租户已停用 | 409 `conflict`，邀请保持待接受 |
| 接受者面对的租户已删除 | 404，并撤销该邀请 |
| 读取租户状态失败 | 503，不按可接受处理 |
| 接受读取命中已过期、已接受或已撤销的令牌 | 200，只带 `state` |
| 组织删除后的收回失败 | 删除成功；记一行警告，接受侧仍按租户状态拒绝 |
| 接受赢了条件写而写成员行临时失败 | 结算回待接受，返回失败 |
| 接受赢了条件写而写成员行永久失败（标识或绑定冲突） | 结算为已撤销，返回失败 |
| 签发或探测的预算超限 | 429 `throttled`，带 `Retry-After` |
| 预算计数后端读不到 | 503，不按放行处理 |
| 管理面判定接口缺席，或 `Decide` 返回假 | 403 `forbidden` |
| 发起者是未验证账户，而这是签发或撤销 | 403 `contact-required`，不询问判定接口与 `Invites`、不写审计 |
| 读取发起者的注册验证状态失败 | 503，动作不执行 |
| 管理面强制档审计写不进 | 503，动作不执行 |
| 接受路由依赖不可用 | 503 |
| 签发后通知失败、超时或实现 panic | `email_delivery` 为 `failed`，HTTP 仍为 201，邀请成立且应答带一次令牌；记一行 `Error` 与一条尽力档 `authn.invitation.deliver` 审计行 |
| 装配没有通知接口，或实现答未配置 | `email_delivery` 为 `not_configured`，HTTP 仍为 201；不记日志、不写审计 |
| `status` 查询的值不在封闭集里 | 400，不答未过滤的一页 |
| 签发读取存活期设置失败或取值越界 | 取声明默认值并记一行 `Error`，签发照常进行 |
| 配置里仍写着 `authn.invite.ttl` | 启动失败，按未知键点名该键 |

**回绝映射是封闭的。** 管理面：400（`urn:speed:authn:bad-request`）是地址不成形、租户标识不成形、期限越界、签发目标不合格、正文结构不可读、`status` 不在封闭集里与其余参数错误；403（`urn:speed:authn:forbidden`）是判定接口的拒绝；404 是邀请标识不指向任何一条邀请；409（`urn:speed:authn:conflict`）是 `ErrInvitationAlreadyAccepted`；429 是签发预算耗尽；503（`urn:speed:authn:unavailable`）是其余失败，以及没落地的强制档审计，成因只进日志。接受面：401 是无身份，403 `invitation-email-unproved` 是地址未证明，409 `membership-inactive` 是停用的成员关系，409 `conflict` 是已过期、已撤销、已接受或租户已停用，404 是令牌不存在或租户已删除，429 是探测预算耗尽，503 是其余失败。`Problem.Type` 前缀与码集归 `pkg/authn/internal/mount`。

**通知的失败不进错误映射。** 它是一次尽力而为的附带动作，结果在 201 应答的 `email_delivery` 字段里；把它映射成 4xx 或 5xx 会把一条已经成立的邀请说成没发出去。实现方返回的错误只用于日志与审计行。

### 非保证

- **接受证明的是被邀请地址的归属，不是受邀人的身份。** 当事人必须自己证明该地址；持有链接本身不足以接受，转发链接也不足以让第三方接受。邮箱被共享、被转发或被回收时，能证明该地址的账户就是受邀人，这是邮箱归属作为判据的边界。
- **停用不是终态，删除才是。** 租户停用期间的令牌被拒绝，但邀请保持待接受；租户重新启用后同一令牌可以再兑现。只有已接受、已撤销与已删除租户的行才是不可再兑现的。接受读取对终态与已过期的令牌只暴露状态，不暴露地址、租户名与有效期。
- **签发拒绝的是可判定的不合格目标，不是全部错误目标。** 签发在写行之前拒绝已属于该租户的成员、服务账户与系统租户；它不核对地址是否真的属于受邀人，也不阻止签发者把邀请发给错误的人。
- **签发者选的人对不对不归本模块。** 一次签发给谁由发起者决定，本模块保证发起者的身份与租户范围。
- **投递质量由通知实现与载体承担。** 本模块只知道这次通知的结果是 `sent`、`failed` 还是 `not_configured`；`sent` 不证明地址可达或被阅读，`failed` 也不证明消息没有送出（超时与未知结果同样记作 `failed`）。本模块不重试、不排队。

### 假设与守住它们的测试

以下行为未经运行核实，依赖它们的断言以对应的测试守住，测试失败即视为前提不成立：

- **假设账户的标识里，一条已证明邮箱足以代表地址归属。** 接受按 `(kind, value)` 找标识并以 `Verification.Proves` 判定；测试断言 `email-code`、`sms-code` 与 `idp:<提供方标识>` 通过，`invite`、`admin`、`import-unverified` 与 `self` 被拒绝，且被拒绝时不写成员行。
- **假设条件写在两个并发接受之间只放一个赢家。** 测试并发兑现同一令牌，断言恰好一条成员行、恰好一次兑现成功。
- **假设成员关系变更事件会被至少送达一次。** 撤销以「状态仍为待接受」为条件，所以重复送达不改变结果；测试断言停用与移除各撤销一行，已接受与已撤销的行不动。
- **假设成员关系的写是条件写，读到活跃与写到落地之间不会被停用或移除穿透。** 测试在读写之间停用成员，断言接受答 409、邀请被撤销、没有成员行被复活。
- **假设租户状态接缝如实报告在服务、停用与删除。** 测试断言停用的租户答 409 且行保持待接受、重新启用后同一令牌可接受；已删除的租户答 404 且行被撤销；组织删除的收回只动该租户状态为待接受的行。

## 并发与事务边界

- **抢先兑现是条件写。** 接受的条件写是「状态仍为待接受且未过期」的更新，`RowsAffected` 为一是赢的条件；输者不写任何行。争用下接受最多重读并重试三次，每次都重新核对身份、租户状态与成员关系；仍未赢得翻转时答 503。
- **成员关系的写也是条件写。** `PutJoining` 只在账户原本没有成员关系、或其成员关系仍活跃时写下活跃成员；读过之后被停用或移除的成员关系不被复活，接受答 409 并撤销邀请。
- **撤销与收回都以状态为条件。** 成员停用或移除的事件撤销、组织删除后的 `RevokeTenantInvitations`，都以「状态仍为待接受」更新；重复调用不改变结果，已接受与已撤销的行不被改写。收回失败不回滚删除，接受侧仍按租户状态拒绝。
- **本模块只在自己的事务里写自己的表。** 邀请行的写在本模块的事务里；成员行的写在 `authn.account` 的事务里。跨模块的两次写用结算补偿，至少一次、非原子；正确性的底线由条件写与状态检查承担，不靠结算做完（[design-authn](design-authn.md)）。
- **签发在同一事务里取代旧行并插入新行。** 两条写共享一条事务，部分唯一索引在插入处仲裁并发签发。
- **管理面处理器不持有事务。** 接口的询问、强制审计与 `Invites` 的写各自独立；强制审计不在调用方事务里（[design-authn-admin](design-authn-admin.md)）。
- **通知在签发事务之外、在应答之前同步调用。** 它带五秒上限，不持有邀请行的事务；失败的尽力档审计也不在事务里。实现方必须并发安全，且不得把令牌写进日志、追踪、事件或错误文本。实现方的 panic 在调用处被收回并记为 `failed`，不会把一条已完成的签发变成 500。
- **`Invites` 的实现在请求路径上同步调用**，必须并发安全。

## 扩展点

- **新增一条签发的资格判据。** 谁可以签发由 `Decider` 实现决定，本模块只保证 `Decision` 的填法（路径里的租户、`membership.manage` 动作）。新增资格不改本模块。
- **邀请换成另一种地址种类**（例如按手机号邀请）：扩展的是地址的规范化、送出通知的通道与接受时的证明核对，签发与状态机不变；接受仍然只写成员行，不为当事人开户。
- **通知是扩展点，由宿主或 `authn.invitemail` 实现。** 换一个通知实现即换渲染语言、正文与投递渠道；本模块只交出 `InviteNotice`，不定义渠道、模板与语言目录。`authn.invitemail` 是仓库提供的那一份，把通知渲染成邮件后交给 `pkg/mail.Sender`。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| `Invites` 接口、值类型与哨兵 | 已实现 | `pkg/authn/invite/seam.go` `Invites`、`IssueInvite`、`Issued`、`Invitation`、`AcceptInvite`、`InvitationStatus`、`ErrInvitationNotFound`、`ErrInvitationExpired`、`ErrInvitationRevoked`、`ErrInvitationAlreadyAccepted`、`ErrInvitationPending`、`ErrInvitationUnauthenticated`、`ErrInvitationEmailUnproved`、`ErrInvitationMembershipInactive`、`ErrInvitationTenantInactive`、`ErrInvalidEmail`、`ErrInvite` |
| 通知接口 `InviteNotifier`、`InviteNotice`、`InviteDelivery`、签发应答的 `email_delivery` 与「投递成功时不带令牌」 | 已实现 | `pkg/authn/invite_notice.go` `InviteNotifier`、`InviteNotice`、`InviteDelivery`、`InviteSent`、`InviteFailed`、`InviteNotConfigured`；`pkg/authn/invite/deliver.go` `(*product).deliverInvite`、`(*product).notify`；`pkg/authn/invite/admin.go` `handleIssue`；`pkg/authn/invite/views.go` `issuedViewOf` |
| 签发通知失败的日志与尽力档审计行 | 已实现 | `pkg/authn/invite/deliver.go` `(*product).deliverInvite`；`pkg/authn/invite/audit.go` `actionDeliver`、`deliveryAudit` |
| 列表的 `status` 筛选与状态索引 | 已实现 | `pkg/authn/invite/query.go` `listFilter`、`parseListFilter`、`(*product).list`；`pkg/authn/invite/admin.go` `handleList`；`pkg/authn/invite/migrations/postgres/0002_authn_invitations_status_index.sql`、`pkg/authn/invite/migrations/sqlite/0002_authn_invitations_status_index.sql` |
| 邀请邮件的渲染与投递模块 `authn.invitemail` | 已实现 | `pkg/authn/invitemail/module.go` `Module`；`pkg/authn/invitemail/config.go`；`pkg/authn/invitemail/messages.go`、`locales/zh-CN.json`、`locales/en-US.json` |
| `authn.invite` 模块、`authn_invitations` 表与迁移、状态机、签发／列表／撤销／接受 | 已实现 | `pkg/authn/invite/invite.go` `Module`；`pkg/authn/invite/store.go` `tableInvitations`；`pkg/authn/invite/migrations/postgres/0001_authn_invitations.sql`、`pkg/authn/invite/migrations/sqlite/0001_authn_invitations.sql`；`pkg/authn/invite/issue.go` `Issue`；`pkg/authn/invite/query.go` `List`、`Revoke`；`pkg/authn/invite/accept.go` `Accept` |
| 管理面路由（判定顺序、强制审计、回绝映射） | 已实现 | `pkg/authn/invite/routes.go` `mountRoutes`；`pkg/authn/invite/admin.go` `handleIssue`、`handleList`、`handleRevoke`；`pkg/authn/invite/pipeline.go` `admit`、`asks`、`callDecider`、`auditRequired`、`auditBestEffort`、`answer` |
| 接受路由（公开读取、需身份的兑现） | 已实现 | `pkg/authn/invite/routes.go` `mountRoutes`；`pkg/authn/invite/accept_http.go` `handleAcceptView`、`handleAccept`、`viewToken` |
| 接受时核对调用者的已证明邮箱与来源为 `invite` 的成员行 | 已实现 | `pkg/authn/invite/accept.go` `(*product).Accept`、`(*product).requireProvenInvitedEmail`、`acceptedMembership`；`pkg/authn/invite/token.go` `SourceInvite` |
| 验证渠道 `invite` 保留在封闭集但不证明归属 | 已实现 | `pkg/authn/internal/model/directory.go` `VerifiedViaInvite`、`Verification.Valid`、`Verification.Proves` |
| 签发与探测预算 | 已实现 | `pkg/authn/invite/budget.go` `(*product).chargeIssue`、`(*product).chargeProbe`；`pkg/authn/invite/accept_http.go`、`pkg/authn/invite/admin.go` 的调用点 |
| 成员被停用或移除时撤销待接受邀请 | 已实现 | `pkg/authn/invite/lifecycle.go` `(*product).onMembershipChanged`；`pkg/authn/invite/invite.go` `wire` |
| 组织删除时收回一个租户的待接受邀请（`authn.InvitationRevoker`） | 已实现 | `pkg/authn/invite/lifecycle.go` `(*product).RevokeTenantInvitations`；`pkg/authn/invite_cleanup.go` `InvitationRevoker`；`pkg/tenant/ownership/delete.go` `(*product).DeleteOrganization` |
| 接受按租户状态拒绝停用或已删除的租户 | 已实现 | `pkg/authn/invite/accept.go` `(*product).refuseUnservedTenant`；`pkg/tenant/catalog/roster.go` `(*roster).TenantStateOf` |
| 成员关系写是条件写，不复活已停用的成员 | 已实现 | `pkg/authn/invite/accept.go` `(*product).Accept`、`(*product).settle`；`pkg/authn/account/memberships.go` `(*product).PutJoining` |
| 接受读取对终态与已过期的令牌只返回状态 | 已实现 | `pkg/authn/invite/accept_http.go` `(*product).handleAcceptView`、`(*product).viewToken`；`pkg/authn/invite/views.go` `readState` |
| operation 声明与 OpenAPI（[REF-openapi](../../../references.md)）片段生成 | 已实现 | `pkg/authn/invite/operations.go`；`pkg/authn/invite/openapi_generated_test.go`；片段 `pkg/authn/invite/openapi/invite.json` |
| 静态特性 `authn.invitations` 的声明 | 已实现 | `pkg/authn/invite/invite.go` `Module`；`pkg/authn/invite/feature_test.go` `TestTheModuleDeclaresTheInvitationsFeature` |
| 注册验证状态的门控 | 已实现 | `pkg/authn/invite/pipeline.go` `(*product).gateSignup`；`pkg/authn/invite/admin.go` `handleIssue`、`handleRevoke`；`pkg/authn/invite/invite.go` `Module`、`product.verification` |
