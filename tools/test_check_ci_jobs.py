#!/usr/bin/env python3
"""Cases for check_ci_jobs.py's criteria and exit codes.

The subject is one ruling: the `check` workflow runs exactly the legs `make
check` runs, and a leg split over jobs covers every module `go.work` lists.
Each case drives the gate over a scratch tree whose Makefile, ci.yml and
go.work are written by the case, so the finding is read from the gate's
output rather than inferred from this repository. The last case scans this
repository.

Stdlib-only (unittest + tempfile). Run directly:

    python3 tools/test_check_ci_jobs.py
"""

from __future__ import annotations

import contextlib
import io
import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)

sys.path.insert(0, HERE)

import check_ci_jobs as m  # noqa: E402

MAKEFILE = """\
check: check-lint check-test
\t@true

check-announce:
\t@echo "== check"

check-lint: check-announce
\t$(call mark,lint)
\t@set -o pipefail; $(MAKE) --no-print-directory lint $(call segment-output,lint)

check-test: check-announce
\t$(call mark,test)
\t@set -o pipefail; $(MAKE) --no-print-directory test $(call segment-output,test)

check-test-race: check-announce
\t$(call mark,test-race)
\t@set -o pipefail; $(MAKE) --no-print-directory test-race $(call segment-output,test-race)
"""

RACE_MAKEFILE = MAKEFILE.replace(
    "check: check-lint check-test",
    "check: check-lint check-test check-test-race",
)

GO_WORK = """\
go 1.27.4

use (
\t./pkg
\t./starters/demo/server
)
"""

GATE_ENV = """\
env:
  GATE_JOBS: "4"
  GOFLAGS: -count=1

"""

CLEAN_CI = GATE_ENV + """\
jobs:
  static:
    runs-on: ubuntu-26.04
    steps:
      - name: Static checks
        run: make lint
  go-test:
    runs-on: ubuntu-26.04
    steps:
      - name: Go tests
        run: |
          set -e
          make test
"""

SPLIT_RACE_CI = GATE_ENV + """\
jobs:
  static:
    runs-on: ubuntu-26.04
    steps:
      - name: Static checks
        run: make lint test
  race-pkg:
    runs-on: ubuntu-26.04
    steps:
      - name: pkg race
        run: make test-race MODULES=./pkg
  race-demo:
    runs-on: ubuntu-26.04
    steps:
      - name: demo race
        run: make test-race MODULES=./starters/demo/server
"""


class GateCase(unittest.TestCase):
    makefile = MAKEFILE
    workflow = CLEAN_CI
    go_work = GO_WORK

    def setUp(self) -> None:
        self.scratch = tempfile.TemporaryDirectory()
        self.root = self.scratch.name
        self.write("Makefile", self.makefile)
        self.write(os.path.join(".github", "workflows", "ci.yml"), self.workflow)
        self.write("go.work", self.go_work)

    def tearDown(self) -> None:
        self.scratch.cleanup()

    def write(self, relative: str, content: str) -> None:
        path = os.path.join(self.root, relative)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(content)

    def run_gate(self, root: str | None = None) -> tuple[int, str, str]:
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", root if root is not None else self.root])
        return code, out.getvalue(), err.getvalue()


