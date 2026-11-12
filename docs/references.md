# 外部来源

本项目全部文档共用这一份外部来源登记：文档引用的每个公开外部来源登记一条，各类文档以 `REF-<slug>` 引用它。

条目只标识来源：名称、来源的类别与覆盖范围、公开地址，以及所引内容依赖的版本、级别或日期，不依赖特定版本时记“—”。选用理由归决策记录，用法归设计文档。

## 规范与标准

| 编号 | 名称 | 是什么 | 地址 | 版本 |
|---|---|---|---|---|
| REF-bcp47 | BCP 47: Tags for Identifying Languages | IETF 当前最佳实践（RFC 5646 与 RFC 4647），定义语言标签的语法与匹配 | <https://www.rfc-editor.org/info/bcp47/> | — |
| REF-fips-140-3 | FIPS 140-3: Security Requirements for Cryptographic Modules | 美国联邦信息处理标准，规定密码模块的安全要求 | <https://csrc.nist.gov/pubs/fips/140-3/final> | 140-3 |
| REF-iana-http-authschemes | IANA Hypertext Transfer Protocol (HTTP) Authentication Scheme Registry | IANA 登记表，列出已登记的 HTTP 认证方案名 | <https://www.iana.org/assignments/http-authschemes/> | — |
| REF-iana-ipv4-special-registry | IANA IPv4 Special-Purpose Address Registry | IANA 登记表，列出 IPv4 特殊用途地址块及其可路由、可全局到达等属性 | <https://www.iana.org/assignments/iana-ipv4-special-registry/> | 2026-10-11 |
| REF-iana-ipv6-special-registry | IANA IPv6 Special-Purpose Address Registry | IANA 登记表，列出 IPv6 特殊用途地址块及其可路由、可全局到达等属性 | <https://www.iana.org/assignments/iana-ipv6-special-registry/> | 2026-10-11 |
| REF-iana-tz | IANA Time Zone Database | IANA 维护的时区数据库，定义 `Asia/Shanghai` 形式的时区名及其历史偏移规则 | <https://www.iana.org/time-zones> | — |
| REF-iso3166 | ISO 3166: Country codes | 国际标准，定义国家与地区代码，含两字母的 alpha-2 代码 | <https://www.iso.org/iso-3166-country-codes.html> | — |
| REF-iso8601 | ISO 8601: Date and time format | 国际标准，定义日期与时间的表示格式 | <https://www.iso.org/iso-8601-date-and-time-format.html> | — |
| REF-itu-t-e164 | ITU-T Recommendation E.164: The international public telecommunication numbering plan | 国际电信联盟建议书，定义国际公共电话号码的编号格式 | <https://www.itu.int/rec/T-REC-E.164> | — |
| REF-json-schema | JSON Schema | 描述与校验 JSON 数据结构的规范 | <https://json-schema.org/specification> | 2020-12 |
| REF-ndjson | NDJSON: Newline Delimited JSON | 以换行符分隔 JSON 文本的流式格式规范 | <https://github.com/ndjson/ndjson-spec> | — |
| REF-nist-sp-800-131a | NIST SP 800-131A: Transitioning the Use of Cryptographic Algorithms and Key Lengths | 美国国家标准与技术研究院的指南，规定密码算法与密钥长度的最低安全强度 | <https://csrc.nist.gov/pubs/sp/800/131/a/r2/final> | Rev. 2 |
| REF-nist-sp-800-63b | NIST SP 800-63B: Digital Identity Guidelines: Authentication and Authenticator Management | 美国国家标准与技术研究院的数字身份指南，规定认证器类别与认证要求 | <https://pages.nist.gov/800-63-4/sp800-63b.html> | SP 800-63B-4 |
| REF-openapi | OpenAPI Specification | OpenAPI Initiative 规范，定义以 JSON 或 YAML 描述 HTTP API 的文档格式 | <https://spec.openapis.org/oas/v3.1.0> | 3.1 |
| REF-openid-connect-core | OpenID Connect Core 1.0 | OpenID 基金会规范，在 OAuth 2.0 之上定义身份层：ID 令牌、标准声明、`nonce` 与 userinfo 端点 | <https://openid.net/specs/openid-connect-core-1_0.html> | 1.0 |
| REF-openid-connect-discovery | OpenID Connect Discovery 1.0 | OpenID 基金会规范，定义 OpenID 提供方元数据文档（`/.well-known/openid-configuration`） | <https://openid.net/specs/openid-connect-discovery-1_0.html> | 1.0 |
| REF-opentelemetry-semconv | OpenTelemetry Semantic Conventions | OpenTelemetry 规范，定义跨度名、属性名、计量单位与桶边界等语义约定 | <https://opentelemetry.io/docs/specs/semconv/> | — |
| REF-opentelemetry-specification | OpenTelemetry Specification | OpenTelemetry 规范，定义 API、SDK 行为与 `OTEL_*` 环境变量 | <https://opentelemetry.io/docs/specs/otel/> | — |
| REF-otlp | OpenTelemetry Protocol (OTLP) Specification | OpenTelemetry 规范，定义遥测数据在 gRPC 与 HTTP 上的编码与传输 | <https://opentelemetry.io/docs/specs/otlp/> | — |
| REF-otpauth-key-uri-format | Key Uri Format | Google Authenticator 项目 wiki 中的格式说明，定义以 `otpauth://` URI 编码 TOTP 与 HOTP 密钥及其参数 | <https://github.com/google/google-authenticator/wiki/Key-Uri-Format> | — |
| REF-owasp-asvs | OWASP Application Security Verification Standard (ASVS) | OWASP 发布的应用安全验证标准，按章节列出可验证的安全要求 | <https://asvs.owasp.org/> | 5.0 |
| REF-owasp-password-storage | OWASP Password Storage Cheat Sheet | OWASP 发布的口令存储指南，给出口令哈希算法的选择与最低参数 | <https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html> | — |
| REF-phc-string-format | PHC string format | Password Hashing Competition 定义的口令哈希字符串编码格式 | <https://github.com/P-H-C/phc-string-format/blob/master/phc-sf-spec.md> | — |
| REF-public-suffix-list | Public Suffix List | Mozilla 维护的公共后缀列表，列出可供公众注册子域名的域名后缀 | <https://publicsuffix.org/> | — |
| REF-rfc3056 | RFC 3056: Connection of IPv6 Domains via IPv4 Clouds | IETF 标准，定义 6to4 机制及其在 `2002::/16` 中内嵌 IPv4 地址的方式 | <https://www.rfc-editor.org/info/rfc3056/> | — |
| REF-rfc3339 | RFC 3339: Date and Time on the Internet: Timestamps | IETF 标准，定义互联网协议中日期与时间戳的文本格式 | <https://www.rfc-editor.org/info/rfc3339/> | — |
| REF-rfc3492 | RFC 3492: Punycode: A Bootstring encoding of Unicode for Internationalized Domain Names in Applications (IDNA) | IETF 标准，定义把国际化域名标签编码为 ASCII 的 Punycode 算法 | <https://www.rfc-editor.org/info/rfc3492/> | — |
| REF-rfc3986 | RFC 3986: Uniform Resource Identifier (URI): Generic Syntax | IETF 互联网标准，定义 URI 的通用语法、字符类别与规范化 | <https://www.rfc-editor.org/info/rfc3986/> | — |
| REF-rfc4380 | RFC 4380: Teredo: Tunneling IPv6 over UDP through Network Address Translations (NATs) | IETF 标准，定义 Teredo 隧道及其 `2001::/32` 地址格式 | <https://www.rfc-editor.org/info/rfc4380/> | — |
| REF-rfc5322 | RFC 5322: Internet Message Format | IETF 标准，定义电子邮件报文格式，含邮箱地址的语法 | <https://www.rfc-editor.org/info/rfc5322/> | — |
| REF-rfc5958 | RFC 5958: Asymmetric Key Packages | IETF 标准，定义非对称私钥的封装结构（PKCS #8 的后继） | <https://www.rfc-editor.org/info/rfc5958/> | — |
| REF-rfc5987 | RFC 5987: Character Set and Language Encoding for Hypertext Transfer Protocol (HTTP) Header Field Parameters | IETF 规范，定义 HTTP 头字段参数中非 ASCII 取值的 `*` 扩展编码；已由 RFC 8187 取代 | <https://www.rfc-editor.org/info/rfc5987/> | — |
| REF-rfc6052 | RFC 6052: IPv6 Addressing of IPv4/IPv6 Translators | IETF 标准，定义 NAT64 熟知前缀 `64:ff9b::/96` 及 IPv4 地址在 IPv6 地址中的内嵌位置 | <https://www.rfc-editor.org/info/rfc6052/> | — |
| REF-rfc6147 | RFC 6147: DNS64: DNS Extensions for Network Address Translation from IPv6 Clients to IPv4 Servers | IETF 标准，定义 DNS64 为纯 IPv6 客户端合成 AAAA 记录的机制 | <https://www.rfc-editor.org/info/rfc6147/> | — |
| REF-rfc6238 | RFC 6238: TOTP: Time-Based One-Time Password Algorithm | IETF 信息性规范，定义基于时间的一次性口令算法 | <https://www.rfc-editor.org/info/rfc6238/> | — |
| REF-rfc6265bis | Cookies: HTTP State Management Mechanism (draft-ietf-httpbis-rfc6265bis) | IETF 草案，修订 Cookie 规范，定义 `SameSite` 属性与 `__Host-`、`__Secure-` 名称前缀 | <https://datatracker.ietf.org/doc/draft-ietf-httpbis-rfc6265bis/> | — |
| REF-rfc6266 | RFC 6266: Use of the Content-Disposition Header Field in the Hypertext Transfer Protocol (HTTP) | IETF 标准，定义 HTTP `Content-Disposition` 头字段及 `filename*` 参数 | <https://www.rfc-editor.org/info/rfc6266/> | — |
| REF-rfc6749 | RFC 6749: The OAuth 2.0 Authorization Framework | IETF 标准，定义 OAuth 2.0 授权框架的角色、授权类型、端点与错误应答 | <https://www.rfc-editor.org/info/rfc6749/> | — |
| REF-rfc6750 | RFC 6750: The OAuth 2.0 Authorization Framework: Bearer Token Usage | IETF 标准，定义承载令牌在 HTTP 请求中的传递方式与 `Bearer` 挑战 | <https://www.rfc-editor.org/info/rfc6750/> | — |
| REF-rfc7009 | RFC 7009: OAuth 2.0 Token Revocation | IETF 标准，定义 OAuth 2.0 令牌撤销端点 | <https://www.rfc-editor.org/info/rfc7009/> | — |
| REF-rfc7468 | RFC 7468: Textual Encodings of PKIX, PKCS, and CMS Structures | IETF 标准，定义证书、私钥等结构的 PEM 文本编码 | <https://www.rfc-editor.org/info/rfc7468/> | — |
| REF-rfc7515 | RFC 7515: JSON Web Signature (JWS) | IETF 标准，定义 JSON Web 签名的结构、JOSE 头与紧凑序列化 | <https://www.rfc-editor.org/info/rfc7515/> | — |
| REF-rfc7516 | RFC 7516: JSON Web Encryption (JWE) | IETF 标准，定义 JSON Web 加密的结构与序列化 | <https://www.rfc-editor.org/info/rfc7516/> | — |
| REF-rfc7517 | RFC 7517: JSON Web Key (JWK) | IETF 标准，定义以 JSON 表示密钥的 JWK 与密钥集 JWK Set | <https://www.rfc-editor.org/info/rfc7517/> | — |
| REF-rfc7518 | RFC 7518: JSON Web Algorithms (JWA) | IETF 标准，定义 JOSE 使用的算法标识，如 `ES256`、`RS256`、`HS256`、`none` | <https://www.rfc-editor.org/info/rfc7518/> | — |
| REF-rfc7519 | RFC 7519: JSON Web Token (JWT) | IETF 标准，定义 JSON Web 令牌的结构与注册声明 | <https://www.rfc-editor.org/info/rfc7519/> | — |
| REF-rfc7636 | RFC 7636: Proof Key for Code Exchange by OAuth Public Clients | IETF 标准，定义 OAuth 授权码交换的 PKCE 机制及 `S256` 变换 | <https://www.rfc-editor.org/info/rfc7636/> | — |
| REF-rfc7638 | RFC 7638: JSON Web Key (JWK) Thumbprint | IETF 标准，定义 JWK 指纹的计算方法 | <https://www.rfc-editor.org/info/rfc7638/> | — |
| REF-rfc8018 | RFC 8018: PKCS #5: Password-Based Cryptography Specification Version 2.1 | IETF 信息性规范，定义基于口令的密钥派生函数 PBKDF2 | <https://www.rfc-editor.org/info/rfc8018/> | — |
| REF-rfc8089 | RFC 8089: The "file" URI Scheme | IETF 标准，定义 `file` URI 方案 | <https://www.rfc-editor.org/info/rfc8089/> | — |
| REF-rfc8297 | RFC 8297: An HTTP Status Code for Indicating Hints | IETF 规范，定义信息性状态码 103 Early Hints | <https://www.rfc-editor.org/info/rfc8297/> | — |
| REF-rfc8555 | RFC 8555: Automatic Certificate Management Environment (ACME) | IETF 标准，定义自动签发证书的 ACME 协议及 HTTP-01 等挑战 | <https://www.rfc-editor.org/info/rfc8555/> | — |
| REF-rfc8615 | RFC 8615: Well-Known Uniform Resource Identifiers (URIs) | IETF 标准，定义 `/.well-known/` 路径前缀及其登记规则 | <https://www.rfc-editor.org/info/rfc8615/> | — |
| REF-rfc8693 | RFC 8693: OAuth 2.0 Token Exchange | IETF 标准，定义 OAuth 2.0 令牌交换及 `act` 等声明 | <https://www.rfc-editor.org/info/rfc8693/> | — |
| REF-rfc8705 | RFC 8705: OAuth 2.0 Mutual-TLS Client Authentication and Certificate-Bound Access Tokens | IETF 标准，定义以双向 TLS 认证 OAuth 客户端并把访问令牌绑定到证书 | <https://www.rfc-editor.org/info/rfc8705/> | — |
| REF-rfc9068 | RFC 9068: JSON Web Token (JWT) Profile for OAuth 2.0 Access Tokens | IETF 标准，定义以 JWT 承载 OAuth 2.0 访问令牌的格式与 `at+jwt` 类型 | <https://www.rfc-editor.org/info/rfc9068/> | — |
| REF-rfc9106 | RFC 9106: Argon2 Memory-Hard Function for Password Hashing and Proof-of-Work Applications | IETF 信息性规范，定义 Argon2 系列内存困难函数（Argon2d、Argon2i、Argon2id） | <https://www.rfc-editor.org/info/rfc9106/> | — |
| REF-rfc9110 | RFC 9110: HTTP Semantics | IETF 互联网标准，定义 HTTP 的语义、方法、状态码、头字段与认证框架 | <https://www.rfc-editor.org/info/rfc9110/> | — |
| REF-rfc9207 | RFC 9207: OAuth 2.0 Authorization Server Issuer Identification | IETF 标准，定义授权应答中的 `iss` 参数 | <https://www.rfc-editor.org/info/rfc9207/> | — |
| REF-rfc9449 | RFC 9449: OAuth 2.0 Demonstrating Proof of Possession (DPoP) | IETF 标准，定义在应用层证明持有密钥、约束令牌发送方的 DPoP 机制 | <https://www.rfc-editor.org/info/rfc9449/> | — |
| REF-rfc9457 | RFC 9457: Problem Details for HTTP APIs | IETF 标准，定义 HTTP API 错误应答的问题对象字段与 `application/problem+json` 媒体类型 | <https://www.rfc-editor.org/info/rfc9457/> | — |
| REF-rfc9470 | RFC 9470: OAuth 2.0 Step Up Authentication Challenge Protocol | IETF 标准，定义资源服务器要求更高认证强度的挑战协议 | <https://www.rfc-editor.org/info/rfc9470/> | — |
| REF-rfc9562 | RFC 9562: Universally Unique IDentifiers (UUIDs) | IETF 标准，定义 UUID 的格式与各版本，含按时间排序的第 7 版 | <https://www.rfc-editor.org/info/rfc9562/> | — |
| REF-rfc9602 | RFC 9602: Segment Routing over IPv6 (SRv6) Segment Identifiers in the IPv6 Addressing Architecture | IETF 信息性规范，为 SRv6 段标识分配 `5f00::/16` 地址块 | <https://www.rfc-editor.org/info/rfc9602/> | — |
| REF-rfc9637 | RFC 9637: Expanding the IPv6 Documentation Space | IETF 信息性规范，新增 IPv6 文档用地址块 `3fff::/20` | <https://www.rfc-editor.org/info/rfc9637/> | — |
| REF-rfc9700 | RFC 9700: Best Current Practice for OAuth 2.0 Security | IETF 当前最佳实践，汇总 OAuth 2.0 的安全威胁与对策 | <https://www.rfc-editor.org/info/rfc9700/> | — |
| REF-rfc9780 | RFC 9780: Bidirectional Forwarding Detection (BFD) for Multipoint Networks over Point-to-Multipoint MPLS Label Switched Paths (LSPs) | IETF 标准，定义点到多点 MPLS 上的 BFD，并登记 IPv6 特殊用途地址块 `100:0:0:1::/64` | <https://www.rfc-editor.org/info/rfc9780/> | — |
| REF-saml2 | Security Assertion Markup Language (SAML) V2.0 | OASIS 标准，定义 SAML 断言、协议、绑定与元数据 | <https://docs.oasis-open.org/security/saml/v2.0/> | 2.0 |
| REF-trai-tcccpr | TRAI Telecom Commercial Communications Customer Preference Regulations, 2018 | 印度电信管理局的商业通信法规，要求发送方实体与短信模板在 DLT 平台登记 | <https://www.trai.gov.in/tcccpr> | 2018 |
| REF-unicode-cldr | Unicode Common Locale Data Repository (CLDR) | Unicode 联盟维护的区域数据库，含数字格式与复数类别规则 | <https://cldr.unicode.org/> | — |
| REF-unicode-uax15 | Unicode Standard Annex #15: Unicode Normalization Forms | Unicode 标准附件，定义 NFC、NFD、NFKC、NFKD 规范化形式 | <https://www.unicode.org/reports/tr15/> | — |
| REF-unicode-uax44 | Unicode Standard Annex #44: Unicode Character Database | Unicode 标准附件，定义 Unicode 字符数据库及一般类别（如 Cc、Cf、Zl、Zp）等字符属性 | <https://www.unicode.org/reports/tr44/> | — |
| REF-us-a2p-10dlc | The Campaign Registry: A2P 10DLC | 美国运营商要求的应用对个人 10 位长号码短信的品牌与活动登记体系 | <https://www.campaignregistry.com/> | — |
| REF-w3c-baggage | Propagation format for distributed context: Baggage | W3C 规范，定义跨进程传播应用自定义键值对的 `baggage` 头 | <https://www.w3.org/TR/baggage/> | — |
| REF-w3c-trace-context | Trace Context | W3C 推荐标准，定义跨进程传播追踪上下文的 `traceparent` 与 `tracestate` 头 | <https://www.w3.org/TR/trace-context/> | — |
| REF-wcag21 | Web Content Accessibility Guidelines (WCAG) 2.1 | W3C 推荐标准，定义网页内容无障碍的成功准则，含对比度要求 | <https://www.w3.org/TR/WCAG21/> | 2.1 |
| REF-webauthn | Web Authentication: An API for accessing Public Key Credentials | W3C 推荐标准，定义浏览器以公钥凭据登记与断言用户的 WebAuthn API 及依赖方处理 | <https://www.w3.org/TR/webauthn-2/> | Level 2 |
| REF-whatwg-fetch | Fetch Standard | WHATWG 标准，定义浏览器的请求获取模型，含 CORS 协议与预检请求 | <https://fetch.spec.whatwg.org/> | — |
| REF-xmldsig-core | XML Signature Syntax and Processing Version 1.1 | W3C 推荐标准，定义 XML 数字签名的语法与验签处理 | <https://www.w3.org/TR/xmldsig-core1/> | 1.1 |

