#!/usr/bin/env python3
"""Bring this checkout's integration backends up, down, or print their addresses.

The four services -- PostgreSQL, Redis, NATS and Memcached -- are declared by
the repository-root compose.yaml. Each publishes to 127.0.0.1 on the port the
compose file reads from its SPEED_TEST_*_PORT variable, so a stack's ports are
whatever this script hands compose rather than fixed addresses.

`up` gives a checkout a stack of its own. The compose project name is derived
from the checkout root, so two checkouts on one machine never share containers.
When no state file exists, or the project's containers are not all healthy, it
binds 127.0.0.1:0 four times to obtain free ports, records the project name and
the ports in the state file, and runs `docker compose -p <project> up -d
--wait`. An existing healthy stack is reused instead of started again. A failed
start is retried with a fresh allocation, at most three times; the final failure
takes the project down, deletes the state file and prints compose's last output.
A teardown that fails keeps the state file, so `down` can still find the
project's leftover containers.

`down` takes down the project named by the state file and deletes the file. It
never falls back to a project name of its own, so it cannot touch another
checkout's containers.

`env` prints the four addresses the fixtures read, one shell assignment per
line, for `make test-integration` to eval with `set -a`.

The state file holds only the project name and four ports. It lives at
.cache/test-backends.env in the checkout and is not committed.

SPEED_TEST_DOCKER names the docker executable (default: docker); the test suite
uses it to drive a fake compose command.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import socket
import subprocess
import sys
from dataclasses import dataclass

ROOT = pathlib.Path(__file__).resolve().parent.parent
DEFAULT_STATE = ROOT / ".cache" / "test-backends.env"

# The project name prefix followed by a hash of the checkout root. The prefix
# makes the containers recognisable in `docker ps`; the hash keeps one
# checkout's project separate from another's.
PROJECT_PREFIX = "speed-test"

# How many allocations `up` tries before it gives up.
ATTEMPTS = 3

# Each service, the variable compose reads for its host port, the variable the
# fixture reads for its address, and the address template. The order is the
# order `up` hands the ports to the variables.
SERVICES = (
    ("postgres", "SPEED_TEST_PG_PORT", "SPEED_TEST_PG_ADDR", "127.0.0.1:{port}"),
    ("redis", "SPEED_TEST_REDIS_PORT", "SPEED_TEST_REDIS_ADDR", "127.0.0.1:{port}"),
    ("nats", "SPEED_TEST_NATS_PORT", "SPEED_TEST_NATS_URL", "nats://127.0.0.1:{port}"),
    ("memcached", "SPEED_TEST_MEMCACHED_PORT", "SPEED_TEST_MEMCACHED_ADDR", "127.0.0.1:{port}"),
)

PORT_VARIABLES = tuple(port for _, port, _, _ in SERVICES)
EXPECTED_SERVICES = tuple(service for service, _, _, _ in SERVICES)
PROJECT_KEY = "SPEED_TEST_PROJECT"


@dataclass(frozen=True)
class State:
    """The stack one checkout owns: its compose project name and four ports."""

    project: str
    ports: dict[str, int]


def project_name(root: str | os.PathLike[str]) -> str:
    """Return the compose project name a checkout root derives.

    The hash covers the resolved path, so a symlink and its target name the
    same project, and two worktrees of one repository name different ones.
    """
    resolved = os.path.realpath(os.fspath(root))
    digest = hashlib.sha256(resolved.encode("utf-8")).hexdigest()[:12]
    return f"{PROJECT_PREFIX}-{digest}"


def allocate_ports(count: int = len(SERVICES)) -> list[int]:
    """Return `count` distinct ports free on 127.0.0.1.

    Every socket stays bound until all of them have one, so the kernel cannot
    hand the same port out twice in one allocation.
    """
    sockets: list[socket.socket] = []
    try:
        for _ in range(count):
            sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            sock.bind(("127.0.0.1", 0))
            sockets.append(sock)
        return [sock.getsockname()[1] for sock in sockets]
    finally:
        for sock in sockets:
            sock.close()


def write_state(path: pathlib.Path, project: str, ports: list[int]) -> None:
    """Write the project name and the four ports to the state file."""
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [f"{PROJECT_KEY}={project}"]
    lines.extend(f"{variable}={port}" for variable, port in zip(PORT_VARIABLES, ports))
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def read_state(path: pathlib.Path, root: str | os.PathLike[str] = ROOT) -> State | None:
    """Return the state file's contents, or None when it is not this stack's.

    A file that is missing, malformed, carries an unexpected key, or names a
    project other than the one this root derives is not a state file this
    checkout may act on; `down` in particular must never guess a project.
    """
    try:
        text = path.read_text(encoding="utf-8")
    except (FileNotFoundError, NotADirectoryError):
        return None
    values: dict[str, str] = {}
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        key, separator, value = line.partition("=")
        if not separator:
            return None
        values[key] = value
    if set(values) != {PROJECT_KEY, *PORT_VARIABLES}:
        return None
    ports: dict[str, int] = {}
    for variable in PORT_VARIABLES:
        try:
            port = int(values[variable])
        except ValueError:
            return None
        if not 1 <= port <= 65535:
            return None
        ports[variable] = port
    if len(set(ports.values())) != len(ports):
        return None
    project = values[PROJECT_KEY]
    if project != project_name(root):
        return None
    return State(project, ports)


def env_lines(state: State) -> list[str]:
    """Return the four fixture addresses as shell assignments."""
    lines = []
    for _, port_variable, address_variable, template in SERVICES:
        lines.append(f"{address_variable}={template.format(port=state.ports[port_variable])}")
    return lines


def docker_binary() -> str:
    return os.environ.get("SPEED_TEST_DOCKER") or "docker"


def compose(
    docker: str,
    root: str | os.PathLike[str],
    project: str,
    *arguments: str,
    extra_env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    """Run docker compose for one project, capturing its output."""
    environment = os.environ.copy()
    if extra_env:
        environment.update(extra_env)
    return subprocess.run(
        [
            docker,
            "compose",
            "-f",
            os.path.join(os.fspath(root), "compose.yaml"),
            "-p",
            project,
            *arguments,
        ],
        env=environment,
        capture_output=True,
        text=True,
        check=False,
    )


def containers_healthy(output: str) -> bool:
    """Report whether every service is present, running and healthy.

    `docker compose ps --format json` prints one JSON object per line on the
    compose releases this repository runs, and a single array on some newer
    ones, so both shapes are read.
    """
    found: dict[str, tuple[str, str]] = {}
    for line in output.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            return False
        records = record if isinstance(record, list) else [record]
        for item in records:
            if not isinstance(item, dict):
                return False
            service = item.get("Service")
            if service in EXPECTED_SERVICES:
                found[service] = (item.get("State", ""), item.get("Health", ""))
    return all(found.get(service) == ("running", "healthy") for service in EXPECTED_SERVICES)


def stack_healthy(docker: str, root: str | os.PathLike[str], project: str) -> bool:
    """Report whether the named project's four containers are up and healthy."""
    try:
        process = compose(docker, root, project, "ps", "--format", "json")
    except OSError:
        return False
    return process.returncode == 0 and containers_healthy(process.stdout)


