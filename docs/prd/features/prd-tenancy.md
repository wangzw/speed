# 组织与成员

## 问题陈述

一个部署要服务多个彼此隔离的租户。使用者需要创建租户、把他人拉进租户、在租户之间切换；每个用户账户在开户时都会得到自己的个人租户，不论开户来源、不论邮箱是否已证明、也不论是否已属于其他组织。本文规定这些动作对使用者意味着什么，以及不满足条件时得到什么结果。

## 范围

租户的创建、名称与停用；租户名册的列出；成员关系的授予、停用、恢复与移除；邀请的签发、链接、通知投递、重发、撤销与接受；租户切换；个人租户；非成员访问租户资源的可见结果；冒充期间的租户选择与当前租户。

### 不在范围内

除个人租户开通所需的角色与授权外，平台域的权限目录、平台角色与绑定属于平台设置与授权；租户管理员在组织管理面对本租户角色与绑定的管理属于租户管理能力。账户本身的建立、登录、邮箱验证与注销属于自助注册、登录与账户自助；冒充的发起、记录与结束属于平台运营与账户安全。租户的层级与租户树、租户数据的导出不属于本能力。

## 参与者

- **平台运营者**：持有平台域的租户读取与租户管理权限，可登记租户、转移所有者、删除组织、停用与启用、列出名册；名称创建后不可改。发起者为未证明邮箱的账户时，其写动作被拒绝。
- **成员管理员**：在某个租户内持有成员管理权，可在该租户内签发与撤销邀请、查看与增删成员。
- **受邀人**：持有邀请链接的访问者，可以是已有账户，也可以在系统中尚无账户。接受邀请不需额外的租户权限。
- **用户账户**：不论开户来源是自助注册、短信首登、社交或企业首登还是管理员创建，也不论邮箱是否已证明、是否已属于其他组织，每个用户账户在开户时都会获得个人租户，并在其中持有非内建 `tenant-admin` 角色。
- **普通租户成员**：被授予成员关系的账户，可进入该租户并使用其中的资源。
- **冒充中的运营者**：会话以被冒充账户的身份行事，被限定在一个租户内。

## 功能需求

### REQ-tenancy-001

系统必须允许平台运营者登记一个新租户，为它给出名称、显示名与可选资料，新租户处于启用状态；登记者成为该组织的所有者，并成为首任 tenant-admin。内部标识由系统生成，运营者不填写，界面也不展示。

- **验收**：登记一个未被占用的名称与显示名成功，租户随即出现在名册读取结果中且状态为启用；登记者是该租户的所有者并持有 tenant-admin。名称已被别的租户占用时被拒绝，名册不新增任何租户。名称为空、含空白或控制字符时在写入之前被拒绝。界面没有填写或查看内部标识的入口。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-operator-onboards-tenant.spec.ts` `operator-onboards-tenant: an operator registers a tenant, invites its first member, and the member joins`（覆盖登记、名称冲突拒绝、启用状态；登记者即所有者与首任 tenant-admin 由后端用例覆盖）

### REQ-tenancy-002

系统必须为每个租户保持唯一的名称；名称在创建后不可修改。

- **验收**：名称在全部租户中唯一；对已创建租户修改名称的请求被拒绝，名称与状态都不变；以另一个租户已用的名称登记被拒绝。内部标识不随名称变化，也不在界面上出现。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-operator-onboards-tenant.spec.ts` `operator-onboards-tenant: an operator registers a tenant, invites its first member, and the member joins`（覆盖名称冲突拒绝与详情页无改名控件；改名请求被拒绝由后端用例覆盖）

### REQ-tenancy-003

系统必须允许平台运营者停用与重新启用租户，停用不删除租户，名称不变。

- **验收**：停用后名册中该租户状态为停用，仍可按名称读取到它，名称与停用前相同。重新启用后状态为启用。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-004

停用的租户不得由请求面解析出来，发往它的请求与发往不存在的租户的请求得到相同结果。

