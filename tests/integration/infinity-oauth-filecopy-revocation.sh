#!/usr/bin/env bash
# Single disposable GitHub runner: real ZITADEL OAuth -> MCP -> privileged
# Broker -> host systemd binary filecopy -> independent operator revocation.
# Never run on SRV-IA or a persistent VPS.
set -euo pipefail

LAB="${1:?private ZITADEL TLS lab required}"
SUBJECT="${2:?introspected OAuth subject required}"
test "${RUNNER_ENVIRONMENT:-}" = github-hosted
test "${GITHUB_EVENT_NAME:-}" = pull_request
test "${CI:-}" = true
[[ "${GITHUB_RUN_ID:-}" =~ ^[0-9]{1,24}$ ]]
test -d "$LAB"
test ! -e /srv/portico-infinity
test ! -L /srv/portico-infinity
test ! -e /usr/local/libexec/portico-infinity/portico-filecopy-worker
test ! -L /usr/local/libexec/portico-infinity
test "$COMPOSE_PROJECT_NAME" = portico-infinity-oauth
id -u | grep -qx '[0-9]\+'
test -f "$LAB/probe-token.json"
test -f /tmp/probe-machine.json
test -f /tmp/introspection-app.json

IDENT="oauth-revocation-${GITHUB_RUN_ID}"
SCOPE="/opt/infinity-oauth-filecopy-${GITHUB_RUN_ID}"
STATE="/srv/portico-infinity/state/filecopy-jobs"
UNIT=""
CLEANED=0
cleanup_synthetic_host() {
  test "$CLEANED" = 0 || return 0
  CLEANED=1
  set +e
  if [[ "$UNIT" =~ ^vps-agent-filecopy-[0-9a-f]{32}\.service$ ]]; then
    sudo systemctl kill --kill-whom=main --signal=CONT "$UNIT" >/dev/null 2>&1 || true
    sudo systemctl stop "$UNIT" >/dev/null 2>&1 || true
    sudo systemctl reset-failed "$UNIT" >/dev/null 2>&1 || true
  fi
  sudo rm -f -- "/usr/local/libexec/portico-infinity/portico-filecopy-worker"
  sudo rmdir /usr/local/libexec/portico-infinity 2>/dev/null || true
  if test -d "$SCOPE" && test ! -L "$SCOPE"; then
    sudo rm -f -- "$SCOPE/model-source.gguf" \
      "$SCOPE/model-destination-inflight.gguf" \
      "$SCOPE/model-destination-after-fence.gguf"
    sudo rmdir -- "$SCOPE" 2>/dev/null || true
  fi
  if test -d /srv/portico-infinity && test ! -L /srv/portico-infinity; then
    sudo rm -f -- /srv/portico-infinity/scripts/infinity-revoke-filecopy-subject.py
    if test -d "$STATE" && test ! -L "$STATE"; then
      sudo find "$STATE" -maxdepth 1 -type f \
        \( -name 'vps-agent-filecopy-*.request.json' -o \
           -name 'vps-agent-filecopy-*.progress.json' -o \
           -name 'vps-agent-filecopy-*.lock' -o \
           -name 'revoked-subject-*.marker' \) -delete
      sudo rmdir "$STATE" 2>/dev/null || true
    fi
    sudo rmdir /srv/portico-infinity/scripts /srv/portico-infinity/state \
      /srv/portico-infinity 2>/dev/null || true
  fi
}
trap cleanup_synthetic_host EXIT

# A fully isolated, root-owned controller; not the installed production host.
sudo install -d -o root -g root -m 0755 /srv/portico-infinity/scripts
sudo install -d -o root -g root -m 0700 "$STATE"
sudo install -o root -g root -m 0644 \
  scripts/infinity-revoke-filecopy-subject.py \
  /srv/portico-infinity/scripts/infinity-revoke-filecopy-subject.py
