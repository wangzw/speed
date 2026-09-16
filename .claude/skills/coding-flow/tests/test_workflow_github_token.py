"""
回归测试：三个角色 workflow 模板必须显式给 anthropics/claude-code-action 传
github_token 和 anthropic_api_key，不能依赖 action 的默认行为/只设 env。

背景（两个独立根因，都是在真实项目 speed 上端到端运行时发现的，不是缺 secret/缺
id-token 权限那两个已知问题）：

1. designer-planning.yml 和 reviewer-audit.yml 原本没有传 github_token input。
   anthropics/claude-code-action 在没有该 input 时，不会退回使用触发 workflow 自带的
   `github.token`，而是尝试用 OIDC 换取 Claude GitHub App 的安装 token；目标仓库如果没有
   安装该 App（真实场景下的默认情况），这一步会在启动阶段就以 "Claude Code is not
   installed on this repository. Please install the Claude Code GitHub App" 401 失败。
   coder-implementation.yml 一开始就正确（用于 push/PR 需要独立 PAT，见 SKILL.md
   「凭证与权限」）。

2. 三个角色 workflow 都只设置了 env.ANTHROPIC_AUTH_TOKEN，没有传 anthropic_api_key
   input。anthropics/claude-code-action 自带的 validateEnvironmentVariables() 前置校验
   （base-action/src/validate-env.ts）只认 ANTHROPIC_API_KEY / CLAUDE_CODE_OAUTH_TOKEN /
   工作负载身份联邦三者之一，完全不认识 ANTHROPIC_AUTH_TOKEN 这个变量名——即使它作为
   env 已经设置，也会在启动阶段就以 "Environment variable validation failed: Either
   ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN, or workload identity federation ...
   is required" 失败。修复：额外传 anthropic_api_key input（塞同一个
   secrets.ANTHROPIC_AUTH_TOKEN 值）只为过这道前置校验；真正发给 CLI 子进程的认证仍然
   是 env 里的 ANTHROPIC_AUTH_TOKEN + ANTHROPIC_BASE_URL（action 把整个 process.env
   透传给 CLI 子进程，且按 Anthropic SDK 惯例 ANTHROPIC_AUTH_TOKEN 存在时优先，
   走 Bearer 而不是 x-api-key）。

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
    def _claude_action_step_body(self, filename):
        path = TEMPLATES_DIR / filename
        content = path.read_text(encoding="utf-8")
        # 定位 "uses: anthropics/claude-code-action@..." 步骤到下一个
        # "- name:"/文件末尾之间的片段，只在这个片段里找 input，
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
        return match.group("body")

    def test_each_role_workflow_passes_github_token_to_claude_action(self):
        for filename in ROLE_WORKFLOWS:
            with self.subTest(workflow=filename):
                body = self._claude_action_step_body(filename)
                self.assertIn(
                    "github_token:",
                    body,
                    f"{filename} 的 claude-code-action 步骤没有显式传 github_token，"
                    "会导致 action 尝试走 Claude GitHub App 的 OIDC 认证路径，"
                    "在未安装该 App 的仓库上以 401 失败",
                )

    def test_each_role_workflow_passes_anthropic_api_key_to_claude_action(self):
        for filename in ROLE_WORKFLOWS:
            with self.subTest(workflow=filename):
                body = self._claude_action_step_body(filename)
                self.assertIn(
                    "anthropic_api_key:",
                    body,
                    f"{filename} 的 claude-code-action 步骤没有显式传 anthropic_api_key，"
                    "action 自带的 validateEnvironmentVariables() 前置校验不认识 "
                    "ANTHROPIC_AUTH_TOKEN 这个 env，会在启动阶段就以 "
                    "'Environment variable validation failed' 失败",
                )


if __name__ == "__main__":
    unittest.main()