- **验收**：租户停用后，带着该租户名的业务请求得到 404；重新启用后同一请求进入该租户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-005

系统必须允许平台运营者列出全部租户，结果按名称排序；一个租户也没有时给出空列表而不是错误。

- **验收**：名册中有若干租户时，列表包含全部租户且按名称排序。一个租户都没有时，读取成功且列表为空。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-operator-onboards-tenant.spec.ts` `operator-onboards-tenant: an operator registers a tenant, invites its first member, and the member joins`

### REQ-tenancy-006

租户名册的读取必须要求平台域的租户读取权限。

- **验收**：持有读取权限而不持管理权限的调用方能读。不持读取权限者读被拒绝（403）。在某个租户内获得授权但在平台域不持有的调用方，不能借请求面触到名册。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-007

发起者是未证明邮箱的账户时，登记、转移所有者、删除组织与改状态必须被拒绝；没有任何邮箱的账户与服务账户不受这条限制。

- **验收**：邮箱未证明的运营者执行登记、转移所有者、删除组织与改状态各得到 403，名册与组织不发生改变。同一账户证明邮箱后，同一写动作成功。服务账户或没有任何邮箱的账户的写不被这条拒绝。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-008

系统必须允许成员管理员按页列出某个租户的成员，逐行给出用户名、显示名、成员关系启用状态、成员关系来源、加入时间与当前持有的角色名；成员按服务端的稳定次序分页，角色名按升序且不重复；界面不显示账户标识。

- **验收**：第一页的每名成员都带有用户名、显示名、启用状态、来源、加入时间与角色名；界面不出现账户标识；持有多个角色的账户给出全部角色名且按升序、无重复；没有角色的账户给出空角色列表。租户没有成员时，读取成功且列表为空。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-009

系统必须允许成员管理员把一个账户加为该租户的成员。

- **验收**：加入后，该账户带着这个租户的业务请求被准入，不再得到非成员的不可访问结果。对同一账户重复加入成功，成员列表里该账户只出现一次。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-enterprise-sso-onboarding.spec.ts` `enterprise-sso-onboarding: an operator connects an OIDC provider and an employee signs in with the work email`

### REQ-tenancy-010

系统必须允许成员管理员移除某个租户的成员；移除后该账户不再是成员。

- **验收**：移除后，该账户带着这个租户的业务请求得到与非成员相同的不可访问结果，成员列表不再包含它。移除一个本就不是成员的账户不改变任何状态。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-011

系统必须允许成员管理员签发一条邀请，点名租户与被邀请的地址，并在签发应答中一次性交出接受链接。

- **验收**：签发成功时得到一条待接受邀请与一个接受链接，该链接的明文此后不能从任何读取面再次取得。地址不成形时被拒绝，且不产生邀请。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-012

同一租户与同一地址下必须至多存在一条待接受邀请，重新签发必须使先前的链接失效。

- **验收**：对同一租户同一地址再次签发后，只有最新链接可以被接受，先前的链接得到已失效的结果。邀请列表里该地址只有一条待接受邀请。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-013

邀请链接必须以两个可区分的结果区分「不存在」与「不可再兑现」：已过期、已撤销与已接受共用「不可再兑现」这一个结果；失效链接一律不产生成员关系。

