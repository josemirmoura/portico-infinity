#!/usr/bin/env bash
# GitHub-hosted ephemeral QEMU/KVM guest kernel reset and real Pórtico Broker/Gateway systemd services and SQLite.
# Independent proof of guest /proc/.../boot_id change, not just a container restart.
set -euo pipefail
test "${CI:-}" = true && test "${RUNNER_ENVIRONMENT:-}" = github-hosted
test "${GITHUB_EVENT_NAME:-}" = pull_request
[[ "${GITHUB_RUN_ID:-}" =~ ^[0-9]{1,24}$ ]]
test "$(uname -m)" = x86_64
if test -c /dev/kvm && test -w /dev/kvm; then
  ACCEL=kvm
else
  ACCEL=tcg
fi
echo "QEMU guest acceleration mode: $ACCEL (guest kernel still boots and resets independently)"
LAB="$(mktemp -d "${RUNNER_TEMP:?}/portico-qemu-stack-reboot.XXXXXXXX")"
chmod 0700 "$LAB"
QEMU_PID=""
cleanup() {
  if test -n "$QEMU_PID"; then kill "$QEMU_PID" 2>/dev/null || true; wait "$QEMU_PID" 2>/dev/null || true; fi
  rm -rf -- "$LAB"
}
trap cleanup EXIT
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -qq
sudo apt-get install -y -qq qemu-system-x86 qemu-utils cloud-image-utils ovmf socat >/dev/null
command -v qemu-system-x86_64
command -v cloud-localds
OVMF=""
OVMF_VARS=""
for candidate in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd; do
  if test -r "$candidate"; then
    vars="${candidate/CODE/VARS}"
    if test -r "$vars"; then OVMF="$candidate"; OVMF_VARS="$vars"; break; fi
  fi
done
test -n "$OVMF" && test -n "$OVMF_VARS" || { echo "UEFI code/variable firmware not available" >&2; exit 2; }
cp -- "$OVMF_VARS" "$LAB/ovmf-vars.fd"
chmod 0600 "$LAB/ovmf-vars.fd"
BASE=https://cloud-images.ubuntu.com/minimal/releases/noble/release
curl -fsSL --retry 4 --connect-timeout 20 "$BASE/SHA256SUMS" -o "$LAB/SHA256SUMS"
curl -fsSL --retry 4 --connect-timeout 20 "$BASE/ubuntu-24.04-minimal-cloudimg-amd64.img" -o "$LAB/ubuntu.img"
(cd "$LAB"; mv ubuntu.img ubuntu-24.04-minimal-cloudimg-amd64.img; sha256sum -c --ignore-missing SHA256SUMS)
qemu-img resize "$LAB/ubuntu-24.04-minimal-cloudimg-amd64.img" 10G
ssh-keygen -q -t ed25519 -N "" -f "$LAB/key"
PUB="$(cat "$LAB/key.pub")"
cat >"$LAB/user-data" <<EOF
#cloud-config
users:
  - default
  - name: infinity-ci
    lock_passwd: true
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - $PUB
ssh_pwauth: false
package_update: false
runcmd:
  - [ systemctl, enable, ssh ]
EOF
printf "instance-id: infinity-stack-reboot-%s\nlocal-hostname: infinity-stack-reboot-vm\n" "$GITHUB_RUN_ID" >"$LAB/meta-data"
cloud-localds "$LAB/seed.iso" "$LAB/user-data" "$LAB/meta-data"
test ! -e "$LAB/qmp.sock"
! ss -ltn | grep -q ":12022 " || { echo "VM SSH port already bound" >&2; exit 2; }
! ss -ltn | grep -q ":18080 " || { echo "MCP fixture port already bound" >&2; exit 2; }
qemu-system-x86_64 -machine "q35,accel=$ACCEL" -m 3072 -smp 2 -nographic -monitor none \
  -drive "if=pflash,format=raw,unit=0,readonly=on,file=$OVMF" \
  -drive "if=pflash,format=raw,unit=1,file=$LAB/ovmf-vars.fd" \
  -drive "file=$LAB/ubuntu-24.04-minimal-cloudimg-amd64.img,if=virtio,format=qcow2" \
  -drive "file=$LAB/seed.iso,if=virtio,media=cdrom,format=raw" \
  -netdev "user,id=vmnet,hostfwd=tcp:127.0.0.1:12022-:22,hostfwd=tcp:127.0.0.1:18080-:8080" \
  -device virtio-net-pci,netdev=vmnet \
  -qmp "unix:$LAB/qmp.sock,server=on,wait=off" \
  -serial "file:$LAB/console.log" &
