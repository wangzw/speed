#!/usr/bin/env python3
"""Cases for the root Makefile's legs that loop over a list, and for the
actionlint leg that `repo-check` runs beside them, and for the order of the
segments `check` runs.

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

The cases for `check` read the Makefile rather than run it: the segment set,
its order, the product dependencies between segments and the three phase
targets `web-check` splits into are held to written-out expectations, so a
segment dropped from the gate or a command dropped from a phase fails here
instead of running less somewhere else.

Every tool the gate calls that takes a width setting is also held to
GATE_JOBS: the cases drive the legs under stand-ins and check that go build,
go test and go vet, golangci-lint, vitest, Playwright and pnpm -r receive the
width, defaulting to 4 and following an override, so none of them falls back
to the host's core count.

Stdlib-only (unittest, subprocess, tempfile). Run directly:

    python3 tools/test_makefile.py
"""

from __future__ import annotations

import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

from check_pnpm_filters import workspace_package_names  # noqa: E402
from check_test_tiers import MAKE_CALL_RE, prerequisites_of, targets_of  # noqa: E402

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
        "gomaxprocs": os.environ.get("GOMAXPROCS", ""),
        "gate_jobs": os.environ.get("GATE_JOBS", ""),
        "backend_env": {{
            name: os.environ[name]
            for name in os.environ
            if name.startswith("SPEED_TEST_")
            and (name.endswith("_ADDR") or name.endswith("_URL"))
        }},
    }}) + "\\n")
sys.exit(1 if os.path.basename(where) == "red" else 0)
"""

# Stands in for a tool that always fails, whichever directory it runs in, so a
# case can read a wrapper target's exit status without a module deciding it.
ALWAYS_FAILS = """\
#!{python}
import sys
sys.exit(1)
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
    "test-integration": [("go", "test", ("-count=1", "-tags=integration"), "")],
    "test-integration-race": [("go", "test", ("-count=1", "-race", "-tags=integration"), "")],
    "vet-integration": [("go", "vet", ("-tags=integration",), "")],
    "fmt": [("golangci-lint", "fmt", (), "")],
    "lint": [("golangci-lint", "fmt", ("--diff",), ""), ("golangci-lint", "run", (), "")],
    "tidy": [("go", "mod", ("tidy",), "off")],
    "tidy-check": [("go", "mod", ("-diff",), "off")],
}

# A composite action for the scratch repositories: the actionlint leg also lints
# these scripts, and a tree with none is an error of its own.
COMPOSITE_ACTION = """name: stand-in
description: a composite action
runs:
  using: composite
  steps:
    - shell: bash
      run: echo hi
"""

# A shellcheck that reports one finding for every script it is given.
FINDING_SHELLCHECK = """\
#!{python}
import json, sys
sys.stdin.read()
print(json.dumps([{{"line": 2, "column": 1, "level": "info", "code": 2086, "message": "reported"}}]))
sys.exit(1)
"""

# Variables a parent make exports to its children. A case run by `make
# tools-test` would otherwise hand them to the make it starts. GATE_JOBS is
# one of them: GNU make exports a variable set on its own command line, so
# `make check GATE_JOBS=8` puts the width in this process's environment and
# every case that expects the default 4 would read 8 instead. GOMAXPROCS and
# GOFLAGS travel the same way, and a case that checks the default width must
# not read them from the run that started this suite: with the outer value
# already equal to the width, a recipe that dropped its own prefix would
# still record the right number and the case would pass. GATE_TIME_SCALE is
# exported by the Makefile, so a scale set for one gate run reaches the make
# a case starts the same way.
MAKE_ENVIRONMENT = (
    "MAKEFLAGS",
    "MFLAGS",
    "MAKELEVEL",
    "MAKEOVERRIDES",
    "GNUMAKEFLAGS",
    "GOWORK",
    "GATE_JOBS",
    "GOMAXPROCS",
    "GOFLAGS",
    "GATE_TIME_SCALE",
)