- **验收**：不存在的链接提示链接无效或已被删除；已过期、已撤销与已接受的链接共用同一条邀请已失效的提示，不点名是哪一种终态。不存在与不可再兑现都不展示邀请摘要，也都不产生账户或成员关系。未失效的待接受链接则进入接受流程。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`（已接受与已撤销的终态在被驱动；不存在的链接与 409 分支由组件层用例覆盖）

### REQ-tenancy-014

系统必须允许成员管理员撤销一条待接受的邀请；撤销一条已接受的邀请必须被拒绝。

- **验收**：撤销后该链接不能再被接受。撤销一条已接受的邀请得到冲突结果，成员关系保持不变。重复撤销同一条已撤销的邀请成功且无其他副作用。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-015

系统必须允许成员管理员按页列出某个租户的邀请。

- **验收**：租户有邀请时，第一页返回一页之内的邀请，继续翻页可取得其余邀请，直至遍历全部邀请。租户没有邀请时，读取成功且列表为空。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-016

邀请落点在访问者未登录时必须先要求登录或注册，随后展示被邀请的租户、地址与有效期。

- **验收**：匿名访问邀请链接先看到登录面，看不到邀请摘要。登录或注册后看到的租户、地址与有效期与签发时一致。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-017

接受邀请必须把成员关系绑定到被邀请的地址，而不是接受时登录的会话所属账户。

- **验收**：以一个与被邀请地址无关的账户登录后接受，该账户不成为成员。以被邀请地址对应的账户接受，成员关系落在该账户上。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-018

被邀请地址尚无账户时，接受必须用访问者给出的显示名与口令建立新账户，并把它加入租户。

- **验收**：接受后出现一个以该地址为登录标识的新账户，且它是该租户的成员。地址已有账户时，接受不产生第二个账户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-019

被邀请地址正是一条未证明的预留时，接受必须证明该邮箱并把成员关系挂到原账户，不建立第二个账户。

- **验收**：先以该地址完成自助注册、再经邀请接受后，原账户的邮箱转为已验证，该账户成为租户成员；系统中该地址仍只对应一个账户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-020

一条邀请令牌只能被兑现一次；并发或重复接受至多一次成功。

- **验收**：同一链接先后接受两次，第二次得到已接受的结果。并发接受同一链接时恰好一次成功，且只产生一条成员关系。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`（先后的重复兑换被驱动；并发兑换由后端用例覆盖）

### REQ-tenancy-021

接受成功后必须落在租户区首页，并把当前租户设为被接受邀请的租户。

- **验收**：接受成功后界面落在租户区首页，当前租户与邀请摘要中的租户一致，不停留在邀请页或登录面。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-022

系统必须在账户菜单的组织行显示当前组织。

- **验收**：当前组织的名字出现在账户菜单的组织行上。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-tenancy-023

选择一个不同的租户必须把界面切到该租户，此后展示的数据属于新租户，切换前租户的数据不再展示。

- **验收**：切换后控件显示新租户，随后打开的租户数据页读取的是新租户的数据，原租户的数据不再出现。切回原租户后读回原租户的数据。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-account-offboarding.spec.ts` `account-offboarding: an operator disables a member and the member returns after the restore`

### REQ-tenancy-024

冒充期间当前租户必须是冒充授权点名的租户。

- **验收**：冒充开始后，当前租户与冒充横幅中的租户一致。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-impersonation-for-support.spec.ts` `impersonation-for-support: the operator acts as a member and the record and audit trail survive the end`

### REQ-tenancy-025

不是某个租户成员时访问该租户的资源必须得到不区分原因的不可访问结果。

- **验收**：非成员请求该租户的资源页得到 404。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-026

系统必须为每个用户账户在账户建立时建立一个个人租户，不等邮箱验证；个人租户的建立不依赖进入租户区，也不存在按需补齐或再次创建的入口。服务账户不建立个人租户。

- **验收**：新账户建立后立即拥有个人租户，无论开户来源、无论邮箱是否已证明、无论是否已属于其他组织；进入租户区不会触发第二次创建，系统没有补齐个人租户的入口；服务账户没有个人租户。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`

### REQ-tenancy-027

个人租户中非内建 `tenant-admin` 角色的权限必须限于宿主声明的允许集，不含成员管理、邀请、身份提供方管理、冒充、客户端管理与跨成员放行。

- **验收**：个人租户的租户管理员能执行允许集内的业务动作。执行允许集外的租户级管理动作被拒绝。进程后来新声明的租户级权限不自动进入该角色。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-028

邮箱是否已证明不得成为建立个人租户的条件。

- **验收**：邮箱尚未证明的账户在建立时即拥有个人租户，界面同时呈现验证提示；该账户仍被未验证状态限制：绑定外部身份被拒绝并提示先证明邮箱。证明邮箱后同一绑定可进行，个人租户的名称不变。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`（覆盖未证明邮箱即拥有个人租户与验证提示；未验证状态下的绑定被拒绝不在该用例内驱动）

