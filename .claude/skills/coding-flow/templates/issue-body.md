<!--
  新开主题 Issue 时用的正文模板。适用场景：
    - 人类通知 Scheduler 新建主题 Issue（交互式设计收敛后，或直接开一个 bug report/待规划的 Issue）
    - Coder 发现别的主题问题，新开该主题 Issue（见 references/coder.md「上报与阻塞」）
    - Reviewer 发现跨主体问题，新开该主题 Issue（见 references/reviewer.md「产出规则」）
  不是要求逐字照抄，是保证几个关键信息不会被漏掉——尤其"触发来源"和"依赖关系"，
  Scheduler/Designer 后续判断从哪个 phase 起步、要不要建 blocked_by，都靠这里的信息。
-->

## 主题

<!-- 一句话概括这个主题在做什么 -->

## 触发来源

<!-- 三选一，删掉不适用的两个 -->

- **新功能设计**：设计文档见 <链接/路径>
- **Bug report**：<复现步骤 + 期望行为 + 实际行为>
- **审查发现问题**：来自审计结果 Issue #<N>

## 范围

<!-- 这次要覆盖什么、明确排除什么。排除的写下来，不是留白。
     参考 references/designer.md 交互式设计 checklist 里的"范围边界"项。 -->

## 依赖关系

<!-- 有依赖就写 "Blocked by #N（原因）"，没有就写"无" -->
