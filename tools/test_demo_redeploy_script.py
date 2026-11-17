#!/usr/bin/env python3
"""Cases for starters/demo/deploy/redeploy.sh.

The script keeps a deploy's data only when the machine checks pass, falls back
to the public /healthz probe when they cannot be read, and rebuilds the volume
otherwise. These cases run the real script against stand-ins for `flyctl`,
`make`, `curl` and `python3` placed first on PATH. Every stand-in logs its
arguments and answers from a control file, so a case chooses each machine's
state, each check's status, the deploy command's exit code and the probe's
answer without a network, a cloud account or a real deployment. The stand-ins
for `flyctl`, `make` and `curl` are the only programs that could reach outside
this process, and none of them is the real one.

Stdlib-only (unittest, subprocess). Run directly:

    python3 tools/test_demo_redeploy_script.py
"""

from __future__ import annotations

import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "starters" / "demo" / "deploy" / "redeploy.sh"

FLYCTL_STUB = r'''
import json
import os
import sys


def log(argv, code):
    with open(os.environ["STUB_LOG"], "a", encoding="utf-8") as handle:
        handle.write(json.dumps({"program": "flyctl", "argv": argv, "exit": code}) + "\n")


def load_config():
    with open(os.environ["STUB_CONFIG"], encoding="utf-8") as handle:
        return json.load(handle)


def succeeded_deploys():
    path = os.path.join(os.environ["STUB_STATE"], "make-results")
    if not os.path.exists(path):
        return 0
    with open(path, encoding="utf-8") as handle:
        return sum(1 for line in handle if line.strip() == "ok")


def checks(config):
    mode = config.get("machines", "healthy")
    if mode == "unreadable-nochecks":
        return []
    if mode == "by_make":
        healthy = succeeded_deploys() >= 1
    else:
        healthy = mode == "healthy"
    status = "passing" if healthy else "critical"
    return [
        {"name": "servicecheck-00-http-8080", "status": config.get("service_status", status)},
        {"name": "ready", "status": config.get("ready_status", status)},
    ]


argv = sys.argv[1:]
config = load_config()
if not argv:
    log(argv, 2)
    sys.exit(2)

if argv[0] in ("machine", "machines") and "list" in argv:
    if "--json" in argv:
        if config.get("machines") == "unreadable-fail":
            print("error: the machines API did not answer", file=sys.stderr)
            log(argv, 1)
            sys.exit(1)
        machines = [{
            "id": config.get("machine_id", "m-1"),
            "name": "demo",
            "state": config.get("machine_state", "started"),
            "checks": checks(config),
        }]
        print(json.dumps(machines))
        log(argv, 0)
        sys.exit(0)
    print("ID\tSTATE")
    log(argv, 0)
    sys.exit(0)

if argv[0] == "status":
    print("Status: running")
    log(argv, 0)
    sys.exit(0)

if argv[0] == "volumes" and "list" in argv:
    if "--json" in argv:
        volumes = [{
            "id": config.get("volume_id", "vol-1"),
            "name": config.get("volume_name", "speed_demo_data"),
        }]
        print(json.dumps(volumes))
    else:
        print("ID\tNAME")
    log(argv, 0)
    sys.exit(0)

if argv[0] == "volumes" and ("destroy" in argv or "create" in argv):
    log(argv, 0)
    sys.exit(0)

if argv[0] in ("machine", "machines") and "destroy" in argv:
    log(argv, 0)
    sys.exit(0)

if argv[0] == "ssh":
    print("total 0")
    log(argv, 0)
    sys.exit(0)

log(argv, 0)
sys.exit(0)
'''

MAKE_STUB = r'''
import json
import os
import sys

argv = sys.argv[1:]
with open(os.environ["STUB_CONFIG"], encoding="utf-8") as handle:
    config = json.load(handle)
codes = config.get("make_exit_codes", [0])
path = os.path.join(os.environ["STUB_STATE"], "make-results")
results = []
if os.path.exists(path):
    with open(path, encoding="utf-8") as handle:
        results = [line.strip() for line in handle if line.strip()]
code = codes[min(len(results), len(codes) - 1)]
with open(path, "a", encoding="utf-8") as handle:
    handle.write(("ok" if code == 0 else "failed") + "\n")
with open(os.environ["STUB_LOG"], "a", encoding="utf-8") as handle:
    handle.write(json.dumps({"program": "make", "argv": argv, "exit": code}) + "\n")
print("make " + " ".join(argv))
sys.exit(code)
'''

