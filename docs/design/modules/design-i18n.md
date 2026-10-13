# i18n

## 职责

本功能目录交付**面向用户的文本的渲染机制，以及渲染它所用的语言偏好**：它定义消息目录、按一次请求的语言把带模块命名空间的消息键渲染成文案、按请求协商语言、按一条解析链定位语言与时区。一段文本由产生它的模块渲染，本功能目录提供渲染所依据的引擎与偏好，不替消费方集中渲染（[ADR-i18n-scope-2026-10-13](../../adr/adr-i18n-scope-2026-10-13.md)、[ADR-i18n-rendering-ownership-2026-10-13](../../adr/adr-i18n-rendering-ownership-2026-10-13.md)）。渲染是纯读取，不做授权、不改机器码、不与任何外部通道交互。

本功能目录按接口包加实现子包的方式拆分（[ADR-module-packaging-2026-09-22](../../adr/adr-module-packaging-2026-09-22.md)）。根包是接口包：它定义接口、值类型与资源类型，只依赖标准库，不登记任何模块。子包各自登记一个模块：

- `pkg/i18n/catalog/` 登记引擎模块 `i18n`：排他交付 `Localizer`、`Negotiator`、`Resolver`，收集全部已声明目录并并入配置项 `i18n.catalogs-dir` 指定的覆盖目录，从配置项 `i18n.default-locale` 读默认语言，非排他地取用可选的 `Profile`。
- `pkg/i18n/http/` 登记模块 `i18n.http`：经 `http.Router` 在每个端注册两层中间件，把协商出的语言与账户偏好的语言时区写进请求上下文。
- `pkg/i18n/account/` 登记模块 `i18n.account`：非排他交付 `Profile`，并实现 `authn.Provisioner`，在首登开户时把请求语言写进待建账户的属性。

语言是偏好而不是授权事实（[ADR-i18n-locale-type-2026-10-13](../../adr/adr-i18n-locale-type-2026-10-13.md)）：写进与读出上下文的函数因此是导出的，与 `pkg/log` 的 `WithLogger` 同类，不采取 `pkg/authn` 把身份的写侧封进内部包的形态。谁能读到什么仍由 [authz](design-authz.md) 与 [authn](authn/design-authn.md) 判定，本功能目录不参与。

### 非职责

- **不做授权判定。** 语言与时区是偏好，任何判定都不以它们为依据；本功能目录不读身份，也不改变一次请求能到达的范围。
- **不承载账户存储。** 语言与时区存进 `Account.Attributes` 的 `i18n.locale` 与 `i18n.tz`，写账户归 [authn](authn/design-authn.md) 的账户模块，本功能目录只读它，不动认证的表结构（[ADR-i18n-preferences-storage-2026-10-13](../../adr/adr-i18n-preferences-storage-2026-10-13.md)）。
- **不做 HTTP 机制。** 路由、中间件的排序与阶段归 [http](http/design-http.md)；`i18n.http` 只是使用它的登记面在每个端挂层。
- **不改机器码与状态码。** 机器码 `Problem.Type`、`Problem.Status` 与 `Problem.Detail` 原样保留，不随语言变化；本功能目录渲染的是消息文本，不决定错误响应的形状（[ADR-i18n-error-rendering-2026-10-13](../../adr/adr-i18n-error-rendering-2026-10-13.md)）。
- **不替消费方渲染消息。** 一条消息由产生它的模块渲染：那个模块承载自己的目录、取用引擎、把成品文本写出；本功能目录只提供引擎与目录机制，不集中渲染任何模块的文本，也不认识某条消息的插值上下文（[ADR-i18n-rendering-ownership-2026-10-13](../../adr/adr-i18n-rendering-ownership-2026-10-13.md)）。
- **不做复数、数量与日期格式化。** 消息只写 `{{name}}` 插值，值的形态与格式由调用方在交给渲染之前定好。
- **不做热加载。** 目录有内嵌声明与配置指定的覆盖目录两个来源，两者都在装配期读取，此后只读，运行期没有更换目录或增删消息的接口（[ADR-i18n-catalogs-2026-10-13](../../adr/adr-i18n-catalogs-2026-10-13.md)）。
- **不发送消息。** 短信与邮件的模板与发送归各自的模块；本功能目录渲染的是本进程写出的文本，不投递给任何外部通道。

## 术语表

以下术语的含义限于本模块，以及 i18n 主题的决策记录。