### REQ-tenancy-029

已属于其他租户的账户必须同样获得个人租户；其他租户的成员关系不得阻止或替代个人租户的建立。

- **验收**：已属于一个业务租户的账户在建立时同样拥有个人租户，其在业务租户的成员关系不变；个人租户与业务租户是两个不同的租户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-030

系统不得提供按需创建或补齐个人租户的入口；个人租户被停用后，只能由运营者恢复，账户进入租户区不得重新启用它。

- **验收**：系统没有按需创建或补齐个人租户的入口；运营者停用某个个人租户后，账户进入租户区得到租户不可用的结果，该租户保持停用，状态与名称都不变。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-044

账户停用时，其个人租户必须随之停用；账户恢复时随之恢复，但运营者主动停用的个人租户不得因账户恢复而重新启用。

- **验收**：停用账户后，名册中其个人租户显示为停用；恢复账户后，因账户停用而停用的个人租户重新启用；运营者先行停用的个人租户在账户恢复后仍为停用。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-045

账户删除时，必须删除其个人租户的成员关系、非内建 `tenant-admin` 角色与绑定。

- **验收**：删除账户后，运营者在该租户的成员列表与权限绑定列表中不再看到该账户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-046

已删除租户的名称不得被重新使用；以曾被使用的名称登记新租户必须被拒绝。

- **验收**：删除一个租户后，以同一名称登记新租户被拒绝，名册不新增租户；已删除的租户仍以已删除的状态存在，不被新租户取代。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-047

个人租户的创建必须留下审计记录，记明账户、租户与创建动作。

- **验收**：为账户建立个人租户后，运营者在审计检索中以该账户为操作者查到这次创建，记录的对象或明细点明该租户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-048

个人租户的删除必须留下审计记录，记明账户、租户、实际操作者与删除动作。

- **验收**：删除账户及其个人租户后，运营者在审计检索中以执行删除的运营者为操作者查到这次删除，记录点明被删除的账户与该租户；删除后该租户的创建记录仍可查到。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-049

账户删除时，其个人租户必须被标记为已删除；点名该租户的请求必须得到与租户不存在相同的结果。

- **验收**：删除账户后，名册中该个人租户显示为已删除；按该租户名称发起的请求得到与租户不存在相同的结果。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-050

系统必须在账户菜单的组织行与可切换项中把个人租户显示为登录账户的显示名，账户没有显示名时显示「组织」文案，不得显示该租户的名称。

- **验收**：个人租户的那一项显示登录账户的显示名；账户没有显示名时显示「组织」文案；两种情况都不显示该租户的名称。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-031

租户名册的写入必须要求平台域的租户管理权限。

- **验收**：持有读取权限而不持管理权限的调用方写被拒绝（403）。持有管理权限的调用方能登记、转移所有者、删除组织与改状态。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-032

发起者是否为未证明邮箱的账户，不影响租户名册的读取。

- **验收**：邮箱未证明的账户与已证明的账户读取名册都成功，返回结果相同。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-033

租户的邀请列表必须把待接受的邀请排在已结束的邀请之前。

- **验收**：同一租户下同时存在待接受与已结束的邀请时，待接受的邀请出现在已结束的邀请之前。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-034

已过有效期的待接受邀请仍必须出现在邀请列表中，并带着它的有效期。

- **验收**：一条已过有效期的待接受邀请仍出现在列表里，且读出它的有效期。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-035

账户菜单的组织行展开后必须列出该账户持有有效成员关系（成员关系存在且租户处于启用状态）的其他全部租户，当前租户不在展开项中重复。

- **验收**：打开组织行的展开项，能看到该账户持有有效成员关系的全部租户中除当前租户以外的每一项；当前租户只出现在组织行上，不出现在展开项里。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-036

