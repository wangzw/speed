# 登录会话：db 权威存放，scs 只作令牌与 cookie 的桥，cookie 缺省仅主机会话期，跨源由网关检查

## 背景

登录会话是 `authn.session` 持有的状态：浏览器的 cookie 会话、非浏览器客户端的访问令牌与刷新令牌所挂靠的会话、服务账户每个密钥对应的会话。这些会话共用一张表、一套撤销接口，期限语义按种类不同。

**需求。**

- 管理面按账户列出会话并逐条撤销，账户自助的会话界面列出会话、撤销自己的单个会话并一次撤销除当前之外的其余会话，见需求文档（REQ-security-022、REQ-security-023、REQ-security-024、REQ-platform-admin-019、REQ-platform-admin-020）。
- 撤销必须可靠：已撤销的会话不得因任何存储行为复活。
- 访问令牌带会话标识，刷新令牌与冒充记录挂在会话上；撤销一个会话即撤销挂在它上面的一切。
- 网关从 cookie 认出身份，cookie 随浏览器自动发送，需要跨源防护（[ADR-authn-credential-dispatch](adr-authn-credential-dispatch.md)）。

**存放的既有结论。** 状态按“丢失之后校验会不会变宽”放置，撤销标记属于会放宽的一类，进 db（[ADR-authn-state-placement](adr-authn-state-placement.md)）。本记录在这个结论之上决定会话的具体形态。

**现成库的事实。** 会话库按“现成库与 speed 之间的桥”处理，不重复发明。`alexedwards/scs/v2`（[REF-scs](../references.md)）（v2.9.0）覆盖的部分包括：256 位随机令牌、存储中只留令牌的 SHA-256、cookie 属性的写出、创建路径、可换的编码器与带上下文的存储接口。它不覆盖账户概念，没有按账户列出与撤销，没有 CSRF。与 speed 对接时有以下约束：

- scs 的中间件 `LoadAndSave` 包装 `http.ResponseWriter`，在处理器返回后才提交并写 `Set-Cookie`。入口模块的处理器经 gin（[REF-gin](../references.md)）的 `c.Writer` 写响应，包装写入器看不到写入，首次写响应时头已发出，登录应答里没有 `Set-Cookie`。提交点必须在首次写响应之前。
- 设置了 `IdleTimeout` 后，scs 每次加载都把状态置为已修改并强制续写，每个请求一次写库。
- 创建路径 `CommitCtx` 收到的 `expiry` 由 scs 的 `Lifetime` 推出。会话期限是运行期可变的设置，`Init` 里读设置要求取用设置模块，且在设置自己的 `Init` 之前读取返回未载入错误，所以 `Lifetime` 无法取设置的当前值，只能取设置域的上界。
- `RenewToken` 先删旧令牌，到 `Commit` 时才写新令牌，中途失败则会话丢失；它同时重置 scs 自己的期限；它按请求里的令牌寻址，而加强认证与冒充的开始、结束由 API 端调用，手里没有 cookie 令牌，只有公开的会话标识。
- scs 对库里找不到的令牌创建新的空会话，不沿用客户端给的令牌字符串。
- `Secure` 默认关闭。
- 默认编码器是 gob，要求类型登记，解码范围更大。

**cookie 与跨源。** `SameSite=Lax`（[REF-rfc6265bis](../references.md)）不拦同站兄弟子域发起的请求，也不覆盖不支持该属性的旧浏览器。标准库 `net/http.CrossOriginProtection` 以 `Sec-Fetch-Site` 判跨源，缺失时比较 `Origin` 与 `Host`，两个头都没有的请求按非浏览器或同源放行。

## 考虑过的选项

### 会话放 kv

会话记录与撤销标记存在 kv，键为会话标识，`Set` 续期空闲期限；变体是 kv 作热路径缓存、db 作权威。

**否决**：按账户列出与批量撤销无从实现，kv 没有键枚举；丢键的方向对撤销是放宽，撤销失效、会话复活；`Set` 续期与并发 `Delete` 之间没有原子性，续期会把刚撤销的会话写回；没有“取并删”，轮换不能原子；与账户状态、成员变更分处两个存储，没有共同事务。变体里缓存中的撤销滞后没有上界，权威在 db 时 kv 缓存多出一份要失效的副本，换不来 kv 作权威时缺的任何一项性质。完整论证见 [ADR-authn-state-placement](adr-authn-state-placement.md)。

### 完全自写会话，不用 scs

