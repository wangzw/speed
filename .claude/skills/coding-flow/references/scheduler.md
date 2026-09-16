# Scheduler

> 共享的角色分工、Issue 状态机、结构化评论格式见 [../SKILL.md](../SKILL.md)。本文件只记录 Scheduler 专属的规则。

## 职责范围

不做具体工作，只协调：轮询 Issue 状态、解析结构化评论、派发 Designer/Coder/Reviewer/`dashboard-render`；同时是人类下达指令的直接入口（本机常驻会话本身就是交互界面）。

## 轮询节奏

**默认轮询间隔：90 秒**，可通过配置覆盖（如 `.claude/coding-flow/config.yml` 的 `poll_interval_seconds`）。每次全量扫描所有打了 `phase:*` 标签且未 closed 的 Issue，读最新结构化评论判断是否需要动作。单人项目量级小，不做分级轮询。人类可以直接在 Scheduler 会话里手动催办某个 Issue，绕开轮询等待。

**超时检测是常规轮询的一部分**：每轮同时检查所有 `in_progress`/`queued` 的 workflow run 是否已超出正常时长阈值，超时则标记异常并在输出里提示人类，不自动重派——避免 job 在线上卡死却无人知晓。

注意 `timeout-minutes` 是 GitHub Actions **硬性墙钟耗时上限**，从 job 启动那一刻算起，不是"多久没输出就判定卡死"的空闲检测——不管 job 当时进展到哪，一到这个时间点就会被 GitHub 强制杀掉，跟任务是否复杂、是否还在正常产出无关。所以这个值必须留足够余量让复杂任务（尤其实施类任务，真实场景下可能要跑数小时）能在被杀之前主动收尾、提交结构化评论；定得太短会导致任务被强杀却连一条 `continue` 评论都留不下，Scheduler 完全感知不到发生了什么。

默认阈值（Scheduler 判定异常 = job `timeout-minutes` + 5~20 分钟排队缓冲，避免把"排队中"误判为"卡死"）：

| workflow | job `timeout-minutes`（GitHub 硬上限） | Scheduler 判定异常的阈值 |
|---|---|---|
| `designer-planning.yml` | 180（3 小时） | 200 分钟 |
| `coder-implementation.yml` | 300（5 小时） | 320 分钟 |
| `reviewer-audit.yml` | 240（4 小时） | 260 分钟 |
| `dashboard-render.yml` | 5（确定性脚本任务，不涉及 agent 推理，不适用这套逻辑） | 10 分钟 |

`coder-implementation.yml` 定得最长，是因为实施任务可能涉及大代码库的构建/测试和多轮对抗性审查迭代，真实场景下经常要跑数小时；GitHub 托管 runner 本身有 **360 分钟（6 小时）的硬性上限**，300 分钟已经接近这个天花板——如果目标项目经常需要更长时间，要么换自托管 runner（没有该硬限制），要么更依赖"跨运行迭代"机制：不追求单次运行内做完所有事，保证每次运行都能有意义地推进、并在时限内安全收尾提交，下一轮接着来（见 `references/coder.md`「收敛判定」）。这些数值是初始默认值，具体项目可按自身构建/测试耗时调整。

## 离线恢复

设计原则：**正确性不依赖本地缓存，缓存只是性能优化**。恢复不是"回放丢失的事件"，而是每次都做一次全量对账，用 GitHub 上的实时状态（label + 最新结构化评论 + 实际 workflow run 状态）重新推导当前该做什么。

Scheduler 会话启动时执行一次恢复对账，之后转入常规轮询：

1. `gh issue list` 拉取所有打了 `phase:*` 标签且未 closed 的 Issue。
2. 对每个 Issue，按「主循环」定义的同一套判断逻辑处理一遍（先看 `status:blocked`/`status:awaiting-human`，都不是才落到读最新结构化评论的 `action` 字段）——离线恢复本质上就是把主循环这套逻辑一次性跑遍所有 Issue，**不是另一套独立规则**；两处逻辑分开写、各自维护，正是早前版本出现「摘状态后又用一条过期的历史评论 action 字段判断」这类 bug 的根源，所以这里不重复描述，只引用。
3. **幂等防重复派发**（主循环第 4 步同一逻辑）：派发前先查 `gh run list --json displayTitle,status` 是否已有该 Issue+phase 对应的、仍在 `in_progress`/`queued` 的 workflow run（`displayTitle` 就是 workflow 里 `run-name:` 生成的名字，如 `coding-flow coder issue-42 phase:implementation`，直接按 issue 编号+phase 做字符串匹配即可，见「`gh workflow run` 调用的具体参数拼装」）。有则跳过，没有才真正派发——即使本地缓存丢失或过期也不会重复派发。
4. 用所有 `status:blocked` Issue 的 `blocked_by` 关系建依赖图做**循环依赖检测**，发现环则环上 Issue 一律转 `status:awaiting-human`。
5. 对超出正常时长阈值的 `in_progress` run 标记异常，不自动重派，留给简报提示人类。
6. 恢复完成后更新本地缓存，转入正常轮询。

