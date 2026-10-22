#!/usr/bin/env bash
# Operator-only guarded hotfix of SCALE-001 durable cancellation (#20); feature remains OFF.
# NEVER run via the Portico MCP shell: /srv/portico-infinity is outside its authority.
set -Eeuo pipefail
umask 077

BASE=/srv/portico-infinity
OLD=c269d980d91392c2eca9ab92ab2eaa132efc6752
TARGET=119f31aa15212e2b3a1324764433362698ecf29c
ORIGIN=https://github.com/josemirmoura/portico-infinity.git
BACKUP_BASE=/var/backups/portico-infinity
COMPOSE=(docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml)
maintenance_started=0
backup=""
rollback() {
  rc=$?
  trap - ERR
  set +e
  echo "UPGRADE_CANCEL020_FAILED_CODE=$rc"
  if (( maintenance_started )); then
    echo "ROLLBACK: restoring previous approved source and ONLY Broker/Gateway"
    git switch --detach "$OLD"
    "${COMPOSE[@]}" up -d --build --no-deps --force-recreate broker gateway
    echo "ROLLBACK_ATTEMPTED: verify audit and health; backup preserved for operator"
  fi
  echo "BACKUP_LOCATION=$backup"
  exit "$rc"
}
trap rollback ERR

echo "=== OPERATOR PREFLIGHT (fail closed) ==="
test "$EUID" -eq 0
test "$(hostname -s)" = srv-ia
test -d "$BASE" && test ! -L "$BASE"
cd "$BASE"
test "$(git remote get-url origin)" = "$ORIGIN"
test "$(git rev-parse HEAD)" = "$OLD"
test -z "$(git status --porcelain --untracked-files=no)"
test "$(stat -c '%u:%g' "$BASE")" = 0:0
python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
"${COMPOSE[@]}" config --quiet

for svc in broker gateway; do
  name="portico-infinity-$svc-1"
  test "$(docker inspect -f '{{.State.Health.Status}}' "$name")" = healthy
  test "$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.config_files"}}' "$name")" = "$BASE/compose.yaml,$BASE/compose.infinity-auth.remote.yaml"
done

for svc in zitadel-api zitadel-login zitadel-postgres; do
  name="portico-infinity-$svc-1"
  test "$(docker inspect -f '{{.State.Health.Status}}' "$name")" = healthy
done

# SQLite read-only: never run maintenance while any pending unit is active/uncertain.
check_jobs() {
python3 - <<'PY'
import sqlite3, subprocess
db="/srv/portico-infinity/state/state.db"
con=sqlite3.connect("file:"+db+"?mode=ro",uri=True)
con.execute("PRAGMA query_only=ON")
states=("starting","running","active","activating","reloading","deactivating","cancelling")
q=",".join("?" for _ in states)
rows=con.execute("SELECT unit_name FROM jobs WHERE state IN ("+q+")",states).fetchall()
for (unit,) in rows:
    if not unit:
        raise SystemExit("ABORT: job without unit")
    p=subprocess.run(["systemctl","show",unit,"--no-pager","--property=ActiveState,SubState,Result"],
                     capture_output=True,text=True,timeout=10)
    info=dict(line.split("=",1) for line in p.stdout.splitlines() if "=" in line)
    if p.returncode or (info.get("ActiveState"),info.get("SubState"),info.get("Result")) != ("inactive","dead","success"):
        raise SystemExit("ABORT: pending job active/unknown; manual inspection required")
print("PENDING_UNIT_PREFLIGHT_INACTIVE=",len(rows))
PY
}
check_jobs
git fetch --quiet origin main
test "$(git cat-file -t "$TARGET")" = commit
git merge-base --is-ancestor "$OLD" "$TARGET"
git merge-base --is-ancestor "$TARGET" origin/main
git diff --quiet "$OLD" "$TARGET" -- internal/state/
# Hotfix changes only Broker logic; the independently installed host worker
# and shared Compose layout must remain byte-identical to the approved runtime.
git diff --quiet "$OLD" "$TARGET" -- \
  internal/filecopyworker/ cmd/portico-filecopy-worker/ \
  internal/securefs/large_copy.go Dockerfile compose.yaml \
  compose.infinity-auth.remote.yaml
