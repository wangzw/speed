# Reviewer

> 共享的角色分工、Issue 状态机、结构化评论格式见 [../SKILL.md](../SKILL.md)。本文件只记录 Reviewer 专属的规则。

## 职责范围

合并后独立事后审计，角色本身就是审查者，不做实施。此时被审计主题（本主体）的原 Issue 通常已经关闭。由 Scheduler 派发，在 Action 中运行。

## 触发时机

两条路径，都经 Scheduler 派发，不存在绕开 Scheduler 的直接触发：

1. **合并触发**：Scheduler 轮询发现某主题 Issue 收敛并合并后，自动派发一次 Reviewer 审计。
2. **用户主动触发**：人类直接要求 Scheduler 派一个 Reviewer 去审计指定范围（不一定对应某次具体合并）。

## 执行流程

由 Scheduler 通过 `workflow_dispatch` 触发，接收 `scope` input（合并触发时是刚合并的主题 Issue 编号/PR；人类主动触发时是人类描述的审计范围，如某个目录、某次时间区间的改动）。整个流程写成 Claude Code Workflow 脚本（见 SKILL.md「非交互式任务一律用 Claude Code 的动态 Workflow 工具实现」）：

1. **先开审计结果 Issue**（`phase:audit`），正文先写审计范围（可以用 [../templates/review-findings.md](../templates/review-findings.md) 的「审计范围」部分占位，「发现」部分留空待补），先于实际审计动作开出，哪怕这次运行中途失败/超时也留有记录，不会静默丢失。
2. **拉取被审计范围的实际内容**：合并触发时是 `gh pr diff`/合并后的 commit 内容；人类指定范围时按人类描述定位对应的代码/文档。
3. **审计（Workflow: produce → verify）**：
   - `agent()`：对照该主题原本的设计文档/plan（如果还能找到）和代码库整体规范，找可能的问题——不局限于本次改动引入的 bug，也包括设计假设是否仍然成立、是否有更好的实现方式被忽略了。
   - `agent()`（独立 fork）：只拿代码/文档本身，不看上一步的推理过程，逐条验证每个问题是否成立，并判断每个 CONFIRMED 问题**属于本主体还是跨主体**（见「产出规则」末尾的判定标准）。
   - `pipeline()` 串联两步。

   **produce 步骤 prompt：**
   ```
   独立审计以下已合并的改动（{{scope_description}}，diff/内容见 {{content_path}}）。
   如果找得到该主题当初的设计文档/plan（{{design_doc_path}} / {{plan_path}}，找不到就说明找不到，不要编造），拿来对照。

   这不是重新走一遍 Coder 已经做过的对抗性代码审查，找这几类问题：
   1. 设计假设是否仍然成立——合并之后，代码库其它部分有没有变化导致当初的设计前提不再合理。
   2. 有没有更好的实现方式在当时被忽略了（不是吹毛求疵地要求重写，是指明显更简单/更一致的做法）。
   3. 遗留的技术债——为了赶收敛而绕过的边界情况，有没有留下未处理的隐患。
   4. 这次改动有没有影响到其它模块/主题（跨主体的连带影响）。

   每个问题给出：具体位置、问题描述、影响范围（只影响这次改动涉及的模块，还是牵连别的主题）。
   ```

   **verify 步骤 prompt（独立 fork，只传代码/文档 + 问题清单，不传上一步的推理过程）：**
   ```
   下面是一次事后审计提出的问题清单，做对抗性验证。

   对每一条：
   1. 重新独立核实问题是否真实存在，给出具体依据（代码位置/行为，不能只是猜测）。
   2. 判断修复范围是否落在被审计主题当初的设计文档/plan 界定的范围内：
      - 落在范围内（含该范围内没写清楚的边界情况）→ 标记"本主体"。
      - 需要改动范围外的模块/接口才能修复 → 标记"跨主体"，并指出具体涉及哪个主题/模块。
   3. 标记 CONFIRMED 或 NOT_REAL，附理由。

   拿不准的标 NOT_REAL；"本主体"还是"跨主体"判断不了的，保守标"本主体"（错误代价更小，不会漏掉需要处理的问题）。
   ```
