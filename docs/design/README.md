# 设计文档

speed 是一个配置驱动的模块化应用程序框架，以 Go 库的形式交付。它为 Go 应用程序提供模块化的骨架与一批通用模块。

本目录只记录设计结论。决策的理由、考虑过的选项与付出的代价记录在[架构决策记录](../adr/README.md)中；术语见[术语表](../glossary.md)。

## 索引

| 文档 | 内容 |
|---|---|
| [架构](architecture.md) | 系统形状、模块依赖方向、跨切面设计、架构不变量 |
| [core](modules/design-core.md) | 模块化基础设施：描述符、注册、查询、生命周期驱动 |
| [config](modules/design-config.md) | 启动必须项的加载：输入项收集、传输与格式扩展点、各来源的覆盖 |
| [log](modules/design-log.md) | 结构化日志：处理链、内置的格式与目的地、上下文传递、脱敏、文件滚动与清理 |
| [db](modules/design-db.md) | 数据库访问：连接的建立与关闭、迁移的收齐与应用、插件扩展点 |
| [http](modules/design-http.md) | HTTP 入口：监听端、路由与中间件的登记、中间件排序、请求上下文、panic 兜底、两拍停机 |
| [authn](modules/design-authn.md) | 认证：调用方身份的认定、免认证路由的裁定、身份的上下文承载、校验后端的归属与接缝 |
| [tenant](modules/design-tenant.md) | 租户：请求面的租户解析、名册与生命周期、共享表加租户列的语句过滤 |
| [kv](modules/design-kv.md) | 键值原语：不透明字节的存取与过期、原子自增与比较并交换、按最弱后端设计的接口面 |
| [event](modules/design-event.md) | 事件总线：按类型发布与订阅、载荷的结构化读取、投递承诺的下界 |
| [audit](modules/design-audit.md) | 审计：语句、事件、请求与记录接缝的捕获通路、审计表、明细的脱敏 |
| [settings](modules/design-settings.md) | 设置：模块声明的运行期参数、三级回落、以快照读取、变更事件的发布与订阅 |
| [ratelimit](modules/design-ratelimit.md) | 限流：租户内按身份串的请求速率限制、以设置读取额度与窗口、经键值原语按窗口计数 |
| [observability](modules/design-observability.md) | 运行期遥测：上游 SDK 的 provider、请求路径的采集、出站注入、事件订阅、导出后端 |