## 开源组件

| 编号 | 名称 | 是什么 | 地址 | 版本 |
|---|---|---|---|---|
| REF-air | Air | Go 应用的实时重载工具，在源码变化时重新构建并重启进程 | <https://github.com/air-verse/air> | — |
| REF-alexedwards-argon2id | alexedwards/argon2id | Go 语言的 Argon2id 口令哈希与 PHC 编码校验库 | <https://github.com/alexedwards/argon2id> | v1 |
| REF-alibabacloud-dysmsapi-go | alibabacloud-go/dysmsapi-20170525 | 阿里云短信服务 API（2017-05-25 版）的 Go SDK | <https://github.com/alibabacloud-go/dysmsapi-20170525> | v5 |
| REF-aws-sdk-go-v2 | AWS SDK for Go v2 | Amazon Web Services 官方的 Go SDK | <https://github.com/aws/aws-sdk-go-v2> | — |
| REF-axe-core | axe-core | Deque Systems 维护的开源网页无障碍检查引擎，按规则检测页面的无障碍违规 | <https://github.com/dequelabs/axe-core> | v4 |
| REF-axios | Axios | JavaScript 的 HTTP 客户端库 | <https://axios.rest/> | — |
| REF-base64captcha | base64Captcha | Go 语言的自托管人机验证码库，生成数字图片与音频人机验证码 | <https://github.com/mojocn/base64Captcha> | v1.3.8 |
| REF-casbin | Apache Casbin | 访问控制库，以模型文本与策略数据求值 ACL、RBAC、ABAC 等授权模型 | <https://github.com/apache/casbin> | v3 |
| REF-casbin-gorm-adapter | Casbin GORM Adapter | Casbin 的策略存储适配器，经 GORM 把策略存进关系数据库 | <https://github.com/apache/casbin-gorm-adapter> | — |
| REF-casbin-govaluate | casbin/govaluate | Go 语言的表达式求值库 | <https://github.com/apache/casbin-govaluate> | — |
| REF-chanxuehong-wechat | chanxuehong/wechat | 微信公众平台与企业微信接口的 Go SDK | <https://github.com/chanxuehong/wechat> | — |
| REF-chi | chi | Go 语言基于 `net/http` 的路由库，含一组中间件 | <https://github.com/go-chi/chi> | — |
| REF-cobra | Cobra | Go 语言的命令行应用框架，提供子命令树 | <https://github.com/spf13/cobra> | v1 |
| REF-connect-go | connect-go | Go 语言的 Connect RPC 实现，同一处理器兼容 gRPC 与 HTTP | <https://github.com/connectrpc/connect-go> | — |
| REF-crewjam-saml | crewjam/saml | Go 语言的 SAML 2.0 库，覆盖服务提供方与身份提供方 | <https://github.com/crewjam/saml> | — |
| REF-dex | Dex | 开源的 OpenID Connect 身份服务，可联合上游身份提供方 | <https://dexidp.io/> | — |
| REF-doublestar | doublestar | Go 语言的路径通配匹配库，支持 `**` | <https://github.com/bmatcuk/doublestar> | v4 |
| REF-echo | Echo | Go 语言的 HTTP Web 框架 | <https://github.com/labstack/echo> | — |
| REF-emotion | Emotion | JavaScript 的 CSS-in-JS 样式库 | <https://emotion.sh/> | v11 |
| REF-eslint | ESLint | JavaScript 与 TypeScript 的静态检查工具，支持自定义规则 | <https://eslint.org/> | — |
| REF-fiber | Fiber | Go 语言建立在 fasthttp 之上的 HTTP Web 框架 | <https://github.com/gofiber/fiber> | — |
| REF-fosite | fosite | Ory 的 Go 语言 OAuth 2.0 与 OpenID Connect 服务端框架 | <https://github.com/ory/fosite> | — |
| REF-gcp-compute-metadata | cloud.google.com/go/compute/metadata | Google Cloud Go 客户端库中从计算实例元数据服务读取的模块 | <https://pkg.go.dev/cloud.google.com/go/compute/metadata> | — |
| REF-genproto | go-genproto | Google API 的 protobuf 定义生成的 Go 包，已拆分为 `googleapis/*` 等子模块 | <https://github.com/googleapis/go-genproto> | — |
| REF-gin | Gin | Go 语言的 HTTP Web 框架，提供路由、中间件、请求绑定与渲染 | <https://github.com/gin-gonic/gin> | v1 |
| REF-gin-contrib-cors | gin-contrib/cors | Gin 的跨源资源共享（CORS）中间件 | <https://github.com/gin-contrib/cors> | v1 |
| REF-gin-contrib-sessions | gin-contrib/sessions | Gin 的会话中间件，建立在 gorilla/sessions 之上 | <https://github.com/gin-contrib/sessions> | — |
| REF-glebarez-go-sqlite | glebarez/go-sqlite | 不依赖 cgo 的 SQLite `database/sql` 驱动，建立在 modernc.org/sqlite 之上 | <https://github.com/glebarez/go-sqlite> | v1 |
| REF-glebarez-sqlite | glebarez/sqlite | 不依赖 cgo 的 GORM SQLite 方言驱动 | <https://github.com/glebarez/sqlite> | v1 |
| REF-gmsm | tjfoc/gmsm | 国密 SM2、SM3、SM4 算法的 Go 实现 | <https://github.com/tjfoc/gmsm> | — |
| REF-go-crypt | go-crypt/crypt | Go 语言的多算法口令哈希库，解码、校验与写出多种 crypt 格式 | <https://github.com/go-crypt/crypt> | — |
| REF-go-github | go-github | GitHub REST API 的 Go 客户端库 | <https://github.com/google/go-github> | — |
| REF-go-jose | go-jose | Go 语言的 JOSE 实现，覆盖 JWS、JWE、JWK 与 JWT | <https://github.com/go-jose/go-jose> | v4 |
| REF-go-mail | go-mail | Go 语言的邮件组装与 SMTP 发送库 | <https://github.com/wneessen/go-mail> | v0 |
| REF-go-oauth2 | go-oauth2/oauth2 | Go 语言的 OAuth 2.0 服务端库 | <https://github.com/go-oauth2/oauth2> | — |
| REF-go-oidc | go-oidc | Go 语言的 OpenID Connect 依赖方库，含提供方发现与 ID 令牌校验 | <https://github.com/coreos/go-oidc> | v3 |
| REF-go-playground-validator | go-playground/validator | Go 语言的结构体与字段校验库 | <https://github.com/go-playground/validator> | v10 |
| REF-go-redis | go-redis | Redis 的 Go 客户端 | <https://github.com/redis/go-redis> | v9 |
| REF-go-webauthn | go-webauthn/webauthn | Go 语言的 WebAuthn 依赖方库，处理凭据登记与断言仪式 | <https://github.com/go-webauthn/webauthn> | v0 |
| REF-golang-jwt | golang-jwt/jwt | Go 语言的 JWT 解析与签发库 | <https://github.com/golang-jwt/jwt> | v5 |
| REF-golang-x-crypto | golang.org/x/crypto | Go 项目在标准库之外维护的密码学库，含 Argon2 | <https://pkg.go.dev/golang.org/x/crypto> | — |
| REF-golang-x-oauth2 | golang.org/x/oauth2 | Go 项目在标准库之外维护的 OAuth 2.0 客户端库，含 PKCE 辅助函数与 Google 子包 | <https://pkg.go.dev/golang.org/x/oauth2> | — |
| REF-golang-x-text | golang.org/x/text | Go 项目在标准库之外维护的文本处理库，含语言标签匹配、消息目录与 Unicode 规范化 | <https://pkg.go.dev/golang.org/x/text> | — |
| REF-golang-x-time | golang.org/x/time | Go 项目在标准库之外维护的时间相关库，含令牌桶限流器 `rate` | <https://pkg.go.dev/golang.org/x/time> | — |
| REF-google-uuid | google/uuid | Go 语言的 UUID 生成与解析库 | <https://github.com/google/uuid> | v1 |
| REF-gorilla-sessions | gorilla/sessions | Go 语言的 cookie 与文件系统会话库，含签名 cookie 存储 `CookieStore` | <https://github.com/gorilla/sessions> | — |
| REF-gorm | GORM | Go 语言的对象关系映射库，提供模型、关联、查询构造、迁移与回调 | <https://gorm.io/> | v1 |
| REF-gosaml2 | gosaml2 | Go 语言的 SAML 2.0 服务提供方库 | <https://github.com/russellhaering/gosaml2> | v0 |
| REF-goth | Goth | Go 语言的多提供方 OAuth 登录库 | <https://github.com/markbates/goth> | — |
| REF-goxmldsig | goxmldsig | Go 语言的 XML 数字签名库 | <https://github.com/russellhaering/goxmldsig> | v1 |
| REF-grpc | gRPC | 基于 HTTP/2 与 Protocol Buffers 的远程过程调用框架，含 Go 实现 `google.golang.org/grpc` | <https://grpc.io/> | — |
| REF-hashicorp-cap | cap | HashiCorp 的 Go 认证库，提供 OIDC、JWT 与 LDAP 的认证流程 | <https://github.com/hashicorp/cap> | — |
| REF-i18next | i18next | JavaScript 的国际化框架，提供翻译、命名空间与复数处理 | <https://www.i18next.com/> | v26 |
| REF-icu | ICU（International Components for Unicode） | Unicode 联盟维护的国际化库与区域数据，为各 JavaScript 引擎的 `Intl` 提供格式化与显示名称 | <https://icu.unicode.org/> | — |
| REF-jsdom | jsdom | 以 JavaScript 实现的 DOM 与 HTML 标准，供在 Node.js 中无浏览器运行 | <https://github.com/jsdom/jsdom> | — |
| REF-keycloak | Keycloak | 开源的身份与访问管理服务，支持 OIDC 与 SAML | <https://www.keycloak.org/> | — |
| REF-kong-gateway | Kong Gateway | 开源的 API 网关，以按阶段运行、带静态优先级的插件处理请求 | <https://github.com/Kong/kong> | — |
| REF-kubernetes | Kubernetes | 开源的容器编排系统，含 ConfigMap 与 Secret 的配置挂载 | <https://kubernetes.io/> | — |
| REF-libphonenumber-js | libphonenumber-js | Google libphonenumber 的 JavaScript 移植，提供电话号码解析、格式化与校验 | <https://github.com/catamphetamine/libphonenumber-js> | v1 |
| REF-mapstructure | mapstructure | Go 语言把通用映射解码进结构体的库 | <https://github.com/go-viper/mapstructure> | v2 |
| REF-micronaut | Micronaut | JVM 上的应用框架，以命名的过滤器阶段排序 HTTP 过滤器 | <https://micronaut.io/> | — |
| REF-minio-go | MinIO Go Client SDK | 兼容 Amazon S3 的对象存储 Go 客户端，含预签名地址 | <https://github.com/minio/minio-go> | v7 |
| REF-mise | mise | 开发工具链版本管理与环境加载工具 | <https://mise.jdx.dev/> | — |
| REF-mui | Material UI | 实现 Material Design 的 React 组件库，含主题与本地化 | <https://mui.com/material-ui/> | v9 |
| REF-nats | NATS | 开源消息系统，含提供持久流、键值存储与拉取消费的 JetStream | <https://nats.io/> | — |
| REF-opentelemetry-go | OpenTelemetry-Go | OpenTelemetry 的 Go 语言 API、SDK 与导出器 | <https://github.com/open-telemetry/opentelemetry-go> | v1 |
| REF-opentelemetry-go-contrib | OpenTelemetry-Go Contrib | OpenTelemetry Go 的第三方库采集集合，含 Gin 中间件 `otelgin` | <https://github.com/open-telemetry/opentelemetry-go-contrib> | — |
| REF-orval | Orval | 从 OpenAPI 文档生成 TypeScript 客户端与查询钩子的代码生成器 | <https://orval.dev/> | v8 |
| REF-ory-kratos | Ory Kratos | 开源的身份与用户管理服务，提供登录、注册、会话与多因素 | <https://github.com/ory/kratos> | — |
| REF-passlib | Passlib | Python 的口令哈希库，支持多种算法与 PHC 风格编码 | <https://passlib.readthedocs.io/> | — |
| REF-pflag | pflag | Go 语言的 POSIX 风格命令行参数解析库 | <https://github.com/spf13/pflag> | v1 |
| REF-pgx | pgx | PostgreSQL 的 Go 驱动与工具集，含底层连接包 `pgconn` | <https://github.com/jackc/pgx> | v5 |
| REF-powerwechat | PowerWeChat | 微信开放接口的 Go SDK，覆盖公众号、小程序、支付与企业微信 | <https://github.com/ArtisanCloud/PowerWeChat> | — |
| REF-pquerna-otp | pquerna/otp | Go 语言的 TOTP 与 HOTP 一次性口令库 | <https://github.com/pquerna/otp> | v1 |
| REF-qrcode-generator | qrcode-generator | 以 JavaScript 生成 QR 码模块矩阵的库 | <https://github.com/kazuhikoarase/qrcode-generator> | v2 |
| REF-protobuf | Protocol Buffers | Google 的语言中立结构化数据序列化格式及其编译工具 | <https://protobuf.dev/> | — |
| REF-react-hook-form | React Hook Form | React 的表单状态与校验库 | <https://react-hook-form.com/> | v7 |
| REF-react-i18next | react-i18next | i18next 的 React 绑定 | <https://react.i18next.com/> | v17 |
| REF-react-router | React Router | React 的客户端路由库 | <https://reactrouter.com/> | v7 |
| REF-rustcrypto-password-hashes | RustCrypto password-hashes | RustCrypto 项目的 Rust 口令哈希实现集合，含 PBKDF2 的 PHC 写法 | <https://github.com/RustCrypto/password-hashes> | — |
| REF-scs | SCS: HTTP Session Management for Go | Go 语言的 HTTP 会话管理库，提供会话令牌、cookie 写出与可换的存储 | <https://github.com/alexedwards/scs> | v2 |
| REF-silenceper-wechat | silenceper/wechat | 微信开放接口的 Go SDK，覆盖公众号、小程序等 | <https://github.com/silenceper/wechat> | v2 |
| REF-spring-boot | Spring Boot | Java 的应用框架，以约定与自动配置组装 Spring 应用 | <https://spring.io/projects/spring-boot> | — |
| REF-spring-security | Spring Security | Java 的认证与授权框架，以有序的过滤器链处理请求 | <https://spring.io/projects/spring-security> | — |
| REF-swaggest-openapi-go | swaggest/openapi-go | Go 语言从类型反射生成 OpenAPI 文档的库 | <https://github.com/swaggest/openapi-go> | v0 |
| REF-tanstack-query | TanStack Query | 前端的服务端状态获取与缓存库，含 React 绑定 `@tanstack/react-query` | <https://tanstack.com/query/latest> | v5 |
| REF-tencentcloud-sdk-go | tencentcloud-sdk-go | 腾讯云官方的 Go SDK | <https://github.com/TencentCloud/tencentcloud-sdk-go> | — |
| REF-testcontainers-go | Testcontainers for Go | 在测试中以代码启动与销毁容器的 Go 库 | <https://github.com/testcontainers/testcontainers-go> | — |
| REF-testify | testify | Go 语言的测试断言与模拟库 | <https://github.com/stretchr/testify> | — |
| REF-twilio-go | twilio-go | Twilio 官方的 Go SDK | <https://github.com/twilio/twilio-go> | v1 |
| REF-viper | Viper | Go 语言的配置库，读取多种格式与来源并按层叠合并 | <https://github.com/spf13/viper> | v1 |
| REF-vite | Vite | 前端构建工具与开发服务器 | <https://vite.dev/> | v8 |
| REF-xml-roundtrip-validator | xml-roundtrip-validator | Go 语言的 XML 往返一致性校验库，检测 `encoding/xml` 解析与重编码不一致的输入 | <https://github.com/mattermost/xml-roundtrip-validator> | — |
| REF-zitadel-oidc | zitadel/oidc | Go 语言的 OpenID Connect 库，含依赖方与提供方实现 | <https://github.com/zitadel/oidc> | v3 |
| REF-zod | Zod | TypeScript 优先的数据模式声明与校验库 | <https://zod.dev/> | v4 |

