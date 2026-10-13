# HTTP 入口

HTTP 入口的文档索引。主文档给出监听端、路由与中间件的登记、中间件排序、请求上下文、panic 兜底与停机排空；跨源层为其子模块。

## 索引

| 文档 | 内容 |
|---|---|
| [http](design-http.md) | HTTP 入口：监听端、路由与中间件的登记、中间件排序、请求上下文、panic 兜底、停机排空 |
| [http-cors](design-http-cors.md) | 入口的跨源层：桥接 gin-contrib/cors，逐监听端策略，登记在防护阶段最外侧 |

## 实现状态

| 子模块 | 状态 |
|---|---|
| [http-cors](design-http-cors.md) | 已实现 |
