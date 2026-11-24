#!/usr/bin/env python3
"""Cases for check_ci_jobs.py's criteria and exit codes."""

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
        """Test that a workflow running every segment passes."""
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)
        self.assertIn("run the 2 leg(s)", out)

    def test_a_segment_no_job_runs_is_a_finding(self) -> None:
        """Test that a segment no job runs is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            "jobs:\n  static:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make lint\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("no job runs `test`, the leg of check-test", out)

    def test_a_target_that_is_no_segment_is_a_finding(self) -> None:
        """Test that a target that is no segment is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI + "  extra:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make deploy\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("runs `deploy`, which no `check` segment runs", out)

    def test_a_job_calling_check_itself_does_not_satisfy_the_gate(self) -> None:
        """Test that a job calling check itself does not satisfy the gate."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make check\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("runs `check`, which no `check` segment runs", out)
        self.assertIn("no job runs `lint`", out)

    def test_make_flags_and_trailing_segment_output_are_not_targets(self) -> None:
        """Test that make flags and trailing segment output are not targets."""
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_several_targets_on_one_make_line_are_all_counted(self) -> None:
        """Test that several targets on one make line are all counted."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            GATE_ENV
            + "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: make lint test\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_a_commented_out_make_line_is_not_read(self) -> None:
        """Test that a commented out make line is not read."""
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
        """Test that two jobs covering both modules pass."""
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_a_module_no_job_covers_is_a_finding(self) -> None:
        """Test that a module no job covers is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.split("  race-demo:")[0],
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("go.work module starters/demo/server is covered by no job", out)

    def test_a_module_go_work_does_not_list_is_a_finding(self) -> None:
        """Test that a module go work does not list is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.replace("./starters/demo/server", "./elsewhere"),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("MODULES=elsewhere, which go.work does not list", out)

    def test_an_empty_modules_assignment_is_a_finding(self) -> None:
        """Test that an empty modules assignment is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.replace("MODULES=./pkg", "MODULES="),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("MODULES= naming no module", out)

    def test_a_leg_run_without_modules_covers_the_whole_roster(self) -> None:
        """Test that a leg run without modules covers the whole roster."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            SPLIT_RACE_CI.replace(" MODULES=./pkg", "").split("  race-demo:")[0],
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)


