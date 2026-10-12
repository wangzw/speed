# 反转旧树「不用 Casbin」的结论：被否的是默认存储，不是引擎

## 状态

已接受

## 背景

早先的实现 `go/rbac` 与其文档成文否决了 Casbin，否决给出的理由落在存储、适配器与引擎上。存储一侧：官方 `casbin_rule` 表没有 `tenant_id` 列，租户藏在策略串（domain）里，于是全产品最安全敏感的一张表会同时退出租户隔离——GORM 插件注入不到过滤条件、用不了 `Repository[T]`、Postgres 行级安全策略以 `tenant_id` 列为准而无从下手，`tenancytest.AssertIsolated` 对它跑不起来。适配器一侧：官方 `gorm-adapter` 自己持有 `*gorm.DB` 并自行发查询，正是本仓库数据规则禁止的裸访问形态，且默认 `AutoMigrate`。引擎一侧的价值判断：Casbin 的可插拔 `model.conf`、ABAC、RESTful matcher 在那个设计里用不上，判定链就是 `subject → bindings → role → permissions` 加一次物化路径前缀判断。

本设计为授权选择引擎与存储（[ADR-authz-engine-2026-10-12](adr-authz-engine-2026-10-12.md)、[ADR-authz-store-2026-10-12](adr-authz-store-2026-10-12.md)），结论是采纳 Casbin 引擎、经自写薄适配器落到带真实 `tenant_id` 列的表上。这与旧树的结论直接冲突：旧树说的是「不用 Casbin」。旧树仍在仓库里，`docs/internal/05-identity-and-access.md` 与站点 rbac 页把「不用 Casbin」写成了明确的否定结论，读者会把它当作现行设计。

## 考虑过的选项

### 继续自研，不引入 Casbin

保留旧树的自研引擎，判定链不许用第三方策略库。

**否决**：见 [ADR-authz-engine-2026-10-12](adr-authz-engine-2026-10-12.md)——自研引擎的适用范围止于精确匹配加前缀，可插拔模型是宿主可能要用到的扩展面，缺它是永久的能力缺口；旧实现的范围本身就是这条判断的证据。

### 不动旧文档，任其并存

不改动 `docs/internal/05-identity-and-access.md` 与站点 rbac 页里的旧结论，让本设计与旧文并存。

**否决**：读者会先读到旧文并把它当作现行设计。「不用 Casbin」是一条明确的否定结论，与现行设计互斥，并存等于把两份互斥的结论都留在权威位置，谁读到哪一份决定他得到什么结论。

### 桥接到 Casbin v2

沿用旧树时代的 Casbin v2 作为桥接版本。

**否决**：v2 已进维护期，本设计绑定 v3（[ADR-authz-engine-2026-10-12](adr-authz-engine-2026-10-12.md) 的引擎版本）；写成 v2 与设计实际依赖的版本互斥，把文档指向一个不再演进的版本。

### 反转结论并标注旧文

**采纳**，见决策。

## 决策

采纳：反转旧树「不用 Casbin」的结论。被否的是 Casbin 的默认存储，不是引擎。

- **默认存储被替换，引擎被采纳。** 官方 `casbin_rule` 表与官方 `gorm-adapter` 由自写薄适配器加带真实 `tenant_id` 列的表替代（[ADR-authz-store-2026-10-12](adr-authz-store-2026-10-12.md)）——租户列这条理由由真实列消掉，官方适配器自持连接与默认 `AutoMigrate` 两条因不使用官方适配器而不适用。引擎本身被采纳（[ADR-authz-engine-2026-10-12](adr-authz-engine-2026-10-12.md)）。
- **旧树的结论已被取代，指引只落在点名的旧文档。** `docs/internal/05-identity-and-access.md` 与站点 rbac 页在「不用 Casbin」处加一行指向本记录的指引，正文不被改写；`go/rbac` 的源码与其 `AGENTS.md` 保留原文、不加指引；取代关系只由这些指引行承载。
- **模型文本固定在代码里。** 桥接固定的模型文本是设计承诺的一部分（[ADR-authz-engine-2026-10-12](adr-authz-engine-2026-10-12.md)）：判定语言不开放给宿主改写，宿主写的只是策略数据。

## 后果

授权引擎与存储的形态由 [ADR-authz-engine-2026-10-12](adr-authz-engine-2026-10-12.md) 与 [ADR-authz-store-2026-10-12](adr-authz-store-2026-10-12.md) 承载，本记录只承载对旧结论的反转与其中的取舍。

**接受的代价：**

- **旧树价值判断那条理由如实成立，作为代价被接受。** Casbin 的可插拔 `model.conf`、ABAC、RESTful matcher 在本设计里用不上：模型文本固定在代码里（[ADR-authz-engine-2026-10-12](adr-authz-engine-2026-10-12.md)），宿主只能写策略数据，改不了判定语言。需要自定义模型——ABAC 谓词、RESTful matcher、通配权限——的宿主在模型文本之外没有扩展面。本条是采纳引擎时明确接受、不为它增加机制的代价，不是被消掉的旧理由。
- **官方适配器那条理由以换掉适配器的方式处理，代价转移到自写适配器。** 自写薄适配器的实现与维护负担、`persist.Adapter` 的接口面、跨租户读取的处置见 [ADR-authz-store-2026-10-12](adr-authz-store-2026-10-12.md)。
- **旧树仍在仓库里，其正文不被改写。** 读者仍可能先读到旧结论；取代关系靠 `docs/internal/05-identity-and-access.md` 与站点 rbac 页里的一行指引承载，而不是在旧文里重写整段——旧文的实现事实仍成立，被取代的只是「不用 Casbin」这条结论。
- **本记录与旧树的结论互斥，两份文档同时存在时以本记录为准。** 谁在旧文里读到「不用 Casbin」而未读到指引行，会得到与现行设计相反的结论。

## 失效条件

- 若出现需要自定义判定语言的宿主（可插拔 `model.conf`、ABAC、RESTful matcher、通配权限），「模型文本固定在代码里、宿主只写策略数据」成为阻碍，本记录对旧树价值判断那条理由的处置（承认它、接受为代价）需要重新裁定。
- 若仓库移除旧树（`go/`、`docs/internal/`、`docs/site/`），指引行的落点消失，取代关系需要新的承载。
- 若官方 `casbin/gorm-adapter` 改为能接入本仓库的句柄、插件与租户过滤同一条路，且仍带真实租户列，自写薄适配器的必要性需要重新比较（[ADR-authz-store-2026-10-12](adr-authz-store-2026-10-12.md) 记有同一条件的失效面）。
