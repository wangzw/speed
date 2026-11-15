#!/usr/bin/env python3
"""Cases for the root Makefile's legs that loop over a list, and for the
actionlint leg that `repo-check` runs beside them.

Each leg that loops over the modules go.work lists, or over the gates in
tools/, runs every item even after an earlier one failed, and fails at the
end naming the items that failed, so one module's red result does not hide
another's. The cases drive the real Makefile with MODULES and GATES pointed
at scratch directories, with stand-ins for go and golangci-lint first on
PATH, and with tools/backends.py replaced by a stand-in that writes the state
file the integration legs export. A stand-in records the directory it ran in
and its subcommand, and fails in a directory named red. The actionlint leg
runs against scratch repository roots through stand-ins for actionlint and
shellcheck, so no real linter is involved.

Stdlib-only (unittest, subprocess, tempfile). Run directly:

    python3 tools/test_makefile.py
"""

from __future__ import annotations

import glob
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent

# Stands in for go and golangci-lint. `go -C <dir>` and `env -C <dir>`
# both name the directory a module command runs in; the stand-in records
# that directory's name, the arguments after it and whether the command
# ran outside the workspace, then fails where the directory is named red.
STAND_IN = """\
#!{python}
import json, os, sys
args = sys.argv[1:]
where = os.getcwd()
if args[:1] == ["-C"]:
    where, args = args[1], args[2:]
with open(os.environ["STAND_IN_LOG"], "a", encoding="utf-8") as log:
    log.write(json.dumps({{
        "tool": os.path.basename(sys.argv[0]),
        "dir": os.path.basename(where),
        "args": args,
        "gowork": os.environ.get("GOWORK", ""),
        "backend_env": {{
            name: os.environ[name]
            for name in os.environ
            if name.startswith("SPEED_TEST_")
            and (name.endswith("_ADDR") or name.endswith("_URL"))
        }},
    }}) + "\\n")
sys.exit(1 if os.path.basename(where) == "red" else 0)
"""

# Stands in for a gate: it records the directory it lives in and fails
# where that directory is named red.
GATE = """\
import json, os, sys
where = os.path.basename(os.path.dirname(os.path.abspath(__file__)))
with open(os.environ["STAND_IN_LOG"], "a", encoding="utf-8") as log:
    log.write(json.dumps({"tool": "gate", "dir": where, "args": [], "gowork": ""}) + "\\n")
sys.exit(1 if where == "red" else 0)
"""

# Stands in for tools/backends.py. `up` writes the state file the integration
# legs read their addresses from; `env` prints those addresses. The ports are
# fixed here, so a case can tell the Makefile exported exactly this state.
BACKENDS = """\
#!{python}
import os, sys

args = sys.argv[1:]
command = args[0] if args else ""
state = args[args.index("--state") + 1] if "--state" in args else ""
if command == "up" and state:
    os.makedirs(os.path.dirname(state) or ".", exist_ok=True)
    with open(state, "w", encoding="utf-8") as handle:
        handle.write("SPEED_TEST_PROJECT=speed-test-0123456789ab\\n")
        handle.write("SPEED_TEST_PG_PORT=15432\\n")
        handle.write("SPEED_TEST_REDIS_PORT=16379\\n")
        handle.write("SPEED_TEST_NATS_PORT=14222\\n")
        handle.write("SPEED_TEST_MEMCACHED_PORT=11222\\n")
elif command == "env":
    print("SPEED_TEST_PG_ADDR=127.0.0.1:15432")
    print("SPEED_TEST_REDIS_ADDR=127.0.0.1:16379")
    print("SPEED_TEST_NATS_URL=nats://127.0.0.1:14222")
    print("SPEED_TEST_MEMCACHED_ADDR=127.0.0.1:11222")
sys.exit(0)
"""

# The addresses the integration legs must export from BACKENDS's state.
EXPECTED_BACKEND_ENV = {
    "SPEED_TEST_PG_ADDR": "127.0.0.1:15432",
    "SPEED_TEST_REDIS_ADDR": "127.0.0.1:16379",
    "SPEED_TEST_NATS_URL": "nats://127.0.0.1:14222",
    "SPEED_TEST_MEMCACHED_ADDR": "127.0.0.1:11222",
}

