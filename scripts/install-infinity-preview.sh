#!/usr/bin/env bash
# Permanently retired: this older preview could start a privileged Broker
# without the protected /srv/portico-infinity controller checkout contract.
set -euo pipefail

case "${1:-}" in
  --help|-h)
    cat <<'EOF'
Instalador de prévia legado DESATIVADO por segurança.
Use scripts/install-infinity.sh, que exige controle em /srv/portico-infinity.

The legacy Infinity preview is disabled for safety.
Use scripts/install-infinity.sh with the protected /srv controller layout.
EOF
    exit 0 ;;
esac

cat >&2 <<'EOF'
ERRO: o instalador legado install-infinity-preview.sh foi desativado.
Ele não cumpre a separação segura /srv (controle) e /opt (trabalho).
Consulte docs/infinity/FIRST_E2E.md e use scripts/install-infinity.sh.

ERROR: legacy preview disabled; use the protected Infinity installer.
EOF
exit 3
