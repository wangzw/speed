# 租户

租户功能的文档索引。主文档给出请求侧的租户解析、名册与生命周期、共享表加租户列的语句过滤；租户准入层与管理面为其子模块。

## 索引

| 文档 | 内容 |
|---|---|
| [tenant](design-tenant.md) | 租户：请求侧的租户解析、名册与生命周期、共享表加租户列的语句过滤 |
| [tenant-admission](design-tenant-admission.md) | 租户准入层：身份是否获准进入已解析的租户、租户级认证强度要求、冒充的租户范围 |
| [tenant-admin](design-tenant-admin.md) | 租户管理面：名册的平台域读写路由、判定与审计 |
| [tenant-personal](design-tenant-personal.md) | 个人租户：账户事务内的建立与墓碑、生命周期钩子、提交后的幂等补完与种类解析 |

## 实现状态

| 子模块 | 状态 |
|---|---|
| [tenant-admission](design-tenant-admission.md) | 已实现 |
| [tenant-admin](design-tenant-admin.md) | 已实现 |
| [tenant-personal](design-tenant-personal.md) | 已实现 |
