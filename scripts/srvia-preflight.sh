#!/usr/bin/env bash
# Read-only network and host readiness inspection for SRV-IA.
# Do not sudo; do not print private keys, credentials, .env or personal files.
set -u
export LC_ALL=C

DOMAIN="${1:-infinity.josemirmoura.com.br}"
case "$DOMAIN" in
  infinity.josemirmoura.com.br) ;;
  *) printf 'ERRO: dominio nao reconhecido, permitido: infinity.josemirmoura.com.br\n' >&2; exit 2 ;;
esac

say() { printf '\n### %s\n' "$1"; }
run() {
  local cmd="$1"; shift
  if ! command -v "$cmd" >/dev/null 2>&1; then
    printf 'NAO_INSTALADO: %s\n' "$cmd"
    return 0
  fi
  "$cmd" "$@" 2>/dev/null || printf 'NAO_DISPONIVEL: %s (pode exigir permissoes adicionais)\n' "$cmd"
}

say "PÓRTICO INFINITY | PRÉ-VERIFICAÇÃO SRV-IA (SOMENTE LEITURA)"
printf 'Data local: '; date -Is
printf 'Host: '; hostname
printf 'Sistema: '; uname -srm
printf 'Usuário: '; id -un
printf 'UID: '; id -u
if command -v cat >/dev/null 2>&1 && [ -r /etc/os-release ]; then
  . /etc/os-release
  printf 'Distribuição: %s\n' "${PRETTY_NAME:-nao identificada}"
fi

say "DNS público do novo domínio"
if command -v getent >/dev/null 2>&1; then
  # Show only the deduplicated A values, without querying unrelated services.
  addresses="$(getent ahostsv4 "$DOMAIN" 2>/dev/null | awk '$1 ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/ {print $1}' | sort -u || true)"
  if [ -n "$addresses" ]; then
    printf '%s\n' "$addresses"
  else
    printf 'SEM RESPOSTA A no resolvedor desta máquina; conferir propagação/DNS.\n'
  fi
else
  printf 'getent indisponível\n'
fi

say "Interfaces IPv4 e rotas (sem chaves)"
run ip -brief -4 address show
run ip -4 route show
printf 'Rota ao futuro peer VPN (apenas consulta):\n'
run ip -4 route get 10.88.88.1

say "WireGuard e Tailscale (inventário sem credenciais)"
if command -v wg >/dev/null 2>&1; then
  printf 'WireGuard: instalado; interfaces visíveis: '
  wg show interfaces 2>/dev/null || printf 'consulta nao autorizada\n'
else
  printf 'WireGuard: NAO INSTALADO\n'
fi
if command -v tailscale >/dev/null 2>&1; then
  printf 'Tailscale: instalado; estado: '
  tailscale status --json 2>/dev/null | python3 -c '
import json,sys
try:
    d=json.load(sys.stdin)
    print(d.get("BackendState","desconhecido"))
except Exception:
    print("indisponivel")
' 2>/dev/null || printf 'consulta indisponivel\n'
else
  printf 'Tailscale: nao detectado\n'
fi

say "Docker e Compose (sem alterar contêineres)"
if command -v docker >/dev/null 2>&1; then
  docker version --format 'CLI={{.Client.Version}} Servidor={{.Server.Version}}' 2>/dev/null || printf 'Sem acesso ao daemon Docker\n'
  docker compose version 2>/dev/null || printf 'Compose nao disponivel\n'
  docker ps --format '{{.Names}} | {{.Status}}' 2>/dev/null || printf 'Listagem de contêineres nao autorizada\n'
else
  printf 'Docker nao instalado\n'
fi

say "Portas TCP/UDP atualmente escutadas"
run ss -H -lntu

say "Colisões e identidade do controlador"
for p in /srv/portico-infinity /opt/portico-infinity; do
  if [ -L "$p" ]; then
    printf '%s: EXISTE COMO LINK SIMBOLICO, REVISAR\n' "$p"
  elif [ -e "$p" ]; then
    printf '%s: JA EXISTE, NAO SOBRESCREVER\n' "$p"
  else
    printf '%s: livre\n' "$p"
  fi
done
if command -v getent >/dev/null 2>&1; then
  if getent passwd portico-infinity-exec >/dev/null; then
    printf 'Conta portico-infinity-exec: JA EXISTE, inspecionar antes de reutilizar\n'
  else
    printf 'Conta portico-infinity-exec: ainda nao criada\n'
  fi
fi

say "RESULTADO"
printf 'Pré-verificação concluída. Nenhum arquivo, serviço, firewall, rota ou DNS foi alterado.\n'
printf 'NÃO executar comandos de instalação até confirmar a topologia da VPN e o isolamento do controlador.\n'
