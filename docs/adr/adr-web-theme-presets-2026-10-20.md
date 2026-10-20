# 主题预设是纯数据的 token 覆写，效果由 ui-kit 映射，预设不承载组件覆盖

## 状态

已接受

## 背景

token 树是一棵默认树加一次深合并，层数与层名由调用方决定；白标已有项目层与租户层，但没有「传统」「时尚」这类成品风格，也没有任用户切换的通道。同一模板服务不同客户，视觉风格是客户之间的一类差异。

表面系统已把填充、描边、模糊与阴影收进语义表面 token，并经主题映射绘制（[ADR-web-surface-system-2026-10-20](adr-web-surface-system-2026-10-20.md)）。风格只能建立在它之上，不能另造一套效果机制。断点与层级不是风格：`md` 与 MUI 默认相等是布局契约，层序被侧边面板与对话框依赖。

`@speed/tokens` 的运行时面只有 `defaultTokens` 与 `deepMerge`，不认识任何具名风格；风格数据需要另一个归宿。

## 考虑过的选项

### 把预设并入 `@speed/tokens`

**否决**：tokens 是机制（一棵默认树加一次合并），层数与层名由调用方决定；让机制认识具名风格，每新增一套风格都要改平台包，运行时面也不再只有两个符号。

### 预设直接携带 MUI `components` 覆盖

**否决**：效果映射是适配器的事。预设与主题工厂成为两个效果来源，彼此漂移；预设里的 `components` 也到不了 `useTheme()` 与宿主组件，读取方拿到两个不一致的答案。

### 在 `ui-kit` 里加一个风格开关

**否决**：风格是 token 的取值，不是主题工厂的模式开关。开关与 token 成为两个来源，传了 token 却没传开关的宿主得到不一致的结果。

### 独立纯数据包承载预设，`ui-kit` 负责合并、映射与切换

**采纳**，见决策。

## 决策

采纳：**新建 `@speed/theme-presets` 纯数据包，每个预设是一份 `TokensOverride`；`ui-kit` 在项目层之前叠加预设层，并把新增的设计取值映射到组件；效果映射不写进预设。**

- 与 `@speed/tokens` 同层同性质：只有数据与类型，不 import React，不 import MUI，只 `import type { TokensOverride }`，没有运行时依赖。
- 预设可改的段排除 `breakpoints` 与 `zIndex`（`ThemePresetTokens = Omit<TokensOverride, 'breakpoints' | 'zIndex'>`），由类型排除并有测试守住随包预设。
- 装配层序为 `defaultTokens → 预设 → projectTokens → tenantOverrides`：客户品牌色与项目级 token 压过预设，租户层最高。
- `AppThemeProvider` 新增 `presets` 与 `defaultPreset`，新增 `useAppThemePreset()`；`createAppTheme` 的签名不变。活动预设是 provider 的局部状态，与明暗模式正交；`presets` 缺席或为空时不叠预设层；`defaultPreset` 不在列表内时取列表第一项，不抛错；没有 provider 时读取抛错。
- 纯附加的 token 扩展：表面角色的 `opacity`（0–1）、排版的 `textGlow`（`text-shadow` 值）、背景的 `canvas`（明暗各一套 `from`/`to` 色标）。这些字段缺席时默认树逐字节不变。`opacity` 乘在已按模式解析的填充色上；`canvas` 只接受两套色标，是表面系统「本轮不引入背景图」边界的有意延伸。
- 新增取值的映射归 `ui-kit` 适配器：`surface.<角色>.opacity < 1` 时把填充色转成对应 alpha 的 rgba，明暗两种模式各自转换，非十六进制颜色原样保留；`MuiCssBaseline` 的 body 背景色取 `surface.page.fill`（缺省等于 `palette.background.default`，对现有宿主是 no-op）；`textGlow` 落到 body 的 `text-shadow`；`canvas` 落到 body 的 `background-image` 与 `background-attachment: fixed`，浅色取 `canvas.light`、深色取 `canvas.dark`。模糊、描边与阴影已由表面映射绘制，不需要新映射。
- 随包预置 `classic`、`modern`、`compact`、`glass`、`ascii` 与基线 `default`。预设不持有文案，显示名归宿主 i18n；包不注册命名空间，也不 import i18n。
- 切换入口在用户设置页，不占外壳 chrome；`account-ui` 交付受控的 `AppearanceSection`，预设列表与显示名由宿主提供。
- `ui-kit` 对 `@speed/theme-presets` 是类型依赖（只 `import type { ThemePreset }`），预设数据不打进运行时；`package.json` 声明该依赖，构建的 filter 链加入该包。

## 后果

- 客户新增一种风格 = 写一份 `ThemePreset` 数据交给装配，平台包不变；随包预置只是随包交付的起点。
- 预设改不了 `breakpoints` 与 `zIndex`：这两者是布局与层叠的功能契约，需要改它们的需求不是风格需求。
- 预设不携带组件覆盖：新效果的落点是主题工厂与适配器，不是预设；这是表面系统「整体换风格只改一处」的延续。
- 预设层位于项目层之下：客户品牌色始终压过预设，预设对主色与强调色的取值在那些客户处不显现。
- 半透明与画布的对比度由预设作者负责：半透明填充按 `opacity` 合成到该模式画布的两个色标上取最坏值，正文对比度仍须不低于 4.5:1，明暗两套各算一次。
- 改变间距基数的预设（`compact`）会改变所有按 `spacing` 计算的尺寸；触摸目标下限由外壳与控件自身保证，窄栏宽度取 `max(theme.spacing(7), 44)`（见 [ADR-web-shell-layout-variant-2026-10-20](adr-web-shell-layout-variant-2026-10-20.md)），不由预设重新裁定。
- 预设显示名不在包内：新增预设时宿主须补语言包条目；未补时显示名没有来源。
- 活动预设是 provider 的局部状态，不进 URL、不进会话；刷新后的恢复由宿主的设备存储完成，部署配置的默认只作用于没有设备选择的访问。
- 新增的 token 字段都是可选字段，缺席时默认树与主题逐字节不变；`defaultTokens` 仍深度冻结。

## 失效条件

- MUI 原生提供主题预设机制，或半透明表面与画布渐变可由主题面直接表达：本包的适配器映射与 token 扩展随之收缩。
- 客户的风格需求需要改动断点、层级或组件覆写才能满足：「预设只承载设计取值」的边界不再成立，重新裁定预设可改的段。
- 随包预设无法在明暗两种模式下同时满足对比度下限：表面系统需要新的合成能力，而不是在预设里补颜色。