| 术语 | 英文 | 定义 |
|---|---|---|
| 语言 | locale | 一次渲染所用的语言，写作 BCP 47 标签，如 `zh-CN`、`en-US`；在本模块是一份偏好，不是任何判定依据。 |
| 目录 | catalog | 一组消息文件，每个文件按它承载的语言命名，正文是键到消息的 JSON 对象。来源有二：一个模块以 `//go:embed` 声明的内嵌目录，以及配置项 `i18n.catalogs-dir` 指定的覆盖目录。两者都在装配期装载，运行期不再读。 |
| 覆盖目录 | overlay | 配置项 `i18n.catalogs-dir` 指定的目录，与内嵌声明同形。装配期读取并并入同一索引，排在已声明目录之后：同一个键上胜过声明，只被它承载的语言因此进入受支持的语言集。 |
| 键 | key | 一条消息在目录中的标识，扁平且带模块命名空间，如 `http.404`、`authn.login-failed`。 |
| 参数 | args | 一条参数化消息插值用的名值对；消息写 `{{name}}`，缺参时该处原样保留。 |
| 协商 | negotiation | 按请求的 `Accept-Language` 头，在本次支持的受语言集中选出最匹配的一个。 |
| 回落 | fallback | 请求语言在该键上没有条目时，改取配置的默认语言、再不中则取键本身的次序。 |
| 受支持的语言集 | supported locale set | 一次运行能渲染的语言集合，等于各模块声明的目录与配置指定的覆盖目录所覆盖语言的并集；代码不枚举它，它在装配期由目录派生。 |
| 默认语言 | default locale | 一个什么都没指名的请求解析到的语言，也是每条回落所落的语言；它是配置项 `i18n.default-locale`，未设置时为 `en-US`。 |
| 偏好 | preference | 一项源自用户、用于本地化、不参与任何裁定的选择；与授权事实相对。 |

## 外部接口契约

### 登记单元与名字

| 登记单元 | 模块名 | 交付 |
|---|---|---|
| 根包 `pkg/i18n` | — | 接口、值类型与资源类型；不登记模块 |
| `pkg/i18n/catalog` | `i18n` | 排他交付 `Localizer`、`Negotiator`、`Resolver` |
| `pkg/i18n/http` | `i18n.http` | 在每个端注册两层中间件 |
| `pkg/i18n/account` | `i18n.account` | 非排他交付 `i18n.Profile` 与 `authn.Provisioner` |

每个登记模块的模块名由它的包导出为 `ModuleName` 常量，命令树按这个名字纳入它。根包不登记模块，因而 import 它不把任何模块带进宿主的可用集；这与 [authn](authn/design-authn.md) 根包的处置同一条。

### 根包的类型与接口

功能标识的元素类型必须是接口，根包的功能取用写作 `core.Resolve[i18n.Localizer](reg)` 一类。