令牌生成、哈希存储、cookie 写出全部自写。

**否决**：令牌生成、存储哈希、cookie 属性与可换接口是 scs 现成且正确的一半，自写会重复发明；自写部分只需覆盖 scs 没有的账户索引、撤销与轮换，这一部分无论是否用 scs 都要自写。

### gorilla/sessions 的 CookieStore，或 gin-contrib/sessions

`CookieStore` 把会话数据整体放进签名的 cookie；`gin-contrib/sessions`（[REF-gin-contrib-sessions](../references.md)）是 gorilla（[REF-gorilla-sessions](../references.md)）之上的 gin 薄层。

**否决**：`CookieStore` 的数据在客户端，服务端无法撤销单个会话，与踢下线直接冲突。`gin-contrib/sessions` 把会话挂在 `gin.Context` 上而不在 `context.Context` 里，校验后端的 `Verify` 方法取不到它；它建在 gorilla 之上，`go.mod` 声明了多个存储库，进入模块图。gorilla 的服务端存储只有文件系统一种，没有空闲超时与令牌轮换的内建概念。

### 无状态的签名 cookie 或 JWT 当浏览器会话

浏览器会话以自包含的签名令牌表达，服务端不存行。

**否决**：无法撤销，无法列出，与踢下线和“撤销即终止”的需求直接冲突。长期免登录由可撤销的刷新令牌承担，不由浏览器 cookie 承担。

### 按 scs 的默认用法：LoadAndSave、IdleTimeout、RenewToken、传入的 expiry

使用 scs 的中间件、空闲期限、令牌更新与创建时传入的过期时刻。

**否决**：`LoadAndSave` 在 gin 下丢失 `Set-Cookie`，见背景。`IdleTimeout` 使每个请求写一次库，且续期写不带“未撤销”条件，可以写回刚撤销的会话。`RenewToken` 的先删后写使失败即丢会话，且无法按公开标识寻址。传入的 `expiry` 来自被设为设置域上界的 `Lifetime`，直接使用会把几小时的绝对期限静默变成上界，而有效性只看行，没有第二道检查。

### 所有种类共用一套空闲加绝对期限

浏览器会话、令牌会话与服务账户会话统一按空闲期限与绝对期限判有效。

**否决**：令牌会话的空闲约束已由刷新令牌行承担，会话行再设空闲期限会使仍持有有效刷新令牌的客户端被判过期；服务账户会话随服务密钥存续，没有空闲的概念，加空闲期限会使长时间不调用的服务客户端被迫重新取得会话。三种会话的失效方式不同，统一的期限语义要么放宽一种，要么误杀另一种。

### 令牌式 CSRF，或只靠 SameSite

同步令牌或双重提交 cookie 防 CSRF；或只设 `SameSite`。

**否决**：同步令牌要服务端状态与前端配合；双重提交的 cookie 在子域可被覆盖。只靠 `SameSite` 时，兄弟子域与旧浏览器不在其保护内，它是纵深而不是唯一防线。

### 持久 cookie（记住我）

cookie 带 `Max-Age` 或 `Expires`，关闭浏览器后仍在。

**否决**：放大被盗窗口。长期免登录由刷新令牌承担，它可撤销、可轮换、挂在会话上。

### 开发用的不安全开关

一个配置开关放行非回环主机的 http 基址，cookie 随之去掉 `__Host-` 前缀与 `Secure`。

**否决**：本机开发用回环主机的 http 基址即可，这样的基址上 cookie 去掉前缀与 `Secure`、其余属性不变（见决策），流量经回环接口、不出本机，不需要这个开关。开关唯一多放行的是非回环主机上的 http 部署，那里会话 cookie、流程绑定 cookie 与登录时提交的口令以明文经网络传输，路径上任何能读这段流量的人都能拿到；同域的兄弟子域也真实存在，cookie 去掉前缀后它们能写同名 cookie。

### 回环 http 基址上沿用前缀

cookie 名在任何基址上都带 `__Host-` 前缀，配 `cookie-domain` 时带 `__Secure-`。

**否决**：WebKit 27.2 不保存经 http 写入的 `__Host-` 与 `__Secure-` cookie，回环主机也不例外；Chromium 与 Firefox 保存。Safari 27 加入了回环主机上的 `Secure` cookie（[REF-safari-27-release-notes](../references.md)），没有放宽前缀。沿用前缀时，在 Safari 里经 `http://localhost` 登录拿不到流程 cookie 与会话 cookie，登录走不完。