# Each leg that loops over the modules, and the passes it makes over each
# one: a pass is the tool, its subcommand, every flag the pass must carry, and
# the GOWORK value it runs under. The flags are a tuple rather than one value
# because two legs differ by exactly one flag — test-integration and
# test-integration-race — and a pass that asserted only what the two share
# would keep passing after a leg lost the flag that tells them apart.
MODULE_LEGS: dict[str, list[tuple[str, str, tuple[str, ...], str]]] = {
    "build": [("go", "build", (), ""), ("go", "build", (), "off")],
    "test": [("go", "test", (), "")],
    "test-race": [("go", "test", ("-race",), "")],
    "test-integration": [("go", "test", ("-tags=integration",), "")],
    "test-integration-race": [("go", "test", ("-race", "-tags=integration"), "")],
    "vet-integration": [("go", "vet", ("-tags=integration",), "")],
    "fmt": [("golangci-lint", "fmt", (), "")],
    "lint": [("golangci-lint", "fmt", ("--diff",), ""), ("golangci-lint", "run", (), "")],
    "tidy": [("go", "mod", ("tidy",), "off")],
    "tidy-check": [("go", "mod", ("-diff",), "off")],
}

# Variables a parent make exports to its children. A case run by `make
# tools-test` would otherwise hand them to the make it starts.
MAKE_ENVIRONMENT = ("MAKEFLAGS", "MFLAGS", "MAKELEVEL", "MAKEOVERRIDES", "GNUMAKEFLAGS", "GOWORK")


def workspace_package_names(workspace_root: pathlib.Path) -> set[str]:
    """The package.json names of the members a pnpm-workspace.yaml lists.

    The member globs are the `- <glob>` entries under `packages:`, read up
    to the next key; each glob resolves against the workspace root, as pnpm
    resolves it, and a member outside the root (`../...`) counts like any
    other.
    """
    lines = (workspace_root / "pnpm-workspace.yaml").read_text(encoding="utf-8").splitlines()
    start = next(i for i, line in enumerate(lines) if line.strip() == "packages:")
    names: set[str] = set()
    for line in lines[start + 1 :]:
        entry = re.match(r"^\s+-\s+(\S+)", line)
        if entry is None:
            if line.strip() and not line.strip().startswith("#"):
                break
            continue
        for member in glob.glob(str(workspace_root / entry.group(1))):
            manifest = pathlib.Path(member) / "package.json"
            if manifest.is_file():
                names.add(json.loads(manifest.read_text(encoding="utf-8"))["name"])
    return names