QEMU_PID=$!
S=("ssh" "-i" "$LAB/key" "-p" "12022" "-o" "StrictHostKeyChecking=accept-new" "-o" "UserKnownHostsFile=$LAB/known_hosts" "-o" "ConnectTimeout=3" "-o" "BatchMode=yes" "infinity-ci@127.0.0.1")
P=("scp" "-i" "$LAB/key" "-P" "12022" "-o" "StrictHostKeyChecking=accept-new" "-o" "UserKnownHostsFile=$LAB/known_hosts" "-o" "ConnectTimeout=5")
SSH_READY=0
for i in $(seq 1 240); do
  if "${S[@]}" "test -r /proc/sys/kernel/random/boot_id && systemctl is-system-running >/dev/null 2>&1 || test -r /proc/sys/kernel/random/boot_id" >/dev/null 2>&1; then SSH_READY=1; break; fi
  kill -0 "$QEMU_PID" || { tail -n 100 "$LAB/console.log"; exit 2; }
  sleep 1
done
test "$SSH_READY" = 1 || { tail -n 100 "$LAB/console.log"; echo "VM boot/SSH not ready" >&2; exit 2; }
"${S[@]}" "sudo cloud-init status --wait >/dev/null 2>&1; systemctl --version | head -1; uname -r"
BOOT_BEFORE="$("${S[@]}" "cat /proc/sys/kernel/random/boot_id")"
[[ "$BOOT_BEFORE" =~ ^[a-f0-9-]{36}$ ]]

# This first kernel-reboot gate tests the real MCP Broker/Gateway and SQLite
# with a static synthetic bearer. OAuth/ZITADEL inside the SAME guest is still
# a separate blocker, not implied by this evidence.
ssh_guest() { "${S[@]}" "$@"; }
scp_guest() { "${P[@]}" "$@"; }
TOKEN="$(openssl rand -hex 32)"
ADMIN="$(openssl rand -hex 32)"
printf '%s\n' "$TOKEN" >"$LAB/token"
chmod 0600 "$LAB/token"
SCOPE="/opt/infinity-kernel-e2e-$GITHUB_RUN_ID"
STATE="/srv/portico-infinity/state"
CGO_ENABLED=0 go build -trimpath -o "$LAB/vps-agent-broker" ./cmd/vps-agent-broker
CGO_ENABLED=0 go build -trimpath -o "$LAB/vps-agent-gateway" ./cmd/vps-agent-gateway
CGO_ENABLED=0 go build -trimpath -o "$LAB/vps-agent" ./cmd/vps-agent
scp_guest "$LAB/vps-agent-broker" "$LAB/vps-agent-gateway" "$LAB/vps-agent" "infinity-ci@127.0.0.1:/tmp/"
cat >"$LAB/broker-policy.yaml" <<EOF
version: 1
mode: scoped
features:
  full_mode_enabled: false
filesystem:
  read: [$SCOPE]
  write: [$SCOPE]
  actions: [read, write, list, stat, hash]
network:
  mode: blocked
  destinations: []
shell:
  enabled: false
  cwd_roots: []
privilege:
  admin: broker-only
replay:
  require_idempotency_for_safe_writes: true
  blind_retry_non_replay_safe: false
grant:
  required: true
  max_ttl_minutes: 5
