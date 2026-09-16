#!/usr/bin/env bash
# 把渲染好的看板 HTML 发布到 gh-pages 分支。
#
# 用法：publish.sh <rendered_html_path>
# 必须在目标仓库的 git checkout 目录下运行（cwd = 仓库根目录）。
#
# 抽成独立脚本（而不是内嵌在 dashboard-render.yml 的 `run:` 块里），是因为这段逻辑
# 曾经有一个只有真实跑一遍才会暴露的 bug（见下面的关键注释），需要能被自动化测试覆盖——
# 内嵌在 workflow yaml 里的 shell 没法脱离 GitHub Actions 环境单独测试。
#
# gh-pages 内容完全由 render.py 派生（不是需要保留历史意图的人工产物），两次渲染撞车时
# 没有"合并"的意义——谁的内容更新就该谁生效，push 失败时直接 --force 覆盖。

set -euo pipefail

RENDERED_HTML="$1"

git fetch origin gh-pages 2>/dev/null || true

if git show-ref --verify --quiet refs/remotes/origin/gh-pages; then
  git worktree add gh-pages-worktree origin/gh-pages
else
  git worktree add --detach gh-pages-worktree
  (cd gh-pages-worktree && git checkout --orphan gh-pages && git rm -rf . >/dev/null 2>&1 || true)
fi

cp "$RENDERED_HTML" gh-pages-worktree/index.html
cd gh-pages-worktree

# 关键点（曾经的 bug）：gh-pages 分支第一次发布时，index.html 是全新文件，
# 在 `git add` 之前是 untracked 状态——`git diff --quiet`（不加 --cached）根本不检测
# untracked 文件，永远判定"无变化"直接 exit 0，导致首次发布被静默跳过、job 却显示成功。
# 必须先 `git add`，再用 `git diff --cached` 判断是否真的有变化。
git add index.html

if git diff --cached --quiet -- index.html; then
  echo "看板内容无变化，跳过推送"
  cd ..
  git worktree remove --force gh-pages-worktree
  exit 0
fi

git -c user.name="coding-flow-bot" -c user.email="coding-flow-bot@users.noreply.github.com" \
  commit -m "chore(coding-flow): update dashboard"

git push origin HEAD:gh-pages || git push origin HEAD:gh-pages --force
cd ..
# 真实 CI 里每次运行都是全新 checkout，这一步清理主要是为了本地/测试环境里可以
# 反复调用这个脚本而不撞上"worktree 已存在"。
git worktree remove --force gh-pages-worktree