class LoopingLegTests(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.scratch = pathlib.Path(tmp.name)
        self.bin = self.scratch / "bin"
        self.bin.mkdir()
        # The test legs bring the external backends up before they run, and the
        # stand-in for the script that does it writes a state file the legs
        # export, so no daemon is driven and no container starts. pnpm is here
        # for the starter Makefile's build leg, whose frontend step runs before
        # the Go one. actionlint and shellcheck stand in for the linters the
        # workflow leg runs; the leg only needs them to exist and be called.
        for tool in ("go", "golangci-lint", "pnpm", "actionlint", "shellcheck"):
            path = self.bin / tool
            path.write_text(STAND_IN.format(python=sys.executable), encoding="utf-8")
            path.chmod(0o755)
        self.backends = self.scratch / "backends"
        self.backends.write_text(BACKENDS.format(python=sys.executable), encoding="utf-8")
        self.backends.chmod(0o755)
        self.state = self.scratch / "test-backends.env"
        self.log = self.scratch / "calls.jsonl"
        for name in ("red", "green"):
            (self.scratch / name).mkdir()
            (self.scratch / name / f"check_{name}.py").write_text(GATE, encoding="utf-8")
        # Repository roots for the actionlint leg. Each carries workflow files
        # of both extensions, a file GitHub does not run as a workflow, and a
        # nested workflow file GitHub does not read either.
        self.repos = self.scratch / "repos"
        for name in ("red", "green"):
            workflows = self.repos / name / ".github" / "workflows"
            (workflows / "nested").mkdir(parents=True)
            for workflow in ("ci.yml", "deploy.yaml", "notes.md"):
                (workflows / workflow).write_text("name: stand-in\n", encoding="utf-8")
            (workflows / "nested" / "inner.yml").write_text("name: stand-in\n", encoding="utf-8")

    def make(
        self,
        leg: str,
        *names: str,
        backends: str | pathlib.Path | None = None,
        state: str | pathlib.Path | None = None,
        repo: str | pathlib.Path = "green",
        shellcheck: str | pathlib.Path | None = None,
    ) -> subprocess.CompletedProcess[str]:
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        env["PATH"] = f"{self.bin}{os.pathsep}{env.get('PATH', '')}"
        env["STAND_IN_LOG"] = str(self.log)
        dirs = [str(self.scratch / name) for name in names]
        gates = [str(self.scratch / name / f"check_{name}.py") for name in names]
        return subprocess.run(
            [
                "make",
                "-C",
                str(ROOT),
                "--no-print-directory",
                leg,
                f"MODULES={' '.join(dirs)}",
                f"GATES={' '.join(gates)}",
                f"PYTHON={sys.executable}",
                f"BACKENDS={backends if backends is not None else self.backends}",
                f"TEST_BACKENDS_STATE={state if state is not None else self.state}",
                f"REPO_ROOT={self.repos / repo}",
                f"ACTIONLINT={self.bin / 'actionlint'}",
                f"SHELLCHECK={shellcheck if shellcheck is not None else self.bin / 'shellcheck'}",
            ],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def calls(self) -> list[dict]:
        if not self.log.exists():
            return []
        lines = self.log.read_text(encoding="utf-8").splitlines()
        return [json.loads(line) for line in lines]

    def ran(self, name: str, tool: str, sub: str, flags: tuple[str, ...], gowork: str) -> bool:
        return any(
            c["dir"] == name
            and c["tool"] == tool
            and c["args"][:1] == [sub]
            and all(flag in c["args"] for flag in flags)
            and c["gowork"] == gowork
            for c in self.calls()
        )

    def test_a_red_module_hides_no_other_modules_result(self) -> None:
        for leg, passes in MODULE_LEGS.items():
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "red", "green")
                self.assertNotEqual(result.returncode, 0, f"{leg} passed with a red module")
                for tool, sub, flags, gowork in passes:
                    pass_name = f"{tool} {sub} {' '.join(flags)} GOWORK={gowork!r}"
                    self.assertTrue(
                        self.ran("red", tool, sub, flags, gowork),
                        f"{leg} never ran {pass_name} in red:\n{result.stdout}{result.stderr}",
                    )
                    self.assertTrue(
                        self.ran("green", tool, sub, flags, gowork),
                        f"{leg} stopped at red before {pass_name} in green:\n{result.stdout}{result.stderr}",
                    )
                self.assertIn(str(self.scratch / "red"), result.stderr)
                self.assertNotIn(str(self.scratch / "green"), result.stderr)

    def test_the_integration_legs_export_the_state_addresses(self) -> None:
        for leg in ("test-integration", "test-integration-race"):
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "green")
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                calls = [
                    call
                    for call in self.calls()
                    if call["tool"] == "go" and call["args"][:1] == ["test"]
                ]
                self.assertTrue(
                    calls, f"{leg} never ran go test:\n{result.stdout}{result.stderr}"
                )
                for call in calls:
                    self.assertEqual(call["backend_env"], EXPECTED_BACKEND_ENV)

    def test_the_integration_leg_fails_when_the_state_file_is_absent(self) -> None:
        noop = self.scratch / "noop-backends"
        noop.write_text(f"#!{sys.executable}\nimport sys\nsys.exit(0)\n", encoding="utf-8")
        noop.chmod(0o755)
        missing = self.scratch / "absent.env"
        for leg in ("test-integration", "test-integration-race"):
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "green", backends=str(noop), state=str(missing))
                self.assertNotEqual(
                    result.returncode, 0, f"{leg} passed without a backend state file"
                )
                self.assertIn("no backend state", result.stderr)
                self.assertFalse(
                    any(
                        call["tool"] == "go" and call["args"][:1] == ["test"]
                        for call in self.calls()
                    ),
                    "go test ran without a backend state file",
                )

    def starter_make(self, leg: str) -> subprocess.CompletedProcess[str]:
        # The starter's Makefile is the entry point a copy of the skeleton
        # runs; the root Makefile does not call it.
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        env["PATH"] = f"{self.bin}{os.pathsep}{env.get('PATH', '')}"
        env["STAND_IN_LOG"] = str(self.log)
        return subprocess.run(
            [
                "make",
                "-C",
                str(ROOT / "starters" / "demo"),
                "--no-print-directory",
                leg,
            ],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_the_starter_build_names_its_output(self) -> None:
        # The Go build embeds the frontend bundle, so the leg runs both steps.
        # A build of a pattern covering several packages discards its results,
        # which would leave a copy of the skeleton without a program: the leg
        # has to name the binary.
        result = self.starter_make("build")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = self.calls()
        self.assertTrue(
            any(c["tool"] == "pnpm" and c["dir"] == "web" for c in calls),
            f"the starter's build never built the frontend:\n{result.stdout}{result.stderr}",
        )
        builds = [
            c
            for c in calls
            if c["tool"] == "go" and c["dir"] == "server" and c["args"][:1] == ["build"]
        ]
        self.assertTrue(
            builds,
            f"the starter's build never ran go build:\n{result.stdout}{result.stderr}",
        )
        for call in builds:
            self.assertIn(
                "-o",
                call["args"],
                "a pattern build discards its output; the leg has to name the binary",
            )
            position = call["args"].index("-o")
            self.assertLess(
                position + 1,
                len(call["args"]),
                "the build's -o names no file",
            )
            self.assertNotIn(
                "./...",
                call["args"],
                "a pattern build discards its output; the leg has to name the binary",
            )

    def test_a_red_gate_hides_no_other_gates_result(self) -> None:
        result = self.make("repo-check", "red", "green")
        self.assertNotEqual(result.returncode, 0, "repo-check passed with a red gate")
        ran = [c["dir"] for c in self.calls() if c["tool"] == "gate"]
        self.assertEqual(ran, ["red", "green"], result.stdout + result.stderr)
        self.assertIn("check_red.py", result.stderr)
        self.assertNotIn("check_green.py", result.stderr)

    def workflow_calls(self) -> list[dict]:
        return [call for call in self.calls() if call["tool"] == "actionlint"]

    def test_the_workflow_lint_runs_after_a_red_gate_and_is_reported_with_it(self) -> None:
        result = self.make("repo-check", "red", "green", repo="red")
        self.assertNotEqual(result.returncode, 0, "repo-check passed with a red gate and red workflows")
        self.assertTrue(
            self.workflow_calls(),
            f"repo-check stopped at the red gate before the workflow lint:\n{result.stdout}{result.stderr}",
        )
        self.assertIn("check_red.py", result.stderr)
        self.assertIn("gate actionlint", result.stderr)

    def test_repo_check_fails_on_a_workflow_finding_alone(self) -> None:
        result = self.make("repo-check", "green", repo="red")
        self.assertNotEqual(result.returncode, 0, "repo-check passed with a workflow finding")
        self.assertIn("gate actionlint", result.stderr)
        self.assertNotIn("check_green.py", result.stderr)

    def test_the_actionlint_leg_lints_every_workflow_file_in_one_run(self) -> None:
        result = self.make("actionlint")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = self.workflow_calls()
        self.assertEqual(len(calls), 1, "the leg must lint every file in a single run")
        files = [arg for arg in calls[0]["args"] if not arg.startswith("-")]
        self.assertEqual(
            files,
            [".github/workflows/ci.yml", ".github/workflows/deploy.yaml"],
            "the leg must name every .yml and .yaml directly under .github/workflows, "
            "relative to the root, and nothing else",
        )
        self.assertEqual(calls[0]["dir"], "green", "the leg must run from the repository root")

    def test_the_actionlint_leg_uses_the_shellcheck_it_checked_for(self) -> None:
        result = self.make("actionlint")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (call,) = self.workflow_calls()
        self.assertIn(f"-shellcheck={self.bin / 'shellcheck'}", call["args"])

    def test_the_actionlint_leg_fails_on_a_finding(self) -> None:
        result = self.make("actionlint", repo="red")
        self.assertNotEqual(result.returncode, 0, "the leg passed although actionlint failed")
        self.assertTrue(self.workflow_calls(), "actionlint never ran")

    def test_the_actionlint_leg_fails_when_there_is_no_workflow(self) -> None:
        empty = self.scratch / "empty"
        (empty / ".github" / "workflows").mkdir(parents=True)
        (empty / ".github" / "workflows" / "notes.md").write_text("not a workflow\n", encoding="utf-8")
        result = self.make("actionlint", repo=empty)
        self.assertNotEqual(result.returncode, 0, "the leg passed with nothing to lint")
        self.assertIn("no workflow file", result.stderr)
        self.assertFalse(self.workflow_calls(), "actionlint ran with no file to lint")

    def test_the_actionlint_leg_fails_when_shellcheck_is_missing(self) -> None:
        # Without shellcheck, actionlint skips every run: script and still
        # exits 0, so the leg has to refuse instead of reporting a partial pass.
        result = self.make("actionlint", shellcheck=self.scratch / "no-such-shellcheck")
        self.assertNotEqual(result.returncode, 0, "the leg passed without shellcheck")
        self.assertIn("is not on PATH", result.stderr)
        self.assertFalse(self.workflow_calls(), "actionlint ran without shellcheck")

    def test_the_actionlint_leg_does_not_depend_on_the_current_directory(self) -> None:
        elsewhere = self.scratch / "elsewhere"
        elsewhere.mkdir()
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        env["PATH"] = f"{self.bin}{os.pathsep}{env.get('PATH', '')}"
        env["STAND_IN_LOG"] = str(self.log)
        result = subprocess.run(
            [
                "make",
                "-f",
                str(ROOT / "Makefile"),
                "--no-print-directory",
                "actionlint",
                f"MODULES={self.scratch / 'green'}",
                f"ACTIONLINT={self.bin / 'actionlint'}",
                f"SHELLCHECK={self.bin / 'shellcheck'}",
            ],
            cwd=elsewhere,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (call,) = self.workflow_calls()
        self.assertEqual(call["dir"], ROOT.name, "the leg ran from the starting directory, not the repository root")
        workflows = ROOT / ".github" / "workflows"
        expected = sorted(
            f".github/workflows/{path.name}"
            for path in workflows.iterdir()
            if path.is_file() and path.suffix in (".yml", ".yaml")
        )
        self.assertEqual([arg for arg in call["args"] if not arg.startswith("-")], expected)

    def test_help_lists_the_actionlint_leg(self) -> None:
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        result = subprocess.run(
            ["make", "-C", str(ROOT), "--no-print-directory", "help"],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertRegex(result.stdout, r"(?m)^  actionlint\s+\S")

    def test_every_leg_passes_when_every_item_passes(self) -> None:
        for leg in [*MODULE_LEGS, "repo-check"]:
            with self.subTest(leg=leg):
                result = self.make(leg, "green")
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


class EntryPointTests(unittest.TestCase):
    """The root Makefile's local and deployment entry points.

    `make dev` is the local loop and `make fly` is the deployment; the old
    `deploy` name is gone rather than aliased. `-n` prints the recipe without
    running mise, air or flyctl, so the cases pin the entry points without
    starting anything.
    """

    def make(self, target: str) -> subprocess.CompletedProcess[str]:
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        return subprocess.run(
            ["make", "-C", str(ROOT), "--no-print-directory", "-n", target],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_dev_runs_vite_and_air_under_mise(self) -> None:
        result = self.make("dev")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(
            "mise exec -- pnpm -C web --filter demo-web --fail-if-no-match run dev",
            result.stdout,
            "the local loop no longer starts the frontend's dev server",
        )
        self.assertIn(
            "air -c .air.toml",
            result.stdout,
            "the local loop no longer runs the Go program under air",
        )

    def test_pnpm_runs_where_it_finds_the_web_workspace(self) -> None:
        """Every pnpm invocation in a recipe runs inside the web workspace.

        pnpm locates its workspace by walking up from the directory it runs
        in. The demo host (starters/demo/web) is an external member of the
        workspace under web/, and no directory above it holds
        pnpm-workspace.yaml, so pnpm run from there installs as a standalone
        project and asks the registry for the unpublished @speed packages.
        A recipe reaches the host through `-C web --filter demo-web`.
        """
        offenders = []
        for number, line in enumerate(
            (ROOT / "Makefile").read_text(encoding="utf-8").splitlines(), start=1
        ):
            if not line.startswith("\t"):
                continue
            match = re.search(r"(?:^|[\s@])pnpm(?:\s+(?:-C|--dir)[\s=]+(\S+))?(?:\s|$)", line)
            if match is None:
                continue
            directory = ROOT / (match.group(1) or ".")
            holder = next(
                (
                    candidate
                    for candidate in (directory, *directory.parents)
                    if (candidate / "pnpm-workspace.yaml").is_file()
                    and ROOT in (candidate, *candidate.parents)
                ),
                None,
            )
            if holder is None:
                offenders.append(f"Makefile:{number}: {line.strip()}")
        self.assertEqual(
            offenders,
            [],
            "pnpm runs where no pnpm-workspace.yaml is found above it; run it "
            "through `pnpm -C web --filter <package>`",
        )

    def test_every_pnpm_filter_selects_a_workspace_member_or_fails(self) -> None:
        """Every `--filter` in a recipe names a member, and no match fails.

        A filter that matches no package makes pnpm print "No projects
        matched" and exit 0, so a recipe whose filter misses would run nothing
        and pass. Each filter therefore names the package.json name of a
        member web/pnpm-workspace.yaml lists, and its invocation carries
        `--fail-if-no-match`, which turns a miss into a failure if the member
        is later renamed or dropped.
        """
        names = workspace_package_names(ROOT / "web")
        self.assertIn("demo-web", names, "the workspace member scan finds no demo host")
        filtered = []
        offenders = []
        for number, line in enumerate(
            (ROOT / "Makefile").read_text(encoding="utf-8").splitlines(), start=1
        ):
            if not line.startswith("\t") or re.search(r"(?:^|[\s@])pnpm\s", line) is None:
                continue
            selectors = re.findall(r"(?:--filter|-F)(?:\s+|=)(\S+)", line)
            if not selectors:
                continue
            filtered.append(number)
            where = f"Makefile:{number}: {line.strip()}"
            for selector in selectors:
                if selector not in names:
                    offenders.append(f"{where}: {selector!r} is no workspace member's name")
            if re.search(r"\s--fail-if-no-match(?:\s|$)", line) is None:
                offenders.append(f"{where}: no --fail-if-no-match")
        self.assertNotEqual(filtered, [], "no recipe runs pnpm with a filter")
        self.assertEqual(offenders, [])

    def test_fly_is_the_deployment_entry_point(self) -> None:
        result = self.make("fly")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(
            "flyctl deploy",
            result.stdout,
            "the deployment entry point no longer calls flyctl",
        )

    def test_deploy_is_gone_rather_than_aliased(self) -> None:
        result = self.make("deploy")
        self.assertNotEqual(result.returncode, 0, "make deploy still resolves")
        self.assertIn("No rule to make target", result.stderr)


if __name__ == "__main__":
    unittest.main()
