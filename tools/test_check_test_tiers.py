#!/usr/bin/env python3
"""Unit tests for check_test_tiers.py's criteria and exit codes."""

from __future__ import annotations

import contextlib
import io
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
REPO = HERE.parent

sys.path.insert(0, str(HERE))

import check_test_tiers as m  # noqa: E402

MAKEFILE = """check:
\t@$(MAKE) test
test:
\tgo -C ./pkg test ./...
test-race:
\tgo -C ./pkg test -race ./...
test-integration:
\tgo -C ./pkg test -tags=integration ./...
"""

WORKFLOW = """jobs:
  check:
    runs-on: ubuntu-latest # the commit gate
    steps:
      - run: make check
"""

BASELINE = {
    ".github/workflows/ci.yml": WORKFLOW,
    "pkg/unit_test.go": """//go:build integration
package pkg

import "testing"

func TestSomething(t *testing.T) {}
""",
    "pkg/fixture.go": """package pkg

import "testing"

func Connect(tb testing.TB) {}
""",
}


def case_file(constraint: str) -> str:
    head = f"{constraint}\n" if constraint else ""
    return f"{head}package pkg\n\nimport \"testing\"\n\nfunc TestSomething(t *testing.T) {{}}\n"


class GateCase(unittest.TestCase):
    """A scratch tree, written by the case and scanned by the gate."""

    def setUp(self) -> None:
        self.root = tempfile.mkdtemp(prefix="test-tiers-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)

    def write(self, relative: str, text: str) -> None:
        path = os.path.join(self.root, relative)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)

    def run_gate(self) -> tuple[int, str, str]:
        """Run the gate over the scratch tree and capture both streams."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = m.main(["--root", self.root])
            except SystemExit as exc:
                code = exc.code
        return code, out.getvalue(), err.getvalue()

    def tree(self, makefile: str = MAKEFILE, baseline: bool = True, **files: str) -> None:
        self.write("Makefile", makefile)
        for relative, text in dict(BASELINE if baseline else {}, **files).items():
            self.write(relative, text)


class TestTheTagsAgreeWithTheLegs(GateCase):
    def test_a_tag_the_legs_pass_is_clean(self):
        """Test that a tag the legs pass is clean."""
        self.tree(**{"pkg/tagged_test.go": case_file("//go:build integration")})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_the_race_tag_is_established_by_the_race_flag(self):
        """Test that the race tag is established by the race flag."""
        self.tree(
            makefile=MAKEFILE.replace(" -race ./...", " ./..."),
            **{"pkg/race_test.go": case_file("//go:build !race")},
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/race_test.go", out)

    def test_a_misspelled_tag_in_a_file_is_a_finding(self):
        """Test that a misspelled tag in a file is a finding."""
        self.tree(**{"pkg/typo_test.go": case_file("//go:build intergration")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/typo_test.go", out)
        self.assertIn("intergration", out)

    def test_a_misspelled_tag_in_the_makefile_is_a_finding(self):
        """Test that a misspelled tag in the makefile is a finding."""
        self.tree(makefile=MAKEFILE.replace("-tags=integration", "-tags=intergration"))
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("integration", out)

    def test_a_legacy_build_line_is_read_too(self):
        """Test that a legacy build line is read too."""
        self.tree(**{"pkg/typo_test.go": case_file("// +build intergration")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("intergration", out)

    def test_a_constraint_after_the_package_clause_is_not_read(self):
        """Test that a constraint after the package clause is not read."""
        self.tree(**{"pkg/prose.go": "package pkg\n\n// //go:build intergration\n"})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")


class TestAPlatformIsNotATierTag(GateCase):
    """A GOOS or GOARCH term is met by the runner's platform, not by -tags."""

    def test_a_linux_file_is_compiled_on_the_runner(self):
        """Test that a linux file is compiled on the runner."""
        self.tree(**{"pkg/busywait_linux_test.go": case_file("//go:build linux")})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_linux_constraint_alone_is_compiled_on_the_runner(self):
        """Test that a linux constraint alone is compiled on the runner."""
        self.tree(**{"pkg/busywait_test.go": case_file("//go:build linux")})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_linux_name_suffix_alone_is_compiled_on_the_runner(self):
        """Test that a linux name suffix alone is compiled on the runner."""
        self.tree(**{"pkg/busywait_linux_amd64_test.go": case_file("")})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_platform_and_a_tier_tag_are_compiled_by_that_tier_on_the_runner(self):
        """Test that a platform and a tier tag are compiled by that tier on the runner."""
        self.tree(**{"pkg/busywait_test.go": case_file("//go:build linux && integration")})
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_windows_name_suffix_is_a_finding(self):
        """Test that a windows name suffix is a finding."""
        self.tree(**{"pkg/console_windows_test.go": case_file("")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/console_windows_test.go", out)
        self.assertIn("linux/amd64", out)

    def test_a_zos_name_suffix_is_a_finding(self):
        """Test that a zos name suffix is a finding."""
        self.tree(**{"pkg/console_zos_test.go": case_file("")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/console_zos_test.go", out)
        self.assertIn("linux/amd64", out)

    def test_a_hurd_constraint_is_a_platform_term_not_an_unknown_tag(self):
        """Test that a hurd constraint is a platform term not an unknown tag."""
        self.tree(**{"pkg/console_test.go": case_file("//go:build hurd")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/console_test.go", out)
        self.assertIn("no platform a workflow runs on satisfies", out)
        self.assertNotIn("is passed by no leg", out)

    def test_a_32_bit_mips_suffix_variant_is_a_finding(self):
        """Test that a 32 bit mips suffix variant is a finding."""
        self.tree(**{"pkg/console_mips64p32le_test.go": case_file("")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/console_mips64p32le_test.go", out)

    def test_a_windows_constraint_is_a_finding(self):
        """Test that a windows constraint is a finding."""
        self.tree(**{"pkg/console_test.go": case_file("//go:build windows")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/console_test.go", out)

    def test_a_legacy_windows_line_is_a_finding(self):
        """Test that a legacy windows line is a finding."""
        self.tree(**{"pkg/console_test.go": case_file("// +build windows,integration")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/console_test.go", out)

    def test_a_windows_constraint_with_a_tier_tag_is_a_finding(self):
        """Test that a windows constraint with a tier tag is a finding."""
        self.tree(**{"pkg/console_test.go": case_file("//go:build windows && integration")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/console_test.go", out)

    def test_a_linux_file_excluded_on_linux_is_a_finding(self):
        """Test that a linux file excluded on linux is a finding."""
        self.tree(**{"pkg/busywait_linux_test.go": case_file("//go:build !linux")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/busywait_linux_test.go", out)

    def test_an_unknown_tag_beside_a_platform_is_still_a_finding(self):
        """Test that an unknown tag beside a platform is still a finding."""
        self.tree(**{"pkg/busywait_test.go": case_file("//go:build linux && nosuchtier")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("nosuchtier", out)

    def test_an_unknown_tag_is_still_a_finding(self):
        """Test that an unknown tag is still a finding."""
        self.tree(**{"pkg/busywait_test.go": case_file("//go:build nosuchtier")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("nosuchtier", out)

    def test_the_platforms_are_read_from_the_workflows(self):
        """Test that the platforms are read from the workflows."""
        self.tree(**{"pkg/atomic_arm64_test.go": case_file("")})
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/atomic_arm64_test.go", out)

        self.write(".github/workflows/arm.yml", WORKFLOW.replace("ubuntu-latest", "ubuntu-24.04-arm"))
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_the_ubuntu_26_04_runner_is_a_linux_amd64_platform(self):
        """Test that the ubuntu 26 04 runner is a linux amd64 platform."""
        self.tree(
            **{
                ".github/workflows/ci.yml": WORKFLOW.replace("ubuntu-latest", "ubuntu-26.04"),
                "pkg/busywait_linux_test.go": case_file("//go:build linux"),
                "pkg/atomic_amd64_test.go": case_file(""),
            }
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_the_ubuntu_26_04_runner_does_not_run_an_arm64_file(self):
        """Test that the ubuntu 26 04 runner does not run an arm64 file."""
        self.tree(
            **{
                ".github/workflows/ci.yml": WORKFLOW.replace("ubuntu-latest", "ubuntu-26.04"),
                "pkg/atomic_arm64_test.go": case_file(""),
            }
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/atomic_arm64_test.go", out)

    def test_the_ubuntu_26_04_arm_runner_is_a_linux_arm64_platform(self):
        """Test that the ubuntu 26 04 arm runner is a linux arm64 platform."""
        self.tree(
            **{
                ".github/workflows/ci.yml": WORKFLOW.replace("ubuntu-latest", "ubuntu-26.04"),
                "pkg/atomic_arm64_test.go": case_file(""),
            }
        )
        self.write(".github/workflows/arm.yml", WORKFLOW.replace("ubuntu-latest", "ubuntu-26.04-arm"))
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_tree_whose_workflows_name_no_runner_runs_a_platform_file_nowhere(self):
        """Test that a tree whose workflows name no runner runs a platform file nowhere."""
        self.tree(
            **{
                ".github/workflows/ci.yml": "on: push\n",
                "pkg/busywait_linux_test.go": case_file("//go:build linux"),
            }
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/busywait_linux_test.go", out)

    def test_a_runner_label_of_unknown_platform_is_an_infrastructure_error(self):
        """Test that a runner label of unknown platform is an infrastructure error."""
        self.tree(**{".github/workflows/ci.yml": WORKFLOW.replace("ubuntu-latest", "${{ matrix.os }}")})
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("matrix.os", err)

    def test_a_constraint_that_does_not_parse_is_an_infrastructure_error(self):
        """Test that a constraint that does not parse is an infrastructure error."""
        self.tree(**{"pkg/busywait_test.go": case_file("//go:build linux &&")})
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("pkg/busywait_test.go", err)


class TestNoLibrarySkipsACase(GateCase):
    def test_a_fixture_that_skips_is_a_finding(self):
        """Test that a fixture that skips is a finding."""
        self.tree(
            **{
                "pkg/fixture.go": """package pkg

import "testing"

func Connect(tb testing.TB) {
\ttb.Skip("no broker")
}
""",
            }
        )
        code, out, _ = self.run_gate()
        self.assertEqual(1, code)
        self.assertIn("pkg/fixture.go", out)
        self.assertIn("Skip", out)

    def test_a_case_may_skip(self):
        """Test that a case may skip."""
        self.tree(
            **{
                "pkg/skipping_test.go": """package pkg

import "testing"

func TestSomething(t *testing.T) {
\tt.Skip("a sibling case reported it")
}
""",
            }
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")

    def test_a_skip_method_on_a_non_testing_type_is_not_read(self):
        """Test that a skip method on a non testing type is not read."""
        self.tree(
            **{
                "pkg/reader.go": """package pkg

type Reader struct{}

func (Reader) Skip(n int) {}

func use(r Reader) { r.Skip(3) }
""",
            }
        )
        code, out, err = self.run_gate()
        self.assertEqual(0, code, f"stdout={out!r} stderr={err!r}")


class TestTheExitCodes(GateCase):
    def test_a_root_that_is_not_a_directory_is_an_infrastructure_error(self):
        """Test that a root that is not a directory is an infrastructure error."""
        missing = os.path.join(self.root, "not-there")
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                code = m.main(["--root", missing])
            except SystemExit as exc:
                code = exc.code
        self.assertEqual(2, code)

    def test_a_makefile_with_no_tag_is_an_infrastructure_error(self):
        """Test that a makefile with no tag is an infrastructure error."""
        self.tree(makefile="test:\n\tgo -C ./pkg test ./...\n")
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("Makefile", err)

    def test_a_tree_with_no_constraint_has_no_subject(self):
        """Test that a tree with no constraint has no subject."""
        self.tree(baseline=False, **{"pkg/plain.go": case_file("")})
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("build constraint", err)

    def test_a_tree_with_no_library_has_no_subject(self):
        """Test that a tree with no library has no subject."""
        self.tree(baseline=False, **{"pkg/plain_test.go": case_file("//go:build integration")})
        code, _, err = self.run_gate()
        self.assertEqual(2, code)
        self.assertIn("non-test", err)


class TestThisRepository(unittest.TestCase):
    """The gate over the tree it ships in: it has to be clean there."""

    def test_the_repository_is_clean(self):
        """Test that the repository is clean."""
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = m.main(["--root", str(REPO)])
        self.assertEqual(0, code, f"stdout={out.getvalue()!r} stderr={err.getvalue()!r}")

    def test_every_port_the_go_command_builds_for_is_a_platform_term(self):
        """Test that every port the go command builds for is a platform term."""
        listed = subprocess.run(
            ["go", "tool", "dist", "list"], check=True, capture_output=True, text=True
        ).stdout.split()
        ports = [port.split("/", 1) for port in listed]
        self.assertLessEqual({goos for goos, _ in ports}, set(m.GOOS))
        self.assertLessEqual({goarch for _, goarch in ports}, set(m.GOARCH))

    def test_the_platform_terms_are_the_names_go_build_matches_in_file_names(self):
        """Test that the platform terms are the names go build matches in file names."""
        goroot = subprocess.run(
            ["go", "env", "GOROOT"], check=True, capture_output=True, text=True
        ).stdout.strip()
        path = os.path.join(goroot, "src", "internal", "syslist", "syslist.go")
        self.assertTrue(os.path.isfile(path), f"{path} is where go/build keeps its platform names")
        with open(path, encoding="utf-8") as handle:
            source = handle.read()

        def known(variable: str) -> set[str]:
            body = re.search(
                rf"^var {variable} = map\[string\]bool\{{(.*?)^\}}", source, re.MULTILINE | re.DOTALL
            )
            self.assertIsNotNone(body, f"{path} no longer declares {variable} as a map literal")
            names = set(re.findall(r'^\s*"([^"]+)":\s*true,', body.group(1), re.MULTILINE))
            self.assertTrue(names, f"{variable} in {path} lists no name")
            return names

        self.assertEqual(known("KnownOS"), set(m.GOOS))
        self.assertEqual(known("KnownArch"), set(m.GOARCH))

    def test_every_runner_platform_is_a_port(self):
        """Test that every runner platform is a port."""
        for label, (goos, goarch) in m.RUNNER_PLATFORMS.items():
            self.assertIn(goos, m.GOOS, label)
            self.assertIn(goarch, m.GOARCH, label)


if __name__ == "__main__":
    unittest.main()
