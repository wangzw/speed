#!/usr/bin/env python3
"""Cases for the root Makefile's legs that loop over a list, and for the actionlint leg
that `repo-check` runs beside them, and for the order of the segments `check` runs.
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

STAND_IN = """\
#!{python}
import json, os, sys
args = sys.argv[1:]
where = os.getcwd()
if args[:1] == ["-C"]:
    where, args = args[1], args[2:]
sub = args[0] if args else ""
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
failed = os.path.basename(where) == "red" and sub != "list"
if sub == "list" and os.path.basename(where) == "nolist":
    failed = True
if sub == "test" and any(
    arg.rstrip("/").split("/")[-1] == "broken" for arg in args[1:]
):
    failed = True
if sub == "list":
    for dirpath, _dirnames, filenames in os.walk(where):
        if any(name.endswith(".go") for name in filenames):
            print(dirpath + " " + dirpath)
sys.exit(1 if failed else 0)
"""

ALWAYS_FAILS = """\
#!{python}
import sys
sys.exit(1)
"""

GATE = """\
import json, os, sys
where = os.path.basename(os.path.dirname(os.path.abspath(__file__)))
with open(os.environ["STAND_IN_LOG"], "a", encoding="utf-8") as log:
    log.write(json.dumps({
        "tool": "gate",
        "dir": where,
        "args": [],
        "gowork": "",
        "gomaxprocs": os.environ.get("GOMAXPROCS", ""),
        "goflags": os.environ.get("GOFLAGS", ""),
    }) + "\\n")
sys.exit(1 if where == "red" else 0)
"""

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

EXPECTED_BACKEND_ENV = {
    "SPEED_TEST_PG_ADDR": "127.0.0.1:15432",
    "SPEED_TEST_REDIS_ADDR": "127.0.0.1:16379",
    "SPEED_TEST_NATS_URL": "nats://127.0.0.1:14222",
    "SPEED_TEST_MEMCACHED_ADDR": "127.0.0.1:11222",
}

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

COMPOSITE_ACTION = """name: stand-in
description: a composite action
runs:
  using: composite
  steps:
    - shell: bash
      run: echo hi
