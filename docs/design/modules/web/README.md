# 前端

前端功能的文档索引。主文档给出包的分层与判据、契约边界、装配与内容分离、装配入口与稳定性承诺；设计令牌、i18n、组件族、传输运行时、生成的契约面、路由、会话与各能力 UI 包各一份子模块文档。

## 索引

| 文档 | 内容 |
|---|---|
| [web](design-web.md) | 前端：包的分层与判据、契约边界、装配与内容分离、装配入口与稳定性承诺、错误与失败语义、扩展点 |
| [layout](design-web-layout.md) | 布局与响应式：外壳的区域划分与导航层次、断点标度、外壳级与组件级响应手段的分工、页型骨架、窄视口降级 |
| [tokens](design-web-tokens.md) | 设计令牌：默认 token 树、覆写层的深度合并、MUI 对齐口径 |
| [i18n](design-web-i18n.md) | 平台 i18n 层：实例创建、语言协商与持久化、命名空间注册与校验、缺键纪律、错误码文本约定 |
| [ui-kit](design-web-ui-kit.md) | 组件族与主题适配：主题工厂、受控组件、跨表面状态原语 |
| [layout-kit](design-web-layout-kit.md) | 与认证无关的应用外壳：响应式框架与按注入状态放行的门禁 |
| [api-client](design-web-api-client.md) | 契约无关的手写传输运行时：请求函数、`ApiError` 归一、令牌存储、刷新、重试、上报、配置钩子 |
| [api-sdk](design-web-api-sdk.md) | 生成的契约面：orval 产物、运行时接缝与 mutator、生成配置 |
| [route-kit](design-web-route-kit.md) | 路由与导航独立层：声明式注册面、路径匹配、链接构造、导航选中态与未命中回退 |
| [auth-core](design-web-auth-core.md) | 浏览器会话状态机：登录流程、登出、快照、React hook、租户上下文与租户作用域查询键 |
| [auth-ui](design-web-auth-ui.md) | 登录组件族：口令、短信、社交渠道与会话面 |
| [account-ui](design-web-account-ui.md) | 账户管理组件族：自助写（显示名、口令、手机、登录标识）、偏好、会话、社交绑定、两步验证 |
| [tenancy-ui](design-web-tenancy-ui.md) | 租户切换控件 |
| [product-shell](design-web-product-shell.md) | 租户侧装配壳与装配入口 `bootstrapSpeedApp` |
| [console-ui](design-web-console-ui.md) | 平台侧控制台面组件族：租户、设置与审计三个管理面 |
| [objectstore-ui](design-web-objectstore-ui.md) | 对象存储能力面：对象页（列表、上传、下载、删除、详情） |
| [notification-ui](design-web-notification-ui.md) | 站内通知面：未读铃铛、收件箱与已读、偏好矩阵、实时订阅与声明式入口 |

## 实现状态

| 子模块 | 状态 |
|---|---|
| [layout](design-web-layout.md) | 已实现 |
| [tokens](design-web-tokens.md) | 已实现 |
| [i18n](design-web-i18n.md) | 已实现 |
| [ui-kit](design-web-ui-kit.md) | 已实现 |
| [layout-kit](design-web-layout-kit.md) | 已实现 |
| [api-client](design-web-api-client.md) | 已实现 |
| [api-sdk](design-web-api-sdk.md) | 已实现 |
| [route-kit](design-web-route-kit.md) | 已实现 |
| [auth-core](design-web-auth-core.md) | 已实现 |
| [auth-ui](design-web-auth-ui.md) | 已实现 |
| [account-ui](design-web-account-ui.md) | 已实现 |
| [tenancy-ui](design-web-tenancy-ui.md) | 已实现 |
| [product-shell](design-web-product-shell.md) | 已实现 |
| [console-ui](design-web-console-ui.md) | 已实现 |
| [objectstore-ui](design-web-objectstore-ui.md) | 已实现 |
| [notification-ui](design-web-notification-ui.md) | 已实现 |
