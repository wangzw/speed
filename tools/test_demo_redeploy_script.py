#!/usr/bin/env python3
"""Cases for starters/demo/deploy/redeploy.sh."""

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
    elif mode in ("crit_then_healthy", "warn_then_healthy"):
        healthy = succeeded_deploys() >= 2
    else:
        healthy = mode == "healthy"
    status = "passing" if healthy else "critical"
    ready_code = status
    service_code = status
    if mode == "warn_then_healthy":
        ready_code = "passing" if healthy else "warning"
        service_code = "passing"
    service = {"name": "servicecheck-00-http-8080"}
    ready = {"name": "ready"}
    if not config.get("service_status_missing"):
        service["status"] = config.get("service_status", service_code)
    if not config.get("ready_status_missing"):
        ready["status"] = config.get("ready_status", ready_code)
    return [service, ready]


def machine_state(config, machine_id):
    mode = config.get("machine_state", "started")
    if mode != "stopped":
        return mode
    path = os.path.join(os.environ["STUB_STATE"], "started-machines")
    started = []
    if os.path.exists(path):
        with open(path, encoding="utf-8") as handle:
            started = [line.strip() for line in handle if line.strip()]
    return "started" if machine_id in started else "stopped"


argv = sys.argv[1:]
config = load_config()
if not argv:
    log(argv, 2)
    sys.exit(2)

if argv[0] in ("machine", "machines") and "list" in argv:
    if "--json" in argv:
        if config.get("machines_list_non_json"):
            print("this is not a JSON machines list")
            log(argv, 0)
            sys.exit(0)
        if config.get("machines") == "unreadable-fail":
            print("error: the machines API did not answer", file=sys.stderr)
            log(argv, 1)
            sys.exit(1)
        ids = config.get("machine_ids") or [config.get("machine_id", "m-1")]
        machines = []
        for machine_id in ids:
            machine = {
                "name": "demo",
                "state": machine_state(config, machine_id),
                "checks": checks(config),
            }
            if not config.get("machines_omit_id"):
                machine["id"] = machine_id
            machines.append(machine)
        print(json.dumps(machines))
        log(argv, 0)
        sys.exit(0)
    print("ID\tSTATE")
    log(argv, 0)
    sys.exit(0)

if argv[0] in ("machine", "machines") and "start" in argv:
    machine_id = argv[2] if len(argv) > 2 else ""
    if machine_id in config.get("machine_start_fail_ids", []):
        print("error: the machine start failed", file=sys.stderr)
        log(argv, 1)
        sys.exit(1)
    path = os.path.join(os.environ["STUB_STATE"], "started-machines")
    with open(path, "a", encoding="utf-8") as handle:
        handle.write(machine_id + "\n")
    log(argv, 0)
    sys.exit(0)

if argv[0] == "status":
    print("Status: running")
    log(argv, 0)
    sys.exit(0)

if argv[0] == "volumes" and "list" in argv:
    if "--json" in argv:
        path = os.path.join(os.environ["STUB_STATE"], "volumes-json-count")
        count = 0
        if os.path.exists(path):
            with open(path, encoding="utf-8") as handle:
                count = int(handle.read().strip() or 0)
        count += 1
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(str(count))
        if count in config.get("volumes_list_json_fail_calls", []):
            print("error: the volumes API did not answer", file=sys.stderr)
            log(argv, 1)
            sys.exit(1)
        volume = {"name": config.get("volume_name", "speed_demo_data")}
        if not config.get("volumes_omit_id"):
            volume["id"] = config.get("volume_id", "vol-1")
        volumes = [volume]
        print(json.dumps(volumes))
    else:
        print("ID\tNAME")
    log(argv, 0)
    sys.exit(0)

if argv[0] == "volumes" and ("destroy" in argv or "create" in argv):
    failed = "destroy" in argv and len(argv) > 2 and argv[2] in config.get("volume_destroy_fail_ids", [])
    if failed:
        print("error: the volume destroy failed", file=sys.stderr)
    log(argv, 1 if failed else 0)
    sys.exit(1 if failed else 0)

