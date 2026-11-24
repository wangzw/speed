#!/usr/bin/env python3
"""GATE_TIME_SCALE reaching the go test cache key.

Runs a scratch module at scale 1 twice as the control and once at scale 3,
and holds that case-time bounds rerun while a package-level bound stays cached.
Stdlib-only (unittest): python3 tools/test_coretest_cache_key.py
"""

from __future__ import annotations

import os
import pathlib
import re
import secrets
import shutil
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
CORETEST_SRC = ROOT / "pkg" / "core" / "coretest"
PKG_GO_MOD = ROOT / "pkg" / "go.mod"

MODULE_PATH = "example.com/coretest-cache-key"

# A test package that computes its one bound inside the case, so the read lands
# in the test log and therefore in the cache key.
BOUND_PACKAGE = """package {name}

import (
\t"testing"
\t"time"

\t"{module}/coretest"
)

const runTag = "{tag}"

func TestTheBoundIsComputedWhenTheCaseRuns(t *testing.T) {{
\tt.Log(runTag)
\t{call}
}}
"""

# The premise: a bound computed in a package-level variable is read before the
# test log starts, so a scale change never reruns it.
PREMISE_PACKAGE = """package premise

import (
\t"testing"
\t"time"

\t"{module}/coretest"
)

const runTag = "{tag}"

var bound = coretest.Scale(time.Second)

func TestThePackageLevelBoundIsNotInTheKey(t *testing.T) {{
\tt.Log(runTag)
\tif bound <= 0 {{
\t\tt.Fatalf("the package-level bound is %v", bound)
\t}}
}}
"""

BOUND_CALLS = {
    "scale": "_ = coretest.Scale(time.Second)",
    "declared": "_ = coretest.Declared(time.Second)",
    "declaredagainst": "_ = coretest.DeclaredAgainst(t, 0, time.Second, time.Hour)",
}

# A package that imports coretest but never calls it, so only the package's
# own initialization can stop the run.
IMPORTS_SOURCE = """package imports

import (
\t_ "{module}/coretest"
)
"""

IMPORTS_TEST = """package imports

import "testing"

func TestNothing(t *testing.T) {{}}
"""

OK_LINE = re.compile(r"^ok\s+(\S+)\s+(\S+)")