WORKER=/usr/local/libexec/portico-infinity/portico-filecopy-worker
PINNED_WORKER_SHA=4d286ba440d12bec518683c24b0ccafb5869e63980ddfc3311bd1d59a9f8c230
test -f "$WORKER" && test ! -L "$WORKER"
test "$(stat -c '%u:%g' "$WORKER")" = 0:0
test "$(sha256sum "$WORKER" | cut -d' ' -f1)" = "$PINNED_WORKER_SHA"
if grep -Eq '^VPS_AGENT_INFINITY_FILECOPY_JOBS=1[[:space:]]*$' .env; then
  echo 'ABORT: filecopy feature unexpectedly enabled; operator review required'
  exit 1
fi
flag=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' \
  portico-infinity-broker-1 | sed -n 's/^VPS_AGENT_INFINITY_FILECOPY_JOBS=//p')
test "$flag" = 0
echo "FILECOPY_FLAG_PREFLIGHT=OFF"

mkdir -p "$BACKUP_BASE"
test ! -L "$BACKUP_BASE"
chmod 700 "$BACKUP_BASE"
backup="$BACKUP_BASE/scale001-cancel020-$(date -u +%Y%m%dT%H%M%SZ)-c269d980d913"
mkdir -m 700 "$backup"
printf '%s\n' "$OLD" > "$backup/previous-sha.txt"
for svc in zitadel-api zitadel-login zitadel-postgres; do
  docker inspect -f '{{.Id}}' "portico-infinity-$svc-1" > "$backup/$svc.id"
done

echo "=== STOP ONLY GATEWAY/BROKER AND ARCHIVE CONSISTENT RUNTIME ==="
maintenance_started=1
"${COMPOSE[@]}" stop gateway
check_jobs
"${COMPOSE[@]}" stop broker
tar -C "$BASE" -czf "$backup/runtime.tar.gz" \
  .env config state compose.yaml compose.infinity-auth.remote.yaml
tar -tzf "$backup/runtime.tar.gz" >/dev/null
(cd "$backup" && sha256sum runtime.tar.gz > runtime.sha256 && sha256sum -c --status runtime.sha256)
echo "BACKUP_VERIFIED=$backup"

echo "=== DEPLOY APPROVED FILECOPY CANCELLATION HOTFIX, FEATURE OFF ==="
git switch --detach "$TARGET"
test "$(git rev-parse HEAD)" = "$TARGET"
python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
"${COMPOSE[@]}" config --quiet
"${COMPOSE[@]}" up -d --build --no-deps --force-recreate broker gateway

for i in $(seq 1 50); do
  if [[ $(docker inspect -f '{{.State.Health.Status}}' portico-infinity-broker-1 2>/dev/null || :) = healthy &&
        $(docker inspect -f '{{.State.Health.Status}}' portico-infinity-gateway-1 2>/dev/null || :) = healthy ]]; then
    break
  fi
  sleep 2
done
for svc in broker gateway zitadel-api zitadel-login zitadel-postgres; do
  test "$(docker inspect -f '{{.State.Health.Status}}' "portico-infinity-$svc-1")" = healthy
done
for svc in zitadel-api zitadel-login zitadel-postgres; do
  test "$(docker inspect -f '{{.Id}}' "portico-infinity-$svc-1")" = "$(cat "$backup/$svc.id")"
done
docker exec portico-infinity-broker-1 \
  /usr/local/bin/vps-agent audit-status --socket /run/vps-agent/broker.sock
flag=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' \
  portico-infinity-broker-1 | sed -n 's/^VPS_AGENT_INFINITY_FILECOPY_JOBS=//p')
test "$flag" = 0
test "$(sha256sum "$WORKER" | cut -d' ' -f1)" = "$PINNED_WORKER_SHA"
echo "WORKER_FEATURE_ENABLED=NO"
echo "WORKER_SHA256_UNCHANGED=$PINNED_WORKER_SHA"

echo "=== ONE READ-ONLY RETENTION REPORT; NO JOB CLEANUP ==="
python3 scripts/collect-infinity-compose-units.py --retention-days 7 --limit 25
trap - ERR
echo "UPGRADE_CANCEL020_COMPLETED=$TARGET"
echo "BACKUP_LOCATION=$backup"
"${COMPOSE[@]}" ps