def describe(project: str, ports: list[int]) -> str:
    pairs = " ".join(
        f"{service}={port}" for (service, _, _, _), port in zip(SERVICES, ports)
    )
    return f"{project} ({pairs})"


def up(
    state_path: pathlib.Path,
    root: str | os.PathLike[str] = ROOT,
    docker: str | None = None,
) -> int:
    """Start or reuse this checkout's stack, returning a process exit code."""
    docker = docker or docker_binary()
    project = project_name(root)
    state = read_state(state_path, root)
    if state is not None and stack_healthy(docker, root, state.project):
        ports = [state.ports[variable] for variable in PORT_VARIABLES]
        print(f"backends: reusing {describe(state.project, ports)}")
        return 0

    last_output = ""
    for attempt in range(1, ATTEMPTS + 1):
        ports = allocate_ports()
        write_state(state_path, project, ports)
        extra_env = dict(zip(PORT_VARIABLES, (str(port) for port in ports)))
        try:
            process = compose(docker, root, project, "up", "-d", "--wait", extra_env=extra_env)
        except OSError as exc:
            last_output = f"cannot run {docker}: {exc}"
            print(f"backends: attempt {attempt} failed: {last_output}", file=sys.stderr)
            continue
        if process.returncode == 0:
            print(f"backends: started {describe(project, ports)}")
            return 0
        last_output = "\n".join(
            part for part in (process.stdout, process.stderr) if part
        ).strip()
        print(f"backends: attempt {attempt} failed:\n{last_output}", file=sys.stderr)

    # A failed start may have created some of the containers; take the project
    # down before reporting, so a retry starts from nothing. If that teardown
    # itself fails, the leftover containers keep this checkout's project name,
    # so the state file is kept: `down` reads the project from it and can still
    # finish the job. Deleting it would leave the containers with no pointer.
    cleanup_failed = False
    cleanup_output = ""
    try:
        takedown = compose(docker, root, project, "down")
    except OSError as exc:
        cleanup_failed = True
        cleanup_output = f"cannot run {docker}: {exc}"
    else:
        if takedown.returncode != 0:
            cleanup_failed = True
            cleanup_output = "\n".join(
                part for part in (takedown.stdout, takedown.stderr) if part
            ).strip()
    if cleanup_failed:
        print(
            f"backends: no stack for {project} after {ATTEMPTS} attempts; last output:\n{last_output}\n"
            f"backends: taking the project down failed, so {state_path} is kept for "
            f"'make deps-down' to retry:\n{cleanup_output}",
            file=sys.stderr,
        )
        return 1
    state_path.unlink(missing_ok=True)
    print(
        f"backends: no stack for {project} after {ATTEMPTS} attempts; last output:\n{last_output}",
        file=sys.stderr,
    )
    return 1


