"""
回归测试：init.py 不能在目标仓库已经配置 GitHub Pages 时覆盖它。

背景：早期版本的 enable_pages() 在 `gh api .../pages -X POST` 失败（Pages 已存在）时
直接 fallback 到 `-X PUT` 覆盖，在一个真实项目（已经用自定义域名启用了 Pages）上跑过一次，
把它的 source 配置改成了指向 coding-flow 自己创建的占位 gh-pages 分支。

纯标准库 unittest，不引入 pytest 等额外依赖，跟 init.py/render.py 的风格一致。
运行：python3 -m unittest discover -s .claude/skills/coding-flow/tests
"""

import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "scripts"))
import init  # noqa: E402


class FakeCompletedProcess:
    def __init__(self, returncode=0, stdout=""):
        self.returncode = returncode
        self.stdout = stdout


class CheckExistingPagesTest(unittest.TestCase):
    def test_returns_none_when_pages_not_configured(self):
        with mock.patch.object(init, "run_gh", return_value=FakeCompletedProcess(returncode=1)):
            self.assertIsNone(init.check_existing_pages())

    def test_returns_parsed_config_when_pages_exists(self):
        payload = '{"html_url": "https://speed.vislake.com/", "build_type": "workflow"}'
        with mock.patch.object(
            init, "run_gh", return_value=FakeCompletedProcess(returncode=0, stdout=payload)
        ):
            result = init.check_existing_pages()
        self.assertEqual(result["build_type"], "workflow")


class EnsureGhPagesBranchTest(unittest.TestCase):
    def test_does_not_touch_git_when_pages_already_exists(self):
        """核心回归点：Pages 已存在时，绝不能创建/推送 gh-pages 分支。"""
        existing = {"html_url": "https://speed.vislake.com/", "build_type": "workflow"}
        with mock.patch.object(init.subprocess, "run") as mock_subprocess_run, \
             mock.patch.object(init, "run_gh") as mock_run_gh:
            init.ensure_gh_pages_branch(existing, dry_run=False)
        mock_subprocess_run.assert_not_called()
        mock_run_gh.assert_not_called()

    def test_creates_branch_when_pages_absent_and_branch_missing(self):
        with mock.patch.object(
            init, "run_gh", return_value=FakeCompletedProcess(returncode=1)
        ) as mock_run_gh, mock.patch.object(init.subprocess, "run") as mock_subprocess_run:
            init.ensure_gh_pages_branch(None, dry_run=False)
        mock_run_gh.assert_called_once()
        mock_subprocess_run.assert_called_once()


class EnablePagesTest(unittest.TestCase):
    def test_does_not_call_gh_when_pages_already_exists(self):
        """核心回归点：Pages 已存在时，绝不能调用 gh api .../pages 做任何覆盖式更新。"""
        existing = {"html_url": "https://speed.vislake.com/", "build_type": "workflow"}
        with mock.patch.object(init, "run_gh") as mock_run_gh:
            init.enable_pages(existing, dry_run=False)
        mock_run_gh.assert_not_called()

    def test_creates_pages_with_post_when_absent(self):
        with mock.patch.object(
            init, "run_gh", return_value=FakeCompletedProcess(returncode=0)
        ) as mock_run_gh:
            init.enable_pages(None, dry_run=False)
        mock_run_gh.assert_called_once()
        called_args = mock_run_gh.call_args[0][0]
        self.assertIn("POST", called_args)
        # 修复前会在失败时 fallback 成 PUT；修复后不存在 Pages 时只应该有一次 POST 调用。
        self.assertNotIn("PUT", called_args)


class ParseLabelsTest(unittest.TestCase):
    def test_parses_all_six_labels_from_real_file(self):
        skill_root = Path(__file__).resolve().parent.parent
        entries = init.parse_labels(skill_root / "templates" / "labels.yml")
        names = {e["name"] for e in entries}
        self.assertEqual(
            names,
            {
                "phase:design", "phase:planning", "phase:implementation", "phase:audit",
                "status:awaiting-human", "status:blocked",
            },
        )
        for entry in entries:
            self.assertIn("color", entry)
            self.assertIn("description", entry)


if __name__ == "__main__":
    unittest.main()
