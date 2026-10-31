# 短信能力由发送接缝、模板注册表与管理面组成，载体不感知模板

## 背景

短信的**发送**能力（载体接缝与阿里云、腾讯云适配）与短信的**验证码状态机**（挑战行、预算、尝试次数、校验、发送队列与指标）原在同一模块 `pkg/authn/sms` 里，作为认证树的子包。但「按模板把一条短信发出去」与认证无关，是一件通用基础能力，与 `pkg/kv`、`pkg/event` 同级。

约束如下：

- **部分厂商只收预先审核通过的签名与模板，不收自由文本**：阿里云与腾讯云要求逐模板审核、按语言分别审核，位置参数厂商（腾讯云）还要求参数按审核时的次序给出。另一些厂商（AWS Pinpoint、Twilio）接受按模板模型声明的正文，不做逐模板审核。逐模板审核因此是载体的能力，不是本能力的普遍前提（[ADR-sms-template-review-is-carrier-capability](adr-sms-template-review-is-carrier-capability.md)）。通用接口仍以模板为中心，把「模板」做成一等声明：这些厂商要求它，正文也必须由注册表按声明渲染。
- **模板的集合是运行期数据。** 运营者必须在运行期登记、送审、启停与移除模板，并按语言读回厂商的审核状态，模板按语言分别审核（REQ-platform-config-010、REQ-platform-config-011、REQ-platform-config-012、REQ-platform-config-013、REQ-platform-config-031）。一份在装配期冻结、此后只读的模板声明表达不了它。
- **现状已经模板化**：`authn.SMS` 的字段是模板键加参数，不是自由文本。不通用之处只在：模板键的词表是认证的（`login`、`mfa`、`enroll`）、载体配置硬编码这组用途、语言常量取自认证根包、命名空间与整棵树挂在 `pkg/authn` 下。
- **Go 的 `internal` 规则**禁止 `pkg/sms/**` import `pkg/authn/internal/literal`，而两家载体都用它登记凭据的脱敏匹配器；它另被四个登录叶子共用。发送能力离开认证目录时，这个包必须上移到共享单元的位置。
- 认证侧的状态机、预算与队列不因发送能力外移而改变；去掉的只是发送方那一段。

## 考虑过的选项

### 只把发送接缝留在认证目录

保留 `authn.SMSSender` 与厂商适配在 `pkg/authn/sms` 之下，仅把实现整理一遍。

**否决**：任何要发通知的模块都得背上整套认证才能发一条短信。发送能力天然与 `pkg/kv`、`pkg/event` 同级，堵在认证目录里不会让「别的模块要发短信」这个需求消失，只会让它各自复制一份，或在认证之上再叠一层依赖。

### 只改名，不给模板声明

把 `authn.SMSSender` 整体挪到 `pkg/sms` 并改名，但保留自由文本式的消息形状（号码加已渲染的文本）。

**否决**：国内厂商不收自由文本，模板是这一类能力的强制形态。保留自由文本形状，要么让每个载体自己做模板映射与参数拼装，要么在调用方一侧重复同一份模板词表；模板的声明与校验因此分散到各个调用方，正是本改动要消掉的那份重复。模板必须是一等声明。

### 模板集由设置模块承载

把模板集当设置项，用设置模块的声明式存取承载。

**否决**：设置的键集合在装配期定死，运维不能新增一个没有声明过的键；容器（映射、列表）不进设置，而一条模板记录是有序参数表加语言到编号的映射，模板集本身又在运行期增删（[ADR-settings-declaration](adr-settings-declaration.md)）。设置表达不了它。它值得借的是机制形状——快照整体替换、变更事件、兜底 ticker、版本号——不是它的能力；注册表模块仿造这套机制，并自带一张表。

### 顶层能力加模板注册表加消费方核对

新建顶层 `pkg/sms`；一个注册表模块持有模板集并交付注册表接缝，模板来自两个来源——它自己的配置项（只读）与它自己的表（运行期增删）；载体只交付发送接缝，不认识模板；消费方在启动期核对自己的所需被配置来源覆盖。