## 外部服务

| 编号 | 名称 | 是什么 | 地址 | 版本 |
|---|---|---|---|---|
| REF-aliyun-directmail | 阿里云邮件推送（DirectMail） | 阿里云的邮件发送服务文档，提供 OpenAPI 与 SMTP 两种接入 | <https://help.aliyun.com/zh/direct-mail/> | — |
| REF-aliyun-pnvs | 阿里云号码认证服务 | 阿里云的号码认证服务文档，含托管的短信验证码生成、发送与校验接口 `dypnsapi` | <https://help.aliyun.com/zh/pnvs/> | — |
| REF-aliyun-sms | 阿里云短信服务 | 阿里云的短信发送服务文档，含签名与模板审核 | <https://help.aliyun.com/zh/sms/> | — |
| REF-amazon-s3 | Amazon S3 | AWS 对象存储服务及其 API，含预签名 URL | <https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html> | — |
| REF-aws-end-user-messaging-sms | AWS End User Messaging SMS (Amazon Pinpoint SMS and Voice, version 2) API | AWS 短信与语音发送服务的 API 文档，含 `SendTextMessage` | <https://docs.aws.amazon.com/pinpoint/latest/apireference_smsvoicev2/Welcome.html> | v2 |
| REF-cloudflare-turnstile | Cloudflare Turnstile | Cloudflare 的人机验证服务，含服务端校验接口 siteverify | <https://developers.cloudflare.com/turnstile/> | — |
| REF-fly-io | Fly.io | 应用部署平台，含 Machines、卷、机密、Fly Proxy 与 `Fly-Client-IP` 头 | <https://docs.fly.io/> | — |
| REF-github-oauth-apps | GitHub: Authorizing OAuth apps | GitHub OAuth 应用的授权码流程文档 | <https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps> | — |
| REF-github-rest-users | GitHub REST API: Users | GitHub REST API 的用户端点文档，含 `/user` 与 `/user/emails` | <https://docs.github.com/en/rest/users> | — |
| REF-gmail-dots | Gmail: Dots don't matter in Gmail addresses | Gmail 帮助文档，说明地址局部部分的点号不影响收件箱 | <https://support.google.com/mail/answer/7436150> | — |
| REF-google-openid-connect | Google Identity: OpenID Connect | Google 账号作为 OpenID Connect 提供方的接入文档，含 ID 令牌声明与 `hd` 托管域参数 | <https://developers.google.com/identity/openid-connect/openid-connect> | — |
| REF-google-workspace-change-user-email | Overview: Changing a Directory user's name or email address | Google Workspace 管理员帮助文档，说明管理员修改目录用户的姓名或主邮箱地址 | <https://knowledge.workspace.google.com/admin/users/overview-changing-a-directory-users-name-or-email-address> | — |
| REF-pkg-go-dev | pkg.go.dev | Go 项目的包与模块发现站点，列出各模块的文档、版本与导入它的包 | <https://pkg.go.dev/> | 2026-10-10 |
| REF-tencent-cloud-ses | 腾讯云邮件推送（SES） | 腾讯云的邮件发送服务文档，提供 OpenAPI 与 SMTP 两种接入 | <https://cloud.tencent.com/document/product/1288> | — |
| REF-tencent-cloud-sms | 腾讯云短信 | 腾讯云的短信发送服务文档，含签名与模板审核 | <https://cloud.tencent.com/document/product/382> | — |
| REF-twilio-content-api | Twilio Content API | Twilio 消息内容模板接口的文档，以内容资源登记正文并返回 SID | <https://www.twilio.com/docs/content> | — |
| REF-twilio-messaging | Twilio Programmable Messaging: Message resource | Twilio 短信发送接口（Messages API）的文档 | <https://www.twilio.com/docs/messaging/api/message-resource> | — |
| REF-wechat-oa-web-auth | 微信公众平台：微信网页授权 | 微信公众号网页授权文档，授权范围 `snsapi_base` 与 `snsapi_userinfo`，含 UnionID 机制 | <https://developers.weixin.qq.com/doc/service/guide/h5/auth.html> | — |
| REF-wechat-website-login | 微信开放平台：网站应用微信登录开发指南 | 微信开放平台的网站应用扫码登录文档，授权范围 `snsapi_login` | <https://developers.weixin.qq.com/doc/oplatform/Website_App/WeChat_Login/Wechat_Login.html> | — |

