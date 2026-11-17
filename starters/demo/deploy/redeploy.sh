#!/usr/bin/env bash
# Redeploy the demo to Fly, rebuilding its volume when the new version cannot
# start against the existing database.
#
# Order: deploy first and keep the data when the new version is healthy. Health
# is read from `flyctl machines list --json`: every machine must be started,
# and both checks fly.toml declares must pass -- the named `ready` check and
# the service check flyctl generates from [[http_service.checks]], named
# servicecheck-* (port 8080 here). Only when those checks cannot be read
# (flyctl fails, the output is not JSON, or a required check is absent) does
# the script fall back to polling GET /healthz on the public address. A deploy
# that fails or does not become healthy is rebuilt, because a failed deploy can
# still leave part of itself in place; only health ends the run with the data
# untouched. The rebuild destroys the machine, destroys the volume, creates a
# fresh one, and deploys again; the entrypoint builds the database on first
# boot. Nothing here needs ssh, so a crash loop cannot block the repair.
#
# The rebuild is destructive: the volume carries the database, the token
# signing key (/data/var) and the object store (/data/objects), so every
# session is invalidated and uploaded objects are lost.
#
# --dry-run prints the commands and the decision logic and runs nothing.
# --yes skips the confirmation before the destructive step.
#
# The defaults mirror starters/demo/fly.toml; APP, REGION, VOLUME and BASE
# override them, and HEALTH_TIMEOUT and HEALTH_INTERVAL change the health poll.
# --help carries the full text.
set -euo pipefail

usage() {
  cat <<'EOF'
Redeploy the demo to Fly, rebuilding its volume when the new version cannot
start against the existing database.

Usage: starters/demo/deploy/redeploy.sh [options]

Options:
  --dry-run   print the commands and the decision logic; run nothing
  --yes, -y   skip the "type rebuild" confirmation before the destructive step
  --help, -h  print this help and exit

Steps:
  1. print the current state: flyctl status, flyctl volumes list
  2. make -C <repo> fly, then poll the machine checks up to five minutes:
     every machine must be started, and the ready check and the service
     check (servicecheck-*) must pass. Only when those checks
     cannot be read does the script fall back to polling <base>/healthz.
     A passing check ends the run and nothing is cleared, and a failed
     deploy command is polled too because it can still take effect in part
  3. destroy every machine, destroy the volume, create a fresh one
  4. deploy again, wait for health, and read /data through flyctl ssh console
     as evidence

The rebuild is destructive: the database, the token signing key (/data/var)
and the object store (/data/objects) on the volume are lost, and every session
is invalidated.

Defaults:
  app     the top-level "app" key in starters/demo/fly.toml, else
          vislake-speed-demo
  region  the "primary_region" key in starters/demo/fly.toml, else sin
  volume  the mounts entry's "source" key in starters/demo/fly.toml, else
          speed_demo_data
  base    https://<app>.fly.dev

Environment:
  APP, REGION, VOLUME and BASE override the defaults above.
  HEALTH_TIMEOUT  the health poll's limit in seconds (default 300)
  HEALTH_INTERVAL the pause between health attempts in seconds (default 5)
EOF
}

ASSUME_YES=0
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --yes | -y) ASSUME_YES=1 ;;
    --dry-run) DRY_RUN=1 ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      printf 'redeploy: unknown argument: %s\n' "$arg" >&2
      printf 'redeploy: run with --help for usage\n' >&2
      exit 2
      ;;
  esac
done

