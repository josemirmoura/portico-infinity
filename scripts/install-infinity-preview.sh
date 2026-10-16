#!/usr/bin/env bash
# Portico Infinity: PREVIEW ONLY. No persistent root privilege is implemented.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
if [[ ! -f scripts/install.sh || ! -f scripts/delegate-root.py ]]; then
  echo 'ERRO: execute no checkout completo de Portico Infinity.' >&2
  exit 2
fi
if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  cat <<'USAGE'
PREVIEW experimental do Pórtico Infinity
Uso: bash scripts/install-infinity-preview.sh --ack-infinity [--local-only] [--lang pt-BR] [--run-as USUARIO]

Configura /opt como raiz estática read/write/shell no instalador herdado.
Não habilita root/sudo, Full, Docker irrestrito ou rede irrestrita.
É uma etapa de laboratório descartável; NÃO é a edição Infinity pronta.
USAGE
  exit 0
fi
if [[ -e .env || -e config/policy.yaml ]]; then
  echo 'ERRO: instalação existente detectada. Não modifica estado anterior.' >&2
  exit 3
fi
ACK=0
ARGS=()
for arg in "$@"; do
  case "$arg" in
    --ack-infinity) ACK=1 ;;
    --profile|--scope|--ack-whole-host|--dynamic-baseline|--create-scope)
      echo "ERRO: opção incompatível com preview Infinity: $arg" >&2; exit 2 ;;
    *) ARGS+=("$arg") ;;
  esac
done
printf '%s\n' '⚠️ PÓRTICO INFINITY - PREVIEW EXPERIMENTAL' \
  'Você autorizará permanentemente leitura, escrita e shell comum em /opt.' \
  'Uma IA poderá modificar ou excluir arquivos. O root permanente NÃO está pronto.' \
  'Não utilize esta versão experimental em máquina com dados valiosos.'
if [[ "$ACK" -ne 1 ]]; then
  if [[ ! -t 0 ]]; then
    echo 'ERRO: requer confirmação interativa ou --ack-infinity.' >&2; exit 4
  fi
  read -r -p 'Digite AUTORIZO /opt para prosseguir: ' answer
  [[ "$answer" == 'AUTORIZO /opt' ]] || exit 4
fi
bash scripts/install.sh --profile project --scope /opt "${ARGS[@]}"
python3 - <<'PY'
from pathlib import Path
import importlib.util,sys
sys.path.insert(0,str(Path('scripts/lib').resolve()))
spec=importlib.util.spec_from_file_location('delegate','scripts/delegate-root.py')
mod=importlib.util.module_from_spec(spec);spec.loader.exec_module(mod)
policy=mod.parse_lists(Path('config/policy.yaml').read_text())
required=(('filesystem','read'),('filesystem','write'),('shell','cwd_roots'))
assert all('/opt' in policy.get(x,[]) for x in required), 'raiz /opt não está autorizada estaticamente'
print('CHECK: /opt em read, write e shell; root permanente continua desativado.')
PY
