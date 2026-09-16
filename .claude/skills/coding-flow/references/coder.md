# Coder

> 共享的角色分工、Issue 状态机、结构化评论格式见 [../SKILL.md](../SKILL.md)。本文件只记录 Coder 专属的规则。

## 职责范围

依据 plan 实施，内置对代码的对抗性审查环节，收敛后触发合并。由 Scheduler 派发，在 Action 中运行。

**任务终点是"代码合并进 main"**：合并失败（冲突/未过 required check 等）视为任务尚未完成，不是新的异常分支——能自己处理（如 rebase）就处理，处理不了则和"本阶段需要再来一轮"一样，结构化评论 `action: continue`，留在 `phase:implementation`，Scheduler 照常再派一轮。

## 分支与合并

- **分支由 Scheduler 决定，不由 Coder 自己发明**：格式 `<prefix>/issue-<N>-<slug>`，Scheduler 通过 `workflow_dispatch` input 把具体分支名传给 Coder。`prefix` 不固定为 `feat`，按 Scheduler 对该 Issue 性质的判断而定（如 fix/chore/refactor 等）；`slug` 与该 Issue 保持一致（如从 Issue 标题派生），确保同一 Issue 多轮运行使用同一个分支。
- Coder 按自身流程走 PR 合并、不直接 push main；main 是否用分支保护规则强制挡住直接 push，是目标项目自己的选择，本 skill 不要求、不假设。
- **合并动作由 Coder 自己执行**（`gh pr merge`）。门禁具体要不要包含测试/lint 等 CI 项，是目标项目自身分支保护规则的事，本 skill 不硬编码，只保证自己负责的对抗性审查已经收敛。
- Git/GitHub 操作必须用独立 PAT（见 SKILL.md「凭证与权限」），不能用默认 `GITHUB_TOKEN`，否则开出的 PR 不会级联触发目标项目原有的 CI。

## 执行流程

每次运行由 Scheduler 通过 `workflow_dispatch` 触发，接收 `issue_number`、`phase`、`branch` 三个 input（`branch` 由 Scheduler 决定，见「分支与合并」）。整个流程写成一个 Claude Code Workflow 脚本（见 SKILL.md「非交互式任务一律用 Claude Code 的动态 Workflow 工具实现」），大致分以下步骤：

1. **拉取上下文**
   - `git checkout` Scheduler 指定的 `branch`（不存在则从 main 新建）。
   - `gh issue view <issue_number>` 取出 Issue 正文和最新一条结构化评论，还原当前 plan：有已存在的本地副本（`.claude/plan/<issue_number>.md`）就直接用，否则从 Issue 内容重建落盘（见 SKILL.md「Plan 文档的存放约定」）。

2. **实施**
   - 按 plan 里未完成的任务项逐条推进；开始一项前发一条轻量评论说明要做什么，完成后**立刻**（不等运行结束）发评论说明结果 + 把 Issue 正文里 plan 对应的 checklist 勾掉（本地 `.claude/plan/` 副本只是工作副本，最终以正文为准，见 SKILL.md「及时更新 Issue，准确反映进度」）。若这一批任务是并行处理的，checklist 更新按 SKILL.md 里的并发安全规则批量做一次，不要多个并行 subagent 各自抢着改正文。
   - 涉及 bug fix 的任务，按目标项目自身的测试规范补一个能复现问题的测试用例——测试是否落地是判断本项任务"是否完成"的一部分，不是可选项。
   - 若发现 plan 里某一项在当前代码库里已经不成立（比如假设的文件/接口已不存在），按「上报与阻塞」处理，不要即兴替代着写。

3. **对抗性审查（用 Workflow 工具实现 produce → verify）**
   - `agent()`：先对本轮的 diff 做一遍自查，列出潜在问题（维度参考 `code-review` skill：正确性、复用、简化、效率）。
   - `agent()`（独立 fork，不共享上一步的推理过程）：只拿 diff + plan + 验收标准，逐条对抗性验证每个问题是否成立（CONFIRMED / 不成立）。
   - `pipeline()` 串联两步；只有 CONFIRMED 的问题需要处理。

   **produce 步骤 prompt：**
   ```
   审查以下代码改动（diff 见 {{diff_path}}），对照 plan（{{plan_path}}）里这一项的验收标准。

   只关注这几个维度，不要泛泛而谈：
   1. 正确性——有没有逻辑错误、边界条件遗漏、并发/竞态问题；bug fix 类任务，新增的测试是否真的复现了原问题（不是随便断言一个不相关的点）。
   2. 复用——有没有本该复用现有函数/工具却重新实现的地方。
   3. 简化——有没有可以删掉但没删的过渡代码、不必要的抽象。
   4. 效率——有没有明显的性能反模式（不要吹毛求疵到影响交付节奏）。

   每个问题给出：文件+行号、具体问题描述、触发场景（什么输入/时序会暴露这个问题）。
   没有把握的问题也列出来，交给下一步验证，不要自己先筛掉。
   ```

   **verify 步骤 prompt（独立 fork，不传入上一步的推理过程，只传 diff + plan + 上一步列出的问题清单）：**
   ```
   下面是对一份代码 diff 提出的问题清单。你的任务是对每一条做对抗性验证，不是简单认同。

   对每一条问题：
   1. 重新独立审视 diff 和 plan 验收标准，判断这个问题是否真实存在。
   2. 如果存在，给出具体会失败的输入/场景（不能只是"可能有问题"这种模糊说法）。
   3. 标记为 CONFIRMED 或 NOT_REAL，附一句理由。

   偏保守：拿不准就标 NOT_REAL 并说明为什么证据不足，不要为了凑数量放行不确定的问题。
   ```