# The script may be reached through a symlink outside the repository (a copy
# on PATH), so the checkout is located from the file a chain of links ends at.
source_path=$0
while [ -L "$source_path" ]; do
  link_dir=$(cd -P -- "$(dirname -- "$source_path")" && pwd)
  source_path=$(readlink "$source_path")
  case $source_path in
    /*) ;;
    *) source_path=$link_dir/$source_path ;;
  esac
done
SCRIPT_DIR=$(cd -P -- "$(dirname -- "$source_path")" && pwd)

if ! REPO=$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null); then
  printf 'redeploy: %s is not inside a git work tree, so the checkout the\n' "$SCRIPT_DIR" >&2
  printf 'redeploy: deploy command runs in cannot be found\n' >&2
  exit 2
fi

FLY_TOML=$(dirname -- "$SCRIPT_DIR")/fly.toml

# The value of the first `key = "value"` line in fly.toml, empty when the file
# is absent or the key is not there. It reads the simple strings this file
# names and is not a TOML parser.
toml_string() {
  if [ ! -r "$FLY_TOML" ]; then
    return 0
  fi
  awk -v key="$1" '
    BEGIN { pattern = "^[[:space:]]*" key "[[:space:]]*=[[:space:]]*\"" }
    $0 ~ pattern {
      value = $0
      sub(/^[^"]*"/, "", value)
      sub(/".*$/, "", value)
      print value
      exit
    }
  ' "$FLY_TOML"
}

builtin_app=vislake-speed-demo
builtin_region=sin
builtin_volume=speed_demo_data

file_app=$(toml_string app)
file_region=$(toml_string primary_region)
file_volume=$(toml_string source)

if [ -n "${APP:-}" ]; then
  app_source=environment
elif [ -n "$file_app" ]; then
  APP=$file_app
  app_source=$FLY_TOML
else
  APP=$builtin_app
  if [ -r "$FLY_TOML" ]; then
    app_source="built-in default; $FLY_TOML names no app"
  else
    app_source="built-in default; $FLY_TOML is not readable"
  fi
fi

if [ -n "${REGION:-}" ]; then
  region_source=environment
elif [ -n "$file_region" ]; then
  REGION=$file_region
  region_source=$FLY_TOML
else
  REGION=$builtin_region
  if [ -r "$FLY_TOML" ]; then
    region_source="built-in default; $FLY_TOML names no primary_region"
  else
    region_source="built-in default; $FLY_TOML is not readable"
  fi
fi

if [ -n "${VOLUME:-}" ]; then
  volume_source=environment
elif [ -n "$file_volume" ]; then
  VOLUME=$file_volume
  volume_source=$FLY_TOML
else
  VOLUME=$builtin_volume
  if [ -r "$FLY_TOML" ]; then
    volume_source="built-in default; $FLY_TOML names no mounts source"
  else
    volume_source="built-in default; $FLY_TOML is not readable"
  fi
fi

if [ -n "${BASE:-}" ]; then
  base_source=environment
else
  BASE=https://$APP.fly.dev
  base_source="derived from the app"
fi

HEALTH_TIMEOUT=${HEALTH_TIMEOUT:-300}
HEALTH_INTERVAL=${HEALTH_INTERVAL:-5}
HEALTH_ROUTE=none

say() { printf '\n== %s\n' "$*"; }
note() { printf '  %s\n' "$*"; }

# One verdict per machines list: healthy, "unhealthy: <reasons>", or
# unreadable. The named check is the top-level [checks.ready] block in
# fly.toml; the service check is the one flyctl generates from
# [[http_service.checks]], named servicecheck-*. A machine list with neither
# name, or with no checks at all, cannot judge health, so it reports
# unreadable and the caller probes the public address.
MACHINE_HEALTH_PY='
import json, sys

try:
    machines = json.load(sys.stdin)
except Exception:
    print("unreadable")
    raise SystemExit(0)

if not isinstance(machines, list) or not machines:
    print("unreadable")
    raise SystemExit(0)

ready_seen = False
service_seen = False
readable = True
reasons = []
for machine in machines:
    if not isinstance(machine, dict):
        print("unreadable")
        raise SystemExit(0)
    ident = machine.get("id") or machine.get("name") or "?"
    state = machine.get("state") or ""
    if state != "started":
        reasons.append("%s is %s" % (ident, state or "in an unknown state"))
    checks = machine.get("checks")
    if not isinstance(checks, list):
        readable = False
        continue
    ready_status = None
    service_status = None
    for check in checks:
        if not isinstance(check, dict):
            continue
        name = check.get("name") or ""
        status = check.get("status") or "unknown"
        if name == "ready":
            ready_status = status
        elif name.startswith("servicecheck-"):
            service_status = status
    if ready_status is None or service_status is None:
        readable = False
        continue
    ready_seen = True
    service_seen = True
    if ready_status != "passing":
        reasons.append("%s: check ready is %s" % (ident, ready_status))
    if service_status != "passing":
        reasons.append("%s: the service check is %s" % (ident, service_status))

if not readable or not ready_seen or not service_seen:
    print("unreadable")
elif reasons:
    print("unhealthy: " + "; ".join(reasons))
else:
    print("healthy")
'

# 0: every machine is started and both checks pass; 1: readable but not
# healthy; 2: the checks cannot be read, so the caller falls back to the
# public probe.
machine_health() {
  local json status verdict
  if ! json=$(flyctl machines list -a "$APP" --json 2>/dev/null); then
    printf '  machine checks: flyctl machines list --json did not answer\n'
    return 2
  fi
  status=0
  verdict=$(printf '%s\n' "$json" | python3 -I -c "$MACHINE_HEALTH_PY") || status=$?
  if [ "$status" -ne 0 ]; then
    printf '  machine checks: the machines list could not be read\n'
    return 2
  fi
  case $verdict in
    healthy)
      printf '  machine checks: every machine is started and both checks pass\n'
      return 0
      ;;
    unreadable)
      printf '  machine checks: no usable ready or service check in the machines list\n'
      return 2
      ;;
    *)
      printf '  machine checks: %s\n' "${verdict#unhealthy: }"
      return 1
      ;;
  esac
}

# Health is judged from the machine checks. The public listener answers
# /healthz from the frontend's fallback once the process serves: an
# extensionless path is a client-side route, so the entry document answers
# with 200. That probe runs only when the machine checks cannot be read, and
# the output names the route each attempt used.
health() {
  local attempts=0 code deadline machine_status use_probe=0
  deadline=$((SECONDS + HEALTH_TIMEOUT))
  while [ "$SECONDS" -lt "$deadline" ]; do
    attempts=$((attempts + 1))
    if [ "$use_probe" -eq 0 ]; then
      machine_status=0
      machine_health || machine_status=$?
      case $machine_status in
        0)
          printf '  attempt %d (%ds elapsed): healthy via machine checks\n' "$attempts" "$SECONDS"
          HEALTH_ROUTE="machine checks"
          return 0
          ;;
        2)
          use_probe=1
          printf '  attempt %d (%ds elapsed): falling back to the public probe %s/healthz\n' "$attempts" "$SECONDS" "$BASE"
          ;;
        *)
          printf '  attempt %d (%ds elapsed): machine checks not passing yet\n' "$attempts" "$SECONDS"
          ;;
      esac
    fi
    if [ "$use_probe" -eq 1 ]; then
      code=$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 5 --max-time 10 \
        "$BASE/healthz" || true)
      printf '  attempt %d (%ds elapsed): public probe %s/healthz answered %s\n' "$attempts" "$SECONDS" "$BASE" "${code:-none}"
      if [ "$code" = 200 ]; then
        HEALTH_ROUTE="public probe"
        return 0
      fi
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      break
    fi
    sleep "$HEALTH_INTERVAL"
  done
  return 1
}

STEP="startup"
DONE="nothing"
REPORT=1
on_exit() {
  local status=$?
  if [ "$status" -ne 0 ] && [ "$REPORT" -eq 1 ]; then
    {
      printf '\nredeploy stopped at: %s\n' "$STEP"
      printf 'redeploy had done: %s\n' "$DONE"
    } >&2
  fi
}
trap on_exit EXIT

die() {
  printf 'redeploy: %s\n' "$*" >&2
  exit 1
}

say "settings"
note "app:    $APP ($app_source)"
note "region: $REGION ($region_source)"
note "volume: $VOLUME ($volume_source)"
note "base:   $BASE ($base_source)"

if [ "$DRY_RUN" -eq 1 ]; then
  say "dry run: no command runs, no cloud state is read or changed"
  cat <<EOF
1/5 print the current state
     flyctl status -a $APP
     flyctl volumes list -a $APP
2/5 deploy the new version
     make -C $REPO fly
     poll flyctl machines list -a $APP --json every ${HEALTH_INTERVAL}s, within ${HEALTH_TIMEOUT}s:
     every machine started, and the ready check and the service check
     (servicecheck-*) passing
     fall back to polling $BASE/healthz only when those checks cannot be read
     a passing check ends the run and nothing is cleared; a failed make fly is
     polled too, and no passing check continues to step 3
3/5 confirm the rebuild
EOF
  if [ "$ASSUME_YES" -eq 1 ]; then
    note "confirmation: skipped by --yes"
  else
    note 'confirmation: show what is destroyed, then wait for the word "rebuild"'
  fi
  cat <<EOF
     the rebuild destroys the database, the token signing key under /data/var
     and the object store under /data/objects; every session is invalidated
4/5 destroy the machines and the volume, then create the volume again
     flyctl machine destroy <id> --force    one call per id from
                                            flyctl machine list -a $APP --json
     flyctl volumes destroy <id> --yes      for the volume named $VOLUME
     flyctl volumes create $VOLUME --region $REGION --size 1 --yes
5/5 deploy again and collect the evidence
     make -C $REPO fly
     poll the machine checks as in step 2, with the same public probe fallback
     flyctl ssh console -a $APP -C "ls -l /data"
     flyctl ssh console -a $APP -C "test -f /data/demo.db"
EOF
  exit 0
fi

STEP="1/5 print the current state"
say "1/5 current state"
flyctl status -a "$APP" || note "flyctl status did not answer; continuing"
flyctl volumes list -a "$APP" || note "flyctl volumes list did not answer; continuing"

STEP="2/5 deploy the new version and wait for health"
say "2/5 deploy the new version"
deploy_failed=0
if ! make -C "$REPO" fly; then
  deploy_failed=1
  note "the deploy command failed; probing health anyway, since the deploy may have taken effect in part"
fi
if health; then
  say "done: the new version is healthy on the existing database (health via $HEALTH_ROUTE), nothing was cleared"
  exit 0
fi
if [ "$deploy_failed" -eq 1 ]; then
  note "the deploy failed and the new version did not come up healthy"
else
  note "the new version did not come up healthy on the existing database"
fi

if [ "$ASSUME_YES" -ne 1 ]; then
  printf '\nthe next steps destroy the volume %s of %s:\n' "$VOLUME" "$APP"
  printf '  the database, the token signing key under /data/var and the object\n'
  printf '  store under /data/objects are lost, and every session is invalidated.\n'
  printf 'type "rebuild" to continue: '
  if ! read -r answer; then
    answer=""
  fi
  if [ "$answer" != rebuild ]; then
    REPORT=0
    say "stopped: nothing was destroyed"
    exit 1
  fi
fi

STEP="3/5 destroy every machine"
say "3/5 destroy every machine (a volume cannot be destroyed while one uses it)"
machine_ids=$(flyctl machine list -a "$APP" --json |
  python3 -I -c 'import json, sys
for machine in json.load(sys.stdin):
    print(machine["id"])')
destroyed_machines=""
if [ -z "$machine_ids" ]; then
  note "no machines are listed"
  DONE="no machines were listed"
fi
while IFS= read -r id; do
  if [ -z "$id" ]; then
    continue
  fi
  note "destroying machine $id"
  flyctl machine destroy "$id" --force
  if [ -n "$destroyed_machines" ]; then
    destroyed_machines="$destroyed_machines, $id"
  else
    destroyed_machines=$id
  fi
  DONE="destroyed machines: $destroyed_machines"
done <<< "$machine_ids"
if [ -n "$machine_ids" ]; then
  DONE="every machine destroyed"
fi

STEP="4/5 destroy the volume and create a fresh one"
say "4/5 destroy the volume and create a fresh one"
volume_ids=$(flyctl volumes list -a "$APP" --json |
  python3 -I -c 'import json, sys
name = sys.argv[1]
for volume in json.load(sys.stdin):
    if volume["name"] == name:
        print(volume["id"])' "$VOLUME")
destroyed_volumes=""
if [ -n "$volume_ids" ]; then
  while IFS= read -r id; do
    if [ -z "$id" ]; then
      continue
    fi
    note "destroying volume $id"
    flyctl volumes destroy "$id" --yes
    if [ -n "$destroyed_volumes" ]; then
      destroyed_volumes="$destroyed_volumes, $id"
    else
      destroyed_volumes=$id
    fi
    DONE="every machine destroyed; destroyed volumes: $destroyed_volumes"
  done <<< "$volume_ids"
  DONE="every machine destroyed and volume $VOLUME destroyed"
else
  note "no volume named $VOLUME exists; creating one"
fi
flyctl volumes create "$VOLUME" --region "$REGION" --size 1 --yes
DONE="every machine destroyed and volume $VOLUME recreated"

STEP="5/5 deploy again on the fresh volume"
say "5/5 deploy again so a new machine boots on the fresh volume"
if ! make -C "$REPO" fly; then
  die "the deploy command failed after the volume was recreated"
fi
if ! health; then
  die "the new version did not become healthy on the fresh volume"
fi
if ! flyctl ssh console -a "$APP" -C "ls -l /data"; then
  note "the evidence command failed; the rebuild itself succeeded"
fi
if ! flyctl ssh console -a "$APP" -C "test -f /data/demo.db"; then
  note "the database check failed; the rebuild itself succeeded"
fi

say "done: volume rebuilt, new version healthy (health via $HEALTH_ROUTE), database built by the entrypoint"