EOF
cat >"$LAB/broker.env" <<EOF
VPS_AGENT_POLICY=/srv/portico-infinity/policy.yaml
VPS_AGENT_BROKER_SOCKET=/run/portico-infinity/broker.sock
VPS_AGENT_STATE_DB=/srv/portico-infinity/state/state.db
VPS_AGENT_ADMIN_TOKEN=$ADMIN
VPS_AGENT_EXPECTED_SUBJECT=ci-reboot-operator
VPS_AGENT_INSTANCE_ID=ci-guest-kernel-reboot
VPS_AGENT_INSTANCE_NAME=ci-guest-kernel-reboot
VPS_AGENT_PHYSICAL_SCOPE_ROOT=$SCOPE
VPS_AGENT_WHOLE_HOST=0
VPS_AGENT_INFINITY_PROTECTED_ROOT=/srv/portico-infinity
EOF
cat >"$LAB/gateway.env" <<EOF
VPS_AGENT_LISTEN=0.0.0.0:8080
VPS_AGENT_BROKER_SOCKET=/run/portico-infinity/broker.sock
VPS_AGENT_AUTH_MODE=static
VPS_AGENT_STATIC_TOKEN=$TOKEN
VPS_AGENT_STATIC_SUBJECT=ci-reboot-operator
VPS_AGENT_INSTANCE_ID=ci-guest-kernel-reboot
VPS_AGENT_INSTANCE_NAME=ci-guest-kernel-reboot
EOF
chmod 0600 "$LAB/broker.env" "$LAB/gateway.env"
cat >"$LAB/portico-kernel-broker.service" <<'EOF'
[Unit]
Description=Disposable Pórtico Broker for real guest kernel reboot
After=network.target
[Service]
Type=exec
User=root
Group=portico-gateway
UMask=0007
RuntimeDirectory=portico-infinity
RuntimeDirectoryMode=0770
EnvironmentFile=/etc/portico-kernel-broker.env
ExecStart=/usr/local/libexec/portico-infinity/vps-agent-broker
Restart=always
RestartSec=2
NoNewPrivileges=true
[Install]
WantedBy=multi-user.target
EOF
cat >"$LAB/portico-kernel-gateway.service" <<'EOF'
[Unit]
Description=Disposable nonroot Pórtico Gateway for real guest kernel reboot
After=portico-kernel-broker.service
Requires=portico-kernel-broker.service
[Service]
Type=exec
User=portico-gateway
Group=portico-gateway
EnvironmentFile=/etc/portico-kernel-gateway.env
ExecStart=/usr/local/libexec/portico-infinity/vps-agent-gateway
Restart=always
RestartSec=2
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
CapabilityBoundingSet=
[Install]
WantedBy=multi-user.target
EOF
scp_guest "$LAB/broker-policy.yaml" "$LAB/broker.env" "$LAB/gateway.env" \
  "$LAB/portico-kernel-broker.service" "$LAB/portico-kernel-gateway.service" \
  "infinity-ci@127.0.0.1:/tmp/"
ssh_guest "sudo groupadd -g 65532 portico-gateway; sudo useradd -u 65532 -g 65532 -M -s /usr/sbin/nologin portico-gateway; sudo install -d -o root -g root -m 0700 $STATE; sudo install -d -o root -g root -m 0700 $SCOPE; sudo install -d -o root -g root -m 0755 /usr/local/libexec/portico-infinity; sudo install -o root -g root -m 0755 /tmp/vps-agent-broker /usr/local/libexec/portico-infinity/vps-agent-broker; sudo install -o root -g root -m 0755 /tmp/vps-agent-gateway /usr/local/libexec/portico-infinity/vps-agent-gateway; sudo install -o root -g root -m 0755 /tmp/vps-agent /usr/local/libexec/portico-infinity/vps-agent; sudo install -o root -g root -m 0600 /tmp/broker-policy.yaml /srv/portico-infinity/policy.yaml; sudo install -o root -g root -m 0600 /tmp/broker.env /etc/portico-kernel-broker.env; sudo install -o root -g root -m 0600 /tmp/gateway.env /etc/portico-kernel-gateway.env; sudo install -o root -g root -m 0644 /tmp/portico-kernel-broker.service /etc/systemd/system/portico-kernel-broker.service; sudo install -o root -g root -m 0644 /tmp/portico-kernel-gateway.service /etc/systemd/system/portico-kernel-gateway.service; sudo sh -c 'printf before-real-kernel-reset > $SCOPE/durable-before.txt'; sudo systemctl daemon-reload; sudo systemctl enable --now portico-kernel-broker.service portico-kernel-gateway.service"
ready=0
for i in $(seq 1 100); do
  if ssh_guest "systemctl is-active --quiet portico-kernel-broker && systemctl is-active --quiet portico-kernel-gateway && sudo test -S /run/portico-infinity/broker.sock" >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