```go
// Locale 是一次渲染所用的语言，写作 BCP 47 标签。
// 它是一个字符串，以便不带语言匹配库就跨模块边界传递（上下文、接口、声明的目录）。
type Locale string

// 本功能目录读取配置所用的两个名字。受支持的语言集是各模块声明的目录与配置
// 指定的覆盖目录所承载的语言集，代码不枚举任何语言表；在此之上唯一的额外选择
// ——一个什么都没指名的请求解析到哪一种语言——是配置项而非写进代码的值。
//
// 默认语言是短信模块也解析的那一个配置项，一处声明同时服务两个能力目录。因为
// 那个模块需要这两个名字，它们在此导出，而不是在两侧各写一遍字面量。
const (
    // ConfigNamespace 是本功能目录各项所挂的配置命名空间。
    ConfigNamespace = "i18n"
    // DefaultLocaleItem 是承载默认语言的配置项：一个什么都没指名的请求解析到
    // 的语言，也是某条消息在请求语言无该键时回落到的语言。取值是 BCP 47 标签，
    // 未设置时为 "en-US"。
    DefaultLocaleItem = "default-locale"
)

// Key 标识目录中的一条消息，扁平且按渲染它的模块命名空间化。
type Key string

// Args 承载一条参数化消息插值用的值：消息写 {{name}}，缺参时该处原样保留。
type Args map[string]string

// Localizer 按语言渲染一条消息。它由引擎排他交付。
type Localizer interface {
    // Localize 按 ctx 的语言渲染 key，回落配置的默认语言、再回落键本身，结果永不为空。
    Localize(ctx context.Context, key Key, args Args) string
    // Translate 按指名的语言渲染 key，并报告某个目录是否载有它。它不回落。
    Translate(locale Locale, key Key, args Args) (string, bool)
}

// Negotiator 在本次支持的受语言集中，选出最匹配一次请求语言偏好的一个。
// 它由引擎排他交付。
type Negotiator interface {
    // Negotiate 返回与 Accept-Language 头值最匹配的受支持语言，以及是否有匹配。
    // 只指名未受支持的语言、或没有指名任何语言的，报告无匹配。
    Negotiate(acceptLanguage string) (Locale, bool)
}

// Resolver 回答某个上下文与某个具名账户适用哪种语言与时区。它由引擎排他交付。
type Resolver interface {
    // Default 是一个什么都没指名的请求解析到的语言，也是某条消息在请求语言无该
    // 键时回落到的语言；它是本次运行配置的默认语言。
    Default() Locale
    // LocaleOf 是 ctx 的语言：写进它的那个，或 Default 报告的那个。它从不失败。
    LocaleOf(ctx context.Context) Locale
    // TimeZoneOf 是 ctx 的 IANA 时区名，未设置时为空串，读作 UTC。
    TimeZoneOf(ctx context.Context) string
    // LocaleFor 是具名账户的语言：上下文无语言时来自它存的偏好，否则是 Default
    // 报告的那个。
    LocaleFor(ctx context.Context, account string) Locale
    // TimeZoneFor 是具名账户的时区，未存时为空串。
    TimeZoneFor(ctx context.Context, account string) string
}

// Profile 是通往账户所存语言与时区的一条接缝。它非排他且可选：引擎用
// core.ResolveAll 取用每一个实现，一个都没有时回落到配置的默认语言。
type Profile interface {
    // LocaleFor 返回一个账户所存的语言，以及是否存过。
    LocaleFor(ctx context.Context, account string) (Locale, bool)
    // TimeZoneFor 返回一个账户所存的 IANA 时区名，以及是否存过。
    TimeZoneFor(ctx context.Context, account string) (string, bool)
}

// Catalog 是一个模块声明自己消息文件的资源类型。引擎是它的消费方：它取
// 可用集，读到每一份声明的目录，合并进一份索引。
type Catalog struct {
    FS      fs.FS  // 模块读取自己文件所用的文件系统，通常是 embed.FS
    Pattern string // 其下消息文件的通配，每个文件按它承载的语言命名，如 "locales/{tag}.json"
}

// LocaleResolver 标记定一次请求语言的中间件层，供另一层用 After 或 Before 排它。
// 它没有调用方使用的方法；一层在 Middleware.Provides 中写 (*LocaleResolver)(nil) 代表它。
type LocaleResolver interface{ localeResolver() }
```

### 上下文存取与账户属性

语言的写侧是导出的：宿主为某个已知账户启动一项没有请求、也没有头的作业时，在那里写它解析出的语言。请求路径由入口模块的链写它。

```go
// WithLocale 返回携带 locale 的上下文；空的语言不写入。
func WithLocale(ctx context.Context, locale Locale) context.Context

// FromContext 返回该上下文携带的语言，以及是否携带。它不回落。
func FromContext(ctx context.Context) (Locale, bool)

// WithTimeZone 返回携带 IANA 时区名的上下文；空的名不写入，读作 UTC。
func WithTimeZone(ctx context.Context, name string) context.Context

// TimeZoneFromContext 返回该上下文携带的 IANA 时区名，以及是否携带。
func TimeZoneFromContext(ctx context.Context) (string, bool)

// 账户属性中存放语言与时区的键；命名空间化，与宿主自己的属性不相撞。
const (
    LocaleAttributeKey   = "i18n.locale"
    TimeZoneAttributeKey = "i18n.tz"
)

// ValidLocaleTag 报告 name 的形状是否是一个 BCP 47 语言标签；空值合法。
func ValidLocaleTag(name string) bool

// ValidTimeZone 报告 name 是否是一个本进程能加载的时区；空值合法，读作 UTC。
func ValidTimeZone(name string) bool
```

**空值不是一种值。** `WithLocale` 与 `WithTimeZone` 收到空串时不写入，一个什么都没指名的上下文因此读作“未携带任何语言”，`FromContext` 与 `TimeZoneFromContext` 据实报告。回落发生在 `Resolver` 上：需要一种语言而上下文未携带它的调用方经 `Resolver.LocaleOf` 取，默认值只在那里。

**形状检查不等于受支持检查。** `ValidLocaleTag` 只判形状，不判这个标签在本次运行里受不受支持——受支持集是目录承载的语言集，只有引擎知道，一个形状合法而不受支持的标签因此在这里可被接受，落到渲染时回落（[ADR-i18n-locale-type-2026-10-13](../../adr/adr-i18n-locale-type-2026-10-13.md)）。`ValidTimeZone` 用一个 IANA 名去 `time.LoadLocation`，空值合法；固定偏移（如 `+08:00`）与缩写（如 `CST`）不是 IANA 名而被拒。这两个检查是写入侧执行的判据，由写偏好的那一方调用，本包不对任何既有值回头复检。

### 各模块交付与取用的能力