CURL_STUB = r'''
import json
import os
import sys

argv = sys.argv[1:]
with open(os.environ["STUB_CONFIG"], encoding="utf-8") as handle:
    config = json.load(handle)
codes = config.get("curl_codes", [200])
path = os.path.join(os.environ["STUB_STATE"], "curl-count")
count = 0
if os.path.exists(path):
    with open(path, encoding="utf-8") as handle:
        count = int(handle.read().strip() or 0)
code = codes[min(count, len(codes) - 1)]
with open(path, "w", encoding="utf-8") as handle:
    handle.write(str(count + 1))
with open(os.environ["STUB_LOG"], "a", encoding="utf-8") as handle:
    handle.write(json.dumps({"program": "curl", "argv": argv, "exit": 0}) + "\n")
sys.stdout.write(str(code))
sys.exit(0)
'''

PYTHON_STUB = """
import json
import os
import sys

argv = sys.argv[1:]
with open(os.environ["STUB_LOG"], "a", encoding="utf-8") as handle:
    handle.write(json.dumps({"program": "python3", "argv": argv, "exit": 0}) + "\\n")

code = None
rest = []
index = 0
while index < len(argv):
    if argv[index] in ("-I", "-S", "-E"):
        index += 1
        continue
    if argv[index] == "-c":
        code = argv[index + 1]
        rest = argv[index + 2:]
        break
    index += 1

if code is None:
    sys.exit(0)

sys.argv = ["-c"] + rest
exec(compile(code, "<demo-redeploy-stub>", "exec"), {"__name__": "__main__"})
"""


