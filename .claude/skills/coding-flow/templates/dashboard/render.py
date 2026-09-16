#!/usr/bin/env python3
"""
coding-flow 看板渲染脚本。

用途：查询当前仓库所有打了 phase:*/status:* 标签且未 closed 的 Issue，解析每个 Issue
最新一条 `coding-flow:status` 结构化评论，套用 index.html.template，生成静态看板 HTML。

这是一个确定性脚本任务（见 SKILL.md「非交互式任务一律用 Claude Code 的动态 Workflow 工具
实现」的例外说明），不涉及 agent 推理，只依赖 `gh` CLI 和标准库，不需要额外安装依赖。

用法：
    render.py <template_path> <output_path>

运行环境要求：已 `gh auth`（workflow 里通过 GH_TOKEN 环境变量），且当前工作目录是
要渲染看板的目标仓库（`gh issue list` 靠 git remote 自动识别仓库，不显式传 --repo）。
"""

import json
import re
import subprocess
import sys
from datetime import datetime, timezone

PHASE_PREFIX = "phase:"
STATUS_PREFIX = "status:"
PHASE_LABELS = ["phase:design", "phase:planning", "phase:implementation", "phase:audit"]

# 匹配结构化评论里的 <!-- coding-flow:status ... --> 块
STATUS_BLOCK_RE = re.compile(
    r"<!--\s*coding-flow:status\s*(.*?)-->", re.DOTALL
)


def run_gh(args):
    result = subprocess.run(
        ["gh", *args], capture_output=True, text=True, check=True
    )
    return result.stdout


def parse_status_block(comment_body: str) -> dict:
    """解析结构化评论里的字段。格式是简单的 `key: value` 逐行，不用 YAML 库，
    避免引入额外依赖——字段值本身不含冒号+空格这种会歧义的情况（question 字段
    如果含冒号，写入时应加引号，这里做了简单的引号剥离）。"""
    match = STATUS_BLOCK_RE.search(comment_body or "")
    if not match:
        return {}
    fields = {}
    for line in match.group(1).splitlines():
        line = line.strip()
        if not line or line.startswith("#") or ":" not in line:
            continue
        key, _, value = line.partition(":")
        key = key.strip()
        value = value.split("#", 1)[0].strip()  # 去掉行内注释
        value = value.strip('"').strip("'")
        if key:
            fields[key] = value
    return fields


def fetch_issues():
    """只拉取打了 phase:* 标签的 Issue（不是仓库里所有 open Issue）。`gh issue list
    --label` 对多个值的 AND/OR 语义不够明确，干脆按四个 phase label 分别查询、
    按 issue number 去重合并，规则清晰不依赖 gh 的隐含行为。`--limit 1000` 是
    每个 phase 分类各自的上限（不是全局固定 200），避免大项目被静默截断。"""
    issues_by_number = {}
    for label in PHASE_LABELS:
        raw = run_gh(
            [
                "issue", "list",
                "--state", "open",
                "--label", label,
                "--limit", "1000",
                "--json", "number,title,url,labels,updatedAt",
            ]
        )
        for issue in json.loads(raw):
            issues_by_number[issue["number"]] = issue
    return sorted(issues_by_number.values(), key=lambda i: i["number"])


def fetch_last_status_comment(issue_number: int) -> dict:
    """按 references/scheduler.md「结构化评论解析的具体实现」规定的方式取评论：
    用 `gh api ... --paginate` 而不是 `gh issue view --json comments`——后者在评论数
    超过单页时可能拿不到完整列表，见该文档的说明。"""
    raw = subprocess.run(
        [
            "gh", "api",
            f"repos/{{owner}}/{{repo}}/issues/{issue_number}/comments",
            "--paginate",
        ],
        capture_output=True, text=True, check=True,
    ).stdout
    comments = json.loads(raw)
    for comment in reversed(comments):
        fields = parse_status_block(comment.get("body", ""))
        if fields:
            return fields
    return {}


def classify_labels(labels):
    phase = next(
        (l["name"][len(PHASE_PREFIX):] for l in labels if l["name"].startswith(PHASE_PREFIX)),
        "—",
    )
    statuses = [
        l["name"][len(STATUS_PREFIX):] for l in labels if l["name"].startswith(STATUS_PREFIX)
    ]
    return phase, ", ".join(statuses) if statuses else "运行中"


def render_row(issue) -> str:
    phase, status = classify_labels(issue.get("labels", []))
    fields = fetch_last_status_comment(issue["number"])
    summary = fields.get("question") or fields.get("action") or "—"
    blocked_by = fields.get("blocked_by", "—")
    return f"""
    <tr>
      <td><a href="{issue['url']}">#{issue['number']}</a></td>
      <td>{escape(issue['title'])}</td>
      <td><span class="phase">{escape(phase)}</span></td>
      <td><span class="status status-{escape(status.split(',')[0].strip())}">{escape(status)}</span></td>
      <td>{escape(blocked_by)}</td>
      <td>{escape(summary)}</td>
      <td>{escape(issue.get('updatedAt', ''))}</td>
    </tr>"""


def escape(s) -> str:
    return (
        str(s)
        .replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
    )


def main():
    if len(sys.argv) != 3:
        print(f"用法: {sys.argv[0]} <template_path> <output_path>", file=sys.stderr)
        sys.exit(1)

    template_path, output_path = sys.argv[1], sys.argv[2]

    issues = fetch_issues()
    rows = "\n".join(render_row(issue) for issue in issues) or (
        '<tr><td colspan="7" class="empty">当前没有进行中的 Issue</td></tr>'
    )

    with open(template_path, "r", encoding="utf-8") as f:
        template = f.read()

    generated_at = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")
    output = template.replace("<!-- CODING_FLOW_ISSUES -->", rows)
    output = output.replace("{{GENERATED_AT}}", generated_at)

    with open(output_path, "w", encoding="utf-8") as f:
        f.write(output)

    print(f"渲染完成：{len(issues)} 个 Issue → {output_path}")


if __name__ == "__main__":
    main()