| 模块 | 提供者声明 | 取用 |
|---|---|---|
| `i18n` | `Provides: [(*i18n.Localizer)(nil), (*i18n.Negotiator)(nil), (*i18n.Resolver)(nil)]`，均排他；`Requires: [(*i18n.Profile)(nil)]` 标注 `Optional` | `core.Resolve[i18n.Localizer](reg)` 等；`Profile` 用 `core.ResolveAll` |
| `i18n.http` | 只注册中间件层，不声明功能 | 取用 `http.Router`、`i18n.Negotiator`；以 `(*authn.Gate)(nil)` 为 `After` 锚点 |
| `i18n.account` | `Provides: [(*i18n.Profile)(nil), (*authn.Provisioner)(nil)]`，非排他 | 取用 `authn.Accounts`；语言经 `pkg/i18n` 的上下文存取函数与属性键读写 |

**引擎的这几件能力排他交付，一个进程至多一个语言集。** 语言集是目录承载的集合，两个引擎会各自持有不同的一份，`Resolve` 要求唯一，多于一个即装配期 `ErrExclusiveViolated`。**`Profile` 非排他可选取用**：引擎在 `New` 用 `core.ResolveAll[i18n.Profile]` 取用每一个实现，一个都没有是合法配置，账户语言随之缺失，解析链落到配置的默认语言。

### 对既有模块的改动

以下各条落在既有模块里，本功能目录借用它们的既有接缝，不新增跨目录的机制。

- **入口模块 `pkg/http` 取用本功能目录的根包，渲染它自己写出的拒绝。** 它把 `i18n.Localizer` 作为可选依赖取用，并从自己声明的目录取 `http.<status>` 文案；交出 `Problem` 时文本已经是成品，`Problem` 不再承载消息键与参数（[http](http/design-http.md) 的错误响应一节、[ADR-i18n-error-rendering-2026-10-13](../../adr/adr-i18n-error-rendering-2026-10-13.md)）。
- **认证侧不再声明语言闭集，也不再给语言填默认。** 根包 `pkg/authn` 的 `locale.go`（`LocaleEN`、`LocaleZH`、`LocaleOf`）整体删除：一个只读语言的模块 import 认证根包，不再从中拿到一份语言表。登录引擎把 `i18n.FromContext` 定下的语言原样带进流程记录与短信发送（取不到时为 `""`），`authn.sms` 再把请求语言解析到该消息所用模板声明的语言（[ADR-i18n-language-set-2026-10-13](../../adr/adr-i18n-language-set-2026-10-13.md)）。默认语言只由 `i18n.default-locale` 一处给出。
- **认证自助面新增一条偏好路由** `PATCH B/me/preferences`，写在 [authn-login](authn/design-authn-login.md) 的账户自助面里：它把语言与时区写进账户的 `Account.Attributes`，经账户模块的 `AccountAdmin.SetAttributes` 落库，写前用 `ValidLocaleTag` 与 `ValidTimeZone` 判形状。它是 [authn](authn/design-authn.md) 的子包 `pkg/authn/login` 对 `pkg/i18n` 的取用——`pkg/authn` 根包不 import 它（见依赖）。
- **语言与时区存进 `Account.Attributes` 的 `i18n.locale` 与 `i18n.tz`**，不改认证的表结构，不新增列（[ADR-i18n-preferences-storage-2026-10-13](../../adr/adr-i18n-preferences-storage-2026-10-13.md)）。

### 稳定性承诺

`Locale`、`ConfigNamespace`、`DefaultLocaleItem`、`Key`、`Args`、`Localizer`、`Negotiator`、`Resolver`、`Profile`、`Catalog`、`LocaleResolver`、上下文的存取函数、属性键常量与校验函数，以及各模块导出的 `ModuleName`，是本功能目录与其他模块之间的契约：接口的方法集、值类型与资源类型的字段，以及属性键的取值，改一个即破坏每一个实现方与消费方。属性键 `i18n.locale` 与 `i18n.tz` 同时是存储契约：账户属性里已经写下的值按这两个键读，改键会让既有偏好读不出来。配置命名空间 `i18n` 与配置项名 `default-locale`、`catalogs-dir` 是配置契约：短信模块按命名空间与 `default-locale` 两个名字读同一个默认语言，改名会让两个能力在无声中各读一份；`catalogs-dir` 是运维写入覆盖目录的入口，改名会让既有配置静默地读作没有额外文案。模块名由各包导出为常量，改名会让命令树上的纳入静默失配。

## 依赖

| 依赖 | 用途 |
|---|---|
| `pkg/core` | 注册描述符、交付与取用功能、声明与收集资源 |
| `pkg/http` | `i18n.http` 取用 `Router` 登记层 |
| `pkg/authn` | `i18n.http` 以 `(*authn.Gate)(nil)` 为锚点、读请求身份；`i18n.account` 经 `Accounts` 读账户属性、实现 `Provisioner` |