**采纳**，见决策。

### 消费方经 `core.Resources` 声明需求，注册表在 `Prepare` 统一核对

消费方把自己所需的（模板，语言，参数）组合写成一份 `core.Resources` 声明，注册表在 `Prepare` 阶段读全部声明并统一核对。

**否决**：`Registry.Resources` 返回全部已注册模块，含未纳入本次命令树的那些，核对方要核对就得先按「在力」过滤一遍；这一层过滤是纯约定，没有编译期支持，漏过滤或过滤错了都只能靠测试发现。收益是核对早一个阶段（`Prepare` 早于 `New`），而 `New` 返回错误同样终止启动、同样早于服务，早一个阶段不抵这层额外的约定与过滤。核对因此留在唯一知道需求的一侧，用 `Registry.Declared()` 的只读视图逐个比对。

## 决策

采纳：**短信能力由发送接缝、模板注册表与管理面组成，载体不感知模板。**

- **能力归属。** `pkg/sms` 根包是接口包与工厂，不登记模块；它交付三条接缝——`Sender`（发送）、`Provider`（厂商模板操作：`Offer`、`Approval`、`Retire`）、`Registry`（模板集与解析）——以及 `Message`、`Resolved`、`Receipt`、模板声明 `TemplateSpec`、`Declaration`，哨兵 `ErrThrottled`、`ErrRejected`、`ErrUncertain`、`ErrInvalidConfig`、`ErrNotSendable`、`ErrImmutable`、`ErrNoTemplate`、`ErrTemplateExists`。`sms.aliyun` 与 `sms.tencent` 各是一个载体叶子包，用一个 `sms.Product` 值排他交付 `Sender` 与 `Provider`。`sms.registry` 是一个模块，持有模板集并排他交付 `Registry`，经 `Provider` 触达厂商。`sms.admin` 是 HTTP 管理面，挂在 `/api/v1/sms/admin`，每个路由的动作都是经 `Registry` 调注册表，自己不持数据也不持规则。`pkg/authn/sms` 退化为纯验证码状态机，经 `Registry` 解出、`Sender` 发送。
- **模板注册表持有模板集。** 模板集由 `sms.registry` 持有，分两个来源，拥有者不同。配置来源是注册表自己的 `templates` 配置项（`map[string]sms.TemplateSpec`），启动期读入一次、冻结为一个 `sms.Declaration`、此后只读——写它的运维已经让厂商过了审，注册表既不管它也不记它的审核状态，管理面调它即 `ErrImmutable`。运行期来源是注册表自己的 `sms_templates` 表，模板在运行期登记、送审、启停、删除，各自带**按语言**的审核状态，后台轮询读回。`Resolve` 先查配置来源、再查运行期来源；发送时只校验**该消息所用语言**那一条的审核状态，未过审或已停用即 `ErrNotSendable`。
- **覆盖由消费方对配置来源核对。** 注册表只报告自己持有什么（`Registry.Declared()`），不声明谁需要哪些模板。需要一组固定模板的消费方在启动期用自己的需求集核对注册表的配置来源——模板名齐备、每个模板覆盖配置的默认语言、参数名表相等。运行期来源的模板不属于这个契约：它在启动期还不存在，消费方对它的期望只能落在发送路径上。
- **发送方接缝不感知用途与语言闭集。** `Message` 的模板名由调用方定义，语言是不透明非空串，空值一律拒绝。用途词表与语言闭集留在认证。`Sender` 收一条由注册表解出的 `Resolved`，载体不持模板集。
- **载体由运行期选择项 `sms.carrier` 选择，日志载体是保底。** 厂商载体与 `pkg/sms/log` 都编入二进制，一次运行恰好一个生效；没有配置任何厂商载体时，日志载体把消息写进进程日志（[ADR-carrier-selection](adr-carrier-selection.md)）。
- **依赖方向。** `pkg/sms` 不 import `pkg/authn`；调用方（`pkg/authn/sms`）import `pkg/sms` 的根包。根包不 import 厂商 SDK，SDK 只落在绑定它的载体叶子包里。注册表经 `Provider` 触达厂商，不 import 任何载体叶子包。
- **`pkg/authn/internal/literal` 上移为 `pkg/internal/literal`。** 它被两个 SMS 载体与四个登录叶子共用，是共享单元，落在 `pkg/internal/` 下，不登记模块。上移是这次改动的必要前提，不只是整理。