### 回环 http 基址上只去掉前缀，保留 `Secure`

三个 cookie 在回环 http 基址上不带前缀，`Secure` 与 https 基址上一样恒开。

**否决**：Linux 上的 WebKit 不保存经 http 写入的 `Secure` cookie，回环主机也不例外。实测中，一个 `http://localhost` 源写入两个只差 `Secure` 的 cookie，Linux 上的 WebKit 27.2 只回送不带 `Secure` 的那个，Chromium 159 两个都回送；macOS 上的 WebKit 保存回环主机上不带前缀的 `Secure` cookie，Chromium 与 Firefox 也保存。保留 `Secure` 时，Linux 上的 WebKit 经 `http://localhost` 拿不到重置 cookie、流程 cookie 与会话 cookie，密码重置与登录都走不完。`Secure` 在回环 http 源上也不保护任何东西：浏览器经 http 发往回环主机的请求本就是明文，只经回环接口，不出本机。

### 回环 http 基址上只去掉前缀，词干仍是 `sid`

会话 cookie 在 https 上叫 `__Host-sid`，回环 http 基址上叫 `sid`。

**否决**：`sid` 是常见的会话 cookie 名。浏览器按主机隔离 cookie、不按端口，同一回环主机上另一个应用写的 `sid` 会随请求送到本服务，与本服务的会话 cookie 同名；网关对同名的两个 cookie 整体拒绝，本服务的登录随之失效。前缀在 https 上挡住了这一类覆盖，去掉前缀后要靠名字本身避开。

### 按回环判据决定前缀与 `Secure`

cookie 的前缀与 `Secure` 由“对外基址是否为回环主机的 http 地址”决定，与启动期准入 http 基址共用一个回环判据。

**否决**：浏览器是否保存带 `Secure` 或前缀的 cookie，看的是写入它的源，也就是对外基址，判断从源的 scheme 出发：非回环主机的 http 源上这类 cookie 不被保存，回环主机的 http 源上各浏览器不一致，见“回环 http 基址上沿用前缀”与“回环 http 基址上只去掉前缀，保留 `Secure`”。主机是否回环回答的是另一个问题：明文流量会不会出本机，这是准入要判的。按回环判据定 cookie，非回环主机的 http 基址会得到浏览器不保存的带前缀、带 `Secure` 的 cookie，这条规则只因准入先拒掉了这类基址才不出错，cookie 规则与准入规则因此互相依赖。只看 scheme 时，cookie 规则自己成立，准入只决定哪些基址能启动；终结 TLS 的代理之后对外基址是 https，cookie 写 `Secure`，与代理到进程那一跳的 scheme 无关。

### 各模块自己写 cookie

登录引擎、冒充等模块各自按需拼 `Set-Cookie`，会话模块只管会话行。

**否决**：cookie 的名字、前缀、`SameSite`、`Domain` 与清除的写法分散在多个模块，任何一处漏写 `Secure` 或 `HttpOnly` 都是缺陷，宿主换上自己的会话模块时也无从统一。cookie 的写出收在 `Sessions` 一处，别的模块没有拼写的机会。

### 不缓存，每个 cookie 请求读库

会话校验不设进程内缓存，撤销在所有副本上即时生效。

**否决**：每个已认证请求一次库读，库成为请求路径上的固定成本，db 抖动直接放大为全站延迟。缓存有效期设为零就得到这一形态，所以它保留为运维可选的取值，不作缺省。

### 较长的缺省缓存有效期

会话校验缓存缺省取更长的有效期，以减少库读。

**否决**：撤销在其他副本上的生效更晚，缓存有效期就是放宽的上界。缺省取短值，上限由设置的取值域封顶，更长的取舍属于宿主。

### db 权威存放，scs 作桥，cookie 缺省仅主机会话期，网关做跨源检查

**采纳**，见决策。

## 决策

采纳：登录会话的权威存放在 db 的 `authn_sessions`；scs 只承担令牌生成、哈希存储、cookie 写出与创建路径，其余由 authn 自写；每种会话种类按自己的期限语义判有效；cookie 缺省为仅主机的会话期 cookie，由 `authn.session` 一处写出；浏览器可能自己附带的凭据下的非安全方法由网关用标准库 `CrossOriginProtection` 检查。表结构、方法集、cookie 取值、绕开项、校验阶段与缓存见设计文档，本记录只定判据。