# The segments `check` runs, in the order a plain `make check` runs them: the
# fast, commonly failing checks first, then the Go build and compile-only vet
# legs, then the tests and the race detector, with the
# frontend build last. The tuple is written out rather than derived, so a
# segment deleted from `check` fails this case instead of shrinking the gate
# silently. `web-check` itself is not here: it splits into the three phase
# targets, and the whole frontend gate is their three commands.
CHECK_SEGMENTS = (
    "lint",
    "tidy-check",
    "repo-check",
    "tools-test",
    "vet-web-bundle",
    "web-check-static",
    "build",
    "vet-integration",
    "test",
    "web-check-test",
    "test-race",
    "web-check-build",
)

# The Go legs that compile the demo program, and so read the bundle the
# frontend build writes into starters/demo/server/dist through go:embed. The
# frontend build runs after all of them and declares them as prerequisites,
# so the two never overlap, including under -j; ordering it first would only
# hand them a fresher bundle, which none of them reads the contents of.
EMBEDS_FRONTEND_BUILD = (
    "build",
    "vet-integration",
    "test",
    "test-race",
)

# The three phases `web-check` splits into, in order, and the commands each
# carries. Together they are the whole frontend gate -- install, lint,
# typecheck and the rule tests; the unit tests and the route-level sweep; the
# build -- with the width every command takes from GATE_JOBS. One command is an
# exception to GATE_JOBS as a workspace width: the test leg's package scripts
# each start their own vitest process, so its workspace width is 1 and each
# process takes the whole GATE_JOBS: the product of the two widths is what the
# budget bounds, not either factor alone. The build's `build:self` scripts
# compile only their own package into its own dist/, so its workspace width is
# GATE_JOBS too.
WEB_CHECK_PHASES = {
    "web-check-static": (
        "pnpm -C web install --frozen-lockfile",
        "pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort lint",
        "pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort typecheck",
        "pnpm -C web exec vitest run --maxWorkers=$(GATE_JOBS) eslint-rules/ scripts/",
    ),
    "web-check-test": (
        "pnpm -C web -r --workspace-concurrency=1 test --maxWorkers=$(GATE_JOBS)",
        "$(MAKE) web-a11y",
    ),
    "web-check-build": (
        "pnpm -C web -r --filter !@speed/test-utils --filter !demo-web "
        "--fail-if-no-match --workspace-concurrency=$(GATE_JOBS) run build:self",
        "pnpm -C web --filter demo-web --fail-if-no-match exec vite build",
    ),
}

WEB_CHECK_PHASE_ORDER = ("web-check-static", "web-check-test", "web-check-build")


