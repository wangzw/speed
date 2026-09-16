#!/usr/bin/env python3
"""
coding-flow `init` 的实现（对应 SKILL.md「角色分发」表里的 `init` 入口）。

用途：把 coding-flow 落地到目标项目：
  1. 把整个 skill 目录拷贝到 <target>/.claude/skills/coding-flow/ 并提交入库
     （见 SKILL.md「Skill 封装与复用」——skill 内容是 vendor 进目标项目的，
     不是运行时动态引用；后续想同步 coding-flow 的更新，重新跑一次 init 即可，幂等覆盖）。
  2. 用 templates/labels.yml 创建 phase:*/status:* label（`gh label create --force`，幂等）。
  3. 把 templates/workflows/*.yml 拷贝到 <target>/.github/workflows/。
  4. 创建/确保 gh-pages 分支存在（orphan + 占位 index.html）——**仅当目标仓库还没有
     配置 Pages 时才做**；已经配置过 Pages（不管指向哪个分支）一律跳过，绝不覆盖。
  5. 通过 `gh api` 启用 GitHub Pages，指向 gh-pages 分支根目录——同样只在 Pages 未配置时做。
  6. 打印后续需要人工完成的事项（配置 secret、按需设置分支保护规则）。

**必须在本机执行**（需要仓库 admin 权限的 `gh` 身份），不能塞进 GitHub Actions 里跑——
启用/修改仓库 Pages 设置是仓库管理级操作，Action 默认的 GITHUB_TOKEN 权限不够。

不引入额外依赖（不用 pyyaml），跟 render.py 保持一致的"标准库 + gh CLI"风格；
labels.yml 的解析是按其固定的三行一组格式做简单文本解析，不是通用 YAML 解析器。

用法：
    python3 scripts/init.py [--target TARGET_DIR] [--dry-run]
默认 --target 是当前工作目录（假定就是目标项目的 git 仓库根目录）。
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path

SKILL_NAME = "coding-flow"


def run_gh(args, check=True):
    return subprocess.run(
        ["gh", *args], capture_output=True, text=True, check=check
    )


def _unquote(value: str) -> str:
    value = value.split("#", 1)[0].strip()
    return value.strip('"').strip("'")


def copy_skill(skill_root: Path, target: Path, dry_run: bool = False) -> None:
    dest = target / ".claude" / "skills" / SKILL_NAME
    if dest.resolve() == skill_root.resolve():
        print("[1/5] 目标就是本仓库自身，跳过拷贝 skill 目录")
        return
    if dry_run:
        note = "（会覆盖已存在的目录）" if dest.exists() else ""
        print(f"[1/5][DRY RUN] 将拷贝 skill 目录到 {dest}{note}")
        return
    dest.parent.mkdir(parents=True, exist_ok=True)
    if dest.exists():
        shutil.rmtree(dest)
    shutil.copytree(skill_root, dest)
    print(f"[1/5] 已拷贝 skill 目录到 {dest}")


def parse_labels(labels_file: Path) -> list[dict]:
    entries: list[dict] = []
    current: dict = {}
    for raw_line in labels_file.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if line.startswith("- name:"):
            if current:
                entries.append(current)
            current = {"name": _unquote(line.split(":", 1)[1])}
        elif line.startswith("color:"):
            current["color"] = _unquote(line.split(":", 1)[1])
        elif line.startswith("description:"):
            current["description"] = _unquote(line.split(":", 1)[1])
    if current:
        entries.append(current)
    return entries


def create_labels(skill_root: Path, dry_run: bool = False) -> None:
    entries = parse_labels(skill_root / "templates" / "labels.yml")
    for entry in entries:
        if dry_run:
            print(f"  [DRY RUN] 将创建/更新 label {entry['name']}（color={entry['color']}）")
            continue
        run_gh(
            [
                "label", "create", entry["name"],
                "--color", entry["color"],
                "--description", entry["description"],
                "--force",
            ]
        )
        print(f"  - label {entry['name']} 已就绪")
    print(f"[2/5]{'[DRY RUN]' if dry_run else ''} {len(entries)} 个 label")


def copy_workflows(skill_root: Path, target: Path, dry_run: bool = False) -> None:
    src_dir = skill_root / "templates" / "workflows"
    dest_dir = target / ".github" / "workflows"
    files = sorted(src_dir.glob("*.yml"))
    if dry_run:
        for src in files:
            exists = (dest_dir / src.name).exists()
            print(f"  [DRY RUN] 将拷贝 {src.name} 到 {dest_dir}{'（覆盖已存在文件）' if exists else ''}")
        print(f"[3/5][DRY RUN] {len(files)} 个 workflow")
        return
    dest_dir.mkdir(parents=True, exist_ok=True)
    for src in files:
        shutil.copyfile(src, dest_dir / src.name)
    print(f"[3/5] 已拷贝 {len(files)} 个 workflow 到 {dest_dir}")


def check_existing_pages() -> dict | None:
    """只读检查，不做任何修改。返回 None 表示目标仓库还没有配置 Pages；
    否则返回当前配置（原样返回，调用方绝不能用它去派生"覆盖式更新"的参数）。

    这个检查存在的原因：早期版本发现 Pages 已存在时会直接 `-X PUT` 覆盖，
    在一个已经启用 Pages（哪怕从未真正部署过内容）的真实仓库上跑过一次，
    产生了非预期的配置变更。coding-flow 的看板是锦上添花的功能，
    绝不能以覆盖目标项目已有的 Pages 配置为代价。"""
    result = run_gh(["api", "repos/{owner}/{repo}/pages"], check=False)
    if result.returncode != 0:
        return None
    return json.loads(result.stdout)


def ensure_gh_pages_branch(existing_pages: dict | None, dry_run: bool = False) -> None:
    if existing_pages is not None:
        print("[4/5] 目标仓库已经配置了 GitHub Pages（非本次 init 创建），跳过——不新建 gh-pages 分支")
        return

    result = run_gh(["api", "repos/{owner}/{repo}/branches/gh-pages"], check=False)
    if result.returncode == 0:
        print("[4/5] gh-pages 分支已存在，跳过创建")
        return

    if dry_run:
        print("[4/5][DRY RUN] gh-pages 分支不存在，将创建并推送初始占位页面")
        return

    # 幂等清理：如果上一次跑到一半失败过（比如 push 失败），本地可能残留一个真实的
    # gh-pages 分支引用和/或陈旧的 worktree 注册信息（`git worktree remove` 只在
    # 成功跑到最后才会执行，中途失败不会清理），这里先统一清一遍，保证重跑不会
    # 因为 "分支已存在" 而崩溃。
    script = """