4. **把最终发现写进审计结果 Issue 正文**（覆盖式更新「发现」部分，按 [../templates/review-findings.md](../templates/review-findings.md) 的结构，只列 CONFIRMED 的问题），再按「产出规则」落地：
   - 完全没有 CONFIRMED 问题 → 正文写"未发现问题"，关闭 Issue。
   - 有本主体问题 → 审计结果 Issue 转 `phase:planning`（不新开）。
   - 有跨主体问题 → 为每个涉及主题新开 Issue（正文按 [../templates/issue-body.md](../templates/issue-body.md)），与审计结果 Issue 建 `blocked_by`；若同时没有本主体问题，关联建好后关闭审计结果 Issue。
5. **产出结构化评论**：按 SKILL.md 定义的格式追加（人类可读部分按 [../templates/status-comment.md](../templates/status-comment.md)），记录发现了什么、新开了哪些 Issue、审计结果 Issue 最终去向——评论里不重复贴一遍完整发现列表，正文已经有。

## 产出规则

核心目的：审计结果必须被追踪、不丢失，且按主题分类归档。**Issue 关闭即任务完结的历史记录，不再重新打开**。

- **无条件新开一个"审计结果 Issue"**（`phase:audit`），记录本次审计的范围与结论，不管有没有发现问题。
- **发现本主体（被审计主题自身）的问题** → 该审计结果 Issue 本身转 `phase:planning`，直接成为本主题的后续修复任务，不再另开一个"本主题"的 Issue。这条路径**不是关闭**，是继续往下走——该 Issue 后续会像任何普通主题 Issue 一样产出 plan、进入 `phase:implementation`，最终由 Coder 在合并前执行 SKILL.md「关闭 Issue 前检查 plan checklist」那道闸门，这里不需要重复处理。
- **发现跨主体的问题** → 为每个涉及的其它主题新开一个 Issue，与审计结果 Issue 建立 `blocked_by` 依赖。
- **例外**：完全没发现本主体问题、只发现跨主体问题时，审计结果 Issue 没有本主题待办事项——建好与跨主体 Issue 的依赖关联后直接关闭。
- **checklist 检查不适用于这里**：审计结果 Issue 实际关闭的两种情况（完全没有 CONFIRMED 问题；只发现跨主体问题）都是从未进入过 `phase:planning` 就直接关闭——没有 plan，也就没有 checklist 可查。SKILL.md「关闭 Issue 前检查 plan checklist」那道闸门只针对 Coder 合并前的场景，这里不需要重复一遍。

每轮运行结束按 SKILL.md 定义的格式追加结构化评论，声明这一轮结论和下一步请求。

"本主体"与"跨主体"的判定准则：问题的修复范围是否落在被审计主题当初的设计文档/plan 界定的范围内——落在范围内（哪怕是该范围内没写清楚的边界情况）算本主体；需要改动范围外的模块/接口才能修复，算跨主体。

## `scope` input 的格式约定

`scope` 是一个字符串，用简单的 `<kind>:<value>` 前缀区分几种情况，Scheduler 派发时按来源自动生成，人类主动触发时按下面的格式手写：

| 前缀 | 含义 | 示例 |
|---|---|---|
| `issue:<N>` | 合并触发的默认格式：刚合并的主题 Issue 编号，审计范围就是那次合并的 diff | `issue:42` |
| `path:<glob>` | 人类指定审计某个目录/文件模式，不针对特定一次合并 | `path:src/auth/**` |
| `range:<ref1>..<ref2>` | 人类指定审计某个 commit/tag 区间的所有改动 | `range:v1.2.0..v1.3.0` |
| `free:<text>` | 前面几种都不合适时的自由文本描述，Reviewer 自己判断具体要看什么 | `free:检查上周所有跟支付相关的改动` |

Reviewer 执行流程第 2 步（拉取被审计范围的实际内容）按前缀分流：`issue:`/`range:` 走 `gh pr diff` / `git diff`，`path:` 走对当前 main 分支该路径下代码的静态审计（不是 diff，是审计现状），`free:` 由 Reviewer 自己先做一步"把自由描述转成明确范围"再继续，转不清楚就归为 `status:awaiting-human` 请人类澄清。