账户菜单的组织行展开后不得列入当前租户，使当前租户无法被选作切换目标。

- **验收**：展开组织行，当前租户不出现在可选项里，因此不能对当前租户发起切换。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-member-organization.spec.ts` `member-organization: a member receives, reads and configures notifications`

### REQ-tenancy-037

没有当前租户时，账户菜单的组织行必须显示占位文案，展开后仍列出账户可进入的每个租户。

- **验收**：账户没有当前租户时，组织行显示占位文案而不是租户名；展开后列出该账户可进入的全部租户。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-038

冒充期间账户菜单不得出现组织行，冒充结束后恢复。

- **验收**：冒充开始后租户区的账户菜单不出现组织行；冒充结束后组织行恢复出现。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-impersonation-for-support.spec.ts` `impersonation-for-support: the operator acts as a member and the record and audit trail survive the end`

### REQ-tenancy-039

冒充中的身份在冒充授权点名的租户以外一律被拒绝。

- **验收**：冒充身份向另一个租户发出请求得到与非成员相同的不可访问结果。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-040

非成员访问租户资源时，界面必须呈现无法访问当前租户的说明并指引联系管理员，而不是呈现为服务没有应答。

- **验收**：页面显示无法访问当前租户的说明与联系管理员的指引，不呈现为服务没有应答。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-041

非成员访问租户资源时的不可访问界面不得提供重试。

- **验收**：页面不显示重试控件。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-042

带具体资源不存在含义的 404 仍必须按资源不存在呈现，不与租户不可访问混同。

- **验收**：对确实不存在的具体资源的请求，页面按资源不存在呈现，不显示无法访问当前租户的说明。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-043

接受成功后必须给出已成为该租户成员的确认。

- **验收**：接受成功后界面显示已成为该租户成员的确认。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-051

系统必须允许成员管理员按文本查询筛选成员列表，按显示名或登录标识的前缀匹配；查询为空时不筛选，清空查询恢复全部成员。

- **验收**：以某成员显示名的前缀查询，列表只含显示名以该前缀开头的成员；以用户名或邮箱的前缀查询，列表只含登录标识以该前缀开头的成员；不匹配的成员不出现；查询为空或清空后恢复未筛选的列表；筛选与分页同时使用时，每页只含匹配成员。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the administrator invites, governs and switches the organization`

### REQ-tenancy-052

系统必须允许成员管理员把成员列表收窄为在当前租户持有指定角色名的成员，角色名精确匹配；同时给出文本查询时，结果同时满足文本查询与角色筛选；指定角色名没有任何成员持有时返回空列表而不是错误。

- **验收**：选择一个有成员持有的角色后，列表只含在该租户持有该角色名的成员，其他成员不出现；选择一个没有成员持有的角色后，列表为空且读取成功；同时给出文本查询时，结果只含同时满足文本查询与角色筛选的成员；清空角色筛选后恢复按文本查询筛选的列表。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the administrator invites, governs and switches the organization` 的 step 5 覆盖按角色筛选收窄列表与清除筛选后恢复。没有成员持有该角色时的空列表、角色筛选与文本查询同时给出时的组合结果，未由 e2e 覆盖，分别由 `pkg/authn/admin/members_test.go` 的 `TestTheMemberListFiltersByRoleExactly` 与 `TestTheMemberListCombinesTheRoleFilterWithTheQuery` 覆盖。

### REQ-tenancy-053

系统必须在成员列表的每一页给出下一页的游标，最后一页给出空游标；按游标遍历不重复、不跳过成员；改变文本查询或角色筛选后从第一页重新开始。

- **验收**：从第一页按游标连续读取，取得的成员集合与全部匹配成员一致，没有重复或遗漏；最后一页给出空游标；改变文本查询或角色筛选后，下一读取从第一页开始，不携带旧查询或旧筛选下的游标。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-054

系统必须允许成员管理员停用与恢复某个租户的成员关系。停用后该账户按非成员处理，成员关系与角色绑定保留；恢复后成员关系与角色绑定带来的权限恢复。

