# 通知

通知能力的文档索引。主文档给出类型分类法、渠道词汇、出站投递流水线与实时推送的总体设计；站内信收件箱、渠道偏好、出站投递流水线与 SSE 推送各自为其子模块。

## 索引

| 文档 | 内容 |
|---|---|
| [notification](design-notification.md) | 通知能力：类型分类法、渠道词汇、出站投递流水线、收件箱与实时推送的总体设计 |
| [notification-inbox](design-notification-inbox.md) | 站内信收件箱：写行与去重、查、未读数、标记已读、路由 |
| [notification-preferences](design-notification-preferences.md) | 渠道偏好矩阵与类型分类法的消费点 |
| [notification-delivery](design-notification-delivery.md) | 出站投递流水线：派发校验、目的地归属校验、计投递预算并入队、消费作业、逐渠道发送、去重落账 |
| [notification-stream](design-notification-stream.md) | SSE 推送：每副本一个 Hub、订阅收件箱创建事件 |

## 实现状态

| 子模块 | 状态 |
|---|---|
| [notification-inbox](design-notification-inbox.md) | 已实现 |
| [notification-preferences](design-notification-preferences.md) | 已实现 |
| [notification-delivery](design-notification-delivery.md) | 已实现 |
| [notification-stream](design-notification-stream.md) | 已实现 |
