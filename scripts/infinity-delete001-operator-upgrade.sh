#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

BASE=/srv/portico-infinity
OLD=a16bd9a805106d9cec8bd7597b0f75e44fdde0be
TARGET=1b2bfba22ca9d3a513470781f904adcc8d31472d
ORIGIN=https://github.com/josemirmoura/portico-infinity.git
BACKUP_BASE=/var/backups/portico-infinity
cd "$BASE"
COMPOSE=(docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml)
maint_started=0
on_failure() {
  rc=$?
  trap - ERR
  set +e
  echo "FALHA: etapa interrompida; codigo de saida=$rc"
  if (( maint_started )); then
    echo 'Tentando restaurar APENAS Broker e Gateway ao SHA anterior...'
    git switch --detach "$OLD"
    "${COMPOSE[@]}" up -d --build --no-deps --force-recreate broker gateway
    echo 'Conferir health/auditoria e, se necessario, restaurar o backup consistente pelo console.'
  fi
  exit "$rc"
}
trap on_failure ERR

echo '=== PREFLIGHT SOMENTE LEITURA ==='
test "$(hostname -s)" = srv-ia
test "$(git rev-parse HEAD)" = "$OLD"
test "$(git remote get-url origin)" = "$ORIGIN"
test -z "$(git status --porcelain --untracked-files=no)"
test ! -L "$BASE"
test "$(stat -c '%u:%g' "$BASE")" = '0:0'
python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
"${COMPOSE[@]}" config --quiet
for svc in broker gateway; do
  ctr="portico-infinity-${svc}-1"
  files=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.config_files"}}' "$ctr")
  test "$files" = "$BASE/compose.yaml,$BASE/compose.infinity-auth.remote.yaml"
  test "$(docker inspect -f '{{.State.Health.Status}}' "$ctr")" = healthy
done
for svc in zitadel-login zitadel-api zitadel-postgres; do
  test "$(docker inspect -f '{{.State.Health.Status}}' "portico-infinity-${svc}-1")" = healthy
done

check_jobs() {
python3 - <<'PY'
import sqlite3, subprocess
p = '/srv/portico-infinity/state/state.db'
conn = sqlite3.connect('file:' + p + '?mode=ro', uri=True)
conn.execute('PRAGMA query_only=ON')
states = ('starting','running','active','activating','reloading','deactivating','cancelling')
marks = ','.join('?' for _ in states)
rows = list(conn.execute('SELECT tool,unit_name FROM jobs WHERE state IN (' + marks + ')', states))
for tool,unit in rows:
    if not unit:
        raise SystemExit('ABORTAR: registro sem unidade; investigar')
    r = subprocess.run(['systemctl','show',unit,'--no-pager','--property=ActiveState,SubState,Result'],text=True,capture_output=True,timeout=10)
    props = dict(s.split('=',1) for s in r.stdout.splitlines() if '=' in s)
    if r.returncode or props.get('ActiveState') != 'inactive' or props.get('SubState') != 'dead' or props.get('Result') != 'success':
        raise SystemExit('ABORTAR: job com unidade nao reconciliada ou ativa')
print('PREFLIGHT_JOBS_INATIVOS=',len(rows))
PY
}
check_jobs

git fetch --quiet origin main
test "$(git cat-file -t "$TARGET")" = commit
git merge-base --is-ancestor "$TARGET" origin/main
git merge-base --is-ancestor "$OLD" "$TARGET"
git diff --quiet "$OLD" "$TARGET" -- internal/state/

mkdir -p -- "$BACKUP_BASE"
test ! -L "$BACKUP_BASE"
chmod 700 "$BACKUP_BASE"
BACKUP="$BACKUP_BASE/delete001-$(date -u +%Y%m%dT%H%M%SZ)-${OLD:0:12}"
mkdir -m 700 "$BACKUP"
printf '%s\n' "$OLD" > "$BACKUP/previous-sha.txt"
for svc in zitadel-login zitadel-api zitadel-postgres; do
  docker inspect -f '{{.Id}}' "portico-infinity-${svc}-1" > "$BACKUP/${svc}-id.txt"
done

echo '=== BACKUP CONSISTENTE: INTERROMPER APENAS BROKER/GATEWAY ==='
maint_started=1
"${COMPOSE[@]}" stop gateway
check_jobs
"${COMPOSE[@]}" stop broker
tar -C "$BASE" -czf "$BACKUP/runtime.tar.gz" \
  .env config state compose.yaml compose.infinity-auth.remote.yaml
tar -tzf "$BACKUP/runtime.tar.gz" > /dev/null
(cd "$BACKUP" && sha256sum runtime.tar.gz > sha256.txt && sha256sum -c --status sha256.txt)
echo "BACKUP_VERIFICADO=$BACKUP"

echo '=== PROMOVER O COMMIT APROVADO ==='
git switch --detach "$TARGET"
test "$(git rev-parse HEAD)" = "$TARGET"
python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
"${COMPOSE[@]}" config --quiet
"${COMPOSE[@]}" up -d --build --no-deps --force-recreate broker gateway

for i in $(seq 1 50); do
  if [[ $(docker inspect -f '{{.State.Health.Status}}' portico-infinity-broker-1 2>/dev/null || :) == healthy &&
        $(docker inspect -f '{{.State.Health.Status}}' portico-infinity-gateway-1 2>/dev/null || :) == healthy ]]; then
    break
  fi
  sleep 2
done
for svc in broker gateway zitadel-login zitadel-api zitadel-postgres; do
  test "$(docker inspect -f '{{.State.Health.Status}}' "portico-infinity-${svc}-1")" = healthy
done
for svc in zitadel-login zitadel-api zitadel-postgres; do
  test "$(docker inspect -f '{{.Id}}' "portico-infinity-${svc}-1")" = "$(cat "$BACKUP/${svc}-id.txt")"
done
docker exec portico-infinity-broker-1 /usr/local/bin/vps-agent audit-status --socket /run/vps-agent/broker.sock
trap - ERR
printf 'ATUALIZACAO_CONCLUIDA=%s\nBACKUP=%s\n' "$(git rev-parse HEAD)" "$BACKUP"
"${COMPOSE[@]}" ps