**恢复后必须输出简报**（对话形式，不写进 GitHub）：扫描了多少 Issue、有几个因离线期间状态变化被重新派发（列编号+动作）、有几个仍在 `status:awaiting-human` 等待（列编号+问题摘要）、有几个 `status:blocked` 因依赖收敛而恢复、有没有检测到异常 run 需要人工确认。

**幂等缓存**：本地维护一份缓存（如 `~/.coding-flow/state.json`），记录每个 Issue 处理到的最后一条 `coding-flow:status` 评论 id，用于跳过没有变化的 Issue、减少 API 调用。这只是加速缓存，权威状态始终是 Issue 上的 label + 最新结构化评论，缓存丢失可重新扫全量 Issue 重建。

## 派发 Coder 时决定分支名

**分支由 Scheduler 决定，Coder 不自己发明**：格式 `<prefix>/issue-<N>-<slug>`。`prefix` 不固定为 `feat`，Scheduler 按对该 Issue 性质的判断而定（如 fix/chore/refactor 等）；`slug` 与该 Issue 保持一致（如从 Issue 标题派生）。首次派发 Coder 时生成分支名并通过 `workflow_dispatch` input 传入；同一 Issue 后续多轮派发复用同一个分支名。

## 跨 Issue 依赖联动

- `blocked_by` 解除条件是**依赖 Issue 被关闭**（该主题完全交付），不定义"部分收敛也算解除"这种中间态。"关闭 = 完全交付"这个假设成立，靠的是 Coder 合并前的 plan checklist 检查（见 SKILL.md「关闭 Issue 前检查 plan checklist」）——Scheduler 自己不重复验证，直接信任 Issue 的 closed 状态。
- 检测到循环依赖（见「离线恢复」第 4 步、也在常规轮询里做）时，直接转 `status:awaiting-human`，交给人类打破循环（比如决定谁先 mock 接口）。

## 看板发布

渲染是非交互式任务，由独立的 `dashboard-render` workflow 完成（查询 Issue 状态、套用 `templates/dashboard/index.html.template`、commit + push 到 `gh-pages` 分支），Scheduler 不直接渲染。Scheduler 只负责：

1. 每轮轮询循环结束后，判断状态是否有变化，有变化才派发一次 `dashboard-render`（避免空跑）。
2. workflow 跑完后，从 `gh-pages` 分支拉取刚发布的内容，镜像一份到本机 `.claude/dashboard/index.html`——这一步只是同步到本地文件系统，不是重新渲染。

## 主循环（单轮轮询）

对每个打了 `phase:*` 标签且未 closed 的 Issue：

1. `status:blocked` → 检查 `blocked_by` 目标是否已 closed；是则**摘掉 `status:blocked`，直接按当前 phase 标签重新派发对应角色**（等同于 `action: continue` 的处理）；否则跳过本 Issue。
2. `status:awaiting-human` → 检查是否已有 `/continue` 评论（且晚于上一条机器评论）；是则**摘掉 `status:awaiting-human`，直接按当前 phase 标签重新派发对应角色**（同样等同于 `action: continue` 的处理）；否则跳过本 Issue（继续等待）。
3. 都不是（正常运行中）→ 读最新一条 `coding-flow:status` 结构化评论（没有则说明是刚创建、还没跑过任何角色，按当前 label 直接走首轮派发）：
   - `action: continue` → 按当前 phase 对应角色重新派发同一个 Issue。
   - `action: advance` / `revert` → 按 `next_phase` 对应角色派发。
   - `action: need_human` / `blocked` / `done` → 不应该出现在这个分支（这些状态本该已经被打上对应 label 拦在第 1、2 步）；若出现视为异常，报告人类。
4. 派发前做幂等检查（见「离线恢复」第 3 步同一逻辑），Coder 派发时按「派发 Coder 时决定分支名」生成/复用分支名。

**关键点（此前版本的一处 bug）**：第 1、2 步摘掉 control-status label 之后，**不能**转去读最新一条结构化评论的 `action` 字段来决定派发什么——那条评论字面上还是触发阻塞/等待时留下的历史值（`action: blocked` 或 `action: need_human`），不代表现在该做什么，会被第 3 步的"异常"分支误判。第 1、2 步必须直接依据**当前 phase 标签**决定派发哪个角色，不经过第 3 步的判断路径。