if test "$ready" != 1; then
  ssh_guest "sudo systemctl --no-pager status portico-kernel-broker.service portico-kernel-gateway.service" || true
  echo "guest services did not become active before reboot" >&2
  exit 1
fi
python3 tests/integration/infinity-kernel-stack-probe.py --phase before \
  --endpoint http://127.0.0.1:18080/mcp --token-file "$LAB/token" --scope "$SCOPE"
ssh_guest "sudo /bin/sh -c '. /etc/portico-kernel-broker.env; export VPS_AGENT_ADMIN_TOKEN; exec /usr/local/libexec/portico-infinity/vps-agent audit-status --socket /run/portico-infinity/broker.sock'" > "$LAB/audit-before.json"
python3 - "$LAB/audit-before.json" <<'PY'
import json,sys
j=json.load(open(sys.argv[1]))
assert j['ok'] and j['result']['valid'] and j['result']['events']>=3,j
print("INFINITY_QEMU_BROKER_SQLITE_AUDIT_BEFORE_RESET: PASS")
PY
ssh_guest "sudo test -s $STATE/state.db; sudo sync"
printf '{"execute":"qmp_capabilities"}\n{"execute":"system_reset"}\n' | socat -T 3 - "UNIX-CONNECT:$LAB/qmp.sock" >/dev/null
BOOT_AFTER=""
for i in $(seq 1 280); do
  BOOT_AFTER="$(ssh_guest "cat /proc/sys/kernel/random/boot_id" 2>/dev/null || true)"
  if [[ "$BOOT_AFTER" =~ ^[a-f0-9-]{36}$ ]] && test "$BOOT_AFTER" != "$BOOT_BEFORE"; then break; fi
  kill -0 "$QEMU_PID" || { tail -n 80 "$LAB/console.log"; exit 2; }
  sleep 1
done
test "$BOOT_AFTER" != "$BOOT_BEFORE" && [[ "$BOOT_AFTER" =~ ^[a-f0-9-]{36}$ ]] || {
  echo "real guest kernel boot_id did not change" >&2; exit 2;
}
echo "INFINITY_QEMU_BROKER_GATEWAY_REAL_KERNEL_BOOT_ID_CHANGED: PASS"
ready=0
for i in $(seq 1 120); do
  if ssh_guest "systemctl is-active --quiet portico-kernel-broker && systemctl is-active --quiet portico-kernel-gateway && sudo test -S /run/portico-infinity/broker.sock" >/dev/null 2>&1; then ready=1; break; fi
  sleep 1
done
test "$ready" = 1 || { echo "Broker/Gateway failed to recover after kernel reboot" >&2; exit 1; }
python3 tests/integration/infinity-kernel-stack-probe.py --phase after \
  --endpoint http://127.0.0.1:18080/mcp --token-file "$LAB/token" --scope "$SCOPE"
ssh_guest "sudo /bin/sh -c '. /etc/portico-kernel-broker.env; export VPS_AGENT_ADMIN_TOKEN; exec /usr/local/libexec/portico-infinity/vps-agent audit-status --socket /run/portico-infinity/broker.sock'" > "$LAB/audit-after.json"
python3 - "$LAB/audit-before.json" "$LAB/audit-after.json" <<'PY'
import json,sys
before=json.load(open(sys.argv[1]))
after=json.load(open(sys.argv[2]))
assert before['ok'] and after['ok']
assert before['result']['valid'] and after['result']['valid']
assert after['result']['events']>before['result']['events'], (before,after)
print("INFINITY_QEMU_BROKER_SQLITE_AUDIT_DURABLE_AFTER_KERNEL_RESET: PASS")
PY
ssh_guest "sudo test -s $STATE/state.db; sudo test -f $SCOPE/durable-before.txt; sudo test -f $SCOPE/written-before.txt; sudo test -f $SCOPE/written-after.txt; test \$(systemctl is-enabled portico-kernel-broker) = enabled; test \$(systemctl is-enabled portico-kernel-gateway) = enabled; test \$(systemctl show portico-kernel-gateway -p User --value) = portico-gateway"
echo "INFINITY_REAL_GUEST_KERNEL_REBOOT_WITH_ACTIVE_MCP_BROKER_GATEWAY_SQLITE: PASS"
echo "LIMIT: this VM does NOT contain ZITADEL/OAuth; synthetic static bearer only."