class TestTheGateRefusesASubjectItCannotRead(GateCase):
    def test_a_root_that_is_not_a_directory_exits_2(self) -> None:
        """Test that a root that is not a directory exits 2."""
        code, _, err = self.run_gate(os.path.join(self.root, "missing"))
        self.assertEqual(2, code)
        self.assertIn("not a directory", err)

    def test_a_missing_workflow_exits_2(self) -> None:
        """Test that a missing workflow exits 2."""
        os.remove(os.path.join(self.root, ".github", "workflows", "ci.yml"))
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no workflow under", err)

    def test_a_makefile_without_check_exits_2(self) -> None:
        """Test that a makefile without check exits 2."""
        self.write("Makefile", "lint:\n\t@true\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("no `check` target", err)

    def test_check_with_no_segment_exits_2(self) -> None:
        """Test that check with no segment exits 2."""
        self.write("Makefile", "check:\n\t@true\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("names no `check-*` segment", err)

    def test_a_workflow_with_no_job_exits_2(self) -> None:
        """Test that a workflow with no job exits 2."""
        self.write(os.path.join(".github", "workflows", "ci.yml"), "on: push\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("declares no job", err)

    def test_a_workflow_running_no_make_exits_2(self) -> None:
        """Test that a workflow running no make exits 2."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n      - run: true\n",
        )
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("runs no make target", err)

    def test_go_work_with_no_module_exits_2(self) -> None:
        """Test that go work with no module exits 2."""
        self.write("go.work", "go 1.27.4\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("lists no module", err)


class TestTheWorkflowEnvCarriesTheGateSettings(GateCase):
    def test_a_workflow_without_the_gate_env_is_a_finding(self) -> None:
        """Test that a workflow without the gate env is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace(GATE_ENV, ""),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("env sets no GATE_JOBS", out)
        self.assertIn("GOFLAGS does not contain -count=1", out)

    def test_a_missing_gate_jobs_is_a_finding(self) -> None:
        """Test that a missing gate jobs is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace('  GATE_JOBS: "4"\n', ""),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("env sets no GATE_JOBS", out)
        self.assertNotIn("GOFLAGS does not contain", out)

    def test_goflags_without_count_1_is_a_finding(self) -> None:
        """Test that goflags without count 1 is a finding."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace("GOFLAGS: -count=1", "GOFLAGS: -mod=readonly"),
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("GOFLAGS does not contain -count=1", out)

    def test_count_1_among_other_flags_passes(self) -> None:
        """Test that count 1 among other flags passes."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            CLEAN_CI.replace("GOFLAGS: -count=1", "GOFLAGS: -mod=readonly -count=1"),
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, out + err)

    def test_the_gate_settings_present_pass(self) -> None:
        """Test that the gate settings present pass."""
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
        """Test that continue on error true is a finding."""
        code, out, err = self.write_job_attribute("continue-on-error: true")
        self.assertEqual(1, code, out + err)
        self.assertIn(
            "job go-test sets continue-on-error: true, so its failure would not fail",
            out,
        )

    def test_continue_on_error_false_passes(self) -> None:
        """Test that continue on error false passes."""
        code, out, err = self.write_job_attribute("continue-on-error: false")
        self.assertEqual(0, code, out + err)

    def test_a_condition_that_skips_the_job_is_a_finding(self) -> None:
        """Test that a condition that skips the job is a finding."""
        code, out, err = self.write_job_attribute("if: github.event_name == 'push'")
        self.assertEqual(1, code, out + err)
        self.assertIn("job go-test runs only under if:", out)
        self.assertIn("which can skip a gate segment", out)

    def test_always_is_an_allowed_condition(self) -> None:
        """Test that always is an allowed condition."""
        code, out, err = self.write_job_attribute("if: always()")
        self.assertEqual(0, code, out + err)

    def test_always_in_an_expression_is_an_allowed_condition(self) -> None:
        """Test that always in an expression is an allowed condition."""
        code, out, err = self.write_job_attribute("if: ${{ always() }}")
        self.assertEqual(0, code, out + err)

    def test_skipping_a_draft_pull_request_is_an_allowed_condition(self) -> None:
        """Test that skipping a draft pull request is an allowed condition."""
        code, out, err = self.write_job_attribute(
            "if: github.event_name != 'pull_request' || "
            "!github.event.pull_request.draft"
        )
        self.assertEqual(0, code, out + err)

    def test_skipping_a_draft_in_an_expression_is_an_allowed_condition(
        self,
    ) -> None:
        """Test that skipping a draft in an expression is an allowed condition."""
        code, out, err = self.write_job_attribute(
            "if: ${{ github.event_name != 'pull_request' || "
            "!github.event.pull_request.draft }}"
        )
        self.assertEqual(0, code, out + err)

    def test_a_draft_condition_with_more_terms_is_a_finding(self) -> None:
        """Test that a draft condition with more terms is a finding."""
        code, out, err = self.write_job_attribute(
            "if: ${{ (github.event_name != 'pull_request' || "
            "!github.event.pull_request.draft) && github.ref == 'refs/heads/main' }}"
        )
        self.assertEqual(1, code, out + err)
        self.assertIn("which can skip a gate segment", out)

    def write_step_attribute(self, line: str) -> tuple[int, str, str]:
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            GATE_ENV
            + "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n"
            f"      - name: all\n        {line}\n        run: make lint test\n",
        )
        return self.run_gate()

    def test_a_step_condition_that_skips_a_gate_segment_is_a_finding(self) -> None:
        """Test that a step condition that skips a gate segment is a finding."""
        code, out, err = self.write_step_attribute("if: false")
        self.assertEqual(1, code, out + err)
        self.assertIn("job all step all runs only under if: false", out)
        self.assertIn("which can skip a gate segment", out)

    def test_a_step_continue_on_error_true_is_a_finding(self) -> None:
        """Test that a step continue on error true is a finding."""
        code, out, err = self.write_step_attribute("continue-on-error: true")
        self.assertEqual(1, code, out + err)
        self.assertIn(
            "job all step all sets continue-on-error: true, so its failure would not fail",
            out,
        )

    def test_a_step_that_starts_with_its_run_is_read_too(self) -> None:
        """Test that a step that starts with its run is read too."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            GATE_ENV
            + "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n"
            "      - run: make lint test\n        continue-on-error: true\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn(
            "continue-on-error: true, so its failure would not fail", out
        )

    def test_a_step_that_starts_with_a_block_run_is_read_too(self) -> None:
        """Test that a step that starts with a block run is read too."""
        self.write(
            os.path.join(".github", "workflows", "ci.yml"),
            GATE_ENV
            + "jobs:\n  all:\n    runs-on: ubuntu-26.04\n    steps:\n"
            "      - run: |\n          make lint test\n"
            "        if: false\n",
        )
        code, out, err = self.run_gate()
        self.assertEqual(1, code, out + err)
        self.assertIn("runs only under if: false", out)

    def test_a_step_always_is_an_allowed_condition(self) -> None:
        """Test that a step always is an allowed condition."""
        code, out, err = self.write_step_attribute("if: always()")
        self.assertEqual(0, code, out + err)

    def test_a_step_continue_on_error_false_passes(self) -> None:
        """Test that a step continue on error false passes."""
        code, out, err = self.write_step_attribute("continue-on-error: false")
        self.assertEqual(0, code, out + err)

    def test_a_step_condition_on_a_non_gate_step_is_not_a_finding(self) -> None:
        """Test that a step condition on a non gate step is not a finding."""
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
        """Test that the check workflow runs every segment of check."""
        code, out, err = self.run_gate(REPO)
        self.assertEqual(0, code, out + err)


if __name__ == "__main__":
    unittest.main()