class CacheKeyTests(unittest.TestCase):
    """Runs one scratch module three times and reads what each run replayed."""

    scales = ("1", "1", "3")

    @classmethod
    def setUpClass(cls):
        if shutil.which("go") is None:
            raise unittest.SkipTest("go not on PATH")
        cls.scratch = pathlib.Path(tempfile.mkdtemp())
        cls.addClassCleanup(shutil.rmtree, cls.scratch, ignore_errors=True)
        module = cls.scratch / "module"
        (module / "coretest").mkdir(parents=True)
        for source in sorted(CORETEST_SRC.glob("*.go")):
            if source.name.endswith("_test.go"):
                continue
            shutil.copyfile(source, module / "coretest" / source.name)
        go_directive = next(
            line
            for line in PKG_GO_MOD.read_text(encoding="utf-8").splitlines()
            if line.startswith("go ")
        )
        (module / "go.mod").write_text(
            f"module {MODULE_PATH}\n\n{go_directive}\n", encoding="utf-8"
        )
        tag = secrets.token_hex(8)
        for name, call in BOUND_CALLS.items():
            package = module / name
            package.mkdir()
            (package / "bound_test.go").write_text(
                BOUND_PACKAGE.format(
                    name=name, module=MODULE_PATH, tag=tag, call=call
                ),
                encoding="utf-8",
            )
        premise = module / "premise"
        premise.mkdir()
        (premise / "bound_test.go").write_text(
            PREMISE_PACKAGE.format(module=MODULE_PATH, tag=tag), encoding="utf-8"
        )
        imports = module / "imports"
        imports.mkdir()
        (imports / "imports.go").write_text(
            IMPORTS_SOURCE.format(module=MODULE_PATH), encoding="utf-8"
        )
        (imports / "imports_test.go").write_text(IMPORTS_TEST, encoding="utf-8")
        cls.module = module
        cls.runs = [cls.go_test(scale) for scale in cls.scales]

    @classmethod
    def go_test(cls, scale: str) -> subprocess.CompletedProcess:
        env = dict(os.environ)
        env["GATE_TIME_SCALE"] = scale
        env["GOWORK"] = "off"
        env["GOTOOLCHAIN"] = "local"
        env["GOPROXY"] = "off"
        # CI's -count=1 would disable the cache and blunt every run below.
        # Everything else, -p from tools-test included, is passed on.
        env["GOFLAGS"] = " ".join(
            flag
            for flag in env.get("GOFLAGS", "").split()
            if not flag.startswith("-count=")
        )
        return subprocess.run(
            ["go", "test", "./..."],
            cwd=cls.module,
            env=env,
            capture_output=True,
            text=True,
            timeout=600,
        )

    @staticmethod
    def statuses(completed: subprocess.CompletedProcess) -> dict[str, str]:
        seen = {}
        for line in completed.stdout.splitlines():
            match = OK_LINE.match(line)
            if match:
                seen[match.group(1)] = match.group(2)
        return seen

    def completed(self, index: int) -> subprocess.CompletedProcess:
        run = self.runs[index]
        self.assertEqual(
            run.returncode,
            0,
            f"go test run {index + 1} at GATE_TIME_SCALE={self.scales[index]} failed:\n"
            f"stdout:\n{run.stdout}\nstderr:\n{run.stderr}",
        )
        return run

    def test_the_control_run_replays_from_the_cache(self):
        """Checks that the second run replays every bound package from the cache."""
        statuses = self.statuses(self.completed(1))
        for name in BOUND_CALLS:
            package = f"{MODULE_PATH}/{name}"
            self.assertEqual(
                statuses.get(package),
                "(cached)",
                f"{package} was not replayed from the cache on the second run at "
                f"GATE_TIME_SCALE={self.scales[1]}; the control run must hit the "
                f"cache or the third run cannot show what the scale changes",
            )

    def test_each_bound_is_rerun_under_a_new_scale(self):
        """Checks that each case-time bound reruns when the scale changes."""
        statuses = self.statuses(self.completed(2))
        replayed = []
        for name in BOUND_CALLS:
            package = f"{MODULE_PATH}/{name}"
            self.assertIn(
                package,
                statuses,
                f"{package} reported no result on the third run at "
                f"GATE_TIME_SCALE={self.scales[2]}; output:\n"
                f"{self.runs[2].stdout}",
            )
            if statuses.get(package) == "(cached)":
                replayed.append(package)
        self.assertEqual(
            replayed,
            [],
            f"at GATE_TIME_SCALE={self.scales[2]} these packages replayed a result "
            f"from another scale: {', '.join(replayed)}; the value a bound read did "
            f"not enter the test cache key, so a run at another scale can replay "
            f"this one's passing result",
        )

    def test_a_package_level_bound_stays_cached(self):
        """Checks that a package-level bound stays cached when the scale changes."""
        statuses = self.statuses(self.completed(2))
        package = f"{MODULE_PATH}/premise"
        self.assertEqual(
            statuses.get(package),
            "(cached)",
            f"{package} was rerun at GATE_TIME_SCALE={self.scales[2]}; a "
            f"package-level bound should not reach the cache key, and this case "
            f"pins that premise",
        )

    def test_an_invalid_scale_stops_a_package_that_never_reads_it(self):
        """Checks that the startup validation stops a package importing coretest."""
        env = dict(os.environ)
        env["GATE_TIME_SCALE"] = "abc"
        env["GOWORK"] = "off"
        env["GOTOOLCHAIN"] = "local"
        env["GOPROXY"] = "off"
        run = subprocess.run(
            ["go", "test", "-count=1", "./imports/"],
            cwd=self.module,
            env=env,
            capture_output=True,
            text=True,
            timeout=600,
        )
        output = run.stdout + run.stderr
        self.assertNotEqual(
            0,
            run.returncode,
            f"a package importing coretest passed under GATE_TIME_SCALE=abc:\n{output}",
        )
        self.assertIn("at least 1", output)


if __name__ == "__main__":
    unittest.main()
