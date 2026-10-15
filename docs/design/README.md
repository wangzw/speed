# 设计文档

speed 是一个配置驱动的模块化应用程序框架，以 Go 库的形式交付。它为 Go 应用程序提供模块化的骨架与一批通用模块。

本目录只记录设计结论。决策的理由、考虑过的选项与付出的代价记录在[架构决策记录](../adr/README.md)中；术语见[术语表](../glossary.md)。

## 索引

| 文档 | 内容 |
|---|---|
| [架构](architecture.md) | 系统形状、模块依赖方向、跨切面设计、架构不变量 |
| [core](modules/design-core.md) | 模块化基础设施：描述符、注册、预置、查询、生命周期驱动 |
| [config](modules/design-config.md) | 启动必须项的加载：输入项收集、传输与格式扩展点、各来源的覆盖 |
| [log](modules/design-log.md) | 结构化日志：处理链、内置的格式与目的地、上下文传递、脱敏、文件滚动与清理 |
| [db](modules/design-db.md) | 数据库访问：连接的建立与关闭、迁移的收齐与应用、插件扩展点 |
| [http](modules/http/README.md) | HTTP 入口：监听端、路由与中间件的登记、中间件排序、请求上下文、panic 兜底、停机排空；跨源层为其子模块 |
| [authn](modules/authn/README.md) | 认证：身份系统的职责边界、模块清单与依赖、身份与判决的形状、状态放置、链上次序；网关、账户、登录、会话、令牌、多因素、短信、企业 SSO、冒充、管理面、配置、事件、失败语义、威胁模型为其子模块 |
| [tenant](modules/tenant/README.md) | 租户：请求面的租户解析、名册与生命周期、共享表加租户列的语句过滤；租户准入层为其子模块 |
| [authz](modules/design-authz.md) | 授权：职责边界、模块清单与依赖、判定引擎与策略存储、中间件与路由声明、管理面、承接的权限点 |
| [kv](modules/design-kv.md) | 键值原语：不透明字节的存取与过期、原子自增与比较并交换、按最弱后端设计的接口面 |
| [sms](modules/design-sms.md) | 短信发送与模板：发送接缝、模板注册表、运行期管理面与厂商适配 |
| [event](modules/event/README.md) | 事件总线：按类型发布与订阅、载荷的结构化读取、投递承诺的下界；事务发件箱为其子模块 |
| [audit](modules/design-audit.md) | 审计：语句、事件、请求与记录接缝的捕获通路、审计表、明细的脱敏 |
| [settings](modules/design-settings.md) | 设置：模块声明的运行期参数、三级回落、以快照读取、变更事件的发布与订阅 |
| [ratelimit](modules/design-ratelimit.md) | 限流：租户内按责任键的请求速率限制、以设置读取额度与窗口、经键值原语按窗口计数 |
| [observability](modules/design-observability.md) | 运行期遥测：上游 SDK 的 provider、请求路径的采集、出站注入、导出后端 |
| [i18n](modules/design-i18n.md) | 面向用户文本的渲染机制：消息目录的收集与装载、语言与时区的解析链、Accept-Language 协商、账户偏好的存取 |