def recipe_commands(recipe: str) -> list[str]:
    """The command of every recipe line, with the tab and a leading `@` gone."""
    commands = []
    for line in recipe.splitlines():
        command = line.strip()
        if not command:
            continue
        if command.startswith("@"):
            command = command[1:].strip()
        commands.append(command)
    return commands


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
        # A second roster of the same tools, each of which always fails: no
        # module directory decides it, so a wrapper target's own exit status
        # is what a case reads.
        self.failing_bin = self.scratch / "failing-bin"
        self.failing_bin.mkdir()
        for tool in ("go", "golangci-lint", "pnpm"):
            path = self.failing_bin / tool
            path.write_text(ALWAYS_FAILS.format(python=sys.executable), encoding="utf-8")
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
            action = self.repos / name / ".github" / "actions" / "stub"
            action.mkdir(parents=True)
            (action / "action.yml").write_text(COMPOSITE_ACTION, encoding="utf-8")

    def make(
        self,
        leg: str,
        *names: str,
        backends: str | pathlib.Path | None = None,
        state: str | pathlib.Path | None = None,
        repo: str | pathlib.Path = "green",
        shellcheck: str | pathlib.Path | None = None,
        uncached: str | None = None,
        gate_jobs: str | None = None,
        failing_tools: bool = False,
    ) -> subprocess.CompletedProcess[str]:
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        first = self.failing_bin if failing_tools else self.bin
        env["PATH"] = f"{first}{os.pathsep}{self.bin}{os.pathsep}{env.get('PATH', '')}"
        env["STAND_IN_LOG"] = str(self.log)
        dirs = [str(self.scratch / name) for name in names]
        gates = [str(self.scratch / name / f"check_{name}.py") for name in names]
        extra = [f"UNCACHED_MODULES={self.scratch / uncached}"] if uncached is not None else []
        if gate_jobs is not None:
            extra.append(f"GATE_JOBS={gate_jobs}")
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
                *extra,
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

    def test_the_unit_legs_bypass_the_test_cache_only_for_a_module_that_reads_outside_its_root(
        self,
    ) -> None:
        """The demo program's tests read ../../../contracts, which Go's test
        cache does not track, so a cached pass would outlive an edit to a
        contract. The module that does it runs uncached; the others keep the
        cache."""
        (self.scratch / "outside").mkdir(exist_ok=True)
        for leg, flags in (("test", ()), ("test-race", ("-race",))):
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "green", "outside", uncached="outside")
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                for name, cached in (("outside", False), ("green", True)):
                    calls = [
                        c
                        for c in self.calls()
                        if c["dir"] == name and c["tool"] == "go" and c["args"][:1] == ["test"]
                    ]
                    self.assertEqual(len(calls), 1, f"{leg} ran {name} {len(calls)} times")
                    self.assertTrue(all(flag in calls[0]["args"] for flag in flags))
                    self.assertEqual(
                        "-count=1" in calls[0]["args"],
                        not cached,
                        f"{leg} in {name}: {calls[0]['args']}",
                    )

    def test_the_makefile_names_the_demo_program_as_the_uncached_module(self) -> None:
        result = subprocess.run(
            ["make", "-C", str(ROOT), "--no-print-directory", "-pn", "modules"],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertIn("UNCACHED_MODULES = ./starters/demo/server", result.stdout)

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

    def shellcheck_calls(self) -> list[dict]:
        return [call for call in self.calls() if call["tool"] == "shellcheck"]

    def test_the_actionlint_leg_lints_the_composite_actions_with_the_same_shellcheck(self) -> None:
        # actionlint does not read composite actions, so the leg runs
        # tools/lint_composite_actions.py over them with the shellcheck it
        # checked for. The stand-in sees the call the script makes: the
        # stdin flag, the json format and the repository root as its directory.
        result = self.make("actionlint")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (call,) = self.shellcheck_calls()
        self.assertEqual(call["dir"], "green", "the composite lint ran outside the repository root")
        self.assertEqual(call["args"][-1], "-")
        self.assertIn("json", call["args"])
        self.assertIn("bash", call["args"])

    def test_a_composite_action_finding_fails_the_actionlint_leg_alone(self) -> None:
        finding = self.scratch / "finding-shellcheck"
        finding.write_text(FINDING_SHELLCHECK.format(python=sys.executable), encoding="utf-8")
        finding.chmod(0o755)
        result = self.make("actionlint", shellcheck=finding)
        self.assertNotEqual(result.returncode, 0, "the leg passed with a finding in a composite action")
        self.assertEqual(len(self.workflow_calls()), 1, "actionlint did not run")
        self.assertIn("stub/action.yml", result.stdout)
        self.assertIn("composite-actions", result.stderr)
        self.assertNotIn("workflows", result.stderr)

    def test_a_workflow_finding_does_not_hide_the_composite_lint(self) -> None:
        result = self.make("actionlint", repo="red")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.workflow_calls(), "actionlint never ran")
        self.assertTrue(
            self.shellcheck_calls(),
            f"the composite lint did not run after a red actionlint:\n{result.stdout}{result.stderr}",
        )
        self.assertIn("workflows", result.stderr)

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

    def test_help_lists_the_commit_gate(self) -> None:
        # `check` is the entry point every other one serves. help reads a
        # target's doc from a line that starts with its name, and check's
        # prerequisite list is wrapped, so a doc left at the end of the list
        # falls on a line that starts with a segment name and is not read.
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        result = subprocess.run(
            ["make", "-C", str(ROOT), "--no-print-directory", "help"],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertRegex(result.stdout, r"(?m)^  check\s+\S")

    def test_every_leg_passes_when_every_item_passes(self) -> None:
        for leg in [*MODULE_LEGS, "repo-check"]:
            with self.subTest(leg=leg):
                result = self.make(leg, "green")
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def leg_calls(self, leg: str, gate_jobs: str | None = None) -> list[dict]:
        self.log.unlink(missing_ok=True)
        result = self.make(leg, "green", gate_jobs=gate_jobs)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return self.calls()

    def pnpm_call(self, calls: list[dict], token: str) -> dict:
        for call in calls:
            if call["tool"] == "pnpm" and token in call["args"]:
                return call
        raise AssertionError(f"no pnpm invocation carries {token!r}: {calls}")

    def test_every_go_leg_takes_its_width_from_gate_jobs(self) -> None:
        # go test: -p bounds package binaries, -parallel bounds cases inside
        # one, GOMAXPROCS bounds its threads. All three are GATE_JOBS; none is
        # left to the host's core count. (The three multiply in principle;
        # each is a cap, and this tree's parallel suite is in one package of
        # a small module while its many-package module has few parallel
        # cases.)
        for leg in ("test", "test-race", "test-integration", "test-integration-race"):
            for gate_jobs, width in ((None, "4"), ("7", "7")):
                with self.subTest(leg=leg, gate_jobs=gate_jobs):
                    calls = self.leg_calls(leg, gate_jobs)
                    tests = [
                        call
                        for call in calls
                        if call["tool"] == "go" and call["args"][:1] == ["test"]
                    ]
                    self.assertTrue(tests, f"{leg} ran no go test:\n{calls}")
                    for call in tests:
                        self.assertIn(f"-p={width}", call["args"], f"{leg}: {call['args']}")
                        self.assertIn(
                            f"-parallel={width}", call["args"], f"{leg}: {call['args']}"
                        )
                        self.assertEqual(call["gomaxprocs"], width, f"{leg}: GOMAXPROCS")

    def test_the_go_build_leg_takes_its_width_from_gate_jobs(self) -> None:
        # A build run compiles -p packages at once and each compile is itself
        # multi-threaded up to GOMAXPROCS, so both follow GATE_JOBS; the
        # standalone pass is the same build with GOWORK off.
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(gate_jobs=gate_jobs):
                calls = self.leg_calls("build", gate_jobs)
                builds = [
                    call
                    for call in calls
                    if call["tool"] == "go" and call["args"][:1] == ["build"]
                ]
                self.assertTrue(builds, f"build ran no go build:\n{calls}")
                self.assertEqual(
                    sorted(call["gowork"] for call in builds),
                    ["", "off"],
                    "build no longer runs both the workspace and standalone pass",
                )
                for call in builds:
                    self.assertIn(f"-p={width}", call["args"], f"{call['args']}")
                    self.assertEqual(call["gomaxprocs"], width, f"{call['args']}: GOMAXPROCS")

    def test_every_go_vet_leg_takes_its_width_from_gate_jobs(self) -> None:
        # go vet runs no cases, so it takes -p and GOMAXPROCS but not
        # -parallel, which it rejects.
        for leg in ("vet-integration", "vet-web-bundle"):
            for gate_jobs, width in ((None, "4"), ("7", "7")):
                with self.subTest(leg=leg, gate_jobs=gate_jobs):
                    calls = self.leg_calls(leg, gate_jobs)
                    vets = [
                        call
                        for call in calls
                        if call["tool"] == "go" and call["args"][:1] == ["vet"]
                    ]
                    self.assertTrue(vets, f"{leg} ran no go vet:\n{calls}")
                    for call in vets:
                        self.assertIn(f"-p={width}", call["args"], f"{leg}: {call['args']}")
                        self.assertNotIn("-parallel", " ".join(call["args"]))
                        self.assertEqual(call["gomaxprocs"], width, f"{leg}: GOMAXPROCS")

    def test_the_linter_takes_its_width_from_gate_jobs(self) -> None:
        # --concurrency bounds golangci-lint's own analysis; the go list it
        # runs inside the process follows GOMAXPROCS, so that is set too.
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(gate_jobs=gate_jobs):
                calls = self.leg_calls("lint", gate_jobs)
                runs = [
                    call
                    for call in calls
                    if call["tool"] == "golangci-lint" and call["args"][:1] == ["run"]
                ]
                self.assertTrue(runs, f"lint ran no golangci-lint run:\n{calls}")
                for call in runs:
                    self.assertIn(f"--concurrency={width}", call["args"], f"{call['args']}")
                    self.assertIn("--allow-parallel-runners", call["args"])
                    self.assertEqual(call["gomaxprocs"], width, f"{call['args']}: GOMAXPROCS")

    def test_the_formatter_takes_its_width_from_gate_jobs(self) -> None:
        # `golangci-lint fmt` has no width flag of its own, and takes no
        # process-wide lock either; it is still a Go program, so its threads
        # follow GOMAXPROCS. The same command runs in `fmt` and as lint's
        # read-only pass, so both take the budget the same way.
        for leg in ("fmt", "lint"):
            for gate_jobs, width in ((None, "4"), ("7", "7")):
                with self.subTest(leg=leg, gate_jobs=gate_jobs):
                    calls = self.leg_calls(leg, gate_jobs)
                    formats = [
                        call
                        for call in calls
                        if call["tool"] == "golangci-lint"
                        and call["args"][:1] == ["fmt"]
                    ]
                    self.assertTrue(formats, f"{leg} ran no golangci-lint fmt:\n{calls}")
                    for call in formats:
                        self.assertEqual(
                            call["gomaxprocs"], width, f"{leg}: {call['args']}: GOMAXPROCS"
                        )

    def test_the_tidying_legs_take_their_width_from_gate_jobs(self) -> None:
        # `go mod tidy` accepts no -p flag -- its usage lists only -e -v -x
        # -diff -go and -compat -- but its module loading follows GOMAXPROCS,
        # so that is the width it takes.
        for leg in ("tidy", "tidy-check"):
            for gate_jobs, width in ((None, "4"), ("7", "7")):
                with self.subTest(leg=leg, gate_jobs=gate_jobs):
                    calls = self.leg_calls(leg, gate_jobs)
                    mods = [
                        call
                        for call in calls
                        if call["tool"] == "go" and call["args"][:1] == ["mod"]
                    ]
                    self.assertTrue(mods, f"{leg} ran no go mod:\n{calls}")
                    for call in mods:
                        self.assertEqual(
                            call["gomaxprocs"], width, f"{leg}: {call['args']}: GOMAXPROCS"
                        )
                        self.assertFalse(
                            any(
                                arg == "-p" or arg.startswith("-p=")
                                for arg in call["args"]
                            ),
                            f"{leg}: go mod tidy rejects -p: {call['args']}",
                        )

    def test_the_workflow_lint_takes_its_width_from_gate_jobs(self) -> None:
        # actionlint has no width flag. It sizes its goroutines by
        # runtime.NumCPU(), not by GOMAXPROCS: GOMAXPROCS bounds only the
        # threads those goroutines run on, which is the part the gate can set.
        # How many shellcheck processes it runs at once is decided by
        # actionlint itself through runtime.NumCPU(), not by shellcheck sizing
        # its own concurrency, and that is out of GOMAXPROCS's reach.
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(gate_jobs=gate_jobs):
                calls = self.leg_calls("actionlint", gate_jobs)
                workflows = [
                    call for call in calls if call["tool"] == "actionlint"
                ]
                self.assertTrue(workflows, f"actionlint never ran:\n{calls}")
                for call in workflows:
                    self.assertEqual(call["gomaxprocs"], width, f"{call['args']}: GOMAXPROCS")

    def test_every_frontend_leg_takes_its_width_from_gate_jobs(self) -> None:
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(leg="web-check-static", gate_jobs=gate_jobs):
                calls = self.leg_calls("web-check-static", gate_jobs)
                for script in ("lint", "typecheck"):
                    call = self.pnpm_call(calls, script)
                    self.assertIn(f"--workspace-concurrency={width}", call["args"])
                self.assertIn(f"--maxWorkers={width}", self.pnpm_call(calls, "vitest")["args"])
            with self.subTest(leg="web-check-test", gate_jobs=gate_jobs):
                # The product of the package width and the per-process worker
                # count is what the budget bounds. The packages run one at a
                # time and each vitest process takes the whole GATE_JOBS, so
                # the product is GATE_JOBS, not its square.
                calls = self.leg_calls("web-check-test", gate_jobs)
                test_call = self.pnpm_call(calls, "test")
                self.assertIn("--workspace-concurrency=1", test_call["args"])
                self.assertNotIn(f"--workspace-concurrency={width}", test_call["args"])
                self.assertIn(f"--maxWorkers={width}", test_call["args"])
                self.assertIn(
                    f"--maxWorkers={width}", self.pnpm_call(calls, "test:a11y")["args"]
                )
            with self.subTest(leg="web-check-build", gate_jobs=gate_jobs):
                # `build:self` compiles only its own package, so the workspace
                # runs at GATE_JOBS; the demo host is built separately by vite.
                calls = self.leg_calls("web-check-build", gate_jobs)
                package_build = self.pnpm_call(calls, "build:self")
                self.assertIn(f"--workspace-concurrency={width}", package_build["args"])
                self.assertIn("-r", package_build["args"])
                self.assertIn("!@speed/test-utils", package_build["args"])
                self.assertIn("!demo-web", package_build["args"])
                demo_build = self.pnpm_call(calls, "vite")
                self.assertIn("build", demo_build["args"])
                self.assertIn("demo-web", demo_build["args"])
            with self.subTest(leg="web-browser", gate_jobs=gate_jobs):
                calls = self.leg_calls("web-browser", gate_jobs)
                self.assertIn(f"--maxWorkers={width}", self.pnpm_call(calls, "vitest")["args"])
            with self.subTest(leg="e2e", gate_jobs=gate_jobs):
                calls = self.leg_calls("e2e", gate_jobs)
                playwright = self.pnpm_call(calls, "playwright")
                self.assertIn(f"--workers={width}", playwright["args"])
                # The global setup's Go builds take -p and GOMAXPROCS from the
                # same value, so the recipe has to put it in the Playwright
                # process's environment. GNU make exports a value given on its
                # command line but not the default this file defines, so the
                # un-overridden case is the one that proves the recipe does it.
                self.assertEqual(playwright["gate_jobs"], width)

    def test_a_wrapper_target_fails_when_its_segment_does(self) -> None:
        # A wrapper pipes its segment through awk to prefix the lines, and a
        # pipeline's status is awk's unless `set -o pipefail` is in force. A
        # wrapper that lost it (or grew an `|| true`) would report a failed
        # segment as a pass while the leg's own cases stayed green.
        for wrapper, segment in (
            ("check-lint", "lint"),
            ("check-tidy-check", "tidy-check"),
        ):
            with self.subTest(wrapper=wrapper):
                self.log.unlink(missing_ok=True)
                result = self.make(wrapper, "green", failing_tools=True)
                self.assertNotEqual(
                    result.returncode,
                    0,
                    f"{wrapper} passed with a failing segment:\n{result.stdout}{result.stderr}",
                )
                self.assertIn(
                    f"[{segment}]",
                    result.stdout,
                    "the segment's output did not reach the log with its prefix",
                )

    def test_check_announces_the_gate_width_before_any_segment(self) -> None:
        for gate_jobs, width in ((None, "4"), ("9", "9")):
            with self.subTest(gate_jobs=gate_jobs):
                env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
                extra = [f"GATE_JOBS={gate_jobs}"] if gate_jobs is not None else []
                result = subprocess.run(
                    [
                        "make",
                        "-C",
                        str(ROOT),
                        "--no-print-directory",
                        "-n",
                        "check-announce",
                        *extra,
                    ],
                    env=env,
                    capture_output=True,
                    text=True,
                    check=False,
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(f"GATE_JOBS={width}", result.stdout)

    def test_the_callers_gate_width_does_not_reach_the_case(self) -> None:
        # GNU make exports a variable given on its command line, so running
        # `make check GATE_JOBS=8` puts 8 in this process's environment and in
        # the environment of every make a case starts. The harness drops it
        # with the make variables above, which is what keeps the default-4
        # expectation above an assertion about the Makefile's default.
        with mock.patch.dict(os.environ, {"GATE_JOBS": "8"}):
            for gate_jobs, width in ((None, "4"), ("9", "9")):
                with self.subTest(gate_jobs=gate_jobs):
                    env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
                    extra = [f"GATE_JOBS={gate_jobs}"] if gate_jobs is not None else []
                    result = subprocess.run(
                        [
                            "make",
                            "-C",
                            str(ROOT),
                            "--no-print-directory",
                            "-n",
                            "check-announce",
                            *extra,
                        ],
                        env=env,
                        capture_output=True,
                        text=True,
                        check=False,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn(f"GATE_JOBS={width}", result.stdout)


class CheckSegmentTests(unittest.TestCase):
    """The structure of `check`, the segments it runs and `web-check`'s phases.

    The cases read the Makefile instead of running the target: `check` is the
    commit gate, and a segment that no longer runs there runs nowhere else, so
    the segment set is held to a written-out expectation rather than derived
    from the file it is checking. The product dependencies between segments
    and the timestamp marks are read from the same text.
    """

    def setUp(self) -> None:
        text = (ROOT / "Makefile").read_text(encoding="utf-8")
        self.targets = targets_of(text)
        self.prerequisites = prerequisites_of(text)

    def segments(self) -> list[str]:
        return [
            name.removeprefix("check-")
            for name in self.prerequisites.get("check", [])
            if name.startswith("check-")
        ]

    def test_check_runs_every_segment_once_in_the_fail_fast_order(self) -> None:
        self.assertEqual(
            self.segments(),
            list(CHECK_SEGMENTS),
            "check's segments changed; one dropped runs nowhere else, so the "
            "expectation is written out in CHECK_SEGMENTS",
        )
        self.assertEqual(
            len(self.segments()),
            len(set(self.segments())),
            "check lists a segment twice",
        )

    def test_check_runs_lint_before_the_tests(self) -> None:
        # Lint rejects more changes than any test leg and costs a fraction of
        # a run; running it after them spends the tests before it reports.
        segments = self.segments()
        self.assertIn("lint", segments, "check no longer runs lint")
        for leg in ("test", "test-race"):
            self.assertLess(
                segments.index("lint"),
                segments.index(leg),
                f"check runs lint after {leg}, so a lint error is reported only "
                "after that leg's whole run",
            )

    def test_the_frontend_build_runs_after_every_go_leg_that_embeds_it(self) -> None:
        # The build writes starters/demo/server/dist, which the demo program's
        # go:embed reads at compile time, so the two must not overlap under
        # -j. The Go legs do not read the bundle's contents, so the build runs
        # after them and declares them as prerequisites; a position in a
        # serial list alone would let -j start it beside them.
        segments = self.segments()
        self.assertIn("web-check-build", segments)
        self.assertEqual(
            segments[-1], "web-check-build", "the frontend build is no longer the last segment"
        )
        for leg in EMBEDS_FRONTEND_BUILD:
            with self.subTest(leg=leg):
                self.assertIn(f"check-{leg}", self.prerequisites["check"])
                self.assertIn(
                    f"check-{leg}",
                    self.prerequisites["check-web-check-build"],
                    f"check-web-check-build may start beside check-{leg}, which "
                    "reads the dist it writes",
                )
                self.assertLess(
                    segments.index(leg),
                    segments.index("web-check-build"),
                    f"check runs the frontend build before {leg}",
                )

    def test_the_frontend_test_and_build_phases_install_first(self) -> None:
        # Both phases run pnpm, and pnpm needs the dependencies the static
        # phase installs. Expressed as a prerequisite so -j cannot reorder it.
        for leg in ("web-check-test", "web-check-build"):
            with self.subTest(leg=leg):
                self.assertIn(
                    "check-web-check-static",
                    self.prerequisites[f"check-{leg}"],
                    f"check-{leg} may start before the frontend install",
                )

    def test_a_parallel_run_reaches_the_same_segments(self) -> None:
        # -j changes only when a segment starts. The roster is the prerequisite
        # list above, and each wrapper calls its own target and nothing else,
        # so no segment can be skipped, doubled or redirected when they overlap.
        for segment in CHECK_SEGMENTS:
            with self.subTest(segment=segment):
                self.assertEqual(
                    MAKE_CALL_RE.findall(self.targets[f"check-{segment}"]),
                    [segment],
                    f"check-{segment} no longer runs exactly its own segment",
                )

    def test_web_check_runs_its_three_phases_and_their_commands(self) -> None:
        # A standalone `make web-check` is the whole frontend gate: the three
        # phases, in order. Their commands are the frontend gate's own, each
        # carrying the GATE_JOBS width, so the phase targets cover the frontend
        # gate completely and add nothing to it.
        self.assertEqual(
            MAKE_CALL_RE.findall(self.targets["web-check"]),
            list(WEB_CHECK_PHASE_ORDER),
            "web-check no longer runs its three phases in order",
        )
        for phase in WEB_CHECK_PHASE_ORDER:
            with self.subTest(phase=phase):
                self.assertEqual(
                    recipe_commands(self.targets[phase]),
                    list(WEB_CHECK_PHASES[phase]),
                    f"{phase} no longer carries its own commands",
                )

    def test_every_check_segment_announces_its_start_time(self) -> None:
        # A log of a run has to show where the time went; the timestamp is
        # read when the segment starts, not when the Makefile is parsed.
        for segment in CHECK_SEGMENTS:
            with self.subTest(segment=segment):
                recipe = self.targets[f"check-{segment}"]
                self.assertIn(f"$(call mark,{segment})", recipe)
                self.assertNotIn("$(shell date", recipe)

    def test_every_segment_depends_on_the_announcement(self) -> None:
        # GATE_JOBS is printed once, before any segment starts, whether or not
        # the segments run at once.
        for segment in CHECK_SEGMENTS:
            with self.subTest(segment=segment):
                self.assertIn("check-announce", self.prerequisites[f"check-{segment}"])
        self.assertIn("GATE_JOBS=$(GATE_JOBS)", self.targets["check-announce"])


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
        member web/pnpm-workspace.yaml lists, with a leading `!` read as the
        negation it is, and its invocation carries `--fail-if-no-match`, which
        turns a miss into a failure if the member is later renamed or dropped.
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
                if selector.removeprefix("!") not in names:
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


class GateTimeScaleTests(unittest.TestCase):
    """GATE_TIME_SCALE defaults to 1 and a caller's value reaches the Makefile.

    The frontend suites multiply their wait limits by it, so its default is
    part of what the gate relies on. The suite strips it from the make runs it
    starts, like every other inherited Make variable, so a gate that sets a
    scale does not change the default these cases read.
    """

    def scale(self, caller: str | None = None) -> str:
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        if caller is not None:
            env["GATE_TIME_SCALE"] = caller
        result = subprocess.run(
            ["make", "-C", str(ROOT), "--no-print-directory", "-p", "-n", "help"],
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        match = re.search(r"(?m)^GATE_TIME_SCALE = (\S+)$", result.stdout)
        self.assertIsNotNone(
            match, "make's database does not carry GATE_TIME_SCALE"
        )
        return match.group(1)

    def test_gate_time_scale_defaults_to_one(self) -> None:
        self.assertEqual(self.scale(), "1")

    def test_an_outer_gate_time_scale_does_not_reach_the_default(self) -> None:
        with mock.patch.dict(os.environ, {"GATE_TIME_SCALE": "2.5"}):
            self.assertEqual(self.scale(), "1")

    def test_a_caller_value_reaches_the_makefile(self) -> None:
        self.assertEqual(self.scale("2.5"), "2.5")


if __name__ == "__main__":
    unittest.main()
