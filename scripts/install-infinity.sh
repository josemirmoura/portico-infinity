#!/usr/bin/env bash
# Pórtico Infinity opt-privileged entrypoint, NOT global sudo.
set -euo pipefail
cd "$(dirname "$0")/.."

usage() {
  cat <<'EOF'
PÓRTICO INFINITY: perfil experimental de /opt

Uso: bash scripts/install-infinity.sh [opções]

  --local-only               Prévia local, SEM subir contêineres ou realizar E2E
  --lang pt-BR|en            Idioma de instalação
  --run-as USUARIO           Identidade NÃO-root para o shell
  --domain HOST              Domínio público MCP/OAuth
  --operator-email EMAIL     E-mail do operador
  --operator-username NOME   Nome de login
  --remote-edge              Traefik público externo via túnel WireGuard
  --wg-bind-address IPv4     IP local da interface WireGuard (obrigatório com --remote-edge)
  --yes --ack-infinity-opt   Aprovação explícita sem prompt

Sem --yes, confirmar digitando: AUTORIZO INFINITY /opt
Acesso de arquivos privilegiados tipados (chmod/chown) e shell não-root.
Não libera sudo/root shell, apt, Full ou Docker host.
Não instalar fora de um laboratório descartável até o aceite E2E.
EOF
}

ARGS=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --local-only|--yes|--ack-infinity-opt|--remote-edge)
      ARGS+=("$1"); shift ;;
    --lang|--run-as|--domain|--operator-email|--operator-username|--wg-bind-address)
      if [ "$#" -lt 2 ] || [ -z "$2" ] || [[ "$2" == --* ]]; then
        echo "ERRO: $1 exige valor." >&2; exit 2
      fi
      ARGS+=("$1" "$2"); shift 2 ;;
    *)
      echo "ERRO: argumento não permitido: $1" >&2
      usage >&2
      exit 2 ;;
  esac
done

if [ -e .env ] || [ -L .env ] || [ -e config/policy.yaml ] || [ -L config/policy.yaml ]; then
  echo "ERRO: instalação existente; por segurança não alterar estado." >&2
  exit 3
fi

exec bash scripts/install.sh --profile project --scope /opt --infinity-opt "${ARGS[@]}"