**依赖方向。** 根包 `pkg/i18n` 只依赖标准库，因而一个只读语言的模块 import 它不拉进任何第三方库；这与 [authn](authn/design-authn.md) 根包的位置同一条。子包各自 import 根包与 `pkg/core`，并按其用途 import `pkg/http` 或 `pkg/authn`。**`pkg/authn` 的任何包都不 import `pkg/i18n` 的根包以外的部分，且 `pkg/authn` 根包不 import `pkg/i18n`**（[authn](authn/design-authn.md) 的依赖方向）：认证根包只依赖标准库与自己的内部包，一个语言偏好不改变这一点。需要同时读身份与语言的层要么在 `i18n.http` 里，要么在 `pkg/authn/login` 这类子包里——子包 import `pkg/i18n` 是许可的，本功能目录不 import 认证的任何子包。**`pkg/http` import `pkg/i18n` 的根包**：它渲染自己写出的四类拒绝（无匹配路由的 404、方法不允许的 405、请求体超限的 413、接住 panic 的 500），把 `i18n.Localizer` 作为可选依赖取用（[ADR-i18n-error-rendering-2026-10-13](../../adr/adr-i18n-error-rendering-2026-10-13.md)）。方向是本功能目录的根包被入口模块取用；根包只依赖标准库，因而不使入口模块的依赖预算增长（[ADR-i18n-locale-type-2026-10-13](../../adr/adr-i18n-locale-type-2026-10-13.md)）。

**`golang.org/x/text` 只出现在引擎 `pkg/i18n/catalog`，别处不出现。** 语言匹配需要的 `language` 包只在引擎里出现：它用 `ParseAcceptLanguage` 解析 `Accept-Language` 头值、用 `language.Matcher` 在受支持语言集中匹配。根包与其余子包不拉它，`i18n.http` 经 `i18n.Negotiator` 取用协商结果，一个只读 `Locale` 的模块因而拿不到匹配库。

## UML 图

下图给出契约面上的类型与模块内部的结构。图中画不出的约束：目录在装配期装载，此后索引只读、无 IO、无热加载；`Locale` 是字符串，跨边界不携带匹配库；语言解析的回落只发生在 `Resolver` 上；`Catalog` 只由模块以 `//go:embed` 声明，引擎取可用集，配置指定的覆盖目录与它同形、经同一条装载路径并入其后；`LocaleResolver` 无方法，只作一层的排序锚点。

```mermaid
classDiagram
    class Locale {
        <<string>>
    }
    class Key {
        <<string>>
    }
    class Args {
        <<map>>
    }
    class Localizer {
        <<interface>>
        +Localize(ctx, key, args) string
        +Translate(locale, key, args) string, bool
    }
    class Negotiator {
        <<interface>>
        +Negotiate(acceptLanguage) Locale, bool
    }
    class Resolver {
        <<interface>>
        +Default() Locale
        +LocaleOf(ctx) Locale
        +TimeZoneOf(ctx) string
        +LocaleFor(ctx, account) Locale
        +TimeZoneFor(ctx, account) string
    }
    class Profile {
        <<interface>>
        +LocaleFor(ctx, account) Locale, bool
        +TimeZoneFor(ctx, account) string, bool
    }
    class Catalog {
        +fs.FS FS
        +string Pattern
    }
    class LocaleResolver {
        <<interface>>
        +localeResolver()
    }
    class engine {
        -index index
        -language.Matcher matcher
    }

    Localizer <|.. engine : 实现
    Negotiator <|.. engine : 实现
    Resolver <|.. engine : 实现
    engine ..> Profile : 可选取用
    engine ..> Catalog : 取可用集、合并
    engine ..> Locale : 产出
    Localizer ..> Key : 渲染
    Localizer ..> Args : 插值
```

请求路径上语言从协商到渲染的次序，以及它如何随链上的两层落定：

```mermaid
sequenceDiagram
    participant C as 客户端
    participant O as 观测层 accept-language
    participant G as 认证网关
    participant P as 鉴别层 profile-locale
    participant H as handler
    participant E as 引擎

    C->>O: 请求带 Accept-Language
    O->>E: Negotiate(Accept-Language)
    E-->>O: 最匹配的受支持语言或 无匹配
    O->>O: 有匹配则 WithLocale 写进上下文
    O->>G: 交给链的下一层
    G->>P: 盖章后进入鉴别阶段
    P->>P: 上下文无语言时读身份的 i18n.locale / i18n.tz
    P->>H: 写进上下文后交给 handler
    H->>E: Localize(ctx, key, args)
    E-->>H: 按请求语言渲染的文案
```