def down(
    state_path: pathlib.Path,
    root: str | os.PathLike[str] = ROOT,
    docker: str | None = None,
) -> int:
    """Take down only the project the state file names, returning an exit code."""
    docker = docker or docker_binary()
    if not state_path.exists():
        print(f"backends: no state file at {state_path}; nothing to take down")
        return 0
    state = read_state(state_path, root)
    if state is None:
        print(
            f"backends: {state_path} is not a state file for {project_name(root)}; "
            "refusing to guess which project to take down",
            file=sys.stderr,
        )
        return 1
    try:
        process = compose(docker, root, state.project, "down")
    except OSError as exc:
        print(f"backends: cannot run {docker}: {exc}", file=sys.stderr)
        return 1
    if process.returncode != 0:
        output = "\n".join(part for part in (process.stdout, process.stderr) if part).strip()
        print(f"backends: taking down {state.project} failed:\n{output}", file=sys.stderr)
        return 1
    state_path.unlink(missing_ok=True)
    print(f"backends: took down {state.project}")
    return 0


def print_env(
    state_path: pathlib.Path,
    root: str | os.PathLike[str] = ROOT,
) -> int:
    """Print the four fixture addresses, returning an exit code."""
    state = read_state(state_path, root)
    if state is None:
        print(
            f"backends: no usable state file at {state_path}; run 'make deps-up' first",
            file=sys.stderr,
        )
        return 1
    for line in env_lines(state):
        print(line)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    subcommands = parser.add_subparsers(dest="command", required=True)
    for name, help_text in (
        ("up", "start this checkout's stack, or reuse it when healthy"),
        ("down", "take down this checkout's stack"),
        ("env", "print the four SPEED_TEST_* addresses"),
    ):
        command = subcommands.add_parser(name, help=help_text)
        command.add_argument(
            "--state",
            type=pathlib.Path,
            default=DEFAULT_STATE,
            help=f"the state file (default: {DEFAULT_STATE})",
        )
    args = parser.parse_args(argv)
    if args.command == "up":
        return up(args.state)
    if args.command == "down":
        return down(args.state)
    return print_env(args.state)


if __name__ == "__main__":
    raise SystemExit(main())