## 其他

| 编号 | 名称 | 是什么 | 地址 | 版本 |
|---|---|---|---|---|
| REF-cve-2022-41912 | CVE-2022-41912 | crewjam/saml 的漏洞记录：响应含多个 `Assertion` 元素时的签名绕过 | <https://www.cve.org/CVERecord?id=CVE-2022-41912> | — |
| REF-cve-2026-33487 | CVE-2026-33487 | goxmldsig 的漏洞记录：`validateSignature` 中循环变量捕获导致的签名绕过 | <https://www.cve.org/CVERecord?id=CVE-2026-33487> | — |
| REF-cve-2026-34986 | CVE-2026-34986 | go-jose 的漏洞记录：JWE 解密触发 panic | <https://www.cve.org/CVERecord?id=CVE-2026-34986> | — |
| REF-ghsa-6mp9-8xrw-74r4 | GHSA-6mp9-8xrw-74r4 | go-jose 的安全公告：JSON 解析与 CBC-HMAC 处理中的 panic | <https://github.com/go-jose/go-jose/security/advisories/GHSA-6mp9-8xrw-74r4> | — |
| REF-ghsa-pcgw-qcv5-h8ch | GHSA-pcgw-qcv5-h8ch | gosaml2 的安全公告：接受未签名的 SAML 登出请求 `LogoutRequest` | <https://github.com/advisories/GHSA-pcgw-qcv5-h8ch> | — |
| REF-pre-hijacked-accounts | Pre-hijacked accounts: An Empirical Study of Security Failures in User Account Creation on the Web | Sudhodanan 与 Paverd 发表于 USENIX Security 的论文，研究攻击者在受害者创建账户之前预先占用账户的预劫持攻击 | <https://www.usenix.org/conference/usenixsecurity22/presentation/sudhodanan> | 2022 |
