# 颜色模式偏好跟随账户，系统偏好由主题 provider 解析

## 背景

界面需要三种颜色模式：深色、浅色、跟随设备。前两种是显式选择，第三种要随操作系统的明暗偏好实时变化。

主题工厂只接一个已解析的模式（`light` 或 `dark`），它决定 `palette.mode`（[REF-mui](../references.md)）与两套表面取值；系统偏好是一次媒体查询，与构建主题无关。

语言与时区偏好存账户属性集（[ADR-i18n-preferences-storage](adr-i18n-preferences-storage.md)），换设备仍然生效。颜色模式同属界面偏好，只存设备时换设备不会继承。

账户档案是一次异步读：首屏在它返回之前就要渲染，而首屏的配色不能等它。

## 考虑过的选项

### 只存设备

沿用浏览器 localStorage，与主题预设相同。

**否决**：换设备不继承，与语言、时区的行为不一致；同一类偏好出现两种存储位置。

### 保留页头二态按钮，只在偏好页补第三态

页头继续提供深/浅切换，偏好页另有三个选项。

**否决**：同一偏好两个入口，且页头按钮无法表达“跟随设备”当前解析到哪一态：按下它就是一次显式选择，与偏好页显示的值立刻不一致。

### 把 `system` 作为主题工厂的入参

让 `createAppTheme` 自己判断 `system` 并读媒体查询。

**否决**：工厂会因此持有订阅与重渲染时机，而它是一份纯函数式的 token 映射；同一套解析还需要在 provider 里再有一份才能响应变化。

### 新增第二个 provider 入参 `defaultPreference`，保留 `defaultMode`

**否决**：两个入参同时给出时没有自然的优先级，且现有宿主已经按 `defaultMode` 装配。

## 决策

采纳：颜色模式是账户属性 `presentation.color-mode` 上的封闭取值 `light`、`dark`、`system`，作为 `PATCH B/me` 的 `color_mode` 字段自写。

主题 provider 持有的是**偏好**而不是模式：`system` 由它经 `prefers-color-scheme` 解析为 `light` 或 `dark`，并在偏好为 `system` 期间订阅该媒体查询的变化；主题工厂仍只接收已解析的两个值。

设备 localStorage 保留一份镜像，只用于账户档案读回之前的首屏。账户值非空时，档案读回后以账户值覆盖镜像与当前偏好。

## 后果

- 账户未保存过该值时，沿用设备镜像与系统偏好，不改写账户；写入只发生在偏好页的选择上。
- 三态只在 provider 一处解析，所有使用方读到同一个已解析的 `mode`，主题预设仍是与之正交的另一份状态。
- 首屏与账户值可能短暂不同：镜像落后时，档案读回会覆盖一次。这是异步档案读取代价的一部分。
- 颜色模式进入自写字段集，该集合仍封闭；再加字段要有新的决定（[ADR-authn-selfservice-profile](adr-authn-selfservice-profile.md)）。
- 匿名访问界面没有账户，只按设备镜像与系统偏好呈现，不提供写入。
- `AppThemeProviderProps.defaultMode` 接受三态，`AppThemeModeControl` 以 `preference`、`setPreference` 取代 `setMode`、`toggleMode`；这是可交付包公开类型的变化。

## 失效条件

- 界面需要在同一次渲染里呈现多个账户各自的颜色模式：账户级存储与单一 provider 状态都不再够用。
- 首屏与账户值的一致性成为硬要求：档案读回之前的镜像机制要被同步来源取代。