set -euo pipefail
git worktree prune
rm -rf /tmp/coding-flow-gh-pages-init
git branch -D gh-pages >/dev/null 2>&1 || true
git worktree add /tmp/coding-flow-gh-pages-init --detach
cd /tmp/coding-flow-gh-pages-init
git checkout --orphan gh-pages
git rm -rf . >/dev/null 2>&1 || true
printf '<!doctype html><title>coding-flow dashboard</title><p>看板尚未生成，等待第一次 dashboard-render 运行。</p>' > index.html
git add index.html
git -c user.name=coding-flow-bot -c user.email=coding-flow-bot@users.noreply.github.com \
  commit -m "chore(coding-flow): bootstrap gh-pages"
git push origin HEAD:gh-pages
cd -
git worktree remove /tmp/coding-flow-gh-pages-init --force
"""
    subprocess.run(["bash", "-c", script], check=True)
    print("[4/5] 已创建 gh-pages 分支并推送初始占位页面")


def enable_pages(existing_pages: dict | None, dry_run: bool = False) -> None:
    if existing_pages is not None:
        print(
            f"[5/5] 目标仓库已经配置了 GitHub Pages（{existing_pages.get('html_url', '?')}，"
            f"build_type={existing_pages.get('build_type', '?')}），跳过——不覆盖已有配置。"
            "如果确实想让看板用这个 Pages 站点，需要手动处理现有配置后再重跑 init。"
        )
        return
    if dry_run:
        print("[5/5][DRY RUN] Pages 尚未配置，将启用并指向 gh-pages 分支")
        return
    # 已经在 check_existing_pages() 里确认过 Pages 不存在，这里只做创建，不做覆盖式的 PUT。
    run_gh(
        [
            "api", "repos/{owner}/{repo}/pages",
            "-X", "POST",
            "-f", "source[branch]=gh-pages",
            "-f", "source[path]=/",
        ]
    )
    print("[5/5] 已启用 GitHub Pages（指向 gh-pages 分支）")


def print_followups() -> None:
    print()
    print("完成。接下来需要你手动做的事：")
    print("  1. 配置仓库 secret ANTHROPIC_AUTH_TOKEN（供 anthropics/claude-code-action 使用）")
    print("       gh secret set ANTHROPIC_AUTH_TOKEN")
    print("     按需配置 repo/org variables（非 secret）：ANTHROPIC_BASE_URL（自建网关时）、")
    print("     ANTHROPIC_MODEL、CLAUDE_CODE_EFFORT_LEVEL")
    print("       gh variable set ANTHROPIC_BASE_URL --body <...>")
    print("  2. 配置仓库 secret CODING_FLOW_PAT（独立 PAT，供 Coder 的 git/gh 操作使用，")
    print("     不能用默认 GITHUB_TOKEN，见 SKILL.md「凭证与权限」）")
    print("       gh secret set CODING_FLOW_PAT")
    print()
    print("  !! 重要，请务必确认：Coder 会自主 gh pr merge 合并进 main，不设独立身份、")
    print("     不等人类批准合并——本 skill 只保证自己的对抗性审查已收敛，不会替你在")
    print("     main 上加任何门槛。除非你确实打算让改动全自动合并，强烈建议现在就去给")
    print("     main 配置分支保护规则（至少要求 status checks / review），而不是等出问题")
    print("     之后再补：")
    print("       gh api repos/{owner}/{repo}/branches/main/protection -X PUT ...")
    print("     （具体规则按你自己项目的 CI/review 要求定，本 skill 不代为设置，")
    print("     见 SKILL.md「凭证与权限」「两层审查的区别」）")


def main() -> None:
    parser = argparse.ArgumentParser(description="把 coding-flow 初始化到目标项目")
    parser.add_argument("--target", default=".", help="目标项目根目录，默认当前目录")
    parser.add_argument(
        "--dry-run", action="store_true",
        help="只打印将要执行的操作，不写文件、不调用会产生副作用的 gh/git 命令"
        "（仍会做只读的 gh api 调用来判断 gh-pages 分支是否已存在，便于预演准确）",
    )
    args = parser.parse_args()

    target = Path(args.target).resolve()
    skill_root = Path(__file__).resolve().parent.parent  # .claude/skills/coding-flow/

    if not (target / ".git").exists():
        print(f"错误：{target} 不是一个 git 仓库根目录", file=sys.stderr)
        sys.exit(1)

    # gh 靠当前目录的 git remote 识别目标仓库，后续所有 gh 调用都要在 target 下执行。
    os.chdir(target)

    if args.dry_run:
        print("=== DRY RUN：以下操作不会真正执行 ===\n")

    copy_skill(skill_root, target, dry_run=args.dry_run)
    create_labels(skill_root, dry_run=args.dry_run)
    copy_workflows(skill_root, target, dry_run=args.dry_run)

    existing_pages = check_existing_pages()
    ensure_gh_pages_branch(existing_pages, dry_run=args.dry_run)
    enable_pages(existing_pages, dry_run=args.dry_run)

    if not args.dry_run:
        print_followups()


if __name__ == "__main__":
    main()