- **权威存放与撤销。** 每个会话一行，公开标识是行的主键而不是凭据，行里只存令牌的 SHA-256，不存明文。撤销以行的撤销标记为准，不比较时间，时钟偏差不使撤销失效。撤销接口唯一：访问令牌带会话标识，刷新令牌与冒充记录挂在会话上，撤销会话即撤销它们。会话行的每一次续期与轮换都以“未撤销”为写入条件，写不会把已撤销的会话写回。
- **scs 只作令牌与 cookie 的桥。** scs 提供令牌生成、哈希存储、cookie 写出与创建路径；存储适配器、公开标识、按账户的索引与撤销、空闲续期的节流、令牌轮换与 gin 的适配代码由 authn 自写；编码器不用 gob，避开类型登记与更大的解码范围。令牌绝不由客户端指定，登录与提权仍换令牌作纵深。scs 的默认用法中与上述约束冲突的部分（`LoadAndSave`、`IdleTimeout`、`RenewToken`、创建时传入的 `expiry`）一律绕开，每个绕开项各有一条测试，去掉该绕开项时测试必败，scs 升级时先重跑。
- **种类是封闭集：`browser`、`token`、`service`，各按自己的期限语义判有效。**
  - `browser`：空闲期限与绝对期限并存；空闲期限的续期按节流间隔条件更新。
  - `token`：只有绝对期限；空闲约束由刷新令牌行承担，访问令牌校验不续期。
  - `service`：没有空闲期限；一个服务密钥一行，行只经撤销密钥终结。访问令牌的撤销接口对这一种类是无操作，持有访问令牌的人不能借撤销废掉服务客户端的会话行，也就没有复活被撤销行的路径。
  - 所有种类共有的有效条件：账户处于启用，会话创建时刻不早于账户的 `valid_after`。凡由企业 SSO 建立的会话，不论种类，绝对期限与 SSO 的绝对期限取较小者。
  - 每账户的会话数上限只统计 `browser` 与 `token`，`service` 不占。
- **cookie 缺省仅主机、会话期。** 缺省的 cookie 名带 `__Host-` 前缀，`Secure`，没有开关能关闭它，只有 http 基址上不写（见“cookie 的前缀与 `Secure` 只看对外基址的 scheme”），`HttpOnly`，不设 `Domain`，不设 `Max-Age` 与 `Expires`；没有记住我。对外基址必须是 https，回环主机的 http 基址除外，其余 http 基址在启动期被拒。偏离缺省只经显式配置：配 `cookie-domain` 时名字改用 `__Secure-` 前缀并写出 `Domain`，`same-site` 可取更严的值。哪些开关存在、取值与启动诊断由设计文档与配置文档承载；偏离缺省会失去 `__Host-` 所保证的子域不可写，这一代价写在后果里。
- **cookie 的前缀与 `Secure` 只看对外基址的 scheme，三个 cookie 的词干带 `authn-`。** 会话 cookie、登录流程绑定 cookie 与密码重置绑定 cookie 的名字是前缀加词干，词干分别为 `authn-sid`、`authn-flow`、`authn-reset`。对外基址 `authn.gate.mount.public-url` 是 https 时，`https://localhost` 也是，名字带 `__Host-` 前缀（配 `cookie-domain` 时带 `__Secure-`）并写 `Secure`；是 http 时两者都不带，其余属性不变：`HttpOnly`、`Path=/`、不设 `Domain`，`SameSite` 仍取写出方定的常量。基址的主机不参与这条规则，回环判据只用于准入：启动期只准入回环主机的 http 基址，所以能启动的部署里，cookie 不带前缀与 `Secure` 的只有回环主机的 http 基址。在终结 TLS 的代理之后，对外基址是 https，cookie 写 `Secure`，与代理到进程那一跳用什么 scheme 无关。名字、是否写 `Secure` 与 cookie 本身都由 authn 内部的同一个包给出：写出与清除这三个 cookie 都经它构造，名字、`Secure`、`HttpOnly` 与 `Path=/` 由它定，读取经它取名。http 基址配 `cookie-domain` 在启动期被拒：带 `Domain` 的 cookie 要用 `__Secure-` 前缀，也就要写 `Secure`。理由：WebKit 不保存经 http 写入的带前缀 cookie，Linux 上的 WebKit 连不带前缀的 `Secure` cookie 也不保存，见考虑过的选项；回环源上两者都不保护任何东西：流量不出本机，`Secure` 所防的明文传输在那里不存在，前缀挡不住任何能到达它的人，只有本机进程能为它写 cookie，Chromium 与 Firefox 本就在同一主机的各端口之间共享 cookie。
- **cookie 的写出与清除只有一处。** `Sessions` 的 `SetCookie` 与 `ClearCookie` 是唯一写出点，`authn.login`、`authn.impersonation` 等别的模块以及宿主换上的会话模块都不自己拼。凭据被拒、登出与撤销时清除 cookie；跨源检查不通过时不清除，因为 cookie 本身有效、被拒的是请求的意图，跨站请求不能借一次被拒的 POST 让用户登出。
- **浏览器可能自己附带的凭据的跨源检查在网关。** 载体是 cookie 或 `Authorization` 头里 `Bearer`（[REF-rfc6750](../references.md)）以外的 scheme、且方法不是 `GET`、`HEAD`、`OPTIONS` 的请求，由网关在调用校验后端之前用标准库 `CrossOriginProtection` 检查，载体判据与否决项见 [ADR-authn-credential-dispatch](adr-authn-credential-dispatch.md)。不通过即 401，由网关自产，不写 403。检查与 `SameSite` 互为兜底，不做令牌式 CSRF；外部方回跳的端点登记为检查的旁路；跨源的单页应用用承载令牌，不用 cookie。可信来源的取值与覆盖的路由由设计文档承载。
- **校验缓存的有效期是撤销生效的上界。** 会话校验经进程内缓存，缓存有效期是撤销在其他副本上生效的上界，属于有界放宽，由 [ADR-authn-failure](adr-authn-failure.md) 逐项列出；缺省取短值，上限由设置的取值域封顶。读库出错是 `Unavailable`，失败时拒绝，不是放行。缓存的键、次序与读库次数见 design-authn-session。
- **账户停用、删除与密钥撤销的清理**经账户生命周期钩子与事件补偿完成，会话校验对账户状态与 `valid_after` 的核对是钩子之外的双重校验（[ADR-authn-account-lifecycle](adr-authn-account-lifecycle.md)）。

