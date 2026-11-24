#!/usr/bin/env python3
"""Cases for tools/backends.py, the per-checkout integration stack."""

from __future__ import annotations

import contextlib
import io
import json
import os
import pathlib
import socket
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import backends

PORT_VARIABLES = backends.PORT_VARIABLES
ADDRESS_VARIABLES = [address for _, _, address, _ in backends.SERVICES]

FAKE_DOCKER = """\
#!{python}
import json, os, sys

args = sys.argv[1:]
kind = next((argument for argument in args if argument in ("up", "down", "ps")), "")
with open(os.environ["FAKE_DOCKER_LOG"], "a", encoding="utf-8") as log:
    log.write(json.dumps({{
        "args": args,
        "ports": {{
            name: os.environ.get(name)
            for name in os.environ
            if name.startswith("SPEED_TEST_") and name.endswith("_PORT")
        }},
    }}) + "\\n")

if kind == "ps":
    with open(os.environ["FAKE_DOCKER_PS"], encoding="utf-8") as output:
        sys.stdout.write(output.read())
    sys.exit(0)

if kind == "up":
    count_path = os.environ["FAKE_DOCKER_COUNT"]
    count = int(open(count_path, encoding="utf-8").read() or "0") if os.path.exists(count_path) else 0
    count += 1
    with open(count_path, "w", encoding="utf-8") as counter:
        counter.write(str(count))
    if count <= int(os.environ.get("FAKE_DOCKER_UP_FAILURES", "0")):
        print("bind: address already in use")
        sys.exit(1)
    sys.exit(0)

if kind == "down" and os.environ.get("FAKE_DOCKER_DOWN_FAILS"):
    print("cannot stop container")
    sys.exit(1)

sys.exit(0)
"""


def healthy_stack_output() -> str:
    return "\n".join(
        json.dumps({"Service": service, "State": "running", "Health": "healthy"})
        for service in backends.EXPECTED_SERVICES
    )


class PortAndNameTests(unittest.TestCase):
    def test_allocates_four_distinct_ports_that_bind(self) -> None:
        """Test that allocates four distinct ports that bind."""
        ports = backends.allocate_ports()
        self.assertEqual(len(ports), 4)
        self.assertEqual(len(set(ports)), 4, f"allocate_ports returned a duplicate: {ports}")
        for port in ports:
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
                sock.bind(("127.0.0.1", port))

    def test_project_name_is_stable_for_one_root_and_distinct_for_another(self) -> None:
        """Test that project name is stable for one root and distinct for another."""
        with tempfile.TemporaryDirectory() as first, tempfile.TemporaryDirectory() as second:
            name = backends.project_name(first)
            self.assertRegex(name, r"^speed-test-[0-9a-f]{12}$")
            self.assertEqual(name, backends.project_name(first))
            self.assertNotEqual(name, backends.project_name(second))

    def test_state_file_round_trips_only_the_project_and_ports(self) -> None:
        """Test that state file round trips only the project and ports."""
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            state_path = root / "state.env"
            ports = backends.allocate_ports()
            project = backends.project_name(root)
            backends.write_state(state_path, project, ports)

            state = backends.read_state(state_path, root)
            self.assertIsNotNone(state)
            self.assertEqual(state.project, project)
            self.assertEqual(
                [state.ports[variable] for variable in PORT_VARIABLES],
                ports,
            )
            keys = [
                line.split("=", 1)[0]
                for line in state_path.read_text(encoding="utf-8").splitlines()
                if line
            ]
            self.assertEqual(sorted(keys), sorted([backends.PROJECT_KEY, *PORT_VARIABLES]))

    def test_a_state_file_for_another_root_is_not_read(self) -> None:
        """Test that a state file for another root is not read."""
        with tempfile.TemporaryDirectory() as first, tempfile.TemporaryDirectory() as second:
            state_path = pathlib.Path(first) / "state.env"
            ports = backends.allocate_ports()
            backends.write_state(state_path, backends.project_name(first), ports)
            self.assertIsNone(backends.read_state(state_path, second))

    def test_env_lines_carry_every_variable_the_fixtures_read(self) -> None:
        """Test that env lines carry every variable the fixtures read."""
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            state_path = root / "state.env"
            ports = [15432, 16379, 14222, 11222]
            backends.write_state(state_path, backends.project_name(root), ports)
            state = backends.read_state(state_path, root)
            self.assertIsNotNone(state)
            lines = backends.env_lines(state)
            self.assertEqual(
                lines,
                [
                    "SPEED_TEST_PG_ADDR=127.0.0.1:15432",
                    "SPEED_TEST_REDIS_ADDR=127.0.0.1:16379",
                    "SPEED_TEST_NATS_URL=nats://127.0.0.1:14222",
                    "SPEED_TEST_MEMCACHED_ADDR=127.0.0.1:11222",
                ],
            )
            self.assertEqual(
                [line.split("=", 1)[0] for line in lines],
                ADDRESS_VARIABLES,
            )

    def test_env_fails_loudly_without_a_state_file(self) -> None:
        """Test that env fails loudly without a state file."""
        with tempfile.TemporaryDirectory() as directory:
            missing = pathlib.Path(directory) / "absent.env"
            stderr = io.StringIO()
            with contextlib.redirect_stderr(stderr):
                code = backends.print_env(missing, pathlib.Path(directory))
            self.assertNotEqual(code, 0)
            self.assertIn("make deps-up", stderr.getvalue())


class ComposeTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.scratch = pathlib.Path(temporary.name)
        self.root = self.scratch / "checkout"
        self.root.mkdir()
        self.state = self.scratch / "state.env"
        self.log = self.scratch / "calls.jsonl"
        self.count = self.scratch / "count"
        self.ps_output = self.scratch / "ps.json"
        self.docker = self.scratch / "fake-docker"
        self.docker.write_text(FAKE_DOCKER.format(python=sys.executable), encoding="utf-8")
        self.docker.chmod(0o755)

    def run_with(
        self,
        action: str,
        *,
        up_failures: int = 0,
        down_fails: bool = False,
        ps_output: str = "",
    ) -> tuple[int, str, str]:
        self.ps_output.write_text(ps_output, encoding="utf-8")
        environment = {
            "FAKE_DOCKER_LOG": str(self.log),
            "FAKE_DOCKER_COUNT": str(self.count),
            "FAKE_DOCKER_UP_FAILURES": str(up_failures),
            "FAKE_DOCKER_DOWN_FAILS": "1" if down_fails else "",
            "FAKE_DOCKER_PS": str(self.ps_output),
        }
        stdout, stderr = io.StringIO(), io.StringIO()
        with mock.patch.dict(os.environ, environment), contextlib.redirect_stdout(
            stdout
        ), contextlib.redirect_stderr(stderr):
            if action == "up":
                code = backends.up(self.state, root=self.root, docker=str(self.docker))
            elif action == "down":
                code = backends.down(self.state, root=self.root, docker=str(self.docker))
            else:
                raise AssertionError(action)
        return code, stdout.getvalue(), stderr.getvalue()

    def calls(self, kind: str | None = None) -> list[dict]:
        if not self.log.exists():
            return []
        records = [
            json.loads(line)
            for line in self.log.read_text(encoding="utf-8").splitlines()
            if line
        ]
        if kind is None:
            return records
        return [record for record in records if kind in record["args"]]

    def write_stack(self) -> list[int]:
        ports = [15432, 16379, 14222, 11222]
        backends.write_state(self.state, backends.project_name(self.root), ports)
        return ports

    def test_up_retries_a_failed_start_and_keeps_the_successful_allocation(self) -> None:
        """Test that up retries a failed start and keeps the successful allocation."""
        code, stdout, stderr = self.run_with("up", up_failures=2)
        self.assertEqual(code, 0, stdout + stderr)
        attempts = self.calls("up")
        self.assertEqual(len(attempts), 3, stderr)
        for attempt in attempts:
            self.assertEqual(sorted(attempt["ports"]), sorted(PORT_VARIABLES))
            self.assertTrue(all(value.isdigit() for value in attempt["ports"].values()))
        self.assertTrue(self.state.exists())
        state = backends.read_state(self.state, self.root)
        self.assertIsNotNone(state)
        self.assertEqual(
            [state.ports[variable] for variable in PORT_VARIABLES],
            [int(attempts[-1]["ports"][variable]) for variable in PORT_VARIABLES],
            "the state file does not record the allocation the successful attempt used",
        )
        self.assertIn("started", stdout)

    def test_the_final_failure_removes_the_state_and_reports_the_last_output(self) -> None:
        """Test that the final failure removes the state and reports the last output."""
        code, _, stderr = self.run_with("up", up_failures=99)
        self.assertNotEqual(code, 0)
        self.assertEqual(len(self.calls("up")), backends.ATTEMPTS)
        self.assertFalse(self.state.exists(), "a failed allocation left its state file behind")
        self.assertTrue(self.calls("down"), "the failed project was not taken down")
        self.assertIn("bind: address already in use", stderr)
        self.assertIn("last output", stderr)

    def test_a_failed_cleanup_keeps_the_state_file_for_deps_down(self) -> None:
        """Test that a failed cleanup keeps the state file for deps down."""
        code, _, stderr = self.run_with("up", up_failures=99, down_fails=True)
        self.assertNotEqual(code, 0)
        self.assertTrue(self.calls("down"), "the failed project was not taken down")
        self.assertTrue(
            self.state.exists(),
            "a failed teardown deleted the state file 'make deps-down' needs to finish the job",
        )
        self.assertIsNotNone(backends.read_state(self.state, self.root))
        self.assertIn("cannot stop container", stderr)
        self.assertIn("make deps-down", stderr)
        self.assertIn("last output", stderr)

    def test_a_stale_state_file_is_not_reused(self) -> None:
        """Test that a stale state file is not reused."""
        self.write_stack()
        code, _, stderr = self.run_with("up", ps_output="")
        self.assertEqual(code, 0, stderr)
        self.assertTrue(self.calls("ps"), "the existing state was reused without checking the containers")
        self.assertTrue(self.calls("up"), "a stack whose containers are gone was reused")

    def test_a_healthy_stack_is_reused(self) -> None:
        """Test that a healthy stack is reused."""
        ports = self.write_stack()
        code, stdout, stderr = self.run_with("up", ps_output=healthy_stack_output())
        self.assertEqual(code, 0, stderr)
        self.assertEqual(self.calls("up"), [], "a healthy stack was started again")
        self.assertEqual(len(self.calls("ps")), 1)
        self.assertIn("reusing", stdout)
        state = backends.read_state(self.state, self.root)
        self.assertIsNotNone(state)
        self.assertEqual([state.ports[variable] for variable in PORT_VARIABLES], ports)

    def test_down_names_the_state_projects_project_and_removes_the_file(self) -> None:
        """Test that down names the state projects project and removes the file."""
        self.write_stack()
        project = backends.project_name(self.root)
        code, _, stderr = self.run_with("down")
        self.assertEqual(code, 0, stderr)
        calls = self.calls("down")
        self.assertEqual(len(calls), 1)
        arguments = calls[0]["args"]
        self.assertIn("-p", arguments)
        self.assertEqual(arguments[arguments.index("-p") + 1], project)
        self.assertFalse(self.state.exists())

    def test_down_without_a_state_file_touches_no_project(self) -> None:
        """Test that down without a state file touches no project."""
        code, _, _ = self.run_with("down")
        self.assertEqual(code, 0)
        self.assertEqual(self.calls(), [])

    def test_down_refuses_a_state_file_that_is_not_this_checkout(self) -> None:
        """Test that down refuses a state file that is not this checkout."""
        with tempfile.TemporaryDirectory() as other:
            backends.write_state(self.state, backends.project_name(other), [15432, 16379, 14222, 11222])
        code, _, stderr = self.run_with("down")
        self.assertNotEqual(code, 0)
        self.assertEqual(self.calls(), [])
        self.assertIn("refusing to guess", stderr)


if __name__ == "__main__":
    unittest.main()