**两层各司其职。** `i18n:accept-language` 属于观测阶段、排在本阶段无约束各层的最外，它看到每个请求、从不拒绝，协商出的语言对匿名请求同样成立。`i18n:profile-locale` 属于鉴别阶段、排在认证网关之内，它只在网关已盖章、账户已知之后才有读账户属性的前提，因而以 `(*authn.Gate)(nil)` 为 `After` 锚点；上下文已被上一层写下的语言时它不覆盖。

## 核心数据结构

**索引**是全部已声明目录与配置指定的覆盖目录合并出的只读结构，按语言与键两级组织：语言到（键到模板）的映射，外加一份按模块名归类的键归属表，用于冲突点名与诊断。模板是消息原文，`{{name}}` 在渲染时按 `Args` 插值。

**匹配器**是 `golang.org/x/text/language` 的 `Matcher`，由索引建立时支持的语言集构造一次，此后只读。协商把 `Accept-Language` 的头值解析成一组带 q 值的偏好，交给匹配器选出一个受支持的语言。

## 核心算法与流程

### 目录的收集与装载

引擎在装配期依次做 `Prepare` 校验与 `New` 建立。默认语言是配置项 `i18n.default-locale`，未设置时为 `en-US`，取值须是一个 BCP 47 标签的形状（`ValidLocaleTag`），否则启动失败；它在 `Prepare` 与 `New` 各读一次，两次读的是同一份快照。

**取材域取可用集**（架构不变量 15）。来源是 `core.Resources[i18n.Catalog](reg)` 的全体；判据是**一组消息文件是不是这个二进制的一部分**是静态事实，比任何一次命令长寿，因而未运行、消解落选与只被静态级纳入的模块，其目录一律进索引。**这个消费点不做减法**：声明的目录随二进制一起编译进来，一个本次不运行的模块，它的消息照常可被渲染——这正是“某个错误在某个装配下答得出键、在另一个装配下答不出”要避免的形态（[ADR-i18n-catalogs-2026-10-13](../../adr/adr-i18n-catalogs-2026-10-13.md)）。

**覆盖目录是第二个来源，不是模块声明。** 配置项 `i18n.catalogs-dir` 指定一个目录，与声明同形：一个语言一个 `{tag}.json`，文件名是语言，正文是键到消息的 JSON 对象；引擎用声明目录同一条装载路径读它（`i18n.Catalog{FS: os.DirFS(dir), Pattern: "*.json"}`）。它未设定、指名的路径不是一个存在的目录、或目录里没有 `*.json` 文件时，都读作「没有额外文案」，是合法配置，不失败。读出的条目并入同一索引，排在全部已声明目录之后：同名键上覆盖目录胜过声明，一个声明没有承载的语言因此进入索引、受支持的语言集与协商匹配器。这一读取只发生在装配期，此后不再读、不被监视。

`Prepare` 对收集到的每份目录逐项校验，任一不合格即启动失败：

- **每份声明目录指名了文件系统与通配**，通配合法且至少命中一个文件，每个文件按它承载的语言标签命名。
- **每个声明文件是一个 JSON 对象**，字符串到消息原文的映射。
- **跨模块的同名键不冲突**，冲突时点名该键与两个声明方——键带命名空间，冲突是命名空间被两个模块共用的信号。覆盖目录不参与这一项：它不声明归属，只在同名键上取胜。
- **配置的默认语言有覆盖**：索引非空时，`i18n.default-locale` 所指的语言至少有一份条目，使一个没有指名的请求与每一条回落都落得到一个目录承载的语言。这一项在并入覆盖目录之后判，因而覆盖目录可以补上默认语言。
- **覆盖目录里每份文件是一个合法目录**：文件名是一个语言标签，正文是一个字符串到字符串的 JSON 对象；不满足时以 `ErrInvalidOverlay` 终止启动并点名该目录与该文件。

`New` 把全部声明目录与配置指定的覆盖目录装载进内存索引、构造匹配器，并把取用到的 `Profile` 与配置的默认语言一并收进引擎，此后只读。受支持的语言集由此确定：它是索引里出现的语言的并集——含只由覆盖目录承载的语言——代码不枚举它。

### 语言解析链

一次请求的语言按三级定位（[ADR-i18n-locale-resolution-2026-10-13](../../adr/adr-i18n-locale-resolution-2026-10-13.md)）：

| 次序 | 来源 | 何时命中 |
|---|---|---|
| 一 | 请求上下文 | `i18n.http` 的两层之一已写进语言 |
| 二 | 账户偏好 | 上下文无语言而身份的属性集带 `i18n.locale` |
| 三 | 配置的默认语言 | 两者都没有 |