"""

FINDING_SHELLCHECK = """\
#!{python}
import json, sys
sys.stdin.read()
print(json.dumps([{{"line": 2, "column": 1, "level": "info", "code": 2086, "message": "reported"}}]))
sys.exit(1)
"""

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
    "UNCACHED_PACKAGES",
    "WEB_CACHE",
    "CI",
)

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

EMBEDS_FRONTEND_BUILD = (
    "build",
    "vet-integration",
    "test",
    "test-race",
)

WEB_CHECK_PHASES = {
    "web-check-static": (
        "case '$(WEB_CACHE)' in on|off) ;; *) echo \"WEB_CACHE must be 'on' "
        "or 'off', not '$(WEB_CACHE)'\" >&2; exit 1;; esac",
        "pnpm -C web install --frozen-lockfile --workspace-concurrency=1 "
        "--child-concurrency=$(GATE_JOBS)",
        "pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort lint "
        "--concurrency=off $(WEB_CACHE_LINT_ARGS)",
        "pnpm -C web -r --workspace-concurrency=$(GATE_JOBS) --no-sort typecheck "
        "$(WEB_CACHE_TYPECHECK_ARGS)",
        "pnpm -C web exec vitest run --maxWorkers=$(GATE_JOBS) eslint-rules/ scripts/",
    ),
    "web-check-test": (
        "pnpm -C web exec vitest run --config vitest.unit.config.ts "
        "--maxWorkers=$(GATE_JOBS)",
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
        for tool in ("go", "golangci-lint", "pnpm", "actionlint", "shellcheck"):
            path = self.bin / tool
            path.write_text(STAND_IN.format(python=sys.executable), encoding="utf-8")
            path.chmod(0o755)
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
            (self.scratch / name / f"{name}.go").write_text(
                f"package {name}\n", encoding="utf-8"
            )
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
        uncached_packages: list[str] | None = None,
        gate_jobs: str | None = None,
        goflags: str | None = None,
        failing_tools: bool = False,
        start_dir: str | pathlib.Path | None = None,
        env_extra: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
        if env_extra:
            env.update(env_extra)
        if goflags is not None:
            env["GOFLAGS"] = goflags
        first = self.failing_bin if failing_tools else self.bin
        env["PATH"] = f"{first}{os.pathsep}{self.bin}{os.pathsep}{env.get('PATH', '')}"
        env["STAND_IN_LOG"] = str(self.log)
        dirs = [str(self.scratch / name) for name in names]
        gates = [str(self.scratch / name / f"check_{name}.py") for name in names]
        extra = (
            [f"UNCACHED_PACKAGES={' '.join(uncached_packages)}"]
            if uncached_packages is not None
            else []
        )
        if gate_jobs is not None:
            extra.append(f"GATE_JOBS={gate_jobs}")
        argv = ["make"]
        if start_dir is None:
            argv += ["-C", str(ROOT)]
        else:
            argv += ["-f", str(ROOT / "Makefile")]
        argv += [
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
        ]
        return subprocess.run(
            argv,
            cwd=str(start_dir) if start_dir is not None else None,
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

    def add_package(self, module: str, name: str) -> pathlib.Path:
        path = self.scratch / module / name
        path.mkdir(parents=True, exist_ok=True)
        (path / f"{name}.go").write_text(f"package {name}\n", encoding="utf-8")
        return path

    def go_test_calls(self, module: str) -> list[dict]:
        return [
            call
            for call in self.calls()
            if call["dir"] == module
            and call["tool"] == "go"
            and call["args"][:1] == ["test"]
        ]

    @staticmethod
    def package_args(call: dict) -> list[str]:
        return [arg for arg in call["args"][1:] if arg.startswith("/")]

    def test_the_unit_legs_bypass_the_test_cache_only_for_the_packages_that_read_outside_their_root(
        self,
    ) -> None:
        """A package under UNCACHED_PACKAGES runs with -count=1 on every pass,
        while the other packages of the same module keep the cache."""
        cached = self.add_package("outside", "cachedpkg")
        uncached = self.add_package("outside", "uncachedpkg")
        for leg, race in (("test", False), ("test-race", True)):
            for gate_jobs, width in ((None, "4"), ("7", "7")):
                with self.subTest(leg=leg, gate_jobs=gate_jobs):
                    self.log.unlink(missing_ok=True)
                    result = self.make(
                        leg,
                        "outside",
                        "green",
                        uncached_packages=[str(uncached)],
                        gate_jobs=gate_jobs,
                    )
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    passes = self.go_test_calls("outside")
                    self.assertEqual(
                        len(passes),
                        2,
                        f"{leg} in outside: expected a cached pass and an uncached "
                        f"pass, got {passes}",
                    )
                    cached_pass, uncached_pass = passes
                    self.assertNotIn("-count=1", cached_pass["args"], cached_pass["args"])
                    self.assertEqual(
                        self.package_args(cached_pass), [str(cached)], cached_pass["args"]
                    )
                    self.assertIn("-count=1", uncached_pass["args"], uncached_pass["args"])
                    self.assertEqual(
                        self.package_args(uncached_pass),
                        [str(uncached)],
                        uncached_pass["args"],
                    )
                    others = self.go_test_calls("green")
                    self.assertEqual(
                        len(others),
                        1,
                        f"{leg} in green: expected one cached pass, got {others}",
                    )
                    self.assertNotIn("-count=1", others[0]["args"], others[0]["args"])
                    self.assertEqual(
                        self.package_args(others[0]),
                        [str(self.scratch / "green")],
                        others[0]["args"],
                    )
                    for call in (cached_pass, uncached_pass, others[0]):
                        self.assertIn(f"-p={width}", call["args"], call["args"])
                        self.assertIn(f"-parallel={width}", call["args"], call["args"])
                        self.assertEqual(call["gomaxprocs"], width, call["args"])
                        self.assertEqual("-race" in call["args"], race, call["args"])
                    listings = [
                        call
                        for call in self.calls()
                        if call["tool"] == "go" and call["args"][:1] == ["list"]
                    ]
                    self.assertTrue(
                        listings, "the leg never listed the module's packages"
                    )
                    for call in listings:
                        self.assertIn(f"-p={width}", call["args"], call["args"])
                        self.assertEqual(call["gomaxprocs"], width, call["args"])

    def test_a_caller_count_flag_runs_one_pass_over_the_whole_module(self) -> None:
        """When GOFLAGS already names -count=1, each module gets one go test
        over ./... and no package split."""
        self.add_package("outside", "cachedpkg")
        for leg in ("test", "test-race"):
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "green", "outside", goflags="-count=1")
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                for module in ("green", "outside"):
                    passes = self.go_test_calls(module)
                    self.assertEqual(len(passes), 1, f"{leg} {module}: {passes}")
                    self.assertEqual(passes[0]["args"][-1], "./...", passes[0]["args"])
                    self.assertNotIn("-count=1", passes[0]["args"], passes[0]["args"])
                    self.assertEqual(
                        "-race" in passes[0]["args"],
                        leg == "test-race",
                        passes[0]["args"],
                    )
                self.assertFalse(
                    [call for call in self.calls() if call["args"][:1] == ["list"]],
                    f"{leg} listed packages although the cache is off for the whole run",
                )

    def test_a_failing_cached_pass_still_runs_the_uncached_pass(self) -> None:
        """A failure in the cached pass does not skip the uncached pass, and
        the module still counts as failed."""
        self.add_package("split", "cachedok")
        self.add_package("split", "broken")
        uncached = self.add_package("split", "uncachedpkg")
        for leg in ("test", "test-race"):
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "split", uncached_packages=[str(uncached)])
                passes = self.go_test_calls("split")
                self.assertEqual(
                    len(passes),
                    2,
                    f"{leg}: expected both passes despite the failure, got {passes}",
                )
                cached_pass, uncached_pass = passes
                self.assertNotIn("-count=1", cached_pass["args"], cached_pass["args"])
                self.assertIn(
                    str(self.scratch / "split" / "broken"),
                    self.package_args(cached_pass),
                    cached_pass["args"],
                )
                self.assertIn("-count=1", uncached_pass["args"], uncached_pass["args"])
                self.assertEqual(
                    self.package_args(uncached_pass),
                    [str(uncached)],
                    uncached_pass["args"],
                )
                self.assertNotEqual(
                    result.returncode,
                    0,
                    f"{leg}: the module passed although its cached pass failed:\n"
                    + result.stdout
                    + result.stderr,
                )

    def test_a_failing_uncached_pass_fails_the_module(self) -> None:
        """A failing uncached pass fails the module and is named in the log,
        while the cached pass still runs."""
        self.add_package("split", "cachedok")
        broken = self.add_package("split", "broken")
        for leg in ("test", "test-race"):
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "split", uncached_packages=[str(broken)])
                passes = self.go_test_calls("split")
                self.assertEqual(
                    len(passes), 2, f"{leg}: expected both passes, got {passes}"
                )
                cached_pass, uncached_pass = passes
                self.assertNotIn("-count=1", cached_pass["args"], cached_pass["args"])
                self.assertIn("-count=1", uncached_pass["args"], uncached_pass["args"])
                self.assertEqual(
                    self.package_args(uncached_pass),
                    [str(broken)],
                    uncached_pass["args"],
                )
                self.assertNotEqual(
                    result.returncode,
                    0,
                    f"{leg}: the module passed although its uncached pass failed:\n"
                    + result.stdout
                    + result.stderr,
                )
                self.assertIn("(uncached pass)", result.stderr)

    def test_a_failing_go_list_fails_the_module_without_running_go_test(self) -> None:
        """A go list failure fails the module under (go list) and no go test runs."""
        listing = self.scratch / "nolist"
        listing.mkdir()
        (listing / "pkg.go").write_text("package nolist\n", encoding="utf-8")
        for leg in ("test", "test-race"):
            with self.subTest(leg=leg):
                self.log.unlink(missing_ok=True)
                result = self.make(leg, "nolist")
                self.assertNotEqual(
                    result.returncode,
                    0,
                    f"{leg}: the module passed although go list failed:\n"
                    + result.stdout
                    + result.stderr,
                )
                self.assertIn("(go list)", result.stderr)
                self.assertEqual(
                    self.go_test_calls("nolist"),
                    [],
                    f"{leg}: go test ran although go list failed",
                )

    def test_an_absolute_package_with_a_trailing_slash_is_matched(self) -> None:
        """An absolute UNCACHED_PACKAGES entry is normalized before it is
        matched, so a trailing slash still selects its package."""
        uncached = self.add_package("outside", "uncachedpkg")
        self.add_package("outside", "cachedpkg")
        self.log.unlink(missing_ok=True)
        result = self.make("test", "outside", uncached_packages=[str(uncached) + "/"])
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        passes = self.go_test_calls("outside")
        self.assertEqual(
            len(passes),
            2,
            f"the trailing-slash entry did not split the packages: {passes}",
        )
        cached_pass, uncached_pass = passes
        self.assertNotIn("-count=1", cached_pass["args"], cached_pass["args"])
        self.assertEqual(
            self.package_args(cached_pass),
            [str(self.scratch / "outside" / "cachedpkg")],
            cached_pass["args"],
        )
        self.assertIn("-count=1", uncached_pass["args"], uncached_pass["args"])
        self.assertEqual(
            self.package_args(uncached_pass), [str(uncached)], uncached_pass["args"]
        )

    def test_the_default_relative_packages_resolve_from_the_start_directory(self) -> None:
        """The UNCACHED_PACKAGES default is spelled relative to the repository
        root, so a run started there splits those packages with no override."""
        root = self.scratch / "start-root"
        module = root / "starters" / "demo" / "server"
        for name in ("cachedpkg", "treecheck", "e2e/factorseed"):
            path = module / name
            path.mkdir(parents=True)
            (path / "p.go").write_text(f"package {path.name}\n", encoding="utf-8")
        self.log.unlink(missing_ok=True)
        result = self.make("test", module, start_dir=root)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        passes = self.go_test_calls("server")
        self.assertEqual(
            len(passes),
            2,
            f"the default relative entries did not split the packages: {passes}",
        )
        cached_pass, uncached_pass = passes
        self.assertNotIn("-count=1", cached_pass["args"], cached_pass["args"])
        self.assertEqual(
            self.package_args(cached_pass),
            [str(module / "cachedpkg")],
            cached_pass["args"],
        )
        self.assertIn("-count=1", uncached_pass["args"], uncached_pass["args"])
        self.assertEqual(
            sorted(self.package_args(uncached_pass)),
            sorted([str(module / "treecheck"), str(module / "e2e" / "factorseed")]),
            uncached_pass["args"],
        )

    def test_the_makefile_names_the_uncached_packages(self) -> None:
        """Test that the makefile names the uncached packages."""
        result = subprocess.run(
            ["make", "-C", str(ROOT), "--no-print-directory", "-pn", "modules"],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertIn(
            "UNCACHED_PACKAGES = ./starters/demo/server/treecheck "
            "./starters/demo/server/e2e/factorseed",
            result.stdout,
        )

    def test_a_red_module_hides_no_other_modules_result(self) -> None:
        """Test that a red module hides no other modules result."""
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
        """Test that the integration legs export the state addresses."""
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
        """Test that the integration leg fails when the state file is absent."""
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
        """Test that the starter build names its output."""
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
        """Test that a red gate hides no other gates result."""
        result = self.make("repo-check", "red", "green")
        self.assertNotEqual(result.returncode, 0, "repo-check passed with a red gate")
        ran = [c["dir"] for c in self.calls() if c["tool"] == "gate"]
        self.assertEqual(ran, ["red", "green"], result.stdout + result.stderr)
        self.assertIn("check_red.py", result.stderr)
        self.assertNotIn("check_green.py", result.stderr)

    def workflow_calls(self) -> list[dict]:
        return [call for call in self.calls() if call["tool"] == "actionlint"]

    def test_the_workflow_lint_runs_after_a_red_gate_and_is_reported_with_it(self) -> None:
        """Test that the workflow lint runs after a red gate and is reported with it."""
        result = self.make("repo-check", "red", "green", repo="red")
        self.assertNotEqual(result.returncode, 0, "repo-check passed with a red gate and red workflows")
        self.assertTrue(
            self.workflow_calls(),
            f"repo-check stopped at the red gate before the workflow lint:\n{result.stdout}{result.stderr}",
        )
        self.assertIn("check_red.py", result.stderr)
        self.assertIn("gate actionlint", result.stderr)

    def test_repo_check_fails_on_a_workflow_finding_alone(self) -> None:
        """Test that repo check fails on a workflow finding alone."""
        result = self.make("repo-check", "green", repo="red")
        self.assertNotEqual(result.returncode, 0, "repo-check passed with a workflow finding")
        self.assertIn("gate actionlint", result.stderr)
        self.assertNotIn("check_green.py", result.stderr)

    def test_every_gate_forwards_the_width_to_the_scripts_it_runs(self) -> None:
        """Test that every gate forwards the width to the scripts it runs."""
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(gate_jobs=gate_jobs):
                self.log.unlink(missing_ok=True)
                result = self.make(
                    "repo-check", "green", gate_jobs=gate_jobs, goflags="-count=1"
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                gates = [call for call in self.calls() if call["tool"] == "gate"]
                self.assertTrue(
                    gates, f"repo-check ran no gate:\n{result.stdout}{result.stderr}"
                )
                for call in gates:
                    self.assertEqual(call["gomaxprocs"], width, "repo-check: GOMAXPROCS")
                    flags = call["goflags"].split()
                    self.assertIn(f"-p={width}", flags, f"repo-check: {call['goflags']}")
                    self.assertIn("-count=1", flags, "the caller's GOFLAGS is lost")
                    self.assertLess(
                        flags.index("-count=1"),
                        flags.index(f"-p={width}"),
                        "the width must be appended after the caller's GOFLAGS",
                    )

    def test_tools_test_forwards_the_same_width_and_flags(self) -> None:
        """Test that tools test forwards the same width and flags."""
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(gate_jobs=gate_jobs):
                env = {k: v for k, v in os.environ.items() if k not in MAKE_ENVIRONMENT}
                env["GOFLAGS"] = "-count=1"
                argv = [
                    "make",
                    "-C",
                    str(ROOT),
                    "--no-print-directory",
                    "-n",
                    "tools-test",
                    f"PYTHON={sys.executable}",
                ]
                if gate_jobs is not None:
                    argv.append(f"GATE_JOBS={gate_jobs}")
                result = subprocess.run(
                    argv, env=env, capture_output=True, text=True, check=False
                )
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(f"GOMAXPROCS={width}", result.stdout)
                self.assertIn(f'GOFLAGS="-count=1 -p={width}"', result.stdout)
                self.assertIn("-m unittest discover", result.stdout)

    def test_the_actionlint_leg_lints_every_workflow_file_in_one_run(self) -> None:
        """Test that the actionlint leg lints every workflow file in one run."""
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
        """Test that the actionlint leg uses the shellcheck it checked for."""
        result = self.make("actionlint")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (call,) = self.workflow_calls()
        self.assertIn(f"-shellcheck={self.bin / 'shellcheck'}", call["args"])

    def shellcheck_calls(self) -> list[dict]:
        return [call for call in self.calls() if call["tool"] == "shellcheck"]

    def test_the_actionlint_leg_lints_the_composite_actions_with_the_same_shellcheck(self) -> None:
        """Test that the actionlint leg lints the composite actions with the same
        shellcheck."""
        result = self.make("actionlint")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        (call,) = self.shellcheck_calls()
        self.assertEqual(call["dir"], "green", "the composite lint ran outside the repository root")
        self.assertEqual(call["args"][-1], "-")
        self.assertIn("json", call["args"])
        self.assertIn("bash", call["args"])

    def test_a_composite_action_finding_fails_the_actionlint_leg_alone(self) -> None:
        """Test that a composite action finding fails the actionlint leg alone."""
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
        """Test that a workflow finding does not hide the composite lint."""
        result = self.make("actionlint", repo="red")
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue(self.workflow_calls(), "actionlint never ran")
        self.assertTrue(
            self.shellcheck_calls(),
            f"the composite lint did not run after a red actionlint:\n{result.stdout}{result.stderr}",
        )
        self.assertIn("workflows", result.stderr)

    def test_the_actionlint_leg_fails_on_a_finding(self) -> None:
        """Test that the actionlint leg fails on a finding."""
        result = self.make("actionlint", repo="red")
        self.assertNotEqual(result.returncode, 0, "the leg passed although actionlint failed")
        self.assertTrue(self.workflow_calls(), "actionlint never ran")

    def test_the_actionlint_leg_fails_when_there_is_no_workflow(self) -> None:
        """Test that the actionlint leg fails when there is no workflow."""
        empty = self.scratch / "empty"
        (empty / ".github" / "workflows").mkdir(parents=True)
        (empty / ".github" / "workflows" / "notes.md").write_text("not a workflow\n", encoding="utf-8")
        result = self.make("actionlint", repo=empty)
        self.assertNotEqual(result.returncode, 0, "the leg passed with nothing to lint")
        self.assertIn("no workflow file", result.stderr)
        self.assertFalse(self.workflow_calls(), "actionlint ran with no file to lint")

    def test_the_actionlint_leg_fails_when_shellcheck_is_missing(self) -> None:
        """Test that the actionlint leg fails when shellcheck is missing."""
        result = self.make("actionlint", shellcheck=self.scratch / "no-such-shellcheck")
        self.assertNotEqual(result.returncode, 0, "the leg passed without shellcheck")
        self.assertIn("is not on PATH", result.stderr)
        self.assertFalse(self.workflow_calls(), "actionlint ran without shellcheck")

    def test_the_actionlint_leg_does_not_depend_on_the_current_directory(self) -> None:
        """Test that the actionlint leg does not depend on the current directory."""
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
        """Test that help lists the actionlint leg."""
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
        """Test that help lists the commit gate."""
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
        """Test that every leg passes when every item passes."""
        for leg in [*MODULE_LEGS, "repo-check"]:
            with self.subTest(leg=leg):
                result = self.make(leg, "green")
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def leg_calls(
        self,
        leg: str,
        gate_jobs: str | None = None,
        env_extra: dict[str, str] | None = None,
    ) -> list[dict]:
        self.log.unlink(missing_ok=True)
        result = self.make(leg, "green", gate_jobs=gate_jobs, env_extra=env_extra)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return self.calls()

    def pnpm_call(self, calls: list[dict], token: str) -> dict:
        for call in calls:
            if call["tool"] == "pnpm" and token in call["args"]:
                return call
        raise AssertionError(f"no pnpm invocation carries {token!r}: {calls}")

    def test_every_go_leg_takes_its_width_from_gate_jobs(self) -> None:
        """Test that every go leg takes its width from gate jobs."""
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
        """Test that the go build leg takes its width from gate jobs."""
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
        """Test that every go vet leg takes its width from gate jobs."""
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
        """Test that the linter takes its width from gate jobs."""
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
        """Test that the formatter takes its width from gate jobs."""
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
        """Test that the tidying legs take their width from gate jobs."""
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
        """Test that the workflow lint takes its width from gate jobs."""
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(gate_jobs=gate_jobs):
                calls = self.leg_calls("actionlint", gate_jobs)
                workflows = [
                    call for call in calls if call["tool"] == "actionlint"
                ]
                self.assertTrue(workflows, f"actionlint never ran:\n{calls}")
                for call in workflows:
                    self.assertEqual(call["gomaxprocs"], width, f"{call['args']}: GOMAXPROCS")

    def test_the_frontend_cache_stays_local(self) -> None:
        # The stand-in strips `-C <dir>`, so these are the arguments after it.
        lint = [
            "-r", "--workspace-concurrency=4", "--no-sort",
            "lint", "--concurrency=off",
        ]
        typecheck = [
            "-r", "--workspace-concurrency=4", "--no-sort", "typecheck",
        ]
        cache_flags = [
            "--cache", "--cache-strategy", "content",
            "--cache-location", "node_modules/.cache/eslint/",
        ]
        cases = (
            ("CI unset", {}, True),
            ("CI=false", {"CI": "false"}, True),
            ("CI=true", {"CI": "true"}, False),
            ("CI=1", {"CI": "1"}, False),
            ("CI=true WEB_CACHE=on", {"CI": "true", "WEB_CACHE": "on"}, True),
            ("WEB_CACHE=off", {"WEB_CACHE": "off"}, False),
            ("WEB_CACHE empty", {"WEB_CACHE": ""}, None),
        )
        for label, env_extra, cached in cases:
            with self.subTest(case=label):
                if cached is None:
                    self.log.unlink(missing_ok=True)
                    result = self.make(
                        "web-check-static", "green", env_extra=env_extra
                    )
                    self.assertNotEqual(
                        result.returncode, 0, f"{label}: the leg accepted the value"
                    )
                    self.assertIn("WEB_CACHE", result.stderr)
                    self.assertFalse(
                        [call for call in self.calls() if call["tool"] == "pnpm"],
                        f"{label}: the leg ran pnpm before refusing the value",
                    )
                    continue
                calls = self.leg_calls("web-check-static", env_extra=env_extra)
                self.assertEqual(
                    self.pnpm_call(calls, "lint")["args"],
                    lint + cache_flags if cached else lint,
                    f"{label}: the lint command does not match the cache setting",
                )
                self.assertEqual(
                    self.pnpm_call(calls, "typecheck")["args"],
                    typecheck + (["--incremental"] if cached else []),
                    f"{label}: the typecheck command does not match the cache setting",
                )

    def test_an_unknown_web_cache_value_fails_the_static_leg(self) -> None:
        self.log.unlink(missing_ok=True)
        result = self.make(
            "web-check-static", "green", env_extra={"WEB_CACHE": "maybe"}
        )
        self.assertNotEqual(result.returncode, 0, "the leg accepted WEB_CACHE=maybe")
        self.assertIn("WEB_CACHE", result.stderr)
        self.assertFalse(
            [call for call in self.calls() if call["tool"] == "pnpm"],
            "the leg ran pnpm before refusing the WEB_CACHE value",
        )

    def test_every_frontend_leg_takes_its_width_from_gate_jobs(self) -> None:
        """Test that every frontend leg takes its width from gate jobs."""
        for gate_jobs, width in ((None, "4"), ("7", "7")):
            with self.subTest(leg="web-check-static", gate_jobs=gate_jobs):
                calls = self.leg_calls("web-check-static", gate_jobs)
                install = self.pnpm_call(calls, "install")
                self.assertIn("--workspace-concurrency=1", install["args"])
                self.assertIn(f"--child-concurrency={width}", install["args"])
                lint = self.pnpm_call(calls, "lint")
                self.assertIn(f"--workspace-concurrency={width}", lint["args"])
                self.assertIn("--concurrency=off", lint["args"])
                typecheck = self.pnpm_call(calls, "typecheck")
                self.assertIn(f"--workspace-concurrency={width}", typecheck["args"])
                self.assertIn(f"--maxWorkers={width}", self.pnpm_call(calls, "vitest")["args"])
            with self.subTest(leg="web-check-test", gate_jobs=gate_jobs):
                calls = self.leg_calls("web-check-test", gate_jobs)
                vitest_calls = [
                    call
                    for call in calls
                    if call["tool"] == "pnpm" and "vitest" in call["args"]
                ]
                self.assertEqual(len(vitest_calls), 1, vitest_calls)
                unit_call = vitest_calls[0]
                self.assertIn("--config", unit_call["args"])
                self.assertIn("vitest.unit.config.ts", unit_call["args"])
                self.assertIn(f"--maxWorkers={width}", unit_call["args"])
                self.assertNotIn("-r", unit_call["args"])
                self.assertFalse(
                    any(
                        arg.startswith("--workspace-concurrency=")
                        for arg in unit_call["args"]
                    ),
                    unit_call["args"],
                )
                a11y_calls = [
                    call
                    for call in calls
                    if call["tool"] == "pnpm" and "test:a11y" in call["args"]
                ]
                self.assertEqual(len(a11y_calls), 1, a11y_calls)
                self.assertIn(f"--maxWorkers={width}", a11y_calls[0]["args"])
            with self.subTest(leg="web-check-build", gate_jobs=gate_jobs):
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
                self.assertEqual(playwright["gate_jobs"], width)

    def test_a_wrapper_target_fails_when_its_segment_does(self) -> None:
        """Test that a wrapper target fails when its segment does."""
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
        """Test that check announces the gate width before any segment."""
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
        """Test that the callers gate width does not reach the case."""
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
    """The structure of `check`, the segments it runs and `web-check`'s phases."""

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
        """Test that check runs every segment once in the fail fast order."""
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
        """Test that check runs lint before the tests."""
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
        """Test that the frontend build runs after every go leg that embeds it."""
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
        """Test that the frontend test and build phases install first."""
        for leg in ("web-check-test", "web-check-build"):
            with self.subTest(leg=leg):
                self.assertIn(
                    "check-web-check-static",
                    self.prerequisites[f"check-{leg}"],
                    f"check-{leg} may start before the frontend install",
                )

    def test_a_parallel_run_reaches_the_same_segments(self) -> None:
        """Test that a parallel run reaches the same segments."""
        for segment in CHECK_SEGMENTS:
            with self.subTest(segment=segment):
                self.assertEqual(
                    MAKE_CALL_RE.findall(self.targets[f"check-{segment}"]),
                    [segment],
                    f"check-{segment} no longer runs exactly its own segment",
                )

    def test_web_check_runs_its_three_phases_and_their_commands(self) -> None:
        """Test that web check runs its three phases and their commands."""
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
        """Test that every check segment announces its start time."""
        for segment in CHECK_SEGMENTS:
            with self.subTest(segment=segment):
                recipe = self.targets[f"check-{segment}"]
                self.assertIn(f"$(call mark,{segment})", recipe)
                self.assertNotIn("$(shell date", recipe)

    def test_every_segment_depends_on_the_announcement(self) -> None:
        """Test that every segment depends on the announcement."""
        for segment in CHECK_SEGMENTS:
            with self.subTest(segment=segment):
                self.assertIn("check-announce", self.prerequisites[f"check-{segment}"])
        self.assertIn("GATE_JOBS=$(GATE_JOBS)", self.targets["check-announce"])