## 后果

会话的列出、批量撤销与踢下线是对 `authn_sessions` 的查询与条件更新。会话行与账户行同在 db，会话校验对账户状态的核对不跨存储。令牌与服务账户的撤销落在同一个撤销接口上，不另立撤销表（[ADR-authn-token](adr-authn-token.md)）。冒充记录挂在会话上，会话撤销即冒充终止（[ADR-authn-impersonation](adr-authn-impersonation.md)）。预置令牌的固定攻击从机制上不成立，令牌只由服务端生成。cookie 的写出只有一处，名字与属性不会在别的模块里被各自拼写。

**接受的代价：**

- **每个带 cookie 的请求在缓存未命中时一次库读**（会话行加账户快照）。撤销与停用在其他副本上的生效滞后不超过缓存有效期，设为零即严格，代价是每个请求读库。缓存不可用时每个已认证请求一次读库。
- **db 不可用时已登录用户也被拒**（缓存有效期之外）：失败时拒绝，不是失败时放行。
- **空闲期限的续期按节流间隔条件更新**，实际空闲超时可能比设置长至多一个节流间隔。续期写失败被忽略，请求照常通过，只影响空闲续期的精度。
- **会话要求宿主有 db。** 只有口令表的部署没有会话。
- **scs 的扩展点要绕开几处**，存储适配器、编码器、轮换与续期各由 authn 自写，scs 在这里承担的只剩令牌生成、cookie 写出与创建路径。scs 升级时，绕开项各带的测试要先重跑，存储键的哈希函数若变化，轮换会先失败。
- **非回环主机上只有 http 的部署起不来。** 用局域网地址或内网主机名直接以 http 访问的测试环境要在前面放一个终结 TLS 的代理，或改用回环主机；启动期的 `ErrInsecureConfig` 没有开关可绕。
- **http 基址与 https 基址上 cookie 名与 `Secure` 不同。** 同一浏览器先后经 `http://localhost` 与 `https://localhost` 访问时持有两套 cookie，换基址等于重新登录。http 基址只在回环主机上能启动，那里的 cookie 不带 `Secure`，它们与带 `Secure` 时一样以明文只经回环接口传输。http 基址上的名字没有前缀，同一回环主机上的其他应用能写同名 cookie；`authn-` 词干使只有恰好写 `authn-sid`、`authn-flow` 或 `authn-reset` 的应用才会撞名，撞名时网关拒绝同名的两个 cookie，表现为本服务在该浏览器上的登录失效，不会把另一个应用的值当成凭据放行。http 基址配 `cookie-domain` 起不来。
- **cookie 是否带 `Secure` 取决于对外基址写成什么。** 终结 TLS 的代理之后，对外基址必须写浏览器实际访问的 https 地址；写成代理到进程那一跳的 http 地址时，非回环主机在启动期被拒，回环主机上能启动，但 cookie 不带前缀与 `Secure`。
- **需要跨子域共享 cookie 或更严 `SameSite` 的部署要放弃缺省。** 配 `cookie-domain` 之后 `__Host-` 前缀所保证的子域不可写随之失去，同域下的兄弟子域可以覆盖这个 cookie，启动诊断为此写一行 Warn。`same-site` 取更严的值会使从外站链接进入时首个请求不带 cookie。
- **标准库的跨源检查对既无 `Sec-Fetch-Site` 又无 `Origin` 的请求放行**，只有极旧的浏览器与非浏览器客户端落在这一档；兄弟子域按跨源处理，除非把它的来源加进可信来源。
- **会话令牌在 `HttpOnly` cookie 里**，脚本读不到，但注入的脚本能借浏览器发出同源请求。这一类风险靠应用自身的内容安全策略，不在 authn 的保证之内；会话没有绑定设备或网络，绑定在 NAT 与移动网络下会误杀；列出会话时 `ClientInfo.IP` 在未配置可信代理的代理之后是代理地址，仅供记录与展示。
- **跨源的单页应用不能用 cookie 会话。** 它用承载令牌；单页应用与 API 跨域时需要一个排在网关之外的 CORS（[REF-whatwg-fetch](../references.md)）层（防护阶段），否则预检在网关被 401，该层由宿主提供，authn 不提供。
- **会话期 cookie 随浏览器关闭而丢**，没有记住我；长期免登录只经刷新令牌，面向非浏览器客户端。

