# 界面表面由语义角色 token、主题映射与 SectionCard 原语共同承担

## 背景

token 树只有两级背景（`background.default` 与 `background.paper`），而界面需要三种表面：页面底、内容容器与浮层。三者都经 `background.paper` 取值时，卡片与弹层同色，只靠阴影区分；没有容器的内容块直接落在页面底上。主题的 `components` 只覆盖触摸目标下限，`Paper`、`Card`、`Table` 没有表面默认值，表面由每个调用点临时决定，页面级因此并存手写描边 `Paper` 与裸 `Box component="section"` 两种做法。ui-kit 没有表面原语，把内容块统一成一种表面的动作没有落点。

需要的是一种分层：表面是设计系统给出的语义默认，不是每个调用点的临时选择；整体换风格（含玻璃效果）只改一处，页面代码不参与。

## 考虑过的选项

### 逐页手写 Paper

**否决**：表面仍由每个调用点决定，表格与卡片可以各自选择；整体换风格要逐页修改，任何遗漏都表现为局部割裂。

### 给 `createAppTheme` 增加一个 `surfaceStyle` 参数

**否决**：风格是 token 的取值，不是主题工厂的模式开关。参数与 token 成为两个来源，传了 token 却没传参数的宿主得到不一致的表面；主题值也到不了 `useTheme()` 与宿主组件。

### 在页面内写死玻璃样式（填充色与背景模糊）

**否决**：视觉风格进入页面代码，每个页面都要重写；一种风格的取舍分散在页面里，后续无法一次替换。

### 语义角色 token、主题映射与 SectionCard

**采纳**，见决策。

## 决策

采纳：**表面是 token 的语义段，经主题工厂解析为 `theme.surface` 并经 `theme.components` 应用，内容节由 ui-kit 的 `SectionCard` 承担。**

- **语义角色。** token 的 `surface` 段给出 `page`（页面底）、`container`（内容容器）、`overlay`（浮层）与 `header`（表头）四个角色。每个角色的 `fill`、`borderColor`、`borderWidth`、`elevation`、`backdropFilter` 全部可选，缺省表示继承主题本来就读取的槽，而不是写死一个颜色。
- **解析到主题。** ui-kit 主题工厂把继承关系解析成具体的 `theme.surface.<角色>`（经 `@mui/material/styles` 的模块扩充，[REF-mui](../references.md)），并产出读该值的 `theme.components` 覆写：`MuiCard` 的 `variant="outlined"` 取 container；`MuiDialog`、`MuiMenu`、`MuiPopover` 的 paper、`MuiTooltip` 的 tooltip 与 temporary `MuiDrawer` 的 paper 取 overlay；permanent 与 persistent 抽屉的 paper 取 container；`MuiTableCell.head` 取 header。`MuiPaper.root` 不设覆写，`MuiTableContainer` 不设表面。
- **内容节原语。** `SectionCard` 以 `Card variant="outlined" component="section"` 渲染一个带标题的内容节，是内容容器的唯一原语；表格节以 `flushContent` 让表格贴边，由卡片的裁切收住圆角。
- **风格是一次 token 覆写。** 玻璃效果由 `surface` 角色上的 `fill`、`backdropFilter` 与 `borderColor` 表达，页面代码不参与。

**两处对 MUI 的自愿偏离：**

- **表格不按官方示例再包一层 `TableContainer component={Paper}`。** 表面由外层 `SectionCard` 承担，表格自身不再绘制第二层表面。同一层叠两个表面会让表头底色与卡片底色成为两个来源，换风格时无法一起移动。
- **焦点环写在文档化的 `styleOverrides` 槽里，不使用未文档化的 `theme.focusVisible`。** 焦点环以 `&.Mui-focusVisible` 下的 `outline` 与 `outlineOffset` 落在基础控件的覆写里，颜色取 `palette.primary.main`。该写法只依赖 MUI 文档列出的主题组件面；代价是新增的基础控件要各自获得这段覆写才参与统一焦点环。

## 后果

- 表面风格只有一处来源：改一个 token，内容容器、浮层、表头与页面底一起改变；页面只做组合。
- 容器与浮层有独立的语义角色，弹层不再与卡片同色而只靠阴影区分。两者的继承槽相同，需要区分时由 token 覆写给出。
- 表头底色是独立角色，不与行悬停态共用 `action.hover`，表头与悬停行不会同色。
- `theme.surface` 是新增的公开主题字段：`Theme` 上必填，`ThemeOptions` 上可选。宿主不经 `createAppTheme` 自行组合主题时，主题选项允许缺席该字段，读取方需处理缺席。
- 四个角色是闭合的语义集。新增一种表面是 token 段与主题映射的变更，不是页面里的局部选择。
- 可选字段的缺省是「无」而不是「沿用某个默认视觉」：`borderWidth` 缺省为 0，只有 container 的 token 默认声明 1。需要描边的角色必须声明宽度，只声明 `borderColor` 不画线。
- `header` 的底色取中性色阶而非背景槽，覆写中性色阶会连带改变表头。
- 手写 `Paper` 或裸 `section` 表面与 `SectionCard` 并存会使同一页出现两种表面来源；设计上只有后者是内容节的表面。
- 按官方 Table 示例在 `SectionCard` 内再包 `TableContainer component={Paper}` 会叠出第二层表面；这条偏离必须随设计文档传递。

## 失效条件

- MUI 提供一等公民的表面角色或 `theme.surface` 约定：以 MUI 的公开面替换本包的模块扩充。
- 一次整体换风格仍需要改页面代码而不只是覆写 token：分层未成立，重新评估。
- 出现四个角色覆盖不了的表面，或角色之间的继承关系无法用「缺省即继承」表达：重新裁定角色集。
