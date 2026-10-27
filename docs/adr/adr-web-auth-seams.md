# 认证复用接缝的归属

## 背景

`@speed/auth-core` 是会话生命周期的无 UI 包，主入口承诺无 DOM，位于 `@speed/api-client` 与 `@speed/api-sdk` 之上、各 UI 包之下。`auth-ui`、`account-ui` 与 `console-ui` 是同层的能力 UI 包，同层之间不建依赖（[design-web](../design/modules/web/design-web.md)、`.claude/skills/frontend-coding-standards`）。

复用者是从 `starters/demo` 拷出骨架、按版本区间安装 `@speed/*` 的产品，改不了包源码，只能用包公开的 props、导出与子路径。因此凡宿主合理会改或合理会直接使用的行为，都必须由包提供接缝。

认证族里同一段逻辑各有本地副本：通行密钥仪式驱动在 `auth-ui`、`account-ui` 与 `console-ui` 各有一份（`auth-ui` 版多一个可取消的 `signal`）；步进门控在 `account-ui` 与 `console-ui` 各有一份（前者记住任意动作，后者记住闭包并带 `intercept`）；流程步渲染器在 `auth-ui`、`account-ui` 与 `console-ui` 各有一份。副本已经漂移，同一个修正只到其中一处。步进对话框同样在 `account-ui` 与 `console-ui` 各有一份，而两者都要被宿主使用。

## 考虑过的选项

### 步进门控留各包

**否决**：两份门控已经漂移——一份记动作、一份记闭包并带 `intercept`，同一处修正不会同时到达两边；宿主也拿不到任何一个可直接复用的门控。

### 步进对话框由 `auth-ui` 导出

**否决**：`auth-ui` 是登录面，对话框服务于账户页与控制台的提权写；由它外导会让 `account-ui` 与 `console-ui` import `auth-ui`，那是一条同层边，而且方向与「登录面不拥有提权对话框」相反。

### 通行密钥仪式走 `auth-core` 主入口

**否决**：主入口的无 DOM 承诺由设计文档与包 `AGENTS.md` 共同固定；`navigator.credentials` 一旦进入主入口，只读会话的宿主也会加载触及 DOM 的代码。子路径把 DOM 触点隔离在一处，主入口不受影响。

### 通行密钥仪式留 `auth-ui`，另两包 import 它

**否决**：同层边；`auth-ui` 还会获得一件它不拥有的公开面（通行密钥仪式不是登录界面），而该仪式的驱动逻辑与它渲染的页面无关。

### 流程步渲染器下沉 `auth-core`

**否决**：它渲染 UI，而 `auth-core` 无 UI、无 DOM。下沉到 `ui-kit` 也不行：`ui-kit` 不认识流程步的语义，把步骤渲染塞进通用层会把能力语义带进基础组件库。

### 按层放接缝

**采纳**，见决策。

## 决策

按「逻辑与 DOM 触点归最低层、渲染归拥有该流程的 UI 包、同层不建边」的判据放置认证族的复用接缝：

- 通行密钥仪式驱动归 `@speed/auth-core`，经 `./webauthn` 子路径交付；主入口不引用它。
- 泛型 `useStepUpGate` 归 `@speed/auth-core` 主入口，返回形状取 `account-ui` 现有门控的字段并加 `console-ui` 的 `intercept`。缺省判别认平台自己的步进拒绝码，会话自身能区分「强度不足」与「会话已结束」的调用方改传 `session.isStepUpShortfall`。
- 步进对话框由 `@speed/account-ui` 以 `StepUpDialog` 外导，**只给宿主用**。`console-ui` 与 `auth-ui` 不得 import 它；`console-ui` 保留自己的对话框副本。
- 流程步渲染器在 `auth-ui`、`account-ui` 与 `console-ui` 各自维持本地副本。同步义务登记在 `web/AGENTS.md` 与三包 `AGENTS.md`：一步的渲染变化时，三处在同一次改动里一起改。各包的标签词表归本包所有，不存在跨包共用词表。

## 后果

- `auth-core` 的公开面增加 `./webauthn` 子路径与门控导出；主入口保持无 DOM，只读会话的宿主不加载 WebAuthn 代码。
- `account-ui` 的公开面增加 `StepUpDialog`，它成为宿主的接缝，也是与控制台之间的复制面。
- `console-ui` 与 `account-ui` 的对话框副本不合并：同一对话框的样式与文案变化要在两处分别落地。接受的代价是重复维护。
- 流程步渲染器的副本继续存在，仍由人工保持同步；`console-ui` 包内的源码级平价测试（`web/packages/console-ui/src/internal/flow-step-parity.test.ts`）读取三份文件，比对渲染的步骤种类、答复属性、渲染原语与通行密钥接缝，漏改一处会由该测试点名失败。接受的代价：新增一步的渲染仍要改多处。
- `auth-ui`、`account-ui` 与 `console-ui` 的本地 WebAuthn 驱动不再保留，各包切换到 `./webauthn` 子路径时删除。
- `auth-core` 成为 WebAuthn 驱动的唯一持有处，也是步进门控的唯一实现处；`console-ui` 调用门控时显式传入 `session.isStepUpShortfall`，行为不变。

## 失效条件

- 步进对话框需要被 `console-ui` 或 `auth-ui` 直接复用：只给宿主的边界不再成立，对话框的归属要重新裁定。
- 流程步渲染器出现第四份副本，或其中两份合并：登记在三个包上的同步义务不再描述现状。
- `auth-core` 的主入口开始引用 `navigator.*`：子路径隔离 DOM 触点的依据消失，包级「无 DOM」的例外不再是一处。
