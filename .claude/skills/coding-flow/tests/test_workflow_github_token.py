"""
回归测试：非 Coder 角色的 workflow 模板必须显式给 anthropics/claude-code-action 传
github_token，不能依赖 action 的默认行为。

背景：designer-planning.yml 和 reviewer-audit.yml 原本没有传 github_token input。
anthropics/claude-code-action 在没有该 input 时，不会退回使用触发 workflow 自带的
`github.token`，而是尝试用 OIDC 换取 Claude GitHub App 的安装 token；目标仓库如果没有安装
该 App（真实场景下的默认情况），这一步会在启动阶段就以
"Claude Code is not installed on this repository. Please install the Claude Code
GitHub App" 401 失败——与缺 secret、缺 id-token 权限是两类不同的根因，且不受那两项已知
修复影响。在真实项目（speed）上端到端运行 designer-planning.yml 时复现。

coder-implementation.yml 一开始就正确（用于 push/PR 需要独立 PAT，见 SKILL.md「凭证与权限」），
本测试只覆盖另外两个此前遗漏的角色 workflow。

纯标准库 unittest，不引入 yaml 解析依赖，跟 init.py/render.py 的风格一致：
直接对模板文件做文本检查。
运行：python3 -m unittest discover -s .claude/skills/coding-flow/tests
"""

import re
import unittest
from pathlib import Path

TEMPLATES_DIR = (
    Path(__file__).resolve().parent.parent / "templates" / "workflows"
)

# 只检查真正调用 claude-code-action 的角色 workflow；dashboard-render.yml 是确定性脚本
# 任务，不涉及 claude-code-action，不适用这条规则。
ROLE_WORKFLOWS = [
    "designer-planning.yml",
    "coder-implementation.yml",
    "reviewer-audit.yml",
]


class WorkflowGithubTokenTest(unittest.TestCase):
    def test_each_role_workflow_passes_github_token_to_claude_action(self):
        for filename in ROLE_WORKFLOWS:
            path = TEMPLATES_DIR / filename
            content = path.read_text(encoding="utf-8")
            with self.subTest(workflow=filename):
                # 定位 "uses: anthropics/claude-code-action@..." 步骤到下一个
                # "- name:"/文件末尾之间的片段，只在这个片段里找 github_token，
                # 避免误配到同一文件里其它无关的 with: 块（比如 checkout 步骤）。
                match = re.search(
                    r"uses:\s*anthropics/claude-code-action@\S+"
                    r"(?P<body>.*?)(?=\n\s*-\s*name:|\Z)",
                    content,
                    re.DOTALL,
                )
                self.assertIsNotNone(
                    match,
                    f"{filename} 里没有找到 anthropics/claude-code-action 步骤",
                )
                body = match.group("body")
                self.assertIn(
                    "github_token:",
                    body,
                    f"{filename} 的 claude-code-action 步骤没有显式传 github_token，"
                    "会导致 action 尝试走 Claude GitHub App 的 OIDC 认证路径，"
                    "在未安装该 App 的仓库上以 401 失败",
                )


if __name__ == "__main__":
    unittest.main()
