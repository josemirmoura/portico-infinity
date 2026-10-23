#!/usr/bin/env bash
# GitHub-hosted ephemeral QEMU/KVM guest kernel reset and real Pórtico systemd worker.
# Independent proof of guest /proc/.../boot_id change, not just a container restart.
set -euo pipefail
test "${CI:-}" = true && test "${RUNNER_ENVIRONMENT:-}" = github-hosted
test "${GITHUB_EVENT_NAME:-}" = pull_request
[[ "${GITHUB_RUN_ID:-}" =~ ^[0-9]{1,24}$ ]]
test "$(uname -m)" = x86_64
test -c /dev/kvm && test -w /dev/kvm || { echo "KVM unavailable; real guest reboot NOT VERIFIED" >&2; exit 2; }
LAB="$(mktemp -d "${RUNNER_TEMP:?}/portico-qemu-reboot.XXXXXXXX")"
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
for candidate in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd; do
  if test -r "$candidate"; then OVMF="$candidate"; break; fi
done
test -n "$OVMF" || { echo "UEFI firmware not available" >&2; exit 2; }
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
printf "instance-id: infinity-reboot-%s\nlocal-hostname: infinity-reboot-vm\n" "$GITHUB_RUN_ID" >"$LAB/meta-data"
cloud-localds "$LAB/seed.iso" "$LAB/user-data" "$LAB/meta-data"
test ! -e "$LAB/qmp.sock"
! ss -ltn | grep -q ":12022 " || { echo "VM SSH port already bound" >&2; exit 2; }
qemu-system-x86_64 -accel kvm -machine q35 -m 2048 -smp 2 -nographic -monitor none \
  -bios "$OVMF" \
  -drive "file=$LAB/ubuntu-24.04-minimal-cloudimg-amd64.img,if=virtio,format=qcow2" \
  -drive "file=$LAB/seed.iso,if=virtio,media=cdrom,format=raw" \
  -netdev "user,id=vmnet,hostfwd=tcp:127.0.0.1:12022-:22" \
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
CGO_ENABLED=0 go build -trimpath -o "$LAB/portico-filecopy-worker" ./cmd/portico-filecopy-worker
"${P[@]}" "$LAB/portico-filecopy-worker" "infinity-ci@127.0.0.1:/tmp/portico-filecopy-worker"
JOB_HEX="$(printf "qemu-reboot-%s" "$GITHUB_RUN_ID" | sha256sum | cut -c 1-32)"
JOB="vps-agent-filecopy-$JOB_HEX"
SCOPE="/opt/infinity-reboot-e2e-$GITHUB_RUN_ID"
STATE="/srv/portico-infinity/state/filecopy-jobs"
cat >"$LAB/manifest.json" <<EOF
{"job_id":"$JOB","subject":"qemu-synthetic-disposable","source":"$SCOPE/source.gguf","destination":"$SCOPE/output.gguf","scope_root":"$SCOPE","max_bytes":1073741824}
EOF
"${P[@]}" "$LAB/manifest.json" "infinity-ci@127.0.0.1:/tmp/manifest.json"
"${S[@]}" "sudo install -d -o root -g root -m 0700 $STATE; sudo install -d -o root -g root -m 0700 $SCOPE; sudo install -d -o root -g root -m 0755 /usr/local/libexec/portico-infinity; sudo install -o root -g root -m 0755 /tmp/portico-filecopy-worker /usr/local/libexec/portico-infinity/portico-filecopy-worker; sudo install -o root -g root -m 0600 /tmp/manifest.json $STATE/$JOB.request.json; sudo truncate -s 512M $SCOPE/source.gguf; sudo chmod 0600 $SCOPE/source.gguf"
"${S[@]}" "sudo systemd-run --unit=$JOB --no-block --quiet --property=Type=oneshot --property=RemainAfterExit=yes --property=NoNewPrivileges=yes --property=UMask=0077 --property=CPUQuota=3% --property=ProtectSystem=strict --property=ReadWritePaths=\"$SCOPE $STATE\" --property=PrivateDevices=yes --property=PrivateNetwork=yes -- /usr/local/libexec/portico-infinity/portico-filecopy-worker $JOB"
RUNNING=0
for i in $(seq 1 180); do
  if "${S[@]}" "sudo grep -q '\"phase\":\"running\"' $STATE/$JOB.progress.json" >/dev/null 2>&1; then
    RUNNING=1
    break
  fi
  if "${S[@]}" "sudo grep -Eq '\"phase\":\"(done|failed|cancelled|reconcile_required)\"' $STATE/$JOB.progress.json" >/dev/null 2>&1; then
    echo "copy became terminal before forced reset" >&2; exit 1
  fi
  sleep 0.2
done
test "$RUNNING" = 1 || { echo "no running checkpoint in guest" >&2; exit 1; }
"${S[@]}" "sudo systemctl kill --kill-whom=main --signal=STOP $JOB.service; sudo sync; sudo test ! -e $SCOPE/output.gguf; sudo grep -q '\"phase\":\"running\"' $STATE/$JOB.progress.json"
echo "INFINITY_QEMU_HOST_WORKER_RUNNING_BEFORE_RESET: PASS"
printf '{"execute":"qmp_capabilities"}\n{"execute":"system_reset"}\n' | socat -T 3 - "UNIX-CONNECT:$LAB/qmp.sock" >/dev/null
BOOT_AFTER=""
for i in $(seq 1 280); do
  BOOT_AFTER="$("${S[@]}" "cat /proc/sys/kernel/random/boot_id" 2>/dev/null || true)"
  if [[ "$BOOT_AFTER" =~ ^[a-f0-9-]{36}$ ]] && test "$BOOT_AFTER" != "$BOOT_BEFORE"; then break; fi
  kill -0 "$QEMU_PID" || { tail -n 100 "$LAB/console.log"; exit 2; }
  sleep 1
done
test "$BOOT_AFTER" != "$BOOT_BEFORE" && [[ "$BOOT_AFTER" =~ ^[a-f0-9-]{36}$ ]] || { echo "actual VM kernel reboot not confirmed" >&2; exit 2; }
echo "INFINITY_QEMU_GUEST_KERNEL_BOOT_ID_CHANGED: PASS"
"${S[@]}" "sudo test -f $STATE/$JOB.request.json; sudo test -f $STATE/$JOB.progress.json; sudo test ! -e $SCOPE/output.gguf"
PHASE="$("${S[@]}" "sudo python3 -c 'import json; print(json.load(open(\"$STATE/$JOB.progress.json\"))[\"phase\"])'")"
test "$PHASE" = running || { echo "unreconciled interrupted checkpoint phase=$PHASE" >&2; exit 1; }
LOAD="$("${S[@]}" "systemctl show $JOB.service -p LoadState --value")"
test "$LOAD" = not-found || { echo "transient unit survived guest kernel reset: $LOAD" >&2; exit 1; }
if "${S[@]}" "sudo /usr/local/libexec/portico-infinity/portico-filecopy-worker $JOB" >/dev/null 2>&1; then
  echo "worker replay was incorrectly allowed after hard reboot" >&2; exit 1
fi
"${S[@]}" "sudo test ! -e $SCOPE/output.gguf"
echo "INFINITY_QEMU_PERSISTENT_MANIFEST_PROGRESS_NO_REPLAY_NO_PUBLICATION: PASS"
echo "INFINITY_REAL_GUEST_KERNEL_RESET_WITH_SYSTEMD_WORKER: PASS"