## 后果

发送能力可被任何模块单独引入，不必背上整套认证。模板集只有一处持有，载体与消费方都从注册表得到次序、编号与审核状态；位置参数厂商与键参数厂商共用同一份模板声明。宿主可见面因此在几处改变。

**接受的代价：**

- **宿主可见的命名空间变更。** 厂商适配的模块名从 `authn.sms.aliyun`、`authn.sms.tencent` 变为 `sms.aliyun`、`sms.tencent`，配置命名空间随模块名改变。另有两个新模块 `sms.registry`（模板注册表）与 `sms.admin`（HTTP 管理面），各有自己的配置命名空间。宿主的命令树与配置文件都要相应改写：命令树上点名的模块名与配置文件里的命名空间前缀都变了。
- **模板配置形态变更。** 模板从载体的命名空间搬到注册表的命名空间：注册表配置里的 `templates` 是「模板名 → 结构体（`params` 有序参数名表、`codes` 语言到编号映射）」的嵌套结构。`pkg/config` 把任何 map 判为一个容器叶子，整张 map 从主源取出后经解码器落进字段；值类型为结构体时嵌套 section 的键按 tag 落进字段，所以模板配置必须是嵌套的，不能是 `名: {zh-CN: 编号, params: [...]}` 的混合形式——混合形式要么只能落 `map[string]any` 绕过类型检查，要么在 `map[string]TemplateSpec` 下静默丢掉语言键。嵌套后类型错误在启动期即 `ErrTypeMismatch`。
- **认证根包的导出面收缩。** `authn.SMS`、`authn.SendReceipt`、`authn.SMSSender` 与 `authn.ErrSMSThrottled`、`ErrSMSRejected`、`ErrSMSUncertain` 从根包删除；三个发送哨兵改由 `pkg/sms` 定义，认证的工作池归类它们、不外露。认证根包保留 `SMSCodes` 与 `SendCode`，不再声明语言集或 `LocaleOf`：归一改在 `pkg/authn/sms`，其缺省语言取自共享的 `i18n.default-locale` 项（[ADR-i18n-language-set](adr-i18n-language-set.md)）。
- **`pkg/internal/literal` 的路径变更。** 引用它的包改 import 路径；该包自身的导出面不变。
- **认证侧的空值归一只有一处。** `pkg/sms` 把空 `Locale` 读作调用方缺陷；把空串填成缺省语言的归一留在 `pkg/authn/sms` 一处，缺省语言取自共享的 `i18n.default-locale` 项（[ADR-i18n-language-set](adr-i18n-language-set.md)）。消费方直接调注册表时必须自己先归一。
- **运行期来源的模板带一张表与一个后台轮询。** 注册表自带 `sms_templates` 的迁移与一张表；一个后台轮询按语言读回厂商的审核状态，轮询间隔是注册表的配置项，零则只靠变更事件收敛。多副本各跑一处轮询，靠快照的整体替换与变更事件收敛。

## 失效条件

以下情形出现时，本决策需要重议：

- 若国内厂商开始接受自由文本，或模板不再是发送的强制形态，则「模板是一等声明」这一条的价值下降，接口可以重新按自由文本设计。
- 模板集合在运行期确定的需求已经出现，并由注册表模块承接：模板集由 `sms.registry` 持有，分配置来源与运行期来源两个来源，覆盖核对落在消费方对注册表配置来源的核对。原先「覆盖核对迁到一个能拿到运行期需求的阶段」的预告由此兑现，这一条不再是待议条件。
- 若 `pkg/kv`、`pkg/event` 这类底层能力也被移入某个功能目录，则「发送能力与它们同级」这一判据的前提改变。
