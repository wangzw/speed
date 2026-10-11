# authn 威胁模型

## 职责

本文承载 `pkg/authn` 功能目录的威胁模型，范围含租户准入层 `tenant.admission` 对跨租户访问的拦截：按类别列出威胁、对策与残余风险，写出对策成立所依赖的部署层前提，并列出没有覆盖的威胁。功能目录整体的职责、模块清单、身份与判决的形状见 [design-authn](design-authn.md)。

- **按类别的威胁、对策与残余风险。** 每条威胁对应一组对策与一栏残余风险；对策指向承载它的设计文档，本文不复述被指向文档的表、判定顺序与配置键。
- **对策的分层。** 协议层是库做的，装配层是 authn 自己的代码与装配补的，部署层只能写成对宿主的前提（见[术语表](#术语表)）。
- **部署层前提。** 对策依赖而 authn 无法自行保证的条件，是对宿主的要求。
- **没有覆盖的威胁。** 明确不在本功能目录防护范围内的威胁与它们的归属。
- **未核实的假设。** 依赖未核实行为的断言与守住它们的测试。

威胁类别按攻击面划分。下文的类别是例举，不是封闭集：新威胁归入最近的一类，没有相近的类别时增设一类。

各项对策背后的取舍与被否决的方案，记在各方面对应的决策记录里，索引见[依赖](#依赖)。

### 非职责

- **不承载对策本身的形状。** 退避、会话、令牌、因素、SSO、冒充、管理面、配置、事件的表、判定顺序与配置键由各自的设计文档承载；配置键只在 [design-authn-config](design-authn-config.md) 给全。本文的每个对策单元格只写机制的名字与它防的事。
- **不承载哨兵表与依赖不可用时每条路径的方向。** 它们由 [design-authn-failure](design-authn-failure.md) 承载；本文只写这些方向在威胁上的含义。
- **不承载设计取舍。** 为什么选这个对策、否决了哪些替代、接受了什么代价的论证，在决策记录里。
- **不防护[未覆盖的威胁](#未覆盖的威胁)里列出的威胁。**
- **不做以下防护措施。** 下列各项是例举，凡不在模块清单里的防护同样不提供：人机验证与行为风控；会话绑定来源地址或 User-Agent；定期强制改口令；以「检测到可疑行为」为条件的封号；全局统一的威胁等级开关；泄露口令库比对；持有人证明（DPoP、mTLS）；自适应风险评分。
- **不判定一条对策是否已经实现。** 实现与否以[实现状态](#实现状态)和各模块文档的实现状态为准。

## 术语表

以下术语的含义限于本文档。

| 术语 | 范围 | 定义 |
|---|---|---|
| 协议层对策 | 本文 | 协议或库本身提供的防护，例如 WebAuthn 把凭据绑定到 RP ID 与来源。库没做的校验不属于这一层。 |
| 装配层对策 | 本文 | authn 自己的代码与装配补上的防护：对库的补充校验、状态放置、统一应答、默认拒绝。表中没有标注层次的对策属于这一层。 |
| 部署层前提 | 本文 | 对策成立所依赖、只有宿主能提供的条件，例如时钟同步、秘密的保管、前端的安全响应头。前提不成立时，依赖它的对策失效，失效的范围写在[部署层前提](#部署层前提)里。 |
| 残余风险 | 本文 | 全部对策生效之后仍然存在的风险；不是对策自身的缺陷清单。 |
| 接受 | 本文 | 残余风险栏里的标记，表示这一项风险是明说接受的代价；取舍与理由由[依赖](#依赖)一节「决策记录」栏列出的决策记录承载，本文不汇总。 |
| 无已知剩余项 | 本文 | 残余风险栏的取值，表示没有已知的剩余风险；它不声称风险已经消除，库的新缺陷与未核实的假设仍记在[依赖未核实行为的假设](#依赖未核实行为的假设)与依赖里。 |

## 外部接口契约

本节是威胁模型对读者的承诺面：每类威胁的对策与残余风险、宿主必须提供的前提、没有覆盖的威胁，以及这些承诺的稳定性。

### 威胁、对策与残余风险

表的读法：「对策」栏是已设计的机制，「落点」栏是承载它的设计文档，「残余风险」栏是对策之后仍存在的风险。对策栏里写出的配置键与设置键只作机制的名字，取值与缺省值由 [design-authn-config](design-authn-config.md) 与落点文档承载。

#### 账户接管

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| 口令猜测、撞库、喷洒 | 按提交标识的失败退避：自由尝试用尽后指数退避并设上限，退避期内不派生、不比对；账户级静默退避；口令表的进程内退避在检查时预占一次尝试、成功不清零，并发的猜测互相计数，穿插一次合法请求也清不掉失败计数；口令用 Argon2id；口令派生有并发上限；`Passwords.Verify` 统一应答并对未知标识做诱饵派生 | [design-authn-account](design-authn-account.md)、[design-authn-login](design-authn-login.md) | 用户的口令在别处泄露且攻击者对每个账户只试一两次时，退避挡不住，靠第二因素；不做泄露口令库比对；没有人机验证。**接受** |
| 暴力破解一次性码（TOTP、短信） | 以因素与手机号为键的退避；每个短信码尝试次数用尽即作废；TOTP 计数器只进不退；重发不清零失败计数 | [design-authn-mfa](design-authn-mfa.md)、[design-authn-sms](design-authn-sms.md) | 对单个因素的在线猜测被限到每天百次量级；每天 100 次猜测对 6 位码（10^6 个取值）的命中概率上界是 10^-4，同时有效的码有 k 个时按 k 倍计 |
| 退避被用来锁住受害者 | 退避只针对口令方式，其他方式照常；上限与窗口是设置；标识级与账户级退避并存；运维主体（口令表）另有进程内退避 | [design-authn-login](design-authn-login.md)、[design-authn-account](design-authn-account.md) | 匿名攻击者对任一已知标识连续失败，可使其口令登录在 `throttle-max-delay` 之内不可用；换标识拼写不是缓解，账户级静默计数按账户计；受害者可改用短信、社交、SSO 与通行密钥登录。**接受** |
| SIM 劫持与号码携转 | 短信是受限认证器：不计入管理面动作的强认证；冒充发起与重置他人因素、口令不接受仅短信 | [design-authn-mfa](design-authn-mfa.md)、[design-authn-impersonation](design-authn-impersonation.md) | 以短信作登录或第二因素的普通账户仍受影响，短信登录与短信因素是本功能目录提供的方式；发送前的风险信号检查不做；号码被回收后，新持有者可用短信登录旧账户。**接受** |
| 预占抢注：先用受害者的邮箱建号，等受害者经 SSO 或社交登录并入 | 库里没有未验证的标识；`admin` 与 `import-unverified` 来源的邮箱不作自动关联与 JIT 并入的依据；并入成功即撤销既有会话并删除口令凭据；JIT 缺省只新建 | [design-authn-account](design-authn-account.md)、[design-authn-login](design-authn-login.md) | `preprovisioned` 匹配时采信管理员断言的邮箱，管理员写错邮箱即错配；迁移导入的邮箱不经验证，由导入方负责标成 `import-unverified`。**接受** |
| 社交与 SSO 的账户关联接管 | 键永远是（提供方，主体）；`auto-link` 缺省 `never`，是只取主配置源的放宽开关；邮箱变化不更新账户；解绑与关联要求新鲜的本地多因素；`link-required` 只对提供方已证明邮箱的调用者给出 | [design-authn-account](design-authn-account.md)、[design-authn-login](design-authn-login.md) | `auto-link` 开启时信任提供方的邮箱验证：Google Workspace 管理员可改邮箱，邮箱地址可被回收。**接受** |
| 租户 IdP 对跨租户的全局账户的权限边界 | 企业 SSO 的 JIT 缺省只新建、不并入（`merge_existing` 缺省为假）；并入只限「全部成员关系都在该租户内、没有别的登录方式」的账户；成员关系只在建绑定的同一事务里新建，已有绑定的登录不读不写成员关系，被暂停或移除的成员不会被复活；IdP 声明的多因素只在该提供方所属租户内算数，账户安全类操作不采信；IdP 行的变更是强制审计的管理动作 | [design-authn-sso](design-authn-sso.md)、[design-authn-account](design-authn-account.md) | 租户的 IdP 管理员对自己租户新建的账户有登录控制权，这是企业 SSO 的本质；`merge_existing` 开启时，被并入账户的既有会话与口令被清除，但并入后该 IdP 对它有永久的登录能力，直到绑定被解除。**接受** |
| 租户 A 的 IdP 声明的多因素被租户 B 的 `require-mfa` 采信，或覆盖账户自己的因素 | `idp_assured` 记在会话上，只在该提供方所属的租户内算数；账户有因素时 IdP 的声明盖不过；账户安全类操作与冒充发起不采信 | [design-authn-mfa](design-authn-mfa.md)、[design-authn-sso](design-authn-sso.md) | 租户 A 的 IdP 管理员对自己租户内请求的 MFA 判定有话语权，这是企业 SSO 的信任边界。**接受** |
| 首次登记的因素被抢先：持口令者先于真用户登记自己的因素 | 登录期间登记的因素不计入本次保障等级；账户安全类操作要求本地验证过的因素；冒充发起与管理员重置要求启用已满规定时长的因素（`MFA.Seasoned`） | [design-authn-mfa](design-authn-mfa.md) | 账户尚无因素时，持口令者与真用户不可区分：抢先登记者得到一个普通的多因素会话，真用户被锁在外，要靠管理员重置；首次登记没有带外证明（没有邮件通道）。**接受** |
| 恶意租户管理员用自己的 IdP 断言别家的邮箱 | 企业 SSO 的邮箱域必须属于该租户已验证的域；已验证的域全局唯一 | [design-authn-sso](design-authn-sso.md)、[design-authn-account](design-authn-account.md) | 域名所有权后来转手、旧租户的 IdP 仍可断言，直到管理者移除 |
| 钓鱼与实时代理 | 协议层：WebAuthn 与通行密钥绑定 RP ID 与来源，对钓鱼免疫；装配层：`__Host-` cookie；`return_to` 校验 | [design-authn-mfa](design-authn-mfa.md)、[design-authn-session](design-authn-session.md) | TOTP、短信、口令对实时代理不免疫；启动校验只要求 `rp-id` 等于基址主机或其父域，不核对父域是否为公共后缀，取公共后缀时凭据的作用域覆盖该后缀下的全部站点，见 [design-authn-mfa](design-authn-mfa.md)。**接受** |
| 找回路径绕过 MFA | 没有「忘记口令」流程；经短信、社交、SSO 登录后同样过第二因素；管理员重置不绕过第一因素 | [design-authn-mfa](design-authn-mfa.md)、[design-authn-account](design-authn-account.md) | 只有一个因素又无管理员的账户没有自助找回；管理员重置依赖宿主的核验流程，社会工程是风险。**接受** |
| 账户停用后仍可用 | 状态与 `valid_after` 在 `authn.account` 的事务里写；`Sessions.Status` 把账户启用与 `CreatedAt` 不早于 `valid_after` 写进有效性，所有调用 `Sessions.Status` 的校验路径共用；校验后端再查账户快照；清理由钩子同步调用、事件补偿 | [design-authn-account](design-authn-account.md)、[design-authn-session](design-authn-session.md) | 缓存内的校验至 `check-ttl`；`check-session=false` 下至令牌到期；钩子失败时残留行不能用于认证，但抹除不保证 |
| 账户枚举 | 统一应答与等时；失败退避按提交内容计；账户级退避静默；短信对已登记与未登记的号码走同一条计数路径与同形应答（未登记且注册关闭时写抑制行）；发现只看域不看本地部分；因素清单只在首因素通过后给；`link-required` 只在 `EmailVerified` 为真时给 | [design-authn-login](design-authn-login.md)、[design-authn-sms](design-authn-sms.md)、[design-authn-sso](design-authn-sso.md) | `link-required` 对已被提供方证明拥有该邮箱的调用者暴露其账户存在，接受；口令表的用户名因快速拒绝而可被枚举，接受；开户开放时，未知号码也会真的发送；设置变更之后到账户升级哈希之前，已知与未知标识的口令派生耗时有差别。**接受** |

#### 会话与 CSRF

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| 会话固定 | 令牌只由服务端生成（scs 对未知令牌新建会话，不沿用客户端给的令牌）；没有登录前的匿名会话；登录、提权、冒充开始与结束时换令牌；登录时撤销请求带来的旧会话；`__Host-` cookie 阻止子域预置 | [design-authn-session](design-authn-session.md) | 无已知剩余项 |
| cookie 被盗 | `HttpOnly`、`Secure`（不可关）、`__Host-`；库里只存哈希；空闲与绝对超时；可撤销、可列出；会话期 cookie | [design-authn-session](design-authn-session.md) | 窃取者在会话有效期内可用，没有设备绑定（NAT 与移动网络下绑定会误杀）；缓解是缩短期限与 `strict`。**接受** |
| cookie 投放与子域接管 | `__Host-` 前缀；同名 cookie 出现多次即 401 | [design-authn-session](design-authn-session.md)、[design-authn-gate](design-authn-gate.md) | 设了共享域（`__Secure-`）或开发时关闭 `__Host-`，即失去这一层；开启时写 Warn |
| CSRF（浏览器可能自己附带的凭据） | `SameSite=Lax`；网关对这些载体来源的非安全方法做 `CrossOriginProtection`，失败答 401、不清 cookie、凭据不交给后端；哪些载体算在内的判据只在 [design-authn-gate](design-authn-gate.md) 的请求期判定 | [design-authn-session](design-authn-session.md)、[design-authn-gate](design-authn-gate.md) | 既无 `Sec-Fetch-Site` 又无 `Origin` 的请求被放行（极旧的浏览器与非浏览器客户端）；`same-site` 的兄弟子域按跨源处理，除非列入 `trusted-origins`；同源 XSS 能借浏览器发请求，XSS 是宿主前端的责任 |
| 登录 CSRF：把受害者登入攻击者的账户 | 流程绑定 cookie 加 `seq`；回跳必须与发起流程的浏览器绑定；登录与流程路由的非安全方法过跨源检查 | [design-authn-login](design-authn-login.md) | IdP 发起的 SAML 登录无从绑定：缺省关闭，开启时写警告；绑定 cookie 是 `SameSite=None`，会随任何跨站请求发往该主机，只是不透明值，对其他端点没有权限 |
| 撤销不及时 | 单个与全部撤销、`valid_after`、停用时同步清理；缓存滞后至多 `check-ttl` | [design-authn-session](design-authn-session.md) | 滞后窗口内的请求仍被放行；`check-ttl` 设 0 即严格 |
| 点击劫持、响应被中间缓存 | authn 的端点只返回 JSON、没有界面，界面的 `frame-ancestors` 由宿主加；authn 的全部响应带 `Cache-Control: no-store` | [design-authn-session](design-authn-session.md)、[design-authn-gate](design-authn-gate.md) | 仓库没有安全响应头策略；宿主界面的响应头是宿主的责任 |

#### 令牌的窃取与重放

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| 访问令牌被盗与重放 | 访问令牌寿命短；撤销经会话；`sid` 核对；`valid_after`；令牌不进 URL 与日志 | [design-authn-token](design-authn-token.md) | 寿命内可重放；没有持有人证明；服务账户的令牌无法逐枚撤销。**接受** |
| 刷新令牌被盗 | 每次使用即轮换，重用即撤销整族；只存哈希；空闲与绝对期限；缺省不设宽限；出示任意不存在的秘密不触发撤销，只有出示真实存在的已用旧令牌才判重用 | [design-authn-token](design-authn-token.md) | 窃取者先于合法客户端使用时，合法客户端被踢下线，窃取者持有新令牌直到下次重用被发现；开宽限则多一个窗口 |
| 算法混淆（`none`、RS256 当作 HS256） | 不支持对称算法；白名单等于已配置密钥里的算法；每把密钥一个算法并核对 | [design-authn-token](design-authn-token.md) | 无已知剩余项 |
| `kid`、`jku`、`x5u`、`jwk`、`x5c` 注入 | `kid` 只在本地密钥集里比对；不识别也不抓取其余几个头；不对 `kid` 拼任何查询 | [design-authn-token](design-authn-token.md) | 无已知剩余项 |
| 令牌类型混淆、库默认放行（缺 `exp`） | `typ` 必须是 `at+jwt`；`iss`、`aud` 必校；显式要求 `exp`、`iat`、`jti`、`sub`、`sid`；`exp - iat` 有上界 | [design-authn-token](design-authn-token.md) | 无已知剩余项 |
| 畸形令牌的拒绝服务（go-jose 的 panic 类缺陷） | 先限长；`pkg/go.mod` 对 go-jose 设版本下限（数值见 [design-authn-token](design-authn-token.md) 的依赖）；校验在 `recover` 之内 | [design-authn-token](design-authn-token.md) | 库将来的新缺陷：go-jose 已有高危公告 |
| 签名密钥泄露 | 私钥在文件里，不在库的转储里；双钥轮换；应急撤销（从配置删去并滚动重启） | [design-authn-token](design-authn-token.md)、[design-authn-config](design-authn-config.md) | 文件与部署的秘密管理同时失守，即全部令牌可伪造；没有 KMS 接缝，列为扩展方向。**接受** |
| JWKS 缓存陈旧，致撤销的密钥仍被外部校验方接受 | JWKS 的缓存期有界；认证核心不依赖外部校验方的缓存 | [design-authn-token](design-authn-token.md) | 外部校验方的缓存时间之内 |
| 访问令牌带角色而无法撤销 | 默认不带角色；`ClaimsContributor` 由宿主显式选择 | [design-authn-token](design-authn-token.md) | 宿主选了就承担 |

#### 冒充滥用

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| 管理员账户被攻破后冒充他人；内部人滥用 | `Decider` 默认全拒；发起者要有新鲜的本地多因素、非短信，且名下有启用已满规定时长的非短信因素；限定一个租户，目标须是该租户的成员；有时限；理由必填；强制审计（事务之外先写）；不可链式；`protected` 账户不可冒充 | [design-authn-impersonation](design-authn-impersonation.md)、[design-authn-admin](design-authn-admin.md) | 有权限的发起者在授权范围内的滥用，靠审计事后发现；`Decider` 写得过宽时，管理员账户被攻破即等于可冒充；被冒充者不被通知。**接受** |
| 冒充身份越过租户范围：访问不带租户解析的端，或带着 cookie 与令牌去别处 | 网关对冒充身份只放进准入层所守的端与 `Impersonable` 路由，其余答 401；`authn.impersonation` 把 `ScopeGuard` 当必需依赖；准入层对无租户上下文与范围不符的冒充身份答 404；冒充令牌带 `imp_tenant`，使外部校验方读到范围 | [design-authn-gate](design-authn-gate.md)、[design-tenant-admission](design-tenant-admission.md)、[design-authn-impersonation](design-authn-impersonation.md) | 准入层所守的端上、租户解析豁免的业务路由没有租户上下文，非冒充请求照常放行；`ScopeGuard.Covers` 只说准入层守哪些端，不核对宿主有没有在那些端上开解析豁免；外部资源服务器若不读 `imp_tenant`，则不受范围约束。**接受** |
| 冒充期间改变目标的认证事实、借冒充提权 | authn 的敏感端点在冒充中一律拒绝；冒充身份的等级封顶为单因素，要求多因素的路由对它不满足 | [design-authn-impersonation](design-authn-impersonation.md)、[design-authn-mfa](design-authn-mfa.md) | 业务路由上的敏感操作靠授权层读 `Impersonating()` 拦 |
| 无记录的冒充、冒充令牌被续期 | 审计落点是必需依赖，开始前先写审计，写失败即不开始；不发刷新令牌；`exp` 不晚于记录到期；撤销级联 | [design-authn-impersonation](design-authn-impersonation.md)、[design-authn-events](design-authn-events.md) | 动作失败时，审计里是一条 `granted` 加一条补写的 `failed`（以 `grant_id` 关联）；令牌在有效期内可重放 |
| 冒充绕过租户的 MFA 与 SSO 要求 | 发起者的强认证在开始时核验；冒充限定租户 | [design-authn-impersonation](design-authn-impersonation.md) | 该租户的 SSO 与 MFA 要求，在冒充期间对被冒充者本人不起作用 |

#### SSO 断言攻击

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| XML 签名包装 | 单次解析，使用验过签后返回的元素；要求恰一条断言；核对 `SignatureValidated`；`pkg/go.mod` 对 goxmldsig 设版本下限（数值见 [design-authn-sso](design-authn-sso.md) 的依赖） | [design-authn-sso](design-authn-sso.md) | 库将来的新缺陷：gosaml2 与 goxmldsig 都有过高危公告，这一类缺陷在 SAML 库与 goxmldsig 的历史上反复出现；每个补充校验都要有负向测试 |
| 断言重放 | `authn_saml_assertions` 的唯一约束（db，不是 kv）；断言新鲜度有上界；一次性流程 | [design-authn-sso](design-authn-sso.md) | 无已知剩余项 |
| `InResponseTo` 缺失与跨会话投放；IdP 发起 | 响应与断言两级核对流程记录里的请求标识；IdP 发起缺省关闭 | [design-authn-sso](design-authn-sso.md)、[design-authn-login](design-authn-login.md) | IdP 发起开启时，登录 CSRF 无解 |
| `Destination`、`Audience`、`Recipient`、颁发者混淆 | 库对空值跳过的几处（`IdentityProviderIssuer`、`Destination`、无 `AudienceRestriction`），由 authn 要求非空并复核 | [design-authn-sso](design-authn-sso.md) | 无已知剩余项 |
| 弱算法（SHA-1） | 调用库之前，预扫描解码后的 XML 里的全部 `Algorithm` 属性，出现 SHA-1 即拒绝，除非该 IdP 设了 `allow_sha1` | [design-authn-sso](design-authn-sso.md) | 预扫描按 `Algorithm` 属性的取值判定，出现 SHA-1 取值即拒绝，不区分该属性是否属于已采信的签名，含 SHA-1 取值而未用于验签的文档同样被拒；个别遗留 IdP 要求 SHA-1，开启即接受降级；靠 SHA-1 样本的负向测试守住 |
| XML 实体、压缩炸弹、过大的文档 | 请求体有大小上限；`MaximumDecompressedBodySize` 与 `MaximumXMLTokens` 显式设值；Go 标准库的 `encoding/xml` 不展开外部实体 | [design-authn-sso](design-authn-sso.md) | `xml-roundtrip-validator` 已停止维护。**接受** |
| NameID 碰撞与注释截断 | `transient` 拒绝；邮箱格式的 NameID 须配专门属性；键含 IdP 命名空间；含注释的 NameID 有负向测试 | [design-authn-sso](design-authn-sso.md) | gosaml2 对注释截断的处理未核实（见[依赖未核实行为的假设](#依赖未核实行为的假设)）；IdP 自身重新分配 NameID |
| 属性注入 | `idp.` 前缀只由映射写入，`authn.` 前缀保留，账户属性不得使用 `idp.` 与 `authn.` 前缀，均靠写属性的一方保证，网关在盖章处不判；类型化事实不进属性集 | [design-authn](design-authn.md)、[design-authn-sso](design-authn-sso.md) | 授权若盲信 `idp.*`，即信任 IdP；宿主自写的后端能交出任意 `idp.*` 键，网关拦不住，信任这些键就是信任该后端 |
| 域名声明劫持、跨租户的 IdP 断言 | 域名所有权经 DNS TXT 证明，已验证的域全局唯一；提供方行属于一个租户，成员关系只在该租户内建立 | [design-authn-sso](design-authn-sso.md) | DNS 没有 DNSSEC 校验；域名转手 |
| IdP 元数据或证书被替换；服务端请求伪造 | 元数据 URL 走受限客户端并限大小；证书变更要经管理面（强制审计）；连接时检查实际对端地址（只放行全局单播（IPv6 须在 `2000::/3` 内）且不在特殊用途段的地址，NAT64 与 6to4 抽出内嵌的 IPv4 再判，Teredo 拒绝）；不跟随重定向；不用代理 | [design-authn-sso](design-authn-sso.md)、[design-authn-admin](design-authn-admin.md) | 需要经出站代理的网络里要放弃该检查；受限客户端是自写的安全部件，特殊用途段表与 IANA 登记表一致是假设，见[依赖未核实行为的假设](#依赖未核实行为的假设) |

#### 短信、枚举与时序

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| 短信轰炸与流量泵 | 缺省拒绝所有号码前缀（`allowed-prefixes` 为空）；号码未登记且注册关闭时写抑制行、不发送；重发间隔、每号码每小时与每日的上限由 `authn_challenges` 行数得出，已登记与未登记的号码走同一条计数路径；全局预算按用途分设，登录用途被耗尽不挤占第二因素与登记；每流程至多 `per-flow-sends` 条；每账户每小时的上限只对 `mfa` 与 `enroll` 计；运行期开关 `disabled-methods` 可立刻关掉 `sms`；`ErrSMSUncertain` 不盲目重发；指标 `authn.sms.sends` 的 `budget-exhausted` 取值供告警 | [design-authn-sms](design-authn-sms.md)、[design-authn-login](design-authn-login.md) | 注册开放且前缀放行时，攻击者可对任意允许前缀的号码触发发送，上界是各用途的全局预算；某用途的预算耗尽即拒绝该用途的合法用户，登录、第二因素、登记各有预算，互不挤占。**接受** |
| 验证码重用、重放与暴力 | 每码的尝试次数用尽即作废；单次消费（条件更新）；有效期有界；摘要带密钥 | [design-authn-sms](design-authn-sms.md) | 6 位数字的摘要抗不了离线穷举（库被读走时），只防短期暴露 |
| 计时侧信道 | 口令诱饵派生（按当前设置懒生成，设置变更时重建）；常量时间比较（口令哈希、验证码摘要、流程令牌、`state`） | [design-authn-account](design-authn-account.md)、[design-authn-login](design-authn-login.md) | 常量时间比较与诱饵派生只使服务端处理时间相等，网络与调度抖动造成的差别不在保证内；设置变更之后到账户下次登录升级哈希之前，已知与未知标识的派生耗时有差；口令表的未知用户名因快速拒绝而快（已接受） |
| 口令派生耗尽 CPU 与内存 | 并发信号量，满则 503；Argon2 的取值域与解析上限有界，另有按字节计的内存预算，每个口令后端各持一份；解析条目有界限；口令长度有上限 | [design-authn-account](design-authn-account.md)、[design-authn-gate](design-authn-gate.md) | 并发与内存预算以内的占用，各口令后端的预算互相独立，整个进程的上界是各份之和；已知有效用户名的口令表主体仍可被用来消耗派生，受退避约束；并发内存峰值在目标部署规格之内是假设，见[依赖未核实行为的假设](#依赖未核实行为的假设) |

#### OAuth 与 OIDC 回调

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| `state` 缺失、可预测、可由发起方指定（goth 的缺陷） | 32 字节随机数，存摘要，回跳时常量时间比较；自己生成，不接受外部指定；与流程绑定一同校验 | [design-authn-login](design-authn-login.md) | 无已知剩余项 |
| PKCE 缺失或降级、授权码截获 | Google、GitHub、企业 OIDC 一律 S256；校验串存流程记录 | [design-authn-login](design-authn-login.md) | 微信协议不支持 PKCE；Google 是否强制 PKCE 未核实 |
| 授权码重放与注入、登录 CSRF | 回跳时对流程记录比较并交换，置为「正在交换」，授权码至多换一次；流程绑定 | [design-authn-login](design-authn-login.md) | 无已知剩余项 |
| 开放重定向（`return_to`、`redirect_uri`） | `redirect_uri` 由配置的 `public-url` 拼出；`return_to` 只接受相对路径或可信来源，开始时校验并存服务端，回跳时只用记录里的值 | [design-authn-login](design-authn-login.md) | `trusted-origins` 配宽了即开放；宿主前端若把任意 `return_to` 里的路径当重定向目标，要自己校验 |
| 混淆攻击：把 A 提供方的回跳投给 B | 每个提供方的回跳路径不同；流程里记提供方；回跳带 `iss` 时仍比对 | [design-authn-login](design-authn-login.md) | 无已知剩余项 |
| ID 令牌重放、`nonce` 缺失、`sub` 不一致 | 自己发 `nonce`、自己比对；校验 `aud`、`azp`、`iss`；不调 userinfo | [design-authn-login](design-authn-login.md) | 无已知剩余项 |
| 微信无 PKCE、无 `nonce`、无 ID 令牌，`secret` 在查询串里 | `state`（64 个十六进制字符）加流程绑定；不信任昵称与头像；不合成邮箱；库错误剥去 URL，不记 URL，`app-secret` 登记字面量脱敏 | [design-authn-login](design-authn-login.md) | CSRF 防线比其他提供方少一层；漏净化一处即泄露。**接受** |

#### 基础设施与服务端

| 威胁 | 对策 | 落点 | 残余风险 |
|---|---|---|---|
| 秘密进入日志、事件、审计、指标 | 脱敏按键名、值形状与字面量登记；事件与审计明细只放标识；指标属性是闭集；永不记录请求体与表单 | [design-authn-gate](design-authn-gate.md)、[design-authn-events](design-authn-events.md)、[design-log](design-log.md) | 日志模块只管登记之后的记录；宿主自己的 handler 若记录请求体，authn 管不到；漏净化一处 |
| 数据库转储 | 口令用 Argon2id；令牌只存哈希；TOTP 种子与 IdP 密钥用加密列；短信码存带密钥的摘要；签名私钥不在库里 | [design-authn-account](design-authn-account.md)、[design-authn-config](design-authn-config.md)、[design-db](design-db.md) | 邮箱、手机、显示名是明文；加密列依赖 db 根密钥，根密钥与库一起失守则全失 |
| `Host` 头注入与对外基址 | 基址只来自配置；从不用 `Host` 或转发头拼回跳地址、`iss`、ACS | [design-authn-config](design-authn-config.md)、[design-authn-login](design-authn-login.md) | 无已知剩余项 |
| 跨租户访问：持合法凭据的调用方指名另一个租户 | `tenant.admission` 默认拒绝，非成员的应答与不存在的租户同形（404） | [design-tenant-admission](design-tenant-admission.md) | 准入层的拒绝落在审计与限流之内，靠 `After` 声明保证，装配后的真实次序未经运行核对（见[依赖未核实行为的假设](#依赖未核实行为的假设)） |
| 监听端漏守、配置错误 | 默认守全部端，漏写的端同样被守；启动诊断列出展开后的清单与被开出的口子；放宽项各写一行 Warn；缺省值取最严 | [design-authn-gate](design-authn-gate.md)、[design-authn-config](design-authn-config.md) | 链上仍可有层绕过认证（非保证）：`Order` 取更小值的层排在认证之外；宿主仍可显式放宽 |
| 管理面被远程滥用 | 默认不可用：`authn.admin` 要宿主纳入并交付 `Decider` 与审计；强制审计 | [design-authn-admin](design-authn-admin.md) | 宿主交付的 `Decider` 有缺陷，则管理面有缺陷 |
| 设置写权限被滥用来放宽安全项 | 放宽方向的开关（`signup`、`auto-link`、`check-session`）只取主配置源；其余可在运行期修改的安全项，由 authn 订阅 `settings.changed`，改到更宽松的取值时写 Warn 并记一条尽力档审计；`pkg/authn/AGENTS.md` 写明「`authn.*` 与 `tenant.admission.*` 设置不得对租户或普通管理员开放写权限」 | [design-authn-config](design-authn-config.md)、[design-settings](design-settings.md) | 事后可见、不能事前阻止；拿到设置写权限的主体仍能把 `throttle-*` 与短信预算调松。**接受** |
| 预认证请求使表无界增长 | 每张会增长的表有持有者与清扫协程；`authn_throttles` 的行数上界是失败请求速率乘 `throttle-decay` | [design-authn-account](design-authn-account.md) | 匿名洪泛期间表会变大；缓解是来源地址层的前置减负（缺省关）与入口层限流。**接受** |
| 供应链：依赖库的漏洞 | 固定版本；go-jose 与 goxmldsig 设版本下限；每个库隔在叶子包；升级走专门的变更 | [design-authn-token](design-authn-token.md)、[design-authn-sso](design-authn-sso.md) | 新漏洞出现到升级之间的窗口 |
| 时钟偏差 | 容差有界且可调；撤销不依赖时钟 | [design-authn-failure](design-authn-failure.md) | 偏差超过容差时零星拒绝 |

### 部署层前提

下表是对策依赖、而 authn 无法自行保证的条件。下表是例举，不是封闭集：凡对策成立所依赖、只有宿主才能提供的条件，同属部署层前提，并在引入它的对策旁写明。

| 前提 | 依赖它的对策 | 前提不成立时 |
|---|---|---|
| `public-url` 是宿主实际对外的 https 基址 | 回跳地址、`iss`、ACS 只来自配置；`Secure` 与 `__Host-` cookie | 非 https 基址在启动期以 `ErrInsecureConfig` 失败，除非显式开启 `allow-insecure`（仅限开发）；基址写错则回跳失败 |
| 命令树把 `authn.gate` 纳入每条提供服务的命令 | 网关默认守全部端 | 链上没有认证层，该端全部路由无认证放行；这是放行方向的失败，不产生本功能目录自己的信号（见 [design-authn](design-authn.md) 的非保证） |
| 宿主前端自行设置 `frame-ancestors`，防护 XSS，并校验自己用作重定向目标的 `return_to` | 点击劫持、CSRF 的同源残余、开放重定向 | 这些防护缺席；authn 的端点只返回 JSON，不提供界面 |
| 宿主自己的 handler 与中间件不记录请求体与表单 | 秘密不进日志 | authn 管不到宿主自己写出的记录 |
| 签名私钥文件的权限与部署的秘密管理由宿主负责；db 根密钥与数据库分开保管 | 令牌的签名私钥不在库里；加密列 | 文件与部署的秘密管理失守即全部令牌可伪造；根密钥与库一起失守则加密列全失 |
| 各副本使用 NTP 或等价的时间同步 | 期限类判断；撤销以账户状态与 `valid_after` 为准，不依赖时钟 | 期限类判断偏移，偏差超过容差时零星拒绝；撤销不受影响 |
| 宿主交付的 `Decider` 对授权范围的判断正确 | 管理面与冒充的判定接缝 | `Decider` 过宽则管理面与冒充过宽；缺席时他人账户与租户级资源的管理面动作、冒充一律拒绝 |
| `authn.*` 与 `tenant.admission.*` 设置不对租户或普通管理员开放写权限 | 放宽方向的开关只取主配置源；其余安全项的变更由订阅 `settings.changed` 写 Warn 与审计 | 拿到设置写权限的主体可把 `throttle-*` 与短信预算调松；变更事后可见、不能事前阻止 |
| 管理员重置因素与口令之前，宿主有自己的核验流程 | 找回路径不绕过 MFA | 重置依赖宿主流程的质量，社会工程是风险 |
| 迁移导入的邮箱由导入方标成 `import-unverified` | 预占抢注的对策 | 未标记的导入邮箱被当作已验证标识，可成为自动关联的依据 |
| 外部资源服务器读 `imp_tenant`，并按 JWKS 的缓存期刷新密钥 | 冒充范围对外部校验方成立；密钥撤销对外部校验方生效 | 外部资源服务器不受冒充范围约束；撤销的密钥在其缓存期内仍被接受 |
| 元数据抓取必须经出站代理的网络，由宿主以网络层隔离代替对端地址检查 | 对 IdP 元数据抓取的服务端请求伪造防护 | 对端地址检查被放弃，防护只剩网络层隔离 |
| 来源地址层的前置减负与入口层限流由宿主装配 | 预认证请求使表无界增长的缓解 | 匿名洪泛期间 `authn_throttles` 表变大，行数上界仍是失败请求速率乘 `throttle-decay` |

### 未覆盖的威胁

下列威胁不在本功能目录的防护范围内。列表是例举，不是封闭集：判据是防护需要的信息或能力不在本功能目录手里。

| 威胁 | 归属或处置 |
|---|---|
| 宿主前端的 XSS、点击劫持防护 | 宿主责任 |
| 依赖投毒 | 宿主的构建与供应链责任；authn 只固定已知受影响库的版本下限 |
| 针对 kv、db、事件总线本身的攻击 | 基础设施责任 |
| 机密计算与密钥托管（KMS、HSM） | 没有接缝，列为扩展方向 |
| 设备绑定与持有人证明（DPoP、mTLS） | 不做 |
| 人机验证与行为风控 | 不做 |
| 口令泄露库比对 | 不做：需要出站调用，并有隐私上的权衡 |
| 自适应风险评分 | 不做：没有可用的信号来源，代理之后的对端地址不可靠 |
| 公共后缀表对 WebAuthn `rp-id` 的核对 | 不做；残余风险写在[账户接管](#账户接管)的「钓鱼与实时代理」一行 |

### 稳定性承诺

- **已写入的对策不得削弱而不同时更新本文与对应的决策记录。** 对策是一条威胁所依赖的性质，削弱它等于改变残余风险。
- **残余风险栏的「接受」项增减是对接受的代价的变更**，同时更新对应的决策记录。
- **部署层前提的增减是对宿主可见面的变更**，同时更新[部署层前提](#部署层前提)。
- 类别与行的措辞不是稳定面：威胁的归类可以调整，对策与落点的指向不可以与被指向文档不一致。

## 依赖

本文是叶子文档：被 [design-authn](design-authn.md) 与设计文档索引引用，没有任何代码与文档依赖本文，因此不引入依赖方向问题。本文对其余文档的依赖是引用，不是构建依赖。

对策的落点与取舍记录按类别列出，类别与[威胁、对策与残余风险](#威胁对策与残余风险)的小节一致；「落点」栏列出该类别的对策所在的设计文档，「决策记录」栏列出对应的取舍。

| 类别 | 落点 | 决策记录 |
|---|---|---|
| 账户接管 | [design-authn-account](design-authn-account.md)、[design-authn-login](design-authn-login.md)、[design-authn-mfa](design-authn-mfa.md)、[design-authn-sso](design-authn-sso.md) | [ADR-authn-throttling-2026-10-10](../../adr/adr-authn-throttling-2026-10-10.md)、[ADR-authn-external-binding-2026-10-10](../../adr/adr-authn-external-binding-2026-10-10.md)、[ADR-authn-accounts-2026-10-10](../../adr/adr-authn-accounts-2026-10-10.md)、[ADR-authn-account-lifecycle-2026-10-10](../../adr/adr-authn-account-lifecycle-2026-10-10.md)、[ADR-authn-mfa-2026-10-10](../../adr/adr-authn-mfa-2026-10-10.md)、[ADR-authn-mfa-recovery-2026-10-10](../../adr/adr-authn-mfa-recovery-2026-10-10.md)、[ADR-authn-password-hash-2026-10-10](../../adr/adr-authn-password-hash-2026-10-10.md) |
| 会话与 CSRF | [design-authn-session](design-authn-session.md)、[design-authn-gate](design-authn-gate.md) | [ADR-authn-session-2026-10-10](../../adr/adr-authn-session-2026-10-10.md)、[ADR-authn-credential-dispatch-2026-10-10](../../adr/adr-authn-credential-dispatch-2026-10-10.md) |
| 令牌的窃取与重放 | [design-authn-token](design-authn-token.md) | [ADR-authn-token-2026-10-10](../../adr/adr-authn-token-2026-10-10.md) |
| 冒充滥用 | [design-authn-impersonation](design-authn-impersonation.md)、[design-authn-admin](design-authn-admin.md)、[design-tenant-admission](design-tenant-admission.md) | [ADR-authn-impersonation-2026-10-10](../../adr/adr-authn-impersonation-2026-10-10.md)、[ADR-authn-decision-seam-2026-10-10](../../adr/adr-authn-decision-seam-2026-10-10.md)、[ADR-tenant-admission-2026-10-10](../../adr/adr-tenant-admission-2026-10-10.md) |
| SSO 断言攻击 | [design-authn-sso](design-authn-sso.md) | [ADR-authn-sso-2026-10-10](../../adr/adr-authn-sso-2026-10-10.md) |
| 短信、枚举与时序 | [design-authn-sms](design-authn-sms.md)、[design-authn-login](design-authn-login.md) | [ADR-authn-sms-2026-10-10](../../adr/adr-authn-sms-2026-10-10.md)、[ADR-authn-throttling-2026-10-10](../../adr/adr-authn-throttling-2026-10-10.md) |
| OAuth 与 OIDC 回调 | [design-authn-login](design-authn-login.md) | [ADR-authn-oauth-flow-2026-10-10](../../adr/adr-authn-oauth-flow-2026-10-10.md)、[ADR-authn-wechat-2026-10-10](../../adr/adr-authn-wechat-2026-10-10.md) |
| 基础设施与服务端 | [design-authn-config](design-authn-config.md)、[design-authn-events](design-authn-events.md)、[design-authn-failure](design-authn-failure.md)、[design-authn-gate](design-authn-gate.md) | [ADR-authn-settings-and-secrets-2026-10-10](../../adr/adr-authn-settings-and-secrets-2026-10-10.md)、[ADR-authn-events-and-audit-2026-10-10](../../adr/adr-authn-events-and-audit-2026-10-10.md)、[ADR-authn-failure-2026-10-10](../../adr/adr-authn-failure-2026-10-10.md)、[ADR-authn-guarantees-2026-10-10](../../adr/adr-authn-guarantees-2026-10-10.md)、[ADR-authn-state-placement-2026-10-10](../../adr/adr-authn-state-placement-2026-10-10.md) |

对策依赖的其他模块与库的性质如下，列表是例举，不是封闭集。「失守时」栏写依赖的性质不成立时受影响的对策。

| 依赖 | 依赖的性质 | 失守时 |
|---|---|---|
| `pkg/log`（[design-log](design-log.md)） | 脱敏规则登记之后的记录按键名、值形状与字面量遮蔽 | 日志模块不在参与集里，规则登记不上，脱敏层为空；登记之前绑定的属性不受约束 |
| `pkg/db`（[design-db](design-db.md)） | 加密列与根密钥；唯一约束与条件更新作原子仲裁 | 根密钥与库一起失守，加密列全失；仲裁失效则一次性凭据可被重复消费 |
| `pkg/kv`（[design-kv](design-kv.md)） | `CompareAndSwap` 只在当前值等于旧值时写入 | 流程记录的比较并交换失效，授权码可能被换多次 |
| `pkg/ratelimit`（[design-ratelimit](design-ratelimit.md)） | 入口层限流按责任键计 | 预认证洪泛的前置减负缺席 |
| `pkg/audit`（[design-audit](design-audit.md)） | 强制档审计由调用方在事务之外、动作之前写；尽力档写失败不令动作失败 | 强制档缺席则冒充与管理面动作不开始；尽力档缺失只丢审计行 |
| `pkg/settings`（[design-settings](design-settings.md)） | 变更事件 `settings.changed` 可订阅 | 放宽的设置变更没有 Warn 与审计 |
| `pkg/http`（[design-http](design-http.md)） | `Router.Endpoints()` 与保留端名 `*`；中间件链的 `After` 与 `Order` 排序 | 默认守全部端无从展开；链上次序不成立则准入层与限流的次序保证不成立 |
| `pkg/tenant` 的 `tenant.admission`（[design-tenant-admission](design-tenant-admission.md)） | 非成员与范围不符的冒充身份得 404 | 跨租户访问与冒充越界的拦截缺席 |
| go-jose/v4 | `pkg/go.mod` 的版本下限，对应畸形令牌触发 panic 的缺陷；数值见 [design-authn-token](design-authn-token.md) | 删去该要求会使依赖图把版本带回受影响的版本 |
| goxmldsig | `pkg/go.mod` 的版本下限，对应签名绕过缺陷；数值见 [design-authn-sso](design-authn-sso.md) | 同上 |
| scs | 对库里没有的令牌新建会话，不沿用客户端给的令牌 | 会话固定对策失效 |

## UML 图

### 信任边界

下图固定各方在威胁模型里的信任位置：箭头方向是请求或数据的流向，边上的文字是 authn 在这条边上信任什么或要求什么。

```mermaid
flowchart LR
    subgraph untrusted["不可信"]
        caller["调用方：浏览器或程序"]
        attacker["匿名攻击者"]
    end
    subgraph external["外部方"]
        idp["外部身份提供方：Google、GitHub、微信、租户 IdP"]
        smsvendor["短信厂商"]
        rs["外部资源服务器"]
    end
    subgraph process["authn 所在进程"]
        gate["网关与校验后端"]
        engine["登录引擎、会话、令牌、因素、冒充、管理面"]
        adm["tenant.admission"]
    end
    subgraph infra["基础设施与宿主"]
        db[("db")]
        kv[("kv")]
        host["宿主：前端、Decider、秘密保管、网络与时钟"]
    end
    caller -->|"凭据与登录请求，一律按不可信处理"| gate
    attacker -->|"同样的入口，没有额外权限"| gate
    gate --> engine
    gate --> adm
    idp -->|"签名过的断言与回跳，补充校验之后才采信"| engine
    engine -->|"只取送达与否的归类"| smsvendor
    rs -->|"按 JWKS 验签，自行读 imp_tenant"| engine
    engine -->|"权威状态，撤销依据"| db
    engine -->|"流程记录，丢失只令流程失败"| kv
    host -->|"前提：时钟、秘密、Decider、前端响应头"| engine
```

- 调用方与攻击者在同一侧：所有凭据按不可信处理，校验后端交出事实，网关复核后才盖章（见 [design-authn-gate](design-authn-gate.md)）。
- 外部身份提供方的信任有范围：IdP 声明的多因素只在该提供方所属租户内算数，邮箱验证声明只在 `auto-link` 开启时采信，租户 IdP 管理员只对自己租户新建的账户有登录控制权。
- 外部资源服务器是令牌的校验方，不是 authn 的一部分：冒充范围与密钥撤销能否对它生效，取决于它读不读 `imp_tenant` 与何时刷新 JWKS（见[部署层前提](#部署层前提)）。
- db 持有权威状态，丢失会放宽校验的状态都在 db；kv 只持有丢失只会让流程失败的状态（[ADR-authn-state-placement-2026-10-10](../../adr/adr-authn-state-placement-2026-10-10.md)）。
- 宿主是前提的提供方：本图里 authn 不信任它之外的任何东西，却依赖宿主提供的前提。

### 拦截位置

下图按请求在链上的先后，标出各类威胁被拦截的位置。链上次序由 `After` 与 `Order` 排出，次序与锚点见 [design-authn](design-authn.md)；认证前的公开端点是被登记为免认证的 authn 自己的路由，它们经过网关，但不由网关认证。

```mermaid
flowchart LR
    req["请求"] --> obs["采集"]
    obs --> gate["网关：载体认领、被认领 cookie 并存的拒绝、浏览器可能自己附带的载体的跨源检查、出口检查、冒充范围核对"]
    gate -->|"Exempt 命中的公开路由"| pre["认证前端点：退避、枚举防护、流程绑定、state、PKCE、nonce、短信预算"]
    gate --> resolve["租户解析"]
    resolve --> audit["审计"]
    audit --> rl["限流"]
    rl --> adm["租户准入：成员判定、冒充范围、租户级强度"]
    adm --> authz["授权：不在本文范围"]
    authz --> handler["handler"]
```

网关之前只有采集：排在认证之外的层自答的请求没有经过认证，这是[链上不保证没有能绕过认证的层](design-authn.md#非保证)的直接后果。准入层的 `After` 写全了网关、租户解析、审计与限流，所以准入层的拒绝要进审计、要计入限流。授权在准入之内，认证只为它留接缝（见 [design-authn-admin](design-authn-admin.md)）。

## 核心算法与流程

### 威胁在请求路径上的拦截位置

下表是威胁类别与拦截者的对应，列出的位置是例举的主要拦截点，不是每条威胁的全部拦截点。请求期判定的顺序由 [design-authn-gate](design-authn-gate.md) 承载，本表不重复它。

| 位置 | 拦截的威胁 | 拦截者 |
|---|---|---|
| 认证前的公开端点 | 口令猜测与枚举、登录 CSRF、授权码重放、开放重定向、短信轰炸、SAML 断言重放与 `InResponseTo` 核对 | [design-authn-login](design-authn-login.md)、[design-authn-sms](design-authn-sms.md)、[design-authn-sso](design-authn-sso.md)、[design-authn-token](design-authn-token.md) 的路由处理器 |
| 网关取凭据与分派 | cookie 投放、同名与不同名的被认领 cookie 并存、cookie 与 `Basic` 头来源的 CSRF、载体撞车 | [design-authn-gate](design-authn-gate.md) |
| 校验后端 | 令牌类型与算法混淆、畸形令牌、会话固定、撤销与账户停用后仍可用 | [design-authn-session](design-authn-session.md)、[design-authn-token](design-authn-token.md) |
| 网关出口检查与冒充范围核对 | 后端越权盖章、冒充身份越过范围 | [design-authn-gate](design-authn-gate.md) |
| 租户准入 | 跨租户访问、无租户上下文或范围不符的冒充身份 | [design-tenant-admission](design-tenant-admission.md) |
| authn 自己的敏感端点 | 账户安全类操作缺新鲜因素、冒充中改变认证事实、管理面滥用 | [design-authn-mfa](design-authn-mfa.md)、[design-authn-admin](design-authn-admin.md)、[design-authn-impersonation](design-authn-impersonation.md) |
| 授权与 handler | 资源级越权、业务路由上冒充身份的敏感操作 | 授权模块与宿主，不在本文范围 |

### 贯穿各类别的机制

下列机制各自抵御多类威胁，每条的机制本体由右栏的文档承载。列表是例举，不是封闭集。

| 机制 | 抵御的威胁 | 载体 |
|---|---|---|
| 统一应答与等时、诱饵派生 | 账户枚举、计时侧信道 | [design-authn-account](design-authn-account.md)、[design-authn-login](design-authn-login.md) |
| 一次性凭据与流程的消费靠原子的条件写 | 断言重放、授权码重放、验证码重用、刷新令牌重用 | [design-authn](design-authn.md) 的设计原则；各模块文档 |
| 默认拒绝 | 监听端漏守、管理面滥用、冒充、跨租户访问 | [design-authn-gate](design-authn-gate.md)、[design-authn-admin](design-authn-admin.md)、[design-tenant-admission](design-tenant-admission.md) |
| 放宽项各是显式开关、缺省取最严、启动诊断各占一行 Warn | 配置错误、设置写权限滥用 | [design-authn-config](design-authn-config.md) |
| 状态按丢失的方向放置 | 缓存或 kv 丢失导致校验被放宽 | [ADR-authn-state-placement-2026-10-10](../../adr/adr-authn-state-placement-2026-10-10.md) |
| 凭据与秘密不以明文落地：哈希、摘要、加密列、脱敏 | 数据库转储、秘密进入日志与事件 | [design-authn-account](design-authn-account.md)、[design-authn-events](design-authn-events.md) |
| 对库没做的校验由 authn 补，补充校验失败一律按失败 | XML 签名包装、SAML 空值跳过、JWT 缺 `exp` | [design-authn-sso](design-authn-sso.md)、[design-authn-token](design-authn-token.md) |
| 撤销以账户状态与 `valid_after` 为准，不依赖时钟 | 撤销不及时、时钟偏差 | [design-authn-account](design-authn-account.md)、[design-authn-session](design-authn-session.md) |
| 宁可重复校验也不省略 | 单点校验被绕过 | [design-authn](design-authn.md) 的设计原则 |

## 错误与失败语义

### 失败的方向

依赖不可用时的方向是拒绝：db、kv、签名密钥、短信发送方与 IdP 不可用时，对应路径答 503，不答 200，也不答 401。有界的放宽是封闭集，各自带设置与上界，集合与上界由 [design-authn](design-authn.md) 与 [design-authn-failure](design-authn-failure.md) 承载；上文表中的缓存滞后、`check-session=false` 下的令牌存续、设置变更的传播延迟与证书陈旧窗口是同一批。本文的表不新增放宽项：表中出现的放宽方向的风险，要么属于那个封闭集，要么写在[部署层前提](#部署层前提)的「前提不成立时」一栏里。

### 依赖未核实行为的假设

下表是本文的断言所依赖、尚未核实的行为，是封闭集。每一项以所列测试守住，测试失败即视为对应断言的前提不成立，须回到相关决策记录重判。

| 假设 | 守住它的测试 |
|---|---|
| 链上次序成立：准入层在审计与限流之内、授权之外，网关在租户解析与审计之外；本文的拦截位置图与跨租户访问的对策依赖它 | 该假设与它的装配测试由 [design-authn](design-authn.md#链上次序) 的「链上次序」一节承载，本文不另设测试 |
| gosaml2：校验后能取得 `types.Response`；响应与断言都未签名时拒绝；NameID 中的 XML 注释不被截断，或被补充校验拒绝 | 签名包装向量与「全未签名」向量的表驱动测试，含注释 NameID 的负向用例 |
| goxmldsig 没有另外的拒绝 SHA-1 的途径，预扫描足以拦住 SHA-1 | SHA-1 签名向量的负向测试 |
| go-jose：每把密钥只配一种算法，并在运算时检查，由 authn 自己查令牌头里的算法完成；已解析令牌的 `typ` 头可取 | `alg` 在白名单之外、`typ` 不符、缺 `exp` 的令牌各被拒；依赖测试断言 `pkg/go.mod` 对 go-jose 的版本下限不低于 [design-authn-token](design-authn-token.md) 的依赖写明的版本 |
| scs 对库里没有的令牌新建空会话，不沿用客户端给的令牌；存储键的哈希与 authn 自己轮换时算出的一致 | 提交一个预置令牌，断言判为无此会话；断言两处算出的存储键相等 |
| `CrossOriginProtection` 的旁路模式能覆盖 gin 的路径参数，且只旁路回跳路由 | 对回跳路由的跨站 POST 用例，同时断言其他流程路由的跨源请求仍被拒 |
| Google 的授权端点接受带 `code_challenge`（S256）的请求 | 用测试客户端对授权端点发带 `code_challenge` 的请求 |
| 受限出站客户端的特殊用途地址段表与 IANA 登记表一致；NAT64（RFC 6052）与 6to4（RFC 3056）内嵌 IPv4 的位置与掩码正确 | 对照登记表与 RFC 原文的向量测试；它守住的是核对那一天的状态，复核时机见下 |
| 口令派生在并发上限与内存预算下的并发内存峰值在目标部署规格之内 | 对并发上限下的 Argon2id 派生做压测，记录峰值 |
| 审计的语句级明细不带含加密列的值 | 经模型路径写入含加密列的模型，断言审计行里不出现任何秘密字节 |
| 外部身份的绑定键只含（提供方，主体），邮箱变化不影响账户归属 | 提供方返回的邮箱变化之后，用同一（提供方，主体）登录仍落到同一账户 |

**特殊用途地址段表的复核时机。** 登记表会新增整块，包内不保留它的副本，所以向量测试不会因登记表更新而失败，表与登记表的一致只靠复核维持。复核在下列时点重做：每次发布之前；IANA 的 IPv4 或 IPv6 特殊用途地址登记表出现新增或变更条目时；Go 工具链升级时，因为 `netip.Addr.IsGlobalUnicast` 一类判定的行为随标准库变化，而表只补它没有拦住的部分。复核逐条对照登记表，新增整块补进表与向量，并更新受限出站客户端一节写明的核对版本（见 [design-authn-sso](design-authn-sso.md)）。

### 本文不作的保证

- **本文是设计层的威胁分析，不是渗透测试或形式化证明的结果。** 残余风险栏的「无已知剩余项」只说没有已知的剩余风险。
- **库的未知缺陷不在表里。** 表只写已知的公告与版本下限。
- **对策的存在以实现状态为准。** 表描述设计；对应模块尚未实现时，该对策不存在，见[实现状态](#实现状态)。
- **部署层前提不由 authn 校验。** 启动诊断能让一部分前提的缺席被察觉，不能让它们成立。

## 扩展点

新增校验后端、登录方式、因素、IdP 协议、发送方、路由或会增长的表时，对威胁模型的复核判据如下，判据是例举，不是封闭集。复核的结果是在本文对应类别里增补威胁、对策与残余风险，或写明新增项归入既有的哪一行。

- **新增的路由**经 `mount.Mounter` 登记访问级别；登记为公开的路由，复核它是否有失败退避、枚举防护、流程绑定与跨源检查。
- **新增的凭据载体**由一个校验后端整条认领，账户命名空间与能力声明在出口检查里核对。
- **新增的状态**按丢失的方向放置：丢失会放宽校验的进 db，丢失只会让流程失败的进 kv。
- **新增的秘密**同时登记脱敏（键名、值形状或字面量），并以哈希、带密钥的摘要或加密列存放。
- **新增的一次性凭据或流程**以原子的条件写消费，败者一律拒绝。
- **新增的放宽项**是只取主配置源的显式开关，缺省取最严，并在启动诊断里占一行 Warn。
- **新增的出站请求**走受限客户端，或写明为什么不需要。
- **新增的会增长的表**有持有者与清扫协程，行数上界可由已有速率与衰减窗口得出。
- **新增的响应**对已登记与未登记的标识同形等时，带 `Cache-Control: no-store`。
- **新增的库**隔在叶子包里，固定版本，下限取已知公告修复之后的版本。

## 实现状态

下表是威胁模型所依赖的各项对策的实现状态，以代码为准。账户、会话、令牌、登录、多因素、短信、SSO、冒充与管理面各自的细粒度状态由对应的设计文档承载，本表只给每项对策的汇总。

| 能力 | 状态 | 代码 |
|---|---|---|
| 网关默认守全部端、出口检查、浏览器可能自己附带的载体的跨源检查与冒充范围核对 | 已实现 — 交付 `ScopeGuard` 的准入层尚未实现，装配里没有它时只放 `Impersonable` 路由；属性键的保留前缀不在盖章处判（属性注入一行的对策靠写属性的一方，网关不强制） | `pkg/http/router.go` `(*router).Endpoints`；`pkg/authn/gate/gate.go` `expandGuarded`；`pkg/authn/gate/serve.go` `admit`、`identify`、`impersonationAdmitted`；`pkg/authn/gate/exitcheck.go` `checkPrincipal`；`pkg/authn/gate/dispatch.go` `inNamespace` |
| 后端不可用时拒绝：503 而非匿名、预算、在飞闸门、`recover` | 已实现 | `pkg/authn/gate/serve.go` `enter`、`verify` |
| 判决不可字面构造、零值读作没答 | 已实现 | `pkg/authn/internal/verdict/verdict.go` `Verdict`、`OutcomeOf` |
| 免认证登记按已匹配的路由逐字比对 | 已实现 — `Exempt`、`Optional`、`Impersonable` 与 `ErrAccessConflict` | `pkg/authn/gate/exempt.go` `Exempt`、`Optional`、`Impersonable`、`seal`、`accessOf` |
| 启动诊断列出展开后的受守端、被开出的口子与放宽项 | 已实现 | `pkg/authn/gate/gate.go` `diagnose`、`freeze` |
| 凭据的日志脱敏规则 | 部分实现 — 键名全集、JWT 与令牌前缀形状、`otpauth://` 地址、查询串机密对、会话 cookie 对与按 scheme 的值形状规则已在；缺配置字面量与各模块自己新增的键名 | `pkg/authn/gate/redact.go` `registerNewRules`、`registerValueRule`、`shapeMatcher`、`credentialMatcher` |
| 口令表的口令保护 | 已实现 — PHC 条目与 Argon2id 缺省、校验缓存、进程内失败退避、并发上限与内存预算已在，条目解析的模糊用例与以 `m` 取上限的条目并发校验时内存峰值不超过预算的用例也已在 | `pkg/authn/static/hash.go` `Hash`、`ParseHash`；`pkg/authn/static/static.go` `Verify`；`pkg/authn/internal/password` `FuzzParse`、`TestDerivationsOfLargeEntriesNeverHoldMoreThanTheBudget` |
| 加密列与根密钥 | 已实现 | `pkg/db/crypto.go` `newKeyRing`、`(*keyRing).encrypt` |
| 日志脱敏的登记面 | 已实现 | `pkg/log/log.go` `Redaction` |
| 标识级与账户级失败退避、`valid_after`、账户停用的跨模块清理 | 未实现 | — |
| 登录会话、cookie 与 CSRF 的对策 | 未实现 | — |
| 访问令牌、刷新令牌轮换与重用检测、签名密钥轮换 | 未实现 | — |
| 登录流程绑定、`state`、PKCE、`nonce`、`return_to` 校验 | 未实现 | — |
| 多因素的保障等级合成与账户安全类操作的因素门槛 | 未实现 | — |
| 短信的预算、抑制行与号码前缀白名单 | 未实现 | — |
| 企业 SSO 的断言补充校验、SHA-1 预扫描与受限出站客户端 | 部分实现 — 缺断言补充校验与 SHA-1 预扫描；受限出站客户端已在，追踪头的注入留给调用方 | `pkg/authn/internal/egress/egress.go` `New`；`pkg/authn/internal/egress/address.go` `Permitted` |
| 冒充的结构性限制、租户范围与审计先于动作 | 未实现 | — |
| 判定接缝 `Decider` 与管理面的强制审计 | 未实现 | — |
| 租户准入层 `tenant.admission` | 未实现 | — |
| 放宽方向的设置变更订阅与 `authn.*` 设置写权限的硬要求 | 未实现 | — |
| `pkg/go.mod` 对 go-jose 与 goxmldsig 的版本下限 | 未实现 | — |
| 链上次序的装配测试 | 未实现 | — |