**解析链在 `Resolver` 上收口。** `Default` 报告配置的默认语言；`LocaleOf` 读上下文，未携带时给 `Default`；`LocaleFor(ctx, account)` 在上下文无语言时，经取用的每一个 `Profile` 读该账户存储的语言。两级回落都归引擎，前两级都不报告失败——“没有指名”读作落到 `Default`，而不是一次错误。

### 协商

`Negotiate` 把 `Accept-Language` 的头值交给匹配器，在受支持的语言集中选出最匹配的一个，并报告是否有匹配。**无匹配是 `false`，不是配置的默认语言**：调用方把 `false` 读作“本次不给语言留痕”，与“用配置的默认语言”是两回事，头里一个受支持语言都没有时那两层因而不写上下文，让解析链后面的级去定。

### 阶段行为

| 阶段 | 行为 |
|---|---|
| `Prepare` | 引擎校验已声明的目录与配置指定的覆盖目录；其余模块无动作 |
| `New` | 引擎建立索引（并入配置指定的覆盖目录）与匹配器、取用 `Profile`；`i18n.account` 取用 `authn.Accounts`；`i18n.http` 无动作 |
| `Init` | `i18n.http` 在每端注册两层中间件；其余模块无动作 |
| `Start` | 无动作 |
| `Serve` | 无动作 |
| `Stop` | 无动作 |
| `Close` | 无动作 |

`i18n.http` 的中间件注册跑在 `Init`，此时 `http.Router` 处在接受登记的窗口内（见 [http](http/design-http.md) 的模块形状）。`i18n.account` 的 `Provisioner` 在首登开户之前被引擎调用，调用时机在认证的登录路径上。

## 错误与失败语义

启动期的失败一律终止启动，不降级、不跳过。**引擎为自己判定出的每一类失败给出哨兵错误**，调用方以 `errors.Is` 判别，包裹一律用 `%w`。

| 哨兵错误 | 触发条件 | 修复动作 |
|---|---|---|
| `ErrInvalidCatalog` | 一份声明的目录不合格：未指名文件系统或通配、通配不合法或没有命中文件、文件不是按语言标签命名、文件不是一个字符串到字符串的 JSON 对象 | 点名声明它的模块与文件；修正目录声明或消息文件 |
| `ErrDuplicateKey` | 一个消息键被多于一个模块声明 | 点名该键与两个声明方；其中一方改键的命名空间 |
| `ErrNoCatalog` | 索引非空而配置的默认语言没有任何条目 | 点名那个语言；补一份该语言的目录，或改 `i18n.default-locale` |
| `ErrInvalidOverlay` | 配置的覆盖目录里某份文件不合格：文件名不是一个语言标签，或正文不是一个字符串到字符串的 JSON 对象 | 点名该目录与该文件；修正覆盖文件，或去掉该配置项 |

**渲染期没有失败路径。** `Localize` 按请求语言、配置的默认语言、键本身依次回落，结果永不为空；一个未受支持的语言只是落到配置的默认语言，一个目录里没有的键只是把键写回。`Translate` 不回落，它把“目录里有没有这个键”以第二个返回值交给调用方，缺键时返回空串而不是错误。

**协商的无匹配不是失败。** `Negotiate` 报告 `false` 是合法结果，调用方据此不写上下文；把它当成错误会在每个不接受任何受支持语言的请求上写出一次不存在的故障。

**取不到某个模块声明为构造期依赖的功能时，该模块把 `core` 的 `ErrMissingProvider` 原样外传**，不另立哨兵：`i18n.http` 要 `http.Router` 与 `i18n.Negotiator`，`i18n.account` 要 `authn.Accounts`，其中任一缺失都在依赖解析处以它终止启动，补救动作是宿主纳入相应的提供者（见 [core](design-core.md)）。

### 非保证

以下各类性质本模块声明为不保证。

- **不保证目录穷举。** 一个模块没有为某个键声明任何语言的条目时，该键按回落规则渲染；本功能目录不检查键的覆盖面，只检查配置的默认语言至少有一份。
- **不保证发现指向不存在路径的覆盖目录。** `i18n.catalogs-dir` 指名的路径不是一个存在的目录、或目录里没有消息文件时，引擎读作没有额外文案，不失败、不记诊断；宿主以为覆盖已生效而实际按声明的文案渲染，两处都不报错。
- **不保证协商结果与账户偏好一致。** 协商只看请求头，账户偏好只在上下文尚无语言时补位；一个已带 `Accept-Language` 的已登录请求按头值渲染，不按账户偏好。
- **不保证账户偏好的即时生效。** 账户属性经账户快照读取，宿主经管理面改了属性，要等快照缓存过期才进身份与解析链（见 [authn](authn/design-authn.md) 的非保证）。
- **不保证 `i18n.http` 覆盖到一个端。** 它在遍历 `http.Endpoints` 得到的每个端注册；一个端不在那次遍历里、或那个端由宿主单独装配而不经本模块，就不带本地化，启动期不失败，也没有一行诊断指向它（与 [http](http/design-http.md) 的漏写同源）。
- **不保证一次拒绝的正文语言与请求语言始终相同。** 解析链给的是上下文里已经定下的语言；一个在认证之前就写出的拒绝没有身份，语言只到协商这一级，渲染它的模块据此取文案。