4. **收敛判定**
   - 所有 CONFIRMED 问题都已修复并重新验证通过 → 收敛，进入步骤 5。
   - 还有能力范围内可修复的问题 → 回到步骤 2，在同一次运行内继续迭代；**主要约束是时间**——运行时长超过 **280 分钟**（job `timeout-minutes` 设 300，留 20 分钟做收尾：写 plan、提交、结构化评论）就提前结束；**上限 8 轮**（produce → verify → fix 算一轮）作为兜底安全网，防止极端情况下的字面死循环，正常情况下应该是时间阈值先触发。达到任一上限都提前结束，结构化评论 `action: continue`，交给 Scheduler 再派一轮——迭代计数不跨运行累计，新一轮运行重新计数，避免单次 job 死循环，但不阻碍整体进度。
   - 存在自己判断不了该退到 planning 还是 design 的问题 → 走「上报与阻塞」。

5. **关闭前检查 plan checklist（收敛判定的一部分，合并前做）**：扫一遍 Issue 正文里 plan 的所有勾选项，不能带着未勾选的条目直接关闭 Issue。对每个未勾选的条目判断：
   - **实际已经做了，只是没同步勾选**（状态没更新）→ 补勾选，不需要额外动作。
   - **确实没做/被砍掉了**（遗留任务）→ **新开一个 Issue 追踪**（正文按 [../templates/issue-body.md](../templates/issue-body.md)，触发来源写"来自 #当前 Issue 的遗留任务"），不能让它随着当前 Issue 关闭就悄悄消失；新开的 Issue 不需要跟当前 Issue 建 `blocked_by`（当前 Issue 该收敛就收敛，遗留任务是独立的后续工作，不是阻塞关系）。
   - 检查通过（全部勾选或遗留项已拆出去追踪）才进入下一步的合并动作。
6. **合并**
   - `gh pr create`（若还没开）或复用已有 PR，push 到 Scheduler 指定的分支。
   - `gh pr merge`：
     - 成功 → 结构化评论 `action: done`，关闭 Issue。
     - 失败因为**落后于 main、且 `git rebase main` 能无冲突完成** → 当场 rebase、force-push 后重试合并。
     - 失败因为**其它原因**（rebase 有冲突、required check 未过、需要 review 等）→ 不尝试自动做冲突消解或绕过门禁，`git rebase --abort` 后视为任务未完成，结构化评论 `action: continue`，留在 `phase:implementation`（见「职责范围」的合并失败原则）——下一轮运行会重新走一遍实施+审查流程，届时如果冲突是因为 plan 涉及的文件被别处改了，Coder 会在步骤 2 里自然发现并处理，不需要在合并这一步做代码级冲突消解。

7. **产出结构化评论**：无论运行在哪一步结束，都按 SKILL.md 定义的格式追加一条评论（人类可读叙述部分按 [../templates/status-comment.md](../templates/status-comment.md) 的骨架），`run_id` 用当次 Action run 的 id。

## 上报与阻塞

- 需要回退到 planning/design，但自己判断不了具体该退到哪一级 → 上报 Scheduler，转 `phase:planning`，交给 Designer 判断是否要进一步退到 design。
- 发现是别的主题模块的问题 → 新开该主题的独立 Issue（正文按 [../templates/issue-body.md](../templates/issue-body.md) 填写），当前 Issue 加 `blocked_by`，打 `status:blocked`。

这些数值（`timeout-minutes: 300`、迭代上限 8 轮）与 `references/scheduler.md`「轮询节奏」里 Scheduler 对该 workflow 的异常判定阈值（320 分钟）对应，留出排队和收尾的缓冲，具体项目可按自身构建/测试耗时调整。`timeout-minutes` 是 GitHub Actions 的硬性墙钟耗时上限（从 job 启动算起，不是"多久没输出"的空闲检测），所以内部主动收尾的阈值必须明显小于它，否则会被硬杀而来不及提交进度和结构化评论。