- **验收**：停用后，该账户带着该租户的业务请求得到与非成员相同的不可访问结果，成员列表仍包含它且状态为停用，它的角色绑定仍在；恢复后同一请求被准入，角色带来的权限恢复；停用与恢复其他成员不改变该账户的状态。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the administrator invites, governs and switches the organization`

### REQ-tenancy-055

系统必须拒绝一名成员管理员停用自己的成员关系；另一位管理员可以停用该管理员。拒绝必须以可与最后一名管理员冲突相区分的专用结果报告，且不产生该动作的成功审计记录。

- **验收**：调用方停用自己得到专用拒绝，成员关系保持启用，角色绑定不变；另一位管理员停用同一账户成功，该账户按非成员处理；拒绝不产生停用动作的成功审计记录；该拒绝之外的情形按 REQ-tenancy-054 生效。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the administrator invites, governs and switches the organization`

### REQ-tenancy-056

系统必须允许成员管理员把邀请列表按状态收窄为未过期的待接受、已过期、已接受与已撤销之一；缺省列出全部状态。已过期是待接受邀请按有效期派生的视图，不改变其存储状态，也不从待接受邀请中删除它。

- **验收**：分别按每个取值读取，只得到对应状态的邀请；已过期的待接受邀请出现在已过期视图，不出现在未过期待接受视图；缺省读取包含全部状态；已过期的待接受邀请仍以待接受存储并可被撤销；取值集合之外的筛选被拒绝为请求错误，而不是当作全部。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the administrator invites, governs and switches the organization`

### REQ-tenancy-057

系统必须在签发邀请后向被邀请地址发送一封含接受链接的通知邮件；签发结果报告投递结果，取值为已发送、失败或本部署未配置邮件。投递失败不得撤销邀请、不得使签发失败，接受链接仍在签发结果中一次性给出；通知邮件使用发起签发请求的语言。

- **验收**：配置了邮件投递的部署签发邀请后，被邀请地址收到含接受链接的通知邮件，邮件中的链接与签发结果中的接受链接一致；投递失败时签发仍成功，邀请存在，链接仍可接受，签发结果报告失败并留下投递失败的审计记录；未配置邮件的部署签发成功并报告未配置；以另一种请求语言签发时，邮件按该语言呈现。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-058

系统必须允许成员管理员对邀请列表中的一条邀请重发：以同一地址重新签发一条待接受邀请，先前的待接受邀请随之失效，重发结果给出新的接受链接，并按 REQ-tenancy-057 投递通知。已接受与已撤销的邀请也可重发，重发只新增一条待接受邀请，不改变原邀请的终态。

- **验收**：对一条待接受邀请重发后，列表出现一条新的待接受邀请，原邀请不再能被接受，新链接可接受；对一条已过期邀请重发后同样得到新链接；对已接受或已撤销的邀请重发不改变原邀请的状态，只新增一条待接受邀请；重发后的投递规则与首次签发一致。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-invitation-to-tenant.spec.ts` `invitation-to-tenant: an operator invites an address and the invitee joins the tenant`

### REQ-tenancy-059

个人租户的对外名称必须等于账户的用户名，用户不得单独修改；个人租户的内部标识不得出现在界面中，也不得作为用户可查询的标识。

- **验收**：个人租户在账户菜单、切换器与地址中使用由用户名派生的名称；界面没有查看或修改内部标识的入口；用户修改用户名时被拒绝，名称随之保持不变。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`（覆盖账户菜单以用户名指称个人组织；地址中的名称与用户名修改被拒绝由后端用例覆盖）

### REQ-tenancy-060

每个业务组织必须有一个 owner；owner 默认为该组织的创建者，必须持有 tenant-admin。owner 对普通成员不可见，tenant-admin 可见。

- **验收**：创建业务组织后，owner 为创建者且持有 tenant-admin；普通成员看不到 owner，tenant-admin 能看到 owner。owner 以显示名与用户名指认，显示名重名时用户名足以区分；界面不显示账户标识或租户内部标识。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the owner reads the profile and saves its four descriptive fields`；`tenant-admin: a non-owner administrator reads the profile but sees no ownership controls`