## 并发与事务边界

**索引在装配之后只读。** 装载发生在生命周期驱动的单协程里，此前没有任何请求路径可达它；装载完成后索引与匹配器不再变化，渲染与协商因此是无锁、零 IO 的内存读。这是本功能目录没有热加载接口的直接结果：内嵌声明与配置指定的覆盖目录都在装配期读取，一个要求运行期更换目录的功能会引入一把读路径上的锁，本功能目录以“装配期读取后只读”免掉了它（[ADR-i18n-catalogs-2026-10-13](../../adr/adr-i18n-catalogs-2026-10-13.md)）。

**两层中间件不改共享状态。** 它们只把语言与时区写进本次请求的上下文，一次请求一份，请求之间不共享可写状态。引擎交付的那些接口方法的并发调用是安全的：它们只读索引与匹配器。

**本功能目录不发起事务。** 它不写数据库：`i18n.account` 读账户属性经既有账户接口，写偏好的路径（自助面的偏好路由、开户时的写入）落在 [authn](authn/design-authn.md) 的账户模块与它自己的事务里，本功能目录只交出一个字符串给它写。

## 扩展点

- **`Profile` 是非排他接缝。** 任何能读到账户语言与时区的模块实现它，引擎用 `core.ResolveAll` 取用每一个，一个都没有是合法配置。`i18n.account` 是内置的一个实现。
- **`Catalog` 是声明式的接缝。** 任何模块以 `//go:embed` 声明自己的目录，引擎取可用集合并进索引；新增一种语言的覆盖是加一份目录，不是接入一个模块。
- **配置项 `i18n.catalogs-dir` 指定的覆盖目录是第二个文案来源。** 运维经它不改二进制、不接入模块地覆盖或补一条文案、补一种语言；与声明目录同形，装配期读取并入索引，同名键上胜过声明目录。
- **`ErrorResponder` 是非排他接缝，归入口模块。** 一个拥有某套 API 错误形状的模块交付自己的实现替换默认形状；它只决定形状，不承载语言。没有实现时，[http](http/design-http.md) 写出默认的 `application/problem+json`。
- **`Localizer`、`Negotiator`、`Resolver` 由引擎排他交付，不是可替换的后端。** 语言集是进程级的一份，多于一个引擎即启动失败；换一套语言匹配是改本功能目录的代码，不是接一块实现。

## 实现状态

| 能力 | 状态 | 代码 |
|---|---|---|
| 根包的类型与接口 | 已实现 | `pkg/i18n/i18n.go` `Locale`、`ConfigNamespace`、`DefaultLocaleItem`、`Key`、`Args`、`Localizer`、`Negotiator`、`Resolver`、`Profile`、`Catalog`、`LocaleResolver` |
| 上下文的存取 | 已实现 | `pkg/i18n/context.go` `WithLocale`、`FromContext`、`WithTimeZone`、`TimeZoneFromContext` |
| 账户属性键与形状校验 | 已实现 | `pkg/i18n/attributes.go` `LocaleAttributeKey`、`TimeZoneAttributeKey`、`ValidLocaleTag`、`ValidTimeZone` |
| 目录的收集、校验与装载、默认语言的配置项、语言解析与协商 | 已实现 | `pkg/i18n/catalog` `Module`、`ModuleName`；`pkg/i18n/catalog/config.go` `moduleConfig`、`readConfig` |
| 配置指定的覆盖目录的读取与并入 | 已实现 | `pkg/i18n/catalog/engine.go` `readOverlay`、`newEngine`、`ErrInvalidOverlay` |
| 每端两层的语言与时区中间件 | 已实现 | `pkg/i18n/http` `Module`、`ModuleName` |
| 账户偏好的读桥与开户时写入 | 已实现 | `pkg/i18n/account` `Module`、`ModuleName` |
| 登录语言的原样传递 | 已实现 | `pkg/authn/login/start.go` `localeOf`；`pkg/authn/login/internal/selfservice/phone.go` `smsLocale` |
| 自助面的偏好路由 | 已实现 | `pkg/authn/login` 的账户自助路由 |