sudo install -d -o root -g root -m 0755 "$SCOPE"
sudo truncate -s 8G "$SCOPE/model-source.gguf"
sudo chmod 0644 "$SCOPE/model-source.gguf"
CGO_ENABLED=0 go build -trimpath -o "$RUNNER_TEMP/${IDENT}-host-worker" \
  ./cmd/portico-filecopy-worker
sudo install -d -o root -g root -m 0755 /usr/local/libexec/portico-infinity
sudo install -o root -g root -m 0755 "$RUNNER_TEMP/${IDENT}-host-worker" \
  /usr/local/libexec/portico-infinity/portico-filecopy-worker

# Replace only the *already synthetic* unprivileged Broker. Keep the real
# ZITADEL, verified TLS proxy, real OAuth Gateway and its Unix IPC socket.
docker rm -f portico-ci-e2e-broker >/dev/null
sudo rm -f -- "$LAB/socket/broker.sock"
docker run -d --rm --name portico-ci-e2e-broker \
  --privileged --pid=host --network=host --user 0:65532 \
  --mount "type=bind,src=$LAB/broker-policy.yaml,dst=/etc/vps-agent/policy.yaml,readonly" \
  --mount "type=bind,src=$LAB/socket,dst=/run/vps-agent" \
  --mount "type=bind,src=/srv/portico-infinity/state,dst=/var/lib/vps-agent" \
  --mount "type=bind,src=/opt,dst=/host/opt" \
  -e VPS_AGENT_POLICY=/etc/vps-agent/policy.yaml \
  -e VPS_AGENT_BROKER_SOCKET=/run/vps-agent/broker.sock \
  -e VPS_AGENT_STATE_DB=/var/lib/vps-agent/state.db \
  -e VPS_AGENT_HOST_ROOT=/host \
  -e VPS_AGENT_HOST_NSENTER=1 \
  -e VPS_AGENT_PHYSICAL_SCOPE_ROOT=/opt \
  -e VPS_AGENT_INFINITY_PROTECTED_ROOT=/srv/portico-infinity \
  -e VPS_AGENT_INFINITY_FILECOPY_JOBS=1 \
  -e VPS_AGENT_INFINITY_FILECOPY_MAX_BYTES=9663676416 \
  -e VPS_AGENT_EXPECTED_SUBJECT="$SUBJECT" \
  -e VPS_AGENT_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  portico-gate-c-real-broker:disposable >/dev/null
for _ in $(seq 1 70); do
  sudo test -S "$LAB/socket/broker.sock" && break
  sleep 1
done
sudo test -S "$LAB/socket/broker.sock" || {
  docker logs --tail=60 portico-ci-e2e-broker >&2
  exit 1
}
cp tests/integration/infinity-oauth-filecopy-probe.py "$LAB/oauth-filecopy-probe.py"
chmod 0644 "$LAB/oauth-filecopy-probe.py"

probe() {
  docker run --rm --network ci-edge \
    -e CI=true -e RUNNER_ENVIRONMENT=github-hosted -e GITHUB_EVENT_NAME=pull_request \
    -e GITHUB_RUN_ID="$GITHUB_RUN_ID" \
    --user "$(id -u):$(id -g)" \
    --cap-drop ALL --security-opt no-new-privileges:true \
    --mount "type=bind,src=$LAB,dst=/pki,readonly" \
    python:3.12-slim-bookworm python3 /pki/oauth-filecopy-probe.py "$1"
}

