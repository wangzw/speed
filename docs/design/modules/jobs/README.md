# 作业

作业能力的文档索引。主文档给出提交面、登记面与作业的生命周期；作业队列与周期调度为其子模块。

## 索引

| 文档 | 内容 |
|---|---|
| [jobs](design-jobs.md) | 作业能力：提交与登记两处接缝、三类需求（定时一次、周期、异步执行）、生命周期与分布式语义 |
| [jobs.db](design-jobs-db.md) | 作业队列与登记面：持作业表、有时限的认领、执行、退避与死信、终态事件 |
| [jobs.scheduler](design-jobs-scheduler.md) | 周期调度：按 `jobs.Schedule` 声明 tick、窗口幂等键、按租户展开 |

## 实现状态

| 子模块 | 状态 |
|---|---|
| [jobs.db](design-jobs-db.md) | 未实现 |
| [jobs.scheduler](design-jobs-scheduler.md) | 未实现 |