## 失效条件

- kv 对所有实现统一给出持久性声明、键枚举、“取并删”以及续期与撤销之间的原子性。此时会话放 kv 的否决理由消失，需要按新的 kv 重新检验；放置的总判据与其余状态不因此移动（[ADR-authn-state-placement](adr-authn-state-placement.md)）。
- 出现必须由浏览器 cookie 承载、且跨源的单页应用场景。此时仅靠 `CrossOriginProtection` 加承载令牌的分流不再成立，令牌式 CSRF 需要重新权衡。

以下行为是未经运行核实的假设，依赖它们的断言以对应的测试守住，测试失败即视为本决策的前提不成立：

- 假设 authn 按 scs v2.9.0 的哈希函数算出的存储键，与 scs 自己为同一令牌算出的存储键一致。测试断言两者相等，scs 升级时它先失败。
- 假设 `scs.Load` 对库里找不到的令牌创建新的空会话而不沿用客户端给的字符串，预置令牌的固定攻击因此不成立。测试提交一个预置令牌，断言会话判为无此会话，随后建立的会话令牌不等于预置令牌。
- 假设适配器与编码器按设计文档的写法工作而不破坏 scs 的内部假设，含 `SetDeadline` 的用例。测试覆盖创建、读取、转换到已撤销与过期各条路径。
- 假设忽略 `CommitCtx` 的 `expiry` 之后，绝对期限由 authn 算出的值强制。测试：`Lifetime` 取设置域上界、设置的绝对期限为 12 小时，会话在 12 小时零 1 秒后必须被 `Status` 判为过期；换算出的存储期限小于等于零时不写、按过期处理。去掉忽略 `expiry` 的处理时该测试必败。scs 升级检查加这一条。
- 假设 `Sessions.ForCredential` 对 `credential_id` 唯一约束的原子取或建在各数据库方言上都成立，以影响行数判胜负，不依赖 `RETURNING`。并发首次调用只建一行；账户停用再启用之后服务客户端换令牌即恢复；已撤销的行返回会话已撤销错误。
- 假设标准库 `CrossOriginProtection` 的旁路模式语法可以覆盖 gin 的路径参数，写法是把 `:provider` 写成 `{provider}`。外部回跳端点的旁路测试守住它。
- 假设每种会话种类的期限语义各自成立。测试：`token` 会话在空闲期限之后仍能用访问令牌与刷新令牌；`service` 会话在密钥有效期内任意时刻都能换到令牌；`browser` 会话在空闲期限或绝对期限任一到期后被拒。