### REQ-tenancy-061

业务组织的 owner 必须可以转移给该组织的 active 成员；目标尚未持有 tenant-admin 时，转移自动授予；转移只改变所有权，原 owner 仍是该组织的 active 成员并保留其全部角色绑定（含 tenant-admin），不被移除，其绑定不被清除。平台员工可以发起转移。

- **验收**：owner 把所有权转给 active 成员成功，目标获得 tenant-admin；目标原本不是 tenant-admin 时自动获得；原 owner 仍是该组织的 active 成员，仍持有 tenant-admin 及其余全部角色绑定；转给非成员被拒绝；平台员工可以发起同一转移。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the owner hands the organization to an active member and stays an administrator`；`tenant-admin: a transfer whose target stopped being an active member is refused with the conflict copy`；`starters/demo/web/e2e/specs/journey-operator-onboards-tenant.spec.ts` `operator-onboards-tenant: the operator transfers a tenant owner through the member selector`（平台员工发起转移）

### REQ-tenancy-062

只有业务组织的 owner 或平台员工可以删除该组织；删除必须经过明确的二次确认。

- **验收**：owner 可以删除自己的组织；平台员工可以删除任意业务组织；非 owner 的 tenant-admin 不能删除；删除需要输入组织名称确认。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the owner types the name, deletes the organization, and the name stays taken`；`tenant-admin: a non-owner administrator reads the profile but sees no ownership controls`（非 owner 的 tenant-admin 看不到删除入口）；`starters/demo/web/e2e/specs/journey-operator-onboards-tenant.spec.ts` `operator-onboards-tenant: the operator deletes a tenant and its name cannot be reused`

### REQ-tenancy-063

账户是任何活跃业务组织的 owner 时，系统必须拒绝删除与停用该账户，直到 owner 被转移或组织被删除。

- **验收**：owner 账户删除被拒绝，提示先转移所有权或删除组织；owner 账户停用同样被拒绝；转移 owner 后同一账户可以删除或停用；不属于任何业务组织 owner 的账户不受影响。
- **状态**：已实现
- **端到端测试**：未实现

### REQ-tenancy-064

业务组织删除必须采用标记删除；删除后组织不再提供服务，名称不得复用。业务不提供恢复入口，物理删除由后续合规处理。

- **验收**：删除后按该组织的名称发起请求得到与不存在相同的结果；重新创建相同名称被拒绝；界面没有恢复该组织的入口；数据在物理删除前保留。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-tenant-admin.spec.ts` `tenant-admin: the owner types the name, deletes the organization, and the name stays taken`；`starters/demo/web/e2e/specs/journey-operator-onboards-tenant.spec.ts` `operator-onboards-tenant: the operator deletes a tenant and its name cannot be reused`（覆盖删除与名称不可复用；删除后按名称的请求与无恢复入口不在用例内驱动）

### REQ-tenancy-065

已验证邮箱的已登录用户必须可以自助创建业务组织，只需提供对外名称、显示名与可选描述；创建者成为该组织的 owner 与首任 tenant-admin；创建成功后当前组织切换到新组织。内部标识由系统生成，用户不填写也看不到。

- **验收**：邮箱已证明的账户在创建页提交未占用的名称、显示名与描述后，组织建立，创建者是 owner 且持有 tenant-admin，界面切到新组织；名称不合规、保留或已被占用时拒绝且不建组织；未证明邮箱的账户被拒绝；服务账户与冒充会话被拒绝；同一账户超出活跃组织上限或创建频率上限时被拒绝。
- **状态**：已实现
- **端到端测试**：`starters/demo/web/e2e/specs/journey-first-registration-to-personal-organization.spec.ts` `first-registration: a new visitor reaches a personal organization, and occupancy is refused`（覆盖创建、切换与落在资料页；拒绝分支由后端用例覆盖）