# Derive the exact typed unit from the authenticated subject + operation ID.
# Start an independent host watcher BEFORE the MCP request, avoiding the
# race in which a fast 8 GiB sparse transfer finished before shell polling.
expected_job="$(python3 - "$SUBJECT" "$GITHUB_RUN_ID" <<'PY'
import hashlib,sys
subject,run=sys.argv[1:]
op="oauth-filecopy-inflight-"+run
print("vps-agent-filecopy-"+hashlib.sha256((subject+"\x00"+op).encode()).hexdigest()[:32])
PY
)"
[[ "$expected_job" =~ ^vps-agent-filecopy-[0-9a-f]{32}$ ]]
UNIT="$expected_job.service"
WATCH_LOG="$RUNNER_TEMP/${IDENT}-freeze-watcher.log"
sudo python3 - "$STATE/$expected_job.progress.json" "$UNIT" <<'PY' >"$WATCH_LOG" 2>&1 &
import json,subprocess,sys,time
path,unit=sys.argv[1:]
for _ in range(1800):
    try:
        with open(path,encoding="utf-8") as fd:
            progress=json.load(fd)
        if progress.get("phase")=="running":
            result=subprocess.run(["systemctl","kill","--kill-whom=main",
                                   "--signal=STOP",unit],
                                  capture_output=True,text=True,timeout=5)
            if result.returncode==0:
                print("REAL_OAUTH_SYSTEMD_INFLIGHT_WORKER_FROZEN: PASS")
                sys.exit(0)
            # A process can be between systemd-run and PID registration.
    except (OSError,ValueError,subprocess.TimeoutExpired):
        pass
    time.sleep(0.02)
raise SystemExit("could not freeze active systemd copy before terminal state")
PY
watch_pid=$!
JOB="$(probe start)"
[[ "$JOB" =~ ^vps-agent-filecopy-[0-9a-f]{32}$ ]] || {
  wait "$watch_pid" || true
  echo "E2E failed: no real OAuth MCP filecopy job" >&2
  exit 1
}
test "$JOB" = "$expected_job" || {
  echo "real authenticated principal unexpectedly changed job identity" >&2
  exit 1
}
echo "REAL_ZITADEL_AUTHENTICATED_MCP_FILECOPY_START: PASS"
if ! wait "$watch_pid"; then
  cat "$WATCH_LOG" >&2
  sudo systemctl show "$UNIT" --property=ActiveState,SubState,Result --no-pager >&2 || true
  sudo python3 - "$STATE/$JOB.progress.json" <<'PY'
import json,sys
try:
    doc=json.load(open(sys.argv[1]))
    print("Worker phase:",doc.get("phase"),"bytes:",doc.get("bytes_done"))
except (OSError,ValueError) as error:
    print("Worker checkpoint unavailable:",str(error))
PY
  exit 1
fi
cat "$WATCH_LOG"
test "$(sudo systemctl show "$UNIT" -p ActiveState --value)" = activating
sudo python3 - "$STATE/$JOB.progress.json" <<'PY'
import json,sys
doc=json.load(open(sys.argv[1]))
assert doc["phase"]=="running",doc
print("REAL_OAUTH_FILECOPY_DURABLE_RUNNING_STATE: PASS")
PY

# ZITADEL's unexpired token is still active when the external operator
# fences the subject. Persist the fence FIRST, and only then allow TERM.
OPERATOR_LOG="$RUNNER_TEMP/${IDENT}-operator-revocation.log"
(
  cd /srv/portico-infinity
  sudo python3 scripts/infinity-revoke-filecopy-subject.py \
    --subject "$SUBJECT" --revoke \
    --confirm REVOKE-INFINITY-FILECOPY-SUBJECT
) >"$OPERATOR_LOG" 2>&1 &
operator_pid=$!
marker="$(python3 - "$SUBJECT" <<'PY'
import hashlib,sys
print("revoked-subject-"+hashlib.sha256(sys.argv[1].encode()).hexdigest()+".marker")
PY
)"
for i in $(seq 1 200); do
  sudo test -f "$STATE/$marker" && break
  sleep 0.1
done
if ! sudo test -f "$STATE/$marker"; then
  sudo systemctl kill --kill-whom=main --signal=CONT "$UNIT" || true
  wait "$operator_pid" || true
  echo "operator revocation fence was not persisted" >&2
  exit 1
fi
# systemctl stop may have already completed while the unit was frozen.
# A missing MainPID is benign ONLY if the mandatory durable checkpoint below
# proves cancelled and the destination was never published.
sudo systemctl kill --kill-whom=main --signal=CONT "$UNIT" >/dev/null 2>&1 || true
if ! wait "$operator_pid"; then
  cat "$OPERATOR_LOG" >&2
  exit 1
