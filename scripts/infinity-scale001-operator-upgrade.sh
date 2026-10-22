#!/usr/bin/env bash
# Independent root operator rollout of approved SCALE-001 PRs #16–#19; worker staged OFF.
# NEVER run via the Portico MCP shell: /srv/portico-infinity is outside its authority.
set -Eeuo pipefail
umask 077

BASE=/srv/portico-infinity
OLD=b6102806bfad65e70f3f5c08bfa96281d89e2f16
TARGET=c269d980d91392c2eca9ab92ab2eaa132efc6752
ORIGIN=https://github.com/josemirmoura/portico-infinity.git
BACKUP_BASE=/var/backups/portico-infinity
COMPOSE=(docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml)
maintenance_started=0
worker_installed=0
worker_host=/usr/local/libexec/portico-infinity/portico-filecopy-worker
backup=""
rollback() {
  rc=$?
  trap - ERR
  set +e
  echo "UPGRADE_SCALE001_FAILED_CODE=$rc"
  if (( worker_installed )); then
    echo 'ROLLBACK: remove newly installed host worker'
    if [[ -f "$worker_host" && ! -L "$worker_host" ]]; then
      rm -f -- "$worker_host"
    fi
  fi
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

# The initial rollout must never enable an untested privileged worker.
if grep -Eq '^VPS_AGENT_INFINITY_FILECOPY_JOBS=1[[:space:]]*
mkdir -p "$BACKUP_BASE"
test ! -L "$BACKUP_BASE"
chmod 700 "$BACKUP_BASE"
backup="$BACKUP_BASE/scale001-$(date -u +%Y%m%dT%H%M%SZ)-b6102806bfad"
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

echo "=== DEPLOY APPROVED SCALE001 SHA, FILECOPY WORKER OFF ==="
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

echo "=== STAGE ROOT-OWNED HOST WORKER; KEEP MCP FEATURE DISABLED ==="
# The operator exclusively controls /usr/local/libexec and /srv, not the MCP.
# Never overwrite an existing executable, and keep the feature flag OFF.
docker cp portico-infinity-broker-1:/usr/local/bin/portico-filecopy-worker \
  "$backup/portico-filecopy-worker.image"
test -f "$backup/portico-filecopy-worker.image"
sha256sum "$backup/portico-filecopy-worker.image" > "$backup/worker-image.sha256"
install -d -o root -g root -m 0755 /usr/local/libexec/portico-infinity
test ! -L /usr/local/libexec/portico-infinity
test ! -e "$worker_host" && test ! -L "$worker_host"
install -o root -g root -m 0755 "$backup/portico-filecopy-worker.image" "$worker_host"
worker_installed=1
test "$(stat -c '%u:%g' "$worker_host")" = '0:0'
test "$(sha256sum "$worker_host" | cut -d' ' -f1)" = \
     "$(sha256sum "$backup/portico-filecopy-worker.image" | cut -d' ' -f1)"
install -d -o root -g root -m 0700 "$BASE/state/filecopy-jobs"
test ! -L "$BASE/state/filecopy-jobs"
test "$(stat -c '%u:%g %a' "$BASE/state/filecopy-jobs")" = '0:0 700'
echo "WORKER_STAGED_SHA256=$(sha256sum "$worker_host" | cut -d' ' -f1)"
echo "WORKER_FEATURE_ENABLED=NO"

echo "=== ONE READ-ONLY RETENTION REPORT; NO JOB CLEANUP ==="
python3 scripts/collect-infinity-compose-units.py --retention-days 7 --limit 25
trap - ERR
echo "UPGRADE_SCALE001_COMPLETED=$TARGET"
echo "BACKUP_LOCATION=$backup"
"${COMPOSE[@]}" ps
 .env; then
  echo 'ABORT: privileged binary copy is already enabled; independent operator review required'
  exit 1
fi
worker_host=/usr/local/libexec/portico-infinity/portico-filecopy-worker
if [[ -e "$worker_host" || -L "$worker_host" ]]; then
  echo 'ABORT: existing host worker must be reconciled; no overwrite'
  exit 1
fi
python3 - <<'PY'
import os, stat
for p in ('/usr','/usr/local','/usr/local/libexec','/usr/local/libexec/portico-infinity'):
    if not os.path.lexists(p):
        continue
    st=os.lstat(p)
    if not stat.S_ISDIR(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022:
        raise SystemExit('ABORT: unsafe worker ancestor '+p)
print('WORKER_HOST_PATH_PREFLIGHT=OK')
PY

mkdir -p "$BACKUP_BASE"
test ! -L "$BACKUP_BASE"
chmod 700 "$BACKUP_BASE"
backup="$BACKUP_BASE/scale001-$(date -u +%Y%m%dT%H%M%SZ)-b6102806bfad"
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

echo "=== DEPLOY APPROVED SCALE001 SHA, FILECOPY WORKER OFF ==="
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

echo "=== STAGE ROOT-OWNED HOST WORKER; KEEP MCP FEATURE DISABLED ==="
python3 scripts/collect-infinity-compose-units.py --retention-days 7 --limit 25
trap - ERR
echo "UPGRADE_SCALE001_COMPLETED=$TARGET"
echo "BACKUP_LOCATION=$backup"
"${COMPOSE[@]}" ps