全部 Issue 处理完后，判断是否需要派发一次 `dashboard-render`（见「看板发布」），然后 sleep 轮询间隔，重复。

## Phase → Workflow 映射

| 当前 phase | 派发的 workflow | 说明 |
|---|---|---|
| `phase:planning`，以及 `phase:design`（Issue 已存在时的中途状态，非最初的交互式设计） | `designer-planning.yml` | 同一个 workflow 承担规划和"设计自主处理/等人类恢复后继续"两种情况，由 Designer 角色内部按当前 phase 自行分流，见 `references/designer.md` |
| `phase:implementation` | `coder-implementation.yml` | 额外生成/复用 `branch` input |
| `phase:audit`（仅当人类主动要求或合并触发新审计时） | `reviewer-audit.yml` | 不是从常规轮询的"正常运行中"分支进入，是专门的触发事件 |

初始的交互式设计（Issue 尚不存在，或 Issue 刚创建、设计文档还没写）完全在人类本地会话里进行，Scheduler 不派发任何 workflow，只是等待人类通知。

## 轮询循环的具体实现

Scheduler 就是运行 `/coding-flow scheduler` 的这个 Claude Code 会话本身，不是另外一个独立进程——"轮询"就是这个会话反复执行「主循环」描述的步骤，用 `Bash` 工具跑 `gh` 命令。具体用哪种机制驱动"反复执行"：

- **首选：`/loop` skill**，以 90 秒（或配置的间隔）为周期反复触发本轮检查逻辑；这样轮询节奏、暂停/恢复都交给现成机制，不用自己写计时器。
- **等效替代：`ScheduleWakeup`**，每轮检查完调用一次，`delaySeconds` 设成轮询间隔，`prompt` 传回同一套"执行主循环"的指令，效果和 `/loop` 一致。

人类在会话里插话（手动催办某个 Issue）会打断当前这一轮，处理完人类的请求后回到正常轮询节奏，不需要专门的"打断恢复"逻辑——本来就是同一个会话在做这两件事。

## 结构化评论解析的具体实现

不用 `gh issue view --json comments` 一次性拉全部评论再在本地找最后一条（评论数多了以后有分页风险，见下）。直接用 `gh api` 配合 `--paginate`（自动翻页拼接结果）拿到全部评论：

```bash
gh api "repos/{owner}/{repo}/issues/<ISSUE_NUMBER>/comments" --paginate
```

**注意不能简单取 `.[-1]`（字面上最后一条评论）**：`status:awaiting-human` 期间人类可能自由回复好几条纯文本评论，字面最后一条很可能是人类的话，不含结构化块。正确做法是从后往前扫描，取**第一条真正包含 `<!-- coding-flow:status ... --> ` 块的评论**（`render.py` 的 `fetch_last_status_comment()` 就是这么实现的，Scheduler 应该用同一套逻辑，不要各写一份、字面语义还不一样）。拿到那条评论的 body 后用正则提取结构化块，按 `key: value` 逐行解析（不引入 YAML 库依赖，跟 `render.py`/`init.py` 保持一致的风格）。

## `gh workflow run` 调用的具体参数拼装

统一格式：

```bash
gh workflow run <workflow-file>.yml --ref <default-branch> -f key1=value1 -f key2=value2 ...
```

`--ref` 指向 workflow 定义所在的分支（通常是仓库默认分支，workflow 定义必须先合并到这个分支 GitHub 才认得到），不是被操作的 Issue 分支。按角色对应的具体调用：

```bash
# Designer（规划 / phase:design 中途恢复）
gh workflow run designer-planning.yml --ref main \
  -f issue_number=<N> -f phase=<phase>

# Coder（实施）
gh workflow run coder-implementation.yml --ref main \
  -f issue_number=<N> -f phase=phase:implementation -f branch=<branch>

# Reviewer（审计）
gh workflow run reviewer-audit.yml --ref main -f scope=<scope>

# 看板渲染
gh workflow run dashboard-render.yml --ref main
```

三个角色 workflow 模板都已经加上 `run-name:` 字段把 `issue_number`/`phase`（或 `scope`）编进 run 的名字，所以幂等检查不需要靠触发时间窗猜测，直接：

```bash
gh run list --workflow coder-implementation.yml --json databaseId,displayTitle,status -L 20
```

按 `displayTitle` 里的 issue 编号 + phase 字符串匹配、`status` 在 `in_progress`/`queued` 之列，即可判断是否已有对应的 run 在跑。