fi
cat "$OPERATOR_LOG"
sudo python3 - "$STATE/$JOB.progress.json" <<'PY'
import json,sys
progress=json.load(open(sys.argv[1]))
assert progress["phase"] == "cancelled",progress
print("REAL_OAUTH_FILECOPY_TERMINATED_WITH_DURABLE_CHECKPOINT: PASS")
PY
test ! -e "$SCOPE/model-destination-inflight.gguf"
probe fence-denies

# Finally deactivate the very same OAuth identity OUTSIDE MCP, through an
# independent ZITADEL bootstrap administrator. Never use the bearer token
# as a privileged command channel.
PROBE_USER_ID="$(python3 - <<'PY'
import json
print(json.load(open('/tmp/probe-machine.json'))['userId'])
PY
)"
ACCESS_TOKEN="$(python3 - <<'PY'
import json
print(json.load(open('/tmp/probe-token.json'))['access_token'])
PY
)"
readarray -t INTRO_CREDENTIALS < <(python3 - <<'PY'
import json
x=json.load(open('/tmp/introspection-app.json'))
print(x['clientId'])
print(x['clientSecret'])
PY
)
PAT="$(docker run --rm -v portico-infinity-oauth_zitadel-bootstrap:/zitadel/bootstrap:ro \
  alpine:3.22 cat /zitadel/bootstrap/bootstrap-admin.pat)"
echo "::add-mask::$ACCESS_TOKEN"
echo "::add-mask::$PAT"
echo "::add-mask::${INTRO_CREDENTIALS[1]}"
AUTH_HEADER="$(printf '%s:%s' "${INTRO_CREDENTIALS[0]}" "${INTRO_CREDENTIALS[1]}" | base64 -w0)"
echo "::add-mask::$AUTH_HEADER"
curl_ci() {
  docker run --rm -i --network ci-edge curlimages/curl:8.16.0 \
    -fsS -H 'Host: mcp.example.invalid' -H 'X-Forwarded-Proto: https' "$@"
}
curl_ci -X POST http://zitadel-api:8080/oauth/v2/introspect \
  -H "Authorization: Basic $AUTH_HEADER" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode "token=$ACCESS_TOKEN" >/tmp/revocation-active-before.json
python3 - <<'PY'
import json
assert json.load(open('/tmp/revocation-active-before.json')).get('active') is True
print('REAL_OAUTH_TOKEN_ACTIVE_BEFORE_IDENTITY_DEACTIVATION: PASS')
PY
curl_ci -X POST "http://zitadel-api:8080/v2/users/$PROBE_USER_ID/deactivate" \
  -H "Authorization: Bearer $PAT" \
  -H 'Content-Type: application/json' --data '{}' \
  >/tmp/revocation-deactivation.json
curl_ci -X POST http://zitadel-api:8080/oauth/v2/introspect \
  -H "Authorization: Basic $AUTH_HEADER" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode "token=$ACCESS_TOKEN" >/tmp/revocation-active-after.json
python3 - <<'PY'
import json
assert json.load(open('/tmp/revocation-active-after.json')).get('active') is False
print('REAL_ZITADEL_EXTERNAL_DEACTIVATION_WITH_ACTIVE_FILECOPY: PASS')
PY
probe token-denies

# The job's irreversible result is unchanged after OAuth deactivation.
sudo python3 - "$STATE/$JOB.progress.json" <<'PY'
import json,sys
p=json.load(open(sys.argv[1]))
assert p["phase"]=="cancelled",p
PY
test ! -e "$SCOPE/model-destination-inflight.gguf"
touch /tmp/infinity-oauth-filecopy-integrated-revocation-passed
echo "OAUTH_ZITADEL_MCP_SYSTEMD_EXTERNAL_REVOCATION_COMBINED: PASS"