class EntryPointTests(unittest.TestCase):
    """The root Makefile's local and deployment entry points."""

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
        """Test that dev runs vite and air under mise."""
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
        """Every pnpm invocation in a recipe runs inside the web workspace."""
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
        """Every `--filter` in a recipe names a member, and no match fails."""
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
        """Test that fly is the deployment entry point."""
        result = self.make("fly")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn(
            "flyctl deploy",
            result.stdout,
            "the deployment entry point no longer calls flyctl",
        )

    def test_deploy_is_gone_rather_than_aliased(self) -> None:
        """Test that deploy is gone rather than aliased."""
        result = self.make("deploy")
        self.assertNotEqual(result.returncode, 0, "make deploy still resolves")
        self.assertIn("No rule to make target", result.stderr)


class GateTimeScaleTests(unittest.TestCase):
    """GATE_TIME_SCALE defaults to 1 and a caller's value reaches the Makefile."""

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
        """Test that gate time scale defaults to one."""
        self.assertEqual(self.scale(), "1")

    def test_an_outer_gate_time_scale_does_not_reach_the_default(self) -> None:
        """Test that an outer gate time scale does not reach the default."""
        with mock.patch.dict(os.environ, {"GATE_TIME_SCALE": "2.5"}):
            self.assertEqual(self.scale(), "1")

    def test_a_caller_value_reaches_the_makefile(self) -> None:
        """Test that a caller value reaches the makefile."""
        self.assertEqual(self.scale("2.5"), "2.5")


if __name__ == "__main__":
    unittest.main()