class TestTheLegSetsMustMatch(GateCase):
    def test_a_workflow_running_every_segment_passes(self) -> None:
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)
        self.assertIn("run the 2 leg(s)", out)

    def test_a_segment_no_job_runs_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            "jobs:\n  static:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make lint\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("no job runs `test`, the leg of check-test", out)

    def test_a_target_that_is_no_segment_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI + "  extra:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make deploy\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("runs `deploy`, which no `check` segment runs", out)

    def test_a_job_calling_check_itself_does_not_satisfy_the_gate(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make check\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("runs `check`, which no `check` segment runs", out)
        self.assertIn("no job runs `lint`", out)

    def test_make_flags_and_trailing_segment_output_are_not_targets(self) -> None:
        # The fixture's segments call `$(MAKE) --no-print-directory <leg>
        # $(call segment-output,<leg>)`; a clean pass means neither the flag
        # nor the call's words were read as a leg.
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_several_targets_on_one_make_line_are_all_counted(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            GATE_ENV
            + "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make lint test\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_a_commented_out_make_line_is_not_read(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n"
            "      - run: |\n          make lint\n          # make test\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("no job runs `test`", out)


class TestASplitLegMustCoverTheRoster(GateCase):
    makefile = RACE_MAKEFILE
    workflow = SPLIT_RACE_CI

    def test_two_jobs_covering_both_modules_pass(self) -> None:
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_a_module_no_job_covers_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.split("  race-demo:")[0],
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("go.work module starters/demo/server is covered by no job", out)

    def test_a_module_go_work_does_not_list_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.replace("./starters/demo/server", "./elsewhere"),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("MODULES=elsewhere, which go.work does not list", out)

    def test_an_empty_modules_assignment_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.replace("MODULES=./pkg", "MODULES="),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("MODULES= naming no module", out)

    def test_a_leg_run_without_modules_covers_the_whole_roster(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.replace(" MODULES=./pkg", "").split("  race-demo:")[0],
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)


class TestTheGateRefusesASubjectItCannotRead(GateCase):
    def test_a_root_that_is_not_a_directory_exits_2(self) -> None:
        code, _, err = self.run_gate(os.path.join(self.root, "missing"))
        self.assertEqual(2, code)
        self.assertIn("not a directory", err)

    def test_a_missing_workflow_exits_2(self) -> None:
        os.remove(os.path.join(self.root, ".github", "workflows", "ci.yml"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no workflow under", err)

    def test_a_makefile_without_check_exits_2(self) -> None:
        self.write("Makefile", "lint:\n\t@true\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no `check` target", err)

    def test_check_with_no_segment_exits_2(self) -> None:
        self.write("Makefile", "check:\n\t@true\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("names no `check-*` segment", err)

    def test_a_workflow_with_no_job_exits_2(self) -> None:
        self.write(os.path.join(".github", "workflows", "ci.yml"), "on: push\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("declares no job", err)

    def test_a_workflow_running_no_make_exits_2(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: true\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("runs no make target", err)

    def test_go_work_with_no_module_exits_2(self) -> None:
        self.write("go.work", "go 1.27.4\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("lists no module", err)


class TestTheWorkflowEnvCarriesTheGateSettings(GateCase):
    def test_a_workflow_without_the_gate_env_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace(GATE_ENV, ""),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("env sets no GATE_JOBS", out)
        self.assertIn("GOFLAGS does not contain -count=1", out)

    def test_a_missing_gate_jobs_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace('  GATE_JOBS: "4"\n', ""),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("env sets no GATE_JOBS", out)
        self.assertNotIn("GOFLAGS does not contain", out)

    def test_goflags_without_count_1_is_a_finding(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace("GOFLAGS: -count=1", "GOFLAGS: -mod=readonly"),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("GOFLAGS does not contain -count=1", out)

    def test_count_1_among_other_flags_passes(self) -> None:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace("GOFLAGS: -count=1", "GOFLAGS: -mod=readonly -count=1"),
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_the_gate_settings_present_pass(self) -> None:
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)


class TestEveryGateJobMustBeAllowedToFailTheRun(GateCase):
    def write_job_attribute(self, line: str) -> tuple[int, str, str]:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace(
                "  go-test:\n    runs-on:", f"  go-test:\n    {line}\n    runs-on:"
            ),
        )
        return self.run_gate()

    def test_continue_on_error_true_is_a_finding(self) -> None:
        code, out, err = self.write_job_attribute("continue-on-error: true")
        self.assertEqual(1, code, out + err)
        self.assertIn(
            "job go-test sets continue-on-error: true, so its failure would not fail",
            out,
        )

    def test_continue_on_error_false_passes(self) -> None:
        code, out, err = self.write_job_attribute("continue-on-error: false")
        self.assertEqual(0, code, out + err)

    def test_a_condition_that_skips_the_job_is_a_finding(self) -> None:
        code, out, err = self.write_job_attribute("if: github.event_name == 'push'")
        self.assertEqual(1, code, out + err)
        self.assertIn("job go-test runs only under if:", out)
        self.assertIn("which can skip a gate segment", out)

    def test_always_is_an_allowed_condition(self) -> None:
        code, out, err = self.write_job_attribute("if: always()")
        self.assertEqual(0, code, out + err)

    def test_always_in_an_expression_is_an_allowed_condition(self) -> None:
        code, out, err = self.write_job_attribute("if: ${{ always() }}")
        self.assertEqual(0, code, out + err)

    def write_step_attribute(self, line: str) -> tuple[int, str, str]:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            GATE_ENV
            + "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n"
            f"      - name: all\n        {line}\n        run: make lint test\n",
        )
        return self.run_gate()

    def test_a_step_condition_that_skips_a_gate_segment_is_a_finding(self) -> None:
        code, out, err = self.write_step_attribute("if: false")
        self.assertEqual(1, code, out + err)
        self.assertIn("job all step all runs only under if: false", out)
        self.assertIn("which can skip a gate segment", out)

    def test_a_step_continue_on_error_true_is_a_finding(self) -> None:
        code, out, err = self.write_step_attribute("continue-on-error: true")
        self.assertEqual(1, code, out + err)
        self.assertIn(
            "job all step all sets continue-on-error: true, so its failure would not fail",
            out,
        )

    def test_a_step_always_is_an_allowed_condition(self) -> None:
        code, out, err = self.write_step_attribute("if: always()")
        self.assertEqual(0, code, out + err)

    def test_a_step_continue_on_error_false_passes(self) -> None:
        code, out, err = self.write_step_attribute("continue-on-error: false")
        self.assertEqual(0, code, out + err)

    def test_a_step_condition_on_a_non_gate_step_is_not_a_finding(self) -> None:
        # Only a step that runs a `check` leg is read; a step skipping some
        # other command narrows nothing the gate depends on.
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            GATE_ENV
            + "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n"
            "      - name: all\n        if: false\n        run: make lint test\n"
            "      - name: docs\n        if: false\n        run: go test ./...\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("job all step all runs only under if:", out)
        self.assertNotIn("job all step docs", out)


class TestThisRepository(GateCase):
    def test_the_check_workflow_runs_every_segment_of_check(self) -> None:
        code, out, err = self.run_gate(REPO)
        self.assertEqual(0, code, out + err)


if __name__ == "__main__":
    unittest.main()
