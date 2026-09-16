"""
回归测试：publish.sh 首次发布 gh-pages 不能被静默跳过。

背景：真实跑一遍 dashboard-render.yml 后发现，gh-pages 分支不存在时（比如目标仓库
已经有自己的 GitHub Pages、init 因此跳过了 coding-flow 自己那套 gh-pages 集成设施），
第一次发布会创建全新的 index.html——它在 `git add` 之前是 untracked 状态，脚本原来用
`git diff --quiet`（不带 --cached）判断"是否有变化"，根本不检测 untracked 文件，
永远判定"无变化"直接 exit 0：job 显示成功，但从来没有真正 commit/push 过任何内容。

用真实的本地 git 仓库（bare "远程" + 一份 checkout）跑这个脚本本身，不是重新实现它的逻辑。
"""

import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

PUBLISH_SCRIPT = (
    Path(__file__).resolve().parent.parent / "templates" / "dashboard" / "publish.sh"
)


def run(cmd, cwd):
    return subprocess.run(
        cmd, cwd=cwd, capture_output=True, text=True, check=True,
        env={"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com",
             "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
             "PATH": "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin"},
    )


def gh_pages_commit_count(bare_repo: Path) -> int:
    result = subprocess.run(
        ["git", "-C", str(bare_repo), "rev-list", "--count", "gh-pages"],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        return 0  # 分支还不存在
    return int(result.stdout.strip())


class PublishDashboardTest(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp(prefix="coding-flow-publish-test-"))
        self.origin = self.tmp / "origin.git"
        self.work = self.tmp / "work"

        run(["git", "init", "--bare", "-b", "main", str(self.origin)], cwd=self.tmp)
        # bare 仓库需要先有个初始提交，clone 出来的工作副本才有分支可用。
        seed = self.tmp / "seed"
        run(["git", "init", "-b", "main", str(seed)], cwd=self.tmp)
        (seed / "README.md").write_text("seed\n")
        run(["git", "add", "."], cwd=seed)
        run(["git", "commit", "-m", "seed"], cwd=seed)
        run(["git", "remote", "add", "origin", str(self.origin)], cwd=seed)
        run(["git", "push", "origin", "main"], cwd=seed)

        run(["git", "clone", str(self.origin), str(self.work)], cwd=self.tmp)

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _render(self, content: str) -> Path:
        html_path = self.tmp / "rendered.html"
        html_path.write_text(content)
        return html_path

    def test_first_publish_is_not_silently_skipped(self):
        html = self._render("<html>v1</html>")
        run(["bash", str(PUBLISH_SCRIPT), str(html)], cwd=self.work)
        self.assertEqual(
            gh_pages_commit_count(self.origin), 1,
            "首次发布应该在 gh-pages 上产生一个提交，而不是被 “无变化” 逻辑吞掉",
        )

    def test_unchanged_content_creates_no_new_commit(self):
        html = self._render("<html>v1</html>")
        run(["bash", str(PUBLISH_SCRIPT), str(html)], cwd=self.work)
        first_count = gh_pages_commit_count(self.origin)

        # 内容不变，第二次运行不应该产生新提交。
        run(["bash", str(PUBLISH_SCRIPT), str(html)], cwd=self.work)
        self.assertEqual(gh_pages_commit_count(self.origin), first_count)

    def test_changed_content_creates_a_new_commit(self):
        run(["bash", str(PUBLISH_SCRIPT), str(self._render("<html>v1</html>"))], cwd=self.work)
        first_count = gh_pages_commit_count(self.origin)

        run(["bash", str(PUBLISH_SCRIPT), str(self._render("<html>v2</html>"))], cwd=self.work)
        self.assertEqual(gh_pages_commit_count(self.origin), first_count + 1)


if __name__ == "__main__":
    unittest.main()