class Harness:
    """A scratch PATH of stand-ins plus the controls and the call log."""

    def __init__(self):
        self.directory = tempfile.TemporaryDirectory(prefix="demo-redeploy-")
        self.root = pathlib.Path(self.directory.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.state = self.root / "state"
        self.state.mkdir()
        self.config_path = self.root / "config.json"
        self.log_path = self.root / "calls.jsonl"

    def install(self, name, source):
        path = self.bin / name
        path.write_text("#!" + sys.executable + "\n" + source, encoding="utf-8")
        path.chmod(0o755)

    def configure(self, **settings):
        values = {
            "make_exit_codes": [0],
            "machines": "healthy",
            "machine_state": "started",
            "machine_id": "m-1",
            "volume_id": "vol-1",
            "volume_name": "speed_demo_data",
            "curl_codes": [200],
        }
        values.update(settings)
        self.config_path.write_text(json.dumps(values), encoding="utf-8")

    def run(self, *args, stdin=""):
        env = os.environ.copy()
        env["PATH"] = str(self.bin) + os.pathsep + env.get("PATH", "")
        env["STUB_LOG"] = str(self.log_path)
        env["STUB_CONFIG"] = str(self.config_path)
        env["STUB_STATE"] = str(self.state)
        env["HEALTH_TIMEOUT"] = "1"
        env["HEALTH_INTERVAL"] = "1"
        for name in ("APP", "REGION", "VOLUME", "BASE"):
            env.pop(name, None)
        return subprocess.run(
            [str(SCRIPT), *args],
            input=stdin,
            capture_output=True,
            text=True,
            env=env,
            timeout=120,
            check=False,
        )

    def calls(self):
        if not self.log_path.exists():
            return []
        lines = self.log_path.read_text(encoding="utf-8").splitlines()
        return [json.loads(line) for line in lines if line.strip()]

    def programs(self):
        return {entry["program"] for entry in self.calls()}

    def by_program(self, program):
        return [entry["argv"] for entry in self.calls() if entry["program"] == program]

    def rebuild_calls(self):
        found = []
        for argv in self.by_program("flyctl"):
            head = argv[:2]
            if head in (
                ["machine", "destroy"],
                ["machines", "destroy"],
                ["volumes", "destroy"],
                ["volumes", "create"],
            ):
                found.append(argv)
        return found


class RedeployScriptTest(unittest.TestCase):
    def setUp(self):
        self.harness = Harness()
        self.addCleanup(self.harness.directory.cleanup)
        self.harness.install("flyctl", FLYCTL_STUB)
        self.harness.install("make", MAKE_STUB)
        self.harness.install("curl", CURL_STUB)
        self.harness.install("python3", PYTHON_STUB)
        self.harness.configure()

    def assertOnlyStubsRan(self):
        self.assertLessEqual(
            self.harness.programs(), {"flyctl", "make", "curl", "python3"}
        )

    def test_deploy_with_passing_checks_keeps_the_data(self):
        self.harness.configure(make_exit_codes=[0], machines="healthy")
        result = self.harness.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertOnlyStubsRan()
        self.assertEqual(len(self.harness.by_program("make")), 1)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertEqual(self.harness.by_program("curl"), [])
        self.assertIn("machine checks", result.stdout)
        self.assertIn("healthy via machine checks", result.stdout)
        self.assertNotIn("public probe", result.stdout)
        lists = [argv for argv in self.harness.by_program("flyctl") if "list" in argv]
        self.assertTrue(any("--json" in argv for argv in lists), lists)

    def test_failed_deploy_command_with_passing_checks_keeps_the_data(self):
        self.harness.configure(make_exit_codes=[1], machines="healthy")
        result = self.harness.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertOnlyStubsRan()
        self.assertEqual(len(self.harness.by_program("make")), 1)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("probing health anyway", result.stdout)
        self.assertIn("nothing was cleared", result.stdout)

    def test_unhealthy_without_confirmation_does_nothing_destructive(self):
        self.harness.configure(make_exit_codes=[1, 0], machines="by_make")
        result = self.harness.run(stdin="no\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertOnlyStubsRan()
        self.assertEqual(len(self.harness.by_program("make")), 1)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn('type "rebuild"', result.stdout)
        self.assertIn("stopped: nothing was destroyed", result.stdout)

    def test_yes_rebuilds_in_order_then_deploys_and_collects_evidence(self):
        self.harness.configure(make_exit_codes=[1, 0], machines="by_make")
        result = self.harness.run("--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertOnlyStubsRan()

        calls = self.harness.calls()

        def first(predicate):
            for index, entry in enumerate(calls):
                if predicate(entry):
                    return index
            return -1

        destroy_machine = first(
            lambda entry: entry["program"] == "flyctl"
            and entry["argv"][:2] in (["machine", "destroy"], ["machines", "destroy"])
        )
        destroy_volume = first(
            lambda entry: entry["program"] == "flyctl"
            and entry["argv"][:2] == ["volumes", "destroy"]
        )
        create_volume = first(
            lambda entry: entry["program"] == "flyctl"
            and entry["argv"][:2] == ["volumes", "create"]
        )
        makes = [index for index, entry in enumerate(calls) if entry["program"] == "make"]
        ssh = [
            index
            for index, entry in enumerate(calls)
            if entry["program"] == "flyctl" and entry["argv"][0] == "ssh"
        ]

        self.assertGreater(destroy_machine, -1)
        self.assertGreater(destroy_volume, destroy_machine)
        self.assertGreater(create_volume, destroy_volume)
        self.assertEqual(len(makes), 2)
        self.assertGreater(makes[1], create_volume)
        self.assertEqual(len(ssh), 2)
        self.assertGreater(ssh[0], makes[1])

        destroyed = [
            argv
            for argv in self.harness.by_program("flyctl")
            if argv[:2] in (["machine", "destroy"], ["machines", "destroy"])
        ]
        self.assertEqual(destroyed, [["machine", "destroy", "m-1", "--force"]])
        volumes_destroyed = [
            argv
            for argv in self.harness.by_program("flyctl")
            if argv[:2] == ["volumes", "destroy"]
        ]
        self.assertEqual(volumes_destroyed, [["volumes", "destroy", "vol-1", "--yes"]])
        created = [
            argv
            for argv in self.harness.by_program("flyctl")
            if argv[:2] == ["volumes", "create"]
        ]
        self.assertEqual(len(created), 1)
        self.assertEqual(created[0][:2], ["volumes", "create"])
        self.assertIn("speed_demo_data", created[0])
        self.assertIn("sin", created[0])
        self.assertIn("--yes", created[0])
        self.assertIn("volume rebuilt", result.stdout)
        self.assertIn("machine checks", result.stdout)

    def test_dry_run_runs_nothing(self):
        result = self.harness.run("--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.harness.calls(), [])
        self.assertIn("dry run", result.stdout)
        self.assertIn("flyctl machines list", result.stdout)
        self.assertIn("public probe", result.stdout)
        self.assertIn("servicecheck-*", result.stdout)

    def test_help_and_unknown_argument_run_nothing(self):
        help_result = self.harness.run("--help")
        self.assertEqual(help_result.returncode, 0, help_result.stderr)
        self.assertIn("Usage: starters/demo/deploy/redeploy.sh", help_result.stdout)
        self.assertIn("HEALTH_TIMEOUT", help_result.stdout)

        unknown = self.harness.run("--bogus")
        self.assertEqual(unknown.returncode, 2)
        self.assertIn("unknown argument: --bogus", unknown.stderr)
        self.assertEqual(self.harness.calls(), [])

    def test_unreadable_checks_fall_back_to_the_public_probe(self):
        for mode in ("unreadable-fail", "unreadable-nochecks"):
            with self.subTest(mode=mode):
                self.harness = Harness()
                self.addCleanup(self.harness.directory.cleanup)
                self.harness.install("flyctl", FLYCTL_STUB)
                self.harness.install("make", MAKE_STUB)
                self.harness.install("curl", CURL_STUB)
                self.harness.install("python3", PYTHON_STUB)
                self.harness.configure(machines=mode, curl_codes=[200])

                result = self.harness.run()
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertOnlyStubsRan()
                self.assertEqual(self.harness.rebuild_calls(), [])
                self.assertIn("falling back to the public probe", result.stdout)
                self.assertIn("health via public probe", result.stdout)
                probes = self.harness.by_program("curl")
                self.assertTrue(probes)
                self.assertTrue(any("/healthz" in " ".join(argv) for argv in probes))

    def test_public_probe_not_200_enters_the_rebuild_confirmation(self):
        self.harness.configure(machines="unreadable-nochecks", curl_codes=[503])
        result = self.harness.run(stdin="no\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertOnlyStubsRan()
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("falling back to the public probe", result.stdout)
        self.assertIn("answered 503", result.stdout)
        self.assertNotIn("health via public probe", result.stdout)
        self.assertNotIn("nothing was cleared", result.stdout)
        self.assertIn('type "rebuild"', result.stdout)
        self.assertIn("stopped: nothing was destroyed", result.stdout)
        probes = self.harness.by_program("curl")
        self.assertTrue(probes)
        self.assertTrue(any("/healthz" in " ".join(argv) for argv in probes))
        self.assertEqual(len(self.harness.by_program("make")), 1)

    def test_a_machine_that_is_still_starting_is_not_healthy(self):
        self.harness.configure(machines="healthy", machine_state="starting")
        result = self.harness.run(stdin="no\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertOnlyStubsRan()
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("machine checks: m-1 is starting", result.stdout)
        self.assertNotIn("healthy via machine checks", result.stdout)
        self.assertEqual(self.harness.by_program("curl"), [])
        self.assertIn('type "rebuild"', result.stdout)
        self.assertIn("stopped: nothing was destroyed", result.stdout)
        self.assertEqual(len(self.harness.by_program("make")), 1)

    def test_each_declared_check_gates_health_on_its_own(self):
        cases = (
            ({"ready_status": "critical"}, "check ready is critical"),
            ({"service_status": "critical"}, "the service check is critical"),
        )
        for settings, reason in cases:
            with self.subTest(settings=settings):
                self.harness = Harness()
                self.addCleanup(self.harness.directory.cleanup)
                self.harness.install("flyctl", FLYCTL_STUB)
                self.harness.install("make", MAKE_STUB)
                self.harness.install("curl", CURL_STUB)
                self.harness.install("python3", PYTHON_STUB)
                self.harness.configure(machines="healthy", **settings)

                result = self.harness.run(stdin="no\n")
                self.assertNotEqual(result.returncode, 0)
                self.assertOnlyStubsRan()
                self.assertEqual(self.harness.rebuild_calls(), [])
                self.assertIn(reason, result.stdout)
                self.assertNotIn("healthy via machine checks", result.stdout)
                self.assertEqual(self.harness.by_program("curl"), [])
                self.assertIn('type "rebuild"', result.stdout)
                self.assertIn("stopped: nothing was destroyed", result.stdout)
                self.assertEqual(len(self.harness.by_program("make")), 1)


if __name__ == "__main__":
    unittest.main()
