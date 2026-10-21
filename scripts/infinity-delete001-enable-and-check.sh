#!/usr/bin/env bash
# Explicit operator opt-in to typed file.remove(recursive=true) in existing /opt roots.
# Designed only for independent SSH/console on srv-ia, not for MCP shell.
set -Eeuo pipefail
umask 077
BASE=/srv/portico-infinity
EXPECTED_HEAD=1b2bfba22ca9d3a513470781f904adcc8d31472d
HELPER_SHA=09cf08c85cf745fa571a3c31ba0d8cff43d2ee84
POLICY="$BASE/config/policy.yaml"
ENVFILE="$BASE/.env"
COMPOSE=(docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml)
test "$EUID" -eq 0
test "$(hostname -s)" = srv-ia
cd "$BASE"
test "$(git rev-parse HEAD)" = "$EXPECTED_HEAD"
test "$(git remote get-url origin)" = https://github.com/josemirmoura/portico-infinity.git
test -z "$(git status --porcelain --untracked-files=no)"
python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
"${COMPOSE[@]}" config --quiet

for ctr in portico-infinity-broker-1 portico-infinity-gateway-1; do
  test "$(docker inspect -f '{{.State.Health.Status}}' "$ctr")" = healthy
  test "$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project.config_files"}}' "$ctr")" = "$BASE/compose.yaml,$BASE/compose.infinity-auth.remote.yaml"
done

# Fail closed if a systemd unit corresponding to an active SQLite job is running.
python3 - <<'PY'
import sqlite3
import subprocess
path='/srv/portico-infinity/state/state.db'
conn=sqlite3.connect(f'file:{path}?mode=ro',uri=True)
conn.execute('PRAGMA query_only=ON')
states=('starting','running','active','activating','reloading','deactivating','cancelling')
marks=','.join('?' for _ in states)
rows=conn.execute(f'SELECT unit_name FROM jobs WHERE state IN ({marks})',states).fetchall()
for (unit,) in rows:
    if not unit:
        raise SystemExit('DENIED: unresolved job without unit')
    out=subprocess.run(['systemctl','show',unit,'--no-pager',
                        '--property=ActiveState,SubState,Result'],
                       capture_output=True,text=True,timeout=10)
    props=dict(line.split('=',1) for line in out.stdout.splitlines() if '=' in line)
    if out.returncode or (props.get('ActiveState'),props.get('SubState'),props.get('Result')) != ('inactive','dead','success'):
        raise SystemExit('DENIED: active/uncertain systemd job')
print('JOB_GATE_OK=',len(rows))
PY

mkdir -p -m 700 /var/backups/portico-infinity
backup_dir=$(mktemp -d /var/backups/portico-infinity/delete001-action-XXXXXXXX)
chmod 700 "$backup_dir"
cp -p "$POLICY" "$backup_dir/policy.yaml"
sha256sum "$backup_dir/policy.yaml" > "$backup_dir/policy.sha256"
(cd "$backup_dir" && sha256sum -c --status policy.sha256)
for s in zitadel-api zitadel-login zitadel-postgres; do
  docker inspect -f '{{.Id}}' "portico-infinity-$s-1" > "$backup_dir/$s.id"
done

helper="$backup_dir/enable-recursive.py"
curl -fsSL --retry 2 \
  "https://raw.githubusercontent.com/josemirmoura/portico-infinity/$HELPER_SHA/scripts/enable-infinity-recursive-remove.py" \
  -o "$helper"
chmod 600 "$helper"
python3 -m py_compile "$helper"
python3 "$helper" --policy "$POLICY" --env "$ENVFILE"
changed=0
rollback(){
  rc=$?
  trap - ERR
  set +e
  if (( changed )); then
    echo 'ERRO: restaurando politica original e recriando apenas Broker'
    cp -p "$backup_dir/policy.yaml" "$POLICY"
    "${COMPOSE[@]}" up -d --no-deps --force-recreate broker
  fi
  echo "BACKUP_POLITICA=$backup_dir"
  exit "$rc"
}
trap rollback ERR

if grep -qE '^    - remove_recursive[[:space:]]*$' "$POLICY"; then
  echo 'ACAO_JA_HABILITADA'
else
  python3 "$helper" --policy "$POLICY" --env "$ENVFILE" --apply --ack-opt-recursive
  changed=1
  python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
  "${COMPOSE[@]}" config --quiet
  "${COMPOSE[@]}" up -d --no-deps --force-recreate broker
fi
for i in $(seq 1 30); do
  if [[ $(docker inspect -f '{{.State.Health.Status}}' portico-infinity-broker-1 2>/dev/null || :) == healthy ]]; then
    break
  fi
  sleep 2
done
test "$(docker inspect -f '{{.State.Health.Status}}' portico-infinity-broker-1)" = healthy
test "$(docker inspect -f '{{.State.Health.Status}}' portico-infinity-gateway-1)" = healthy
for s in zitadel-api zitadel-login zitadel-postgres; do
  test "$(docker inspect -f '{{.Id}}' "portico-infinity-$s-1")" = "$(cat "$backup_dir/$s.id")"
  test "$(docker inspect -f '{{.State.Health.Status}}' "portico-infinity-$s-1")" = healthy
done
docker exec portico-infinity-broker-1 /usr/local/bin/vps-agent audit-status \
  --socket /run/vps-agent/broker.sock
trap - ERR
echo "RECURSIVE_ACTION_READY=YES"
echo "POLICY_BACKUP=$backup_dir"
echo "EXISTING_SCOPE=/opt; no additional privileges or roots"