if argv[0] in ("machine", "machines") and "destroy" in argv:
    failed = len(argv) > 2 and argv[2] in config.get("machine_destroy_fail_ids", [])
    if failed:
        print("error: the machine destroy failed", file=sys.stderr)
    log(argv, 1 if failed else 0)
    sys.exit(1 if failed else 0)

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

with open(os.environ["STUB_CONFIG"], encoding="utf-8") as handle:
    config = json.load(handle)
verdict = config.get("machine_health_verdict")
if verdict and "indeterminate: a machine entry is not an object" in code:
    sys.stdout.write(verdict + "\\n")
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

    def script_with_fly_toml(self, text):
        """A copy of the script in a scratch checkout beside its own fly.toml."""
        repo = self.root / "checkout"
        deploy = repo / "starters" / "demo" / "deploy"
        deploy.mkdir(parents=True)
        script = deploy / SCRIPT.name
        script.write_bytes(SCRIPT.read_bytes())
        script.chmod(0o755)
        (deploy.parent / "fly.toml").write_text(text, encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "init", "-q"], check=True)
        return script

    def run(self, *args, stdin="", script=None, extra_env=None):
        env = os.environ.copy()
        env["PATH"] = str(self.bin) + os.pathsep + env.get("PATH", "")
        env["STUB_LOG"] = str(self.log_path)
        env["STUB_CONFIG"] = str(self.config_path)
        env["STUB_STATE"] = str(self.state)
        env["HEALTH_TIMEOUT"] = "1"
        env["HEALTH_INTERVAL"] = "1"
        for name in ("APP", "REGION", "VOLUME", "BASE"):
            env.pop(name, None)
        env.update(extra_env or {})
        return subprocess.run(
            [str(script or SCRIPT), *args],
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
    """Cases for the decision logic, each asserting the exact stub calls it needs."""

    def setUp(self):
        self.harness = Harness()
        self.addCleanup(self.harness.directory.cleanup)
        self.harness.install("flyctl", FLYCTL_STUB)
        self.harness.install("make", MAKE_STUB)
        self.harness.install("curl", CURL_STUB)
        self.harness.install("python3", PYTHON_STUB)
        self.harness.configure()

    def volumes_list_json_calls(self):
        return [
            argv
            for argv in self.harness.by_program("flyctl")
            if argv[:2] == ["volumes", "list"] and "--json" in argv
        ]

    def test_deploy_with_passing_checks_keeps_the_data(self):
        """Test that deploy with passing checks keeps the data."""
        self.harness.configure(make_exit_codes=[0], machines="healthy")
        result = self.harness.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.harness.by_program("make")), 1)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertEqual(self.harness.by_program("curl"), [])
        self.assertIn("machine checks", result.stdout)
        self.assertIn("healthy via machine checks", result.stdout)
        self.assertNotIn("public probe", result.stdout)
        lists = [argv for argv in self.harness.by_program("flyctl") if "list" in argv]
        self.assertTrue(any("--json" in argv for argv in lists), lists)

    def test_failed_deploy_command_with_passing_checks_keeps_the_data(self):
        """Test that failed deploy command with passing checks keeps the data."""
        self.harness.configure(make_exit_codes=[1], machines="healthy")
        result = self.harness.run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.harness.by_program("make")), 1)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("probing health anyway", result.stdout)
        self.assertIn("nothing was cleared", result.stdout)

    def test_unhealthy_without_confirmation_does_nothing_destructive(self):
        """Test that unhealthy without confirmation does nothing destructive."""
        self.harness.configure(make_exit_codes=[1, 0], machines="by_make")
        result = self.harness.run(stdin="no\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.harness.by_program("make")), 1)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn('type "rebuild"', result.stdout)
        self.assertIn("stopped: nothing was destroyed", result.stdout)

    def test_yes_rebuilds_in_order_then_deploys_and_collects_evidence(self):
        """Test that yes rebuilds in order then deploys and collects evidence."""
        self.harness.configure(make_exit_codes=[1, 0], machines="by_make")
        result = self.harness.run("--yes")
        self.assertEqual(result.returncode, 0, result.stderr)

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
        """Test that dry run runs nothing."""
        result = self.harness.run("--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.harness.calls(), [])
        self.assertIn("dry run", result.stdout)
        self.assertIn("flyctl machine list", result.stdout)
        self.assertIn("public probe", result.stdout)
        self.assertIn("servicecheck-*", result.stdout)

    def test_help_and_unknown_argument_run_nothing(self):
        """Test that help and unknown argument run nothing."""
        help_result = self.harness.run("--help")
        self.assertEqual(help_result.returncode, 0, help_result.stderr)
        self.assertIn("Usage: starters/demo/deploy/redeploy.sh", help_result.stdout)
        self.assertIn("HEALTH_TIMEOUT", help_result.stdout)

        unknown = self.harness.run("--bogus")
        self.assertEqual(unknown.returncode, 2)
        self.assertIn("unknown argument: --bogus", unknown.stderr)
        self.assertEqual(self.harness.calls(), [])

    def test_unreadable_checks_fall_back_to_the_public_probe(self):
        """Test that unreadable checks fall back to the public probe."""
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
                self.assertEqual(self.harness.rebuild_calls(), [])
                self.assertIn("falling back to the public probe", result.stdout)
                self.assertIn("health via public probe", result.stdout)
                probes = self.harness.by_program("curl")
                self.assertTrue(probes)
                self.assertTrue(any("/healthz" in " ".join(argv) for argv in probes))

    def test_a_public_probe_that_is_not_200_is_indeterminate(self):
        """Test that a public probe that is not 200 is indeterminate."""
        self.harness.configure(machines="unreadable-nochecks", curl_codes=[503])
        result = self.harness.run()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("falling back to the public probe", result.stdout)
        self.assertIn("answered 503", result.stdout)
        self.assertNotIn("health via public probe", result.stdout)
        self.assertNotIn("nothing was cleared", result.stdout)
        self.assertIn("cannot determine health", result.stderr)
        self.assertNotIn('type "rebuild"', result.stdout)
        probes = self.harness.by_program("curl")
        self.assertTrue(probes)
        self.assertTrue(any("/healthz" in " ".join(argv) for argv in probes))
        self.assertEqual(len(self.harness.by_program("make")), 1)

    def test_an_unrecognised_health_verdict_is_indeterminate(self):
        """Test that an unrecognised health verdict is indeterminate."""
        self.harness.configure(
            machines="healthy",
            machine_health_verdict="confusing",
            curl_codes=["000"],
        )
        result = self.harness.run()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot determine health", result.stderr)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("unrecognised verdict", result.stdout)

    def test_a_machine_that_never_starts_is_indeterminate(self):
        """Test that a machine that never starts is indeterminate."""
        self.harness.configure(
            machines="healthy", machine_state="starting", curl_codes=["000"]
        )
        result = self.harness.run()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("machine checks: m-1 is starting", result.stdout)
        self.assertNotIn("healthy via machine checks", result.stdout)
        self.assertIn("cannot determine health", result.stderr)
        self.assertEqual(len(self.harness.by_program("make")), 1)

    def test_a_required_check_with_no_readable_status_is_indeterminate(self):
        """Test that a required check with no readable status is indeterminate."""
        cases = (
            ({"ready_status_missing": True}, "ready"),
            ({"ready_status": "unknown"}, "ready"),
            ({"ready_status": ""}, "ready"),
            ({"ready_status": 1}, "ready"),
            ({"ready_status": "passed"}, "ready"),
            ({"service_status_missing": True}, "servicecheck-00-http-8080"),
            ({"service_status": "PASSING"}, "servicecheck-00-http-8080"),
        )
        for settings, check_name in cases:
            with self.subTest(settings=settings):
                self.harness = Harness()
                self.addCleanup(self.harness.directory.cleanup)
                self.harness.install("flyctl", FLYCTL_STUB)
                self.harness.install("make", MAKE_STUB)
                self.harness.install("curl", CURL_STUB)
                self.harness.install("python3", PYTHON_STUB)
                self.harness.configure(
                    machines="healthy", curl_codes=["000"], **settings
                )

                result = self.harness.run()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("cannot determine health", result.stderr)
                self.assertEqual(self.harness.rebuild_calls(), [])
                self.assertIn("no readable status", result.stdout)
                self.assertIn("check %s" % check_name, result.stdout)

    def test_a_warning_check_still_reaches_the_rebuild(self):
        """Test that a warning check still reaches the rebuild."""
        self.harness.configure(make_exit_codes=[0], machines="warn_then_healthy")
        result = self.harness.run("--yes")
        self.assertEqual(result.returncode, 0, result.stderr)

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
        self.assertGreater(destroy_machine, -1)
        self.assertGreater(destroy_volume, destroy_machine)
        self.assertGreater(create_volume, destroy_volume)
        self.assertIn("check ready is warning", result.stdout)
        self.assertIn("volume rebuilt", result.stdout)

    def test_each_declared_check_gates_health_on_its_own(self):
        """Test that each declared check gates health on its own."""
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
                self.assertEqual(self.harness.rebuild_calls(), [])
                self.assertIn(reason, result.stdout)
                self.assertNotIn("healthy via machine checks", result.stdout)
                self.assertEqual(self.harness.by_program("curl"), [])
                self.assertIn('type "rebuild"', result.stdout)
                self.assertIn("stopped: nothing was destroyed", result.stdout)
                self.assertEqual(len(self.harness.by_program("make")), 1)

    def test_a_stopped_machine_is_woken_then_judged_healthy(self):
        """Test that a stopped machine is woken then judged healthy."""
        self.harness.configure(machines="healthy", machine_state="stopped")
        result = self.harness.run(extra_env={"HEALTH_TIMEOUT": "5"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertEqual(self.harness.by_program("curl"), [])
        starts = [
            argv
            for argv in self.harness.by_program("flyctl")
            if argv[:2] == ["machine", "start"]
        ]
        self.assertEqual(len(starts), 1)
        self.assertEqual(starts[0][2], "m-1")
        self.assertIn("woke the stopped machines", result.stdout)
        self.assertIn("healthy via machine checks", result.stdout)

    def test_a_machine_that_cannot_be_woken_is_indeterminate(self):
        """Test that a machine that cannot be woken is indeterminate."""
        self.harness.configure(
            machines="healthy",
            machine_state="stopped",
            machine_start_fail_ids=["m-1"],
        )
        result = self.harness.run(extra_env={"HEALTH_TIMEOUT": "5"})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot determine health", result.stderr)
        self.assertIn("could not wake the stopped machines", result.stdout)
        self.assertEqual(self.harness.rebuild_calls(), [])

    def test_a_network_failure_is_indeterminate_and_destroys_nothing(self):
        """Test that a network failure is indeterminate and destroys nothing."""
        self.harness.configure(machines="unreadable-fail", curl_codes=["000"])
        result = self.harness.run()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot determine health", result.stderr)
        self.assertEqual(self.harness.rebuild_calls(), [])
        destroys = [
            entry["argv"]
            for entry in self.harness.calls()
            if entry["program"] == "flyctl"
            and (
                entry["argv"][:2] in (["machine", "destroy"], ["machines", "destroy"])
                or entry["argv"][:2] == ["volumes", "destroy"]
            )
        ]
        self.assertEqual(destroys, [])
        self.assertIn("answered 000", result.stdout)

    def test_a_started_machine_with_failing_checks_reaches_the_rebuild(self):
        """Test that a started machine with failing checks reaches the rebuild."""
        self.harness.configure(make_exit_codes=[0, 0], machines="crit_then_healthy")
        result = self.harness.run("--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
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
        self.assertGreater(destroy_machine, -1)
        self.assertGreater(destroy_volume, destroy_machine)
        self.assertGreater(create_volume, destroy_volume)
        self.assertEqual(len(self.harness.by_program("make")), 2)
        self.assertIn("machine checks not passing yet", result.stdout)
        self.assertIn("volume rebuilt", result.stdout)

    def test_a_failed_cloud_state_read_before_the_confirmation_stops_before_destroying(self):
        """Test that a failed cloud state read before the confirmation stops before
        destroying."""
        self.harness.configure(
            machines="healthy",
            ready_status="critical",
            volumes_list_json_fail_calls=[1],
        )
        result = self.harness.run(stdin="rebuild\n")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(
            "cannot determine cloud state; refusing to destroy anything",
            result.stderr,
        )
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertEqual(len(self.volumes_list_json_calls()), 1)
        self.assertNotIn('type "rebuild" to continue', result.stdout)
        self.assertNotIn(
            "re-reading the cloud state after the confirmation", result.stdout
        )

    def test_a_failed_cloud_state_read_after_the_confirmation_stops_before_destroying(self):
        """Test that a failed cloud state read after the confirmation stops before
        destroying."""
        self.harness.configure(
            machines="healthy",
            ready_status="critical",
            volumes_list_json_fail_calls=[2],
        )
        result = self.harness.run("--yes")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(
            "cannot determine cloud state; refusing to destroy anything",
            result.stderr,
        )
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertEqual(len(self.volumes_list_json_calls()), 2)

    def test_a_machines_list_that_is_not_json_is_indeterminate(self):
        """Test that a machines list that is not json is indeterminate."""
        self.harness.configure(
            machines_list_non_json=True,
            curl_codes=["000"],
        )
        result = self.harness.run()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot determine health", result.stderr)
        self.assertEqual(self.harness.rebuild_calls(), [])
        self.assertIn("the machines list is not JSON", result.stdout)
        self.assertIn("falling back to the public probe", result.stdout)
        self.assertIn("answered 000", result.stdout)

    def test_a_list_entry_without_id_stops_before_destroying(self):
        """Test that a list entry without id stops before destroying."""
        cases = (
            (
                "machines_omit_id",
                "cannot determine cloud state: the machine list has no ids",
                None,
            ),
            (
                "volumes_omit_id",
                "cannot determine cloud state: the volume list has no ids",
                (("volumes", "destroy"), ("volumes", "create")),
            ),
        )
        for setting, reason, guarded in cases:
            with self.subTest(setting=setting):
                self.harness = Harness()
                self.addCleanup(self.harness.directory.cleanup)
                self.harness.install("flyctl", FLYCTL_STUB)
                self.harness.install("make", MAKE_STUB)
                self.harness.install("curl", CURL_STUB)
                self.harness.install("python3", PYTHON_STUB)
                settings = {"make_exit_codes": [1, 0], "machines": "by_make"}
                settings[setting] = True
                self.harness.configure(**settings)

                result = self.harness.run("--yes")
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertIn(reason, result.stderr)
                if guarded is None:
                    self.assertEqual(self.harness.rebuild_calls(), [])
                else:
                    writes = [
                        argv
                        for argv in self.harness.by_program("flyctl")
                        if tuple(argv[:2]) in guarded
                    ]
                    self.assertEqual(writes, [])

    def test_the_redeploy_waits_for_health_after_the_second_deploy(self):
        """Test that the redeploy waits for health after the second deploy."""
        self.harness.configure(make_exit_codes=[1, 0], machines="by_make")
        result = self.harness.run("--yes")
        self.assertEqual(result.returncode, 0, result.stderr)

        calls = self.harness.calls()
        makes = [index for index, entry in enumerate(calls) if entry["program"] == "make"]
        self.assertEqual(len(makes), 2)
        second_make = makes[1]
        machine_lists_after = [
            index
            for index in range(second_make + 1, len(calls))
            if calls[index]["program"] == "flyctl"
            and calls[index]["argv"][:2] in (["machine", "list"], ["machines", "list"])
            and "--json" in calls[index]["argv"]
        ]
        self.assertTrue(machine_lists_after, calls[second_make:])
        self.assertIn(
            "volume rebuilt, new version healthy (health via machine checks)",
            result.stdout,
        )

    def test_a_failed_second_deploy_stops_the_run(self):
        """Test that a failed second deploy stops the run."""
        self.harness.configure(make_exit_codes=[1], machines="by_make")
        result = self.harness.run("--yes")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(
            "the deploy command failed after the volume was recreated", result.stderr
        )
        self.assertIn(
            "redeploy stopped at: 5/5 deploy again on the fresh volume", result.stderr
        )
        self.assertIn(
            "redeploy had done: every machine destroyed and volume speed_demo_data recreated",
            result.stderr,
        )
        self.assertEqual(len(self.harness.by_program("make")), 2)
        evidence = [
            argv for argv in self.harness.by_program("flyctl") if argv and argv[0] == "ssh"
        ]
        self.assertEqual(evidence, [])

    def test_typing_rebuild_runs_the_destruction(self):
        """Test that typing rebuild runs the destruction."""
        self.harness.configure(make_exit_codes=[1, 0], machines="by_make")
        result = self.harness.run(stdin="rebuild\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('type "rebuild"', result.stdout)
        self.assertEqual(len(self.harness.by_program("make")), 2)

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
        self.assertIn("volume rebuilt", result.stdout)

    def test_a_failed_destroy_midway_reports_what_was_done(self):
        """Test that a failed destroy midway reports what was done."""
        self.harness.configure(
            make_exit_codes=[1, 0],
            machines="by_make",
            machine_ids=["m-1", "m-2"],
            machine_destroy_fail_ids=["m-2"],
        )
        result = self.harness.run("--yes")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("redeploy stopped at: 3/5 destroy every machine", result.stderr)
        self.assertIn("redeploy had done: destroyed machines: m-1", result.stderr)

        outcomes = {
            (entry["argv"][2], entry["exit"])
            for entry in self.harness.calls()
            if entry["program"] == "flyctl"
            and entry["argv"][:2] == ["machine", "destroy"]
        }
        self.assertEqual(outcomes, {("m-1", 0), ("m-2", 1)})
        volumes_destroyed = [
            argv
            for argv in self.harness.by_program("flyctl")
            if argv[:2] == ["volumes", "destroy"]
        ]
        self.assertEqual(volumes_destroyed, [])

    def test_fly_toml_values_win_over_the_built_in_defaults(self):
        """Test that fly toml values win over the built in defaults."""
        script = self.harness.script_with_fly_toml(
            'app = "from-toml-app"\n'
            'primary_region = "ams"\n'
            "[[mounts]]\n"
            '  source = "from_toml_volume"\n'
        )
        result = self.harness.run("--dry-run", script=script)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("app:    from-toml-app", result.stdout)
        self.assertIn("region: ams", result.stdout)
        self.assertIn("volume: from_toml_volume", result.stdout)
        self.assertNotIn("vislake-speed-demo", result.stdout)
        self.assertIn("volumes create from_toml_volume --region ams", result.stdout)

    def test_environment_overrides_the_fly_toml_and_the_defaults(self):
        """Test that environment overrides the fly toml and the defaults."""
        result = self.harness.run(
            "--dry-run",
            extra_env={
                "APP": "env-app",
                "REGION": "env-region",
                "VOLUME": "env_vol",
                "BASE": "https://env.example",
            },
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("app:    env-app (environment)", result.stdout)
        self.assertIn("region: env-region (environment)", result.stdout)
        self.assertIn("volume: env_vol (environment)", result.stdout)
        self.assertIn("base:   https://env.example (environment)", result.stdout)
        self.assertIn("-a env-app", result.stdout)
        self.assertIn("volumes create env_vol --region env-region", result.stdout)


if __name__ == "__main__":
    unittest.main()
