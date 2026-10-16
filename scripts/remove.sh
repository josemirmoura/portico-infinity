#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck source=scripts/lib/product.sh
source scripts/lib/product.sh
vps_agent_init_language ""
vps_agent_banner

mode="safe"
remove_source=0
dry_run=0
# Confirmation tokens are supplied by the invoking process, never by .env.
# Capture these BEFORE .env is sourced so installation configuration cannot
# silently waive either of the independent destructive confirmations.
caller_purge_confirmation="${VPS_AGENT_PURGE_CONFIRM:-}"
caller_source_confirmation="${VPS_AGENT_REMOVE_SOURCE_CONFIRM:-}"
readonly caller_purge_confirmation caller_source_confirmation
for arg in "$@"; do
  case "$arg" in
    safe) mode="safe" ;;
    --purge) mode="--purge" ;;
    --remove-source) remove_source=1 ;;
    --dry-run) dry_run=1 ;;
    -h|--help)
      if vps_agent_is_pt_br; then
        cat <<'EOF'
uso: bash scripts/remove.sh [safe|--purge] [--remove-source] [--dry-run]

safe             Para o Portico MCP e gira credenciais locais, preservando
                 configuração, policy, auditoria e identidade integrada.
--purge          Remove runtime, volumes, estado/configuração local,
                 imagens locais padrão e estruturas legadas vazias.
--dry-run        Exibe os recursos identificados sem alterar nada.
--remove-source  Apenas com --purge: remove também este checkout Git após
                 uma segunda confirmação explícita.
EOF
      else
        cat <<'EOF'
usage: bash scripts/remove.sh [safe|--purge] [--remove-source] [--dry-run]

safe             Stop Portico MCP and rotate local credentials while preserving
                 configuration, policy, audit state and integrated identity.
--purge          Delete MCP-owned runtime, volumes, local state/configuration,
                 default local package images and empty legacy scaffolding.
--dry-run        Show discovered resources without modifying them.
--remove-source  With --purge only: also delete this Git checkout after a
                 separate explicit confirmation.
EOF
      fi
      exit 0
      ;;
    *)
      echo "$(vps_agent_text "usage: $0 [safe|--purge] [--remove-source] [--dry-run]" "uso: $0 [safe|--purge] [--remove-source] [--dry-run]")" >&2
      exit 2
      ;;
  esac
done

if [ "$remove_source" -eq 1 ] && [ "$mode" != "--purge" ]; then
  echo "$(vps_agent_text 'ERROR: --remove-source requires --purge.' 'ERRO: --remove-source requer --purge.')" >&2
  exit 2
fi

repo_root="$PWD"

# Infinity has an isolated Docker project and must NEVER use this
# Community-only lifecycle implementation. Fail closed before Docker,
# credential rotation, backup, git fetch or any destructive operation.
if [ "$(pwd -P)" = "/srv/portico-infinity" ] || { [ -f .env ] && grep -Fxq 'COMPOSE_PROJECT_NAME=portico-infinity' .env; }; then
  echo "INFINITY LIFECYCLE DENIED: Community updater/remover cannot manage Infinity. Use scripts/infinity-emergency-stop.py for an owner-only emergency stop." >&2
  exit 2
fi


# BEGIN REMOVE PROJECT OWNERSHIP GUARD
# The bundled removal script acts on a fixed Docker project and identity volumes.
# A copied checkout MUST NOT stop or delete another installation's resources.
if [ "${COMPOSE_PROJECT_NAME:-mcp-vps-agent}" != "mcp-vps-agent" ]; then
  echo "REMOVE BLOCKED: removal supports only the canonical mcp-vps-agent Compose project." >&2
  exit 1
fi
owned_project_ids="$(docker ps -aq --filter label=com.docker.compose.project=mcp-vps-agent)" || {
  echo "REMOVE BLOCKED: cannot inspect existing Docker project ownership." >&2
  exit 1
}
if [ -n "$owned_project_ids" ]; then
  canonical_checkout="$(pwd -P)"
  while IFS= read -r project_container_id; do
    [ -n "$project_container_id" ] || continue
    if [[ ! "$project_container_id" =~ ^[0-9a-f]{12,64}$ ]]; then
      echo "REMOVE BLOCKED: unexpected Docker project container identity." >&2
      exit 1
    fi
    owner_checkout="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' "$project_container_id")" || {
      echo "REMOVE BLOCKED: cannot verify Docker checkout ownership." >&2
      exit 1
    }
    owner_project="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' "$project_container_id")" || {
      echo "REMOVE BLOCKED: cannot verify Docker project label." >&2
      exit 1
    }
    if [ "$owner_checkout" != "$canonical_checkout" ] || [ "$owner_project" != "mcp-vps-agent" ]; then
      echo "REMOVE BLOCKED: Docker resources belong to another checkout or project." >&2
      exit 1
    fi
  done <<< "$owned_project_ids"
fi

# An empty container list cannot prove absence of installation resources.
# Purge also deletes fixed-name volumes, Compose networks and image tags.
# Without a container proving checkout ownership, refuse orphaned resources.
if [ -z "$owned_project_ids" ] && [ "$mode" = "--purge" ] && [ "$dry_run" -eq 0 ]; then
  orphan_volumes="$(docker volume ls --format '{{.Name}}')" || {
    echo "REMOVE BLOCKED: cannot inspect Docker volumes for orphan ownership." >&2
    exit 1
  }
  orphan_networks="$(docker network ls --format '{{.Name}}')" || {
    echo "REMOVE BLOCKED: cannot inspect Docker networks for orphan ownership." >&2
    exit 1
  }
  local_images="$(docker image ls --format '{{.Repository}}:{{.Tag}}')" || {
    echo "REMOVE BLOCKED: cannot inspect Docker images for orphan ownership." >&2
    exit 1
  }
  if grep -Eq '^mcp-vps-agent_' <<< "$orphan_volumes" ||
     grep -Eq '^mcp-vps-agent([_-]|$)' <<< "$orphan_networks" ||
     grep -Eq '^mcp-vps-agent-(gateway|broker):local$' <<< "$local_images"; then
    echo "REMOVE BLOCKED: Pórtico Docker resources remain without an owning container; manual ownership verification required before purge." >&2
    exit 1
  fi
fi
# END REMOVE PROJECT OWNERSHIP GUARD

remove_legacy_sandbox() {
  [ -d /opt/vps-agent-sandbox ] || return 0
  if rmdir /opt/vps-agent-sandbox >/dev/null 2>&1; then
    return 0
  fi
  command -v sudo >/dev/null 2>&1 || return 0
  if [ -t 0 ]; then
    sudo rmdir /opt/vps-agent-sandbox >/dev/null 2>&1 || true
  else
    sudo -n rmdir /opt/vps-agent-sandbox >/dev/null 2>&1 || true
  fi
}

source_verified=0
if [ "$remove_source" -eq 1 ]; then
  git_top="$(git rev-parse --show-toplevel 2>/dev/null || true)"
  if [ "$git_top" != "$repo_root" ] || ! grep -q '^module github.com/josemirmoura/mcp-vps-agent-gateway$' go.mod 2>/dev/null; then
    echo "$(vps_agent_text 'ERROR: refusing source deletion because this directory cannot be verified as a Portico MCP checkout.' 'ERRO: remoção do código recusada porque este diretório não pôde ser verificado como checkout do Portico MCP.')" >&2
    exit 1
  fi
  source_verified=1
fi

# BEGIN REMOVAL PREVIEW
# Read-only inventory; no container, state, volume or credential mutation.
if [ "$dry_run" -eq 1 ]; then
  echo "$(vps_agent_text 'REMOVAL PREVIEW (no changes made)' 'PRÉVIA DA REMOÇÃO (nenhuma alteração executada)')"
  echo "$(vps_agent_text 'Project containers:' 'Contêineres do projeto:')"
  docker ps -a --filter label=com.docker.compose.project=mcp-vps-agent --format '{{.Names}} {{.Status}}'
  echo "$(vps_agent_text 'Project volumes:' 'Volumes do projeto:')"
  docker volume ls --filter label=com.docker.compose.project=mcp-vps-agent --format '{{.Name}}'
  echo "$(vps_agent_text 'Canonical package volume names (even if foreign-owned):' 'Volumes com nomes do pacote (inclusive se pertencem a terceiros):')"
  preview_all_volumes="$(docker volume ls --format '{{.Name}}')" || {
    echo "PREVIEW BLOCKED: Docker volume inventory unavailable." >&2
    exit 1
  }
  for volume in \
    mcp-vps-agent_broker-run \
    mcp-vps-agent_operator-run \
    mcp-vps-agent_zitadel-postgres-data \
    mcp-vps-agent_zitadel-bootstrap \
    mcp-vps-agent_vps-agent-letsencrypt
  do
    if grep -Fxq -- "$volume" <<< "$preview_all_volumes"; then
      preview_owner="$(docker volume inspect --format '{{ index .Labels "com.docker.compose.project" }}' "$volume")" || {
        echo "PREVIEW BLOCKED: volume ownership unavailable: $volume" >&2
        exit 1
      }
      printf '  %s [owner=%s]\n' "$volume" "$preview_owner"
      if [ "$preview_owner" != "mcp-vps-agent" ]; then
        printf '  !! FOREIGN/UNVERIFIED: purge will refuse this volume.\n'
      fi
    fi
  done
  echo "$(vps_agent_text 'Project networks:' 'Redes do projeto:')"
  docker network ls --filter label=com.docker.compose.project=mcp-vps-agent --format '{{.Name}}'
  echo "$(vps_agent_text 'Default locally tagged package images:' 'Imagens locais padrão do pacote:')"
  docker image ls --filter reference=mcp-vps-agent-gateway:local --filter reference=mcp-vps-agent-broker:local --format '{{.Repository}}:{{.Tag}}'
  echo "$(vps_agent_text 'Local state identified:' 'Estado local identificado:')"
  for target in .env config/policy.yaml state backups; do
    if [ -e "$target" ] || [ -L "$target" ]; then
      printf '  %s\n' "$target"
    fi
  done
  # Show mountpoints within state/backups *before* the operator authorizes a
  # purge. These paths may contain unrelated bind-mounted data. A preview is
  # read-only, and if inventory fails it must not claim completeness.
  echo "$(vps_agent_text 'Mountpoints at or beneath purge targets:' 'Montagens dentro ou abaixo dos alvos de remoção:')"
  preview_mounts="$(findmnt --kernel --raw --noheadings --output TARGET)" || {
    echo "PREVIEW BLOCKED: cannot inventory active mountpoints." >&2
    exit 1
  }
  preview_checkout="$(pwd -P)" || {
    echo "PREVIEW BLOCKED: cannot resolve checkout directory." >&2
    exit 1
  }
  preview_mount_blocked=0
  while IFS= read -r mounted_path; do
    [ -n "$mounted_path" ] || continue
    for target in state backups; do
      case "$mounted_path" in
        "$preview_checkout/$target"|"$preview_checkout/$target/"*)
          printf '  !! BLOCKED / FOREIGN-REVIEW: %s [purge target: %s]\n' "$mounted_path" "$target"
          preview_mount_blocked=1
          ;;
      esac
    done
  done <<< "$preview_mounts"
  if [ "$preview_mount_blocked" -eq 0 ]; then
    echo "$(vps_agent_text 'No mountpoints found under state/ or backups/.' 'Nenhuma montagem encontrada dentro de state/ ou backups/.')"
  fi
  echo "$(vps_agent_text 'Traefik, external networks and third-party projects will be preserved.' 'Traefik, redes externas e projetos de terceiros serão preservados.')"
  if [ "$preview_mount_blocked" -eq 1 ]; then
    echo "PREVIEW BLOCKED: foreign or nested mountpoints require operator review before purge." >&2
    exit 1
  fi
  echo "$(vps_agent_text 'Preview complete. No changes made.' 'Prévia finalizada. Sem alterações.')"
  exit 0
fi
# END REMOVAL PREVIEW

# Verify destructive purge targets before loading shell-derived configuration.
# This gate must run before .env is sourced, or a guard failure could occur
# after untrusted shell substitutions have already executed.
# BEGIN PRE-MUTATION PURGE SAFETY GATE
# All project-volume ownership and local data guards run before credential
# rotation, container stops, Compose down or any rm -rf.
if [ "$mode" = "--purge" ]; then
  expected_volumes="$(docker volume ls --format '{{.Name}}')" || {
    echo "REMOVE BLOCKED: cannot preflight Docker volume inventory." >&2
    exit 1
  }
  for volume in \
    mcp-vps-agent_broker-run \
    mcp-vps-agent_operator-run \
    mcp-vps-agent_zitadel-postgres-data \
    mcp-vps-agent_zitadel-bootstrap \
    mcp-vps-agent_vps-agent-letsencrypt
  do
    if grep -Fxq -- "$volume" <<< "$expected_volumes"; then
      owner="$(docker volume inspect --format '{{ index .Labels "com.docker.compose.project" }}' "$volume")" || {
        echo "REMOVE BLOCKED: cannot inspect volume owner before any mutation: $volume" >&2
        exit 1
      }
      if [ "$owner" != "mcp-vps-agent" ]; then
        echo "REMOVE BLOCKED: foreign or unlabeled project-named volume BEFORE any mutation: $volume" >&2
        exit 1
      fi
    fi
  done
  checkout_uid="$(stat -c '%u' -- .)" || exit 1
  mounted_paths="$(findmnt --kernel --raw --noheadings --output TARGET)" || {
    echo "REMOVE BLOCKED: cannot inventory active mounts before destructive purge." >&2
    exit 1
  }
  for target in state backups; do
    if [ -L "$target" ]; then
      echo "REMOVE BLOCKED: local purge target is a symlink: $target" >&2
      exit 1
    fi
    if [ -e "$target" ]; then
      if [ ! -d "$target" ]; then
        echo "REMOVE BLOCKED: local purge target is not a directory: $target" >&2
        exit 1
      fi
      target_uid="$(stat -c '%u' -- "$target")" || exit 1
      if [ "$target_uid" != "$checkout_uid" ] && [ "$target_uid" != "0" ]; then
        echo "REMOVE BLOCKED: local purge target has foreign UID: $target" >&2
        exit 1
      fi
      while IFS= read -r mounted_path; do
        case "$mounted_path" in
          "$(pwd -P)/$target"|"$(pwd -P)/$target/"*)
            echo "REMOVE BLOCKED: mountpoint at or beneath purge target: $target" >&2
            exit 1
            ;;
        esac
      done <<< "$mounted_paths"
    fi
  done
fi
# END PRE-MUTATION PURGE SAFETY GATE

# Parse installation .env as data, never execute it as shell.
# Tabular output stays private to this process; errors disclose line numbers,
# never values or credentials.
remove_load_literal_env() {
  local entries key value
  [ -e .env ] || { [ ! -L .env ] || { echo "REMOVE BLOCKED: .env is a symlink." >&2; return 1; }; return 0; }
  entries="$(python3 - .env <<'PY'
import os
import pathlib
import re
import stat
import sys

p = pathlib.Path(sys.argv[1])
allowed = {
    "COMPOSE_PROJECT_NAME", "VPS_AGENT_WHOLE_HOST", "VPS_AGENT_AUTH_MODE",
    "VPS_AGENT_BUNDLED_PROXY", "VPS_AGENT_ADMIN_TOKEN", "VPS_AGENT_STATIC_TOKEN",
    "VPS_AGENT_PUBLIC_URL", "VPS_AGENT_EDGE_NETWORK",
}
values = {}
fd = -1
try:
    # Pin the file descriptor with O_NOFOLLOW so a concurrent symlink swap
    # cannot redirect a privileged parser to a different file.
    fd = os.open(p, os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW | os.O_NONBLOCK)
    info = os.fstat(fd)
    if not stat.S_ISREG(info.st_mode):
        sys.exit("REMOVE BLOCKED: .env must be a regular non-symlink file.")
    if info.st_size > 256 * 1024:
        sys.exit("REMOVE BLOCKED: .env exceeds maximum safe size.")
    with os.fdopen(fd, "r", encoding="utf-8") as stream:
        fd = -1
        lines = stream.read(256 * 1024 + 1).splitlines()
    if sum(len(line.encode("utf-8")) + 1 for line in lines) > 256 * 1024:
        sys.exit("REMOVE BLOCKED: .env exceeds maximum safe size.")
except (OSError, UnicodeError):
    sys.exit("REMOVE BLOCKED: .env must be a regular non-symlink file or valid UTF-8.")
finally:
    if fd >= 0:
        os.close(fd)
for n, line in enumerate(lines, 1):
    if not line.strip() or line.lstrip().startswith("#"):
        continue
    match = re.fullmatch(r"\s*(?:export\s+)?([A-Za-z_][A-Za-z_0-9]*)\s*=\s*(.*)", line)
    if not match:
        sys.exit(f"REMOVE BLOCKED: invalid .env assignment at line {n}.")
    key, raw = match.groups()
    raw = raw.strip()
    if raw.startswith(("'", '"')):
        quote = raw[0]
        close = raw.find(quote, 1)
        if close < 0 or (raw[close + 1:].strip() and not raw[close + 1:].strip().startswith("#")):
            sys.exit(f"REMOVE BLOCKED: invalid .env quote at line {n}.")
        value = raw[1:close]
    else:
        value = re.split(r"\s+#", raw, maxsplit=1)[0].strip()
        if any(ch.isspace() for ch in value):
            sys.exit(f"REMOVE BLOCKED: unquoted whitespace in .env at line {n}.")
    if re.search(r"[$\x60;\|<>]", value) or re.search(r"[\x00-\x1f\x7f]", value):
        sys.exit(f"REMOVE BLOCKED: shell syntax forbidden in .env at line {n}.")
    if key in allowed:
        values[key] = value
for key, value in values.items():
    print(f"{key}\t{value}")
PY
)" || return 1
  while IFS=$'\t' read -r key value; do
    [ -n "$key" ] || continue
    case "$key" in
      COMPOSE_PROJECT_NAME|VPS_AGENT_WHOLE_HOST|VPS_AGENT_AUTH_MODE|VPS_AGENT_BUNDLED_PROXY|VPS_AGENT_ADMIN_TOKEN|VPS_AGENT_STATIC_TOKEN|VPS_AGENT_PUBLIC_URL|VPS_AGENT_EDGE_NETWORK)
        printf -v "$key" '%s' "$value"
        export "$key" ;;
      *) echo "REMOVE BLOCKED: unknown parsed .env variable." >&2; return 1 ;;
    esac
  done <<< "$entries"
}
remove_load_literal_env || exit 1

# The checkout .env may define COMPOSE_PROJECT_NAME: check AGAIN after parsing.
# Never allow its values to redirect `docker compose down` to another project.
if [ "${COMPOSE_PROJECT_NAME:-mcp-vps-agent}" != "mcp-vps-agent" ]; then
  echo "REMOVE BLOCKED: .env selects a noncanonical Compose project." >&2
  exit 1
fi


if [ "$mode" = "--purge" ] && [ "$caller_purge_confirmation" != "PURGE" ]; then
  if [ ! -t 0 ]; then
    echo "$(vps_agent_text 'Full purge requires VPS_AGENT_PURGE_CONFIRM=PURGE in non-interactive mode.' 'O purge completo requer VPS_AGENT_PURGE_CONFIRM=PURGE em modo não interativo.')" >&2
    exit 1
  fi
  printf '%s' "$(vps_agent_text 'Type PURGE to delete this Portico MCP installation configuration/state: ' 'Digite PURGE para apagar a configuração/estado desta instalação do Portico MCP: ')"
  read -r answer
  if [ "$answer" != "PURGE" ]; then
    echo "$(vps_agent_text 'Purge cancelled.' 'Purge cancelado.')"
    exit 1
  fi
fi

if [ "$remove_source" -eq 1 ] && [ "$caller_source_confirmation" != "REMOVE_SOURCE" ]; then
  if [ ! -t 0 ]; then
    echo "$(vps_agent_text 'Source deletion requires VPS_AGENT_REMOVE_SOURCE_CONFIRM=REMOVE_SOURCE in non-interactive mode.' 'A remoção do código requer VPS_AGENT_REMOVE_SOURCE_CONFIRM=REMOVE_SOURCE em modo não interativo.')" >&2
    exit 1
  fi
  printf '%s' "$(vps_agent_text 'Type REMOVE_SOURCE to also delete this Git checkout: ' 'Digite REMOVE_SOURCE para apagar também este checkout Git: ')"
  read -r source_answer
  if [ "$source_answer" != "REMOVE_SOURCE" ]; then
    echo "$(vps_agent_text 'Source deletion cancelled.' 'Remoção do código cancelada.')"
    exit 1
  fi
fi

if vps_agent_is_pt_br; then
  cat <<EOF
Modo de remoção: $mode
Runtime: Gateway + Broker + proxy do pacote (quando configurado)
Recursos gerenciados da VPS: PRESERVADOS
Policy/auditoria do operador: $([ "$mode" = "safe" ] && echo PRESERVADAS || echo APAGADAS)
Checkout do código: $([ "$remove_source" -eq 1 ] && echo APAGAR || echo PRESERVAR)
EOF
else
  cat <<EOF
Removal mode: $mode
Runtime: Gateway + Broker + package proxy (when configured)
Managed VPS resources: PRESERVED
Operator policy/audit: $([ "$mode" = "safe" ] && echo PRESERVED || echo DELETED)
Source checkout: $([ "$remove_source" -eq 1 ] && echo DELETE || echo PRESERVED)
EOF
fi



project_ids="$(docker ps -aq --filter label=com.docker.compose.project=mcp-vps-agent)" || {
  echo "REMOVE BLOCKED: cannot re-inventory project containers before mutation." >&2
  exit 1
}
# Safe mode preserves the grant database. With no running project containers,
# the Broker cannot attest revocation; a configuration or persisted state
# may be reactivated by a later installation. STOP before any mutation.
if [ "$mode" = "safe" ] && [ -z "$project_ids" ] && { [ -f .env ] || [ -e state ] || [ -L state ]; }; then
  echo "REMOVE BLOCKED: Broker unavailable; cannot confirm temporary grant revocation while configuration or state persists. Restore Broker for supervised revoke-all or inspect state with operator." >&2
  exit 1
fi
if [ "$mode" = "safe" ] && [ -n "$project_ids" ] && [ ! -f .env ]; then
  echo "REMOVE BLOCKED: running project containers without .env; cannot confirm temporary grant revocation in safe mode." >&2
  exit 1
fi
if [ ! -f .env ] && [ -z "$project_ids" ]; then
  if [ "$mode" = "--purge" ]; then
    rm -rf -- state backups
    rm -f -- config/policy.yaml
    docker image rm mcp-vps-agent-gateway:local mcp-vps-agent-broker:local >/dev/null 2>&1 || true
    remove_legacy_sandbox
  fi
  echo "$(vps_agent_text 'Runtime already absent; removal is idempotently complete.' 'O runtime já está ausente; a remoção idempotente está concluída.')"
  if [ "$remove_source" -eq 1 ] && [ "$source_verified" -eq 1 ]; then
    parent="$(dirname "$repo_root")"
    cd /
    rm -rf -- "$repo_root"
    if [ "$(basename "$parent")" = "vps-agent-lab" ]; then
      rmdir "$parent" >/dev/null 2>&1 || true
    fi
    echo "$(vps_agent_text 'Portico MCP source checkout removed.' 'Checkout do Portico MCP removido.')"
  fi
  exit 0
fi

compose=(docker compose --profile operator-portal -f compose.yaml)
if [ "${VPS_AGENT_WHOLE_HOST:-0}" = "1" ]; then
  compose+=(-f compose.host.yaml)
fi
if [ "${VPS_AGENT_AUTH_MODE:-}" = "integrated" ] && [ -f compose.integrated-auth.yaml ]; then
  compose+=(-f compose.integrated-auth.yaml)
  if [ "${VPS_AGENT_BUNDLED_PROXY:-0}" = "1" ] && [ -f compose.integrated-auth.proxy.yaml ]; then
    compose+=(-f compose.integrated-auth.proxy.yaml)
  fi
fi

# A safe removal preserves the Broker grant database, so silent revoke
# failure could resurrect temporary permissions on restart. Refuse to claim
# success unless revocation completed. Purge erases grants with Broker state.
if [ -f .env ] && [ -n "$project_ids" ]; then
  echo "$(vps_agent_text 'Revoking temporary grants before shutdown...' 'Revogando autorizações temporárias antes do desligamento...')"
  if ! docker compose exec -T broker /usr/local/bin/vps-agent revoke-all --socket /run/vps-agent/broker.sock >/dev/null 2>&1; then
    if [ "$mode" = "safe" ]; then
      echo "REMOVE BLOCKED: temporary grant revocation was not confirmed; safe removal cannot preserve potentially live grants." >&2
      exit 1
    fi
    echo "REMOVE NOTICE: revoke-all not confirmed; full purge must complete and destroy Broker state before removal can be accepted." >&2
  fi
fi

if [ -f .env ]; then
  old_admin="${VPS_AGENT_ADMIN_TOKEN:-}"
  old_static="${VPS_AGENT_STATIC_TOKEN:-}"
  new_admin="$(openssl rand -hex 32)"
  new_static="$(openssl rand -hex 32)"
  python3 - .env "$old_admin" "$new_admin" "$old_static" "$new_static" <<'PY'
import pathlib, sys
p=pathlib.Path(sys.argv[1])
text=p.read_text()
old_admin,new_admin,old_static,new_static=sys.argv[2:]
if old_admin: text=text.replace(old_admin,new_admin)
if old_static: text=text.replace(old_static,new_static)
p.write_text(text)
PY
  chmod 600 .env
  echo "$(vps_agent_text 'Local admin/static credentials rotated.' 'Credenciais locais admin/static foram rotacionadas.')"
fi

# BEGIN PROFILE-AWARE REMOVAL
# The operator-portal profile is explicitly enabled in the Compose down
# command. If Compose misses a project container, revalidate both labels
# before removing that individual container (never a global prune).
echo "$(vps_agent_text 'Stopping Portico MCP containers...' 'Parando os contêineres do Portico MCP...')"
down_failed=0
if [ -f .env ]; then
  "${compose[@]}" down --remove-orphans || down_failed=1
else
  down_failed=1
fi

left="$(docker ps -aq --filter label=com.docker.compose.project=mcp-vps-agent)"
if [ -n "$left" ]; then
  echo "$(vps_agent_text 'Cleaning up remaining verified Portico profile containers...' 'Removendo contêineres restantes dos perfis do Portico após verificar propriedade...')"
  while IFS= read -r id; do
    [ -n "$id" ] || continue
    [[ "$id" =~ ^[0-9a-f]{12,64}$ ]] || { echo "REMOVE BLOCKED: unexpected container ID." >&2; exit 1; }
    owner="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' "$id")"
    project="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project" }}' "$id")"
    if [ "$owner" != "$(pwd -P)" ] || [ "$project" != "mcp-vps-agent" ]; then
      echo "REMOVE BLOCKED: refusing to remove a container from another checkout." >&2
      exit 1
    fi
    docker rm -f -- "$id" >/dev/null
  done <<< "$left"
  if [ -f .env ]; then
    "${compose[@]}" down --remove-orphans
    down_failed=0
  fi
elif [ "$down_failed" -eq 1 ] && [ -f .env ]; then
  "${compose[@]}" down --remove-orphans
  down_failed=0
fi

left="$(docker ps -aq --filter label=com.docker.compose.project=mcp-vps-agent)"
if [ -n "$left" ]; then
  echo "$(vps_agent_text "Removal incomplete: package containers still exist: $left" "Remoção incompleta: ainda existem contêineres do pacote: $left")" >&2
  exit 1
fi
# END PROFILE-AWARE REMOVAL
if [ -n "${VPS_AGENT_PUBLIC_URL:-}" ]; then
  code="$(curl --silent --show-error --max-time 5 --output /dev/null --write-out '%{http_code}' "${VPS_AGENT_PUBLIC_URL}" 2>/dev/null || true)"
  case "$code" in
    000|"")
      echo "$(vps_agent_text 'Public MCP endpoint is no longer reachable from this host.' 'O endpoint MCP público não está mais acessível a partir deste host.')"
      ;;
    404|410)
      echo "$(vps_agent_text "Public MCP route is gone (HTTP $code from the remaining edge proxy)." "A rota MCP pública foi removida (HTTP $code no proxy de borda remanescente).")"
      ;;
    *)
      echo "Removal incomplete: the MCP URL still returned HTTP $code instead of disappearing." >&2
      echo "$(vps_agent_text 'Check for a stale proxy route before considering removal complete.' 'Verifique uma possível rota antiga no proxy antes de considerar a remoção concluída.')" >&2
      exit 1
      ;;
  esac
fi

if [ "$mode" = "safe" ]; then
  if vps_agent_is_pt_br; then
    cat <<'EOF'

REMOÇÃO SEGURA CONCLUÍDA
Removidos:
  runtime Gateway/Broker/proxy do pacote
  autorizações temporárias ativas
Invalidadas:
  credenciais bearer locais admin/static anteriores
Preservados:
  .env (com credenciais locais rotacionadas)
  config/policy.yaml
  state/ auditoria e histórico de operações
  estado da identidade integrada
  todos os recursos da VPS que o Portico podia gerenciar
EOF
  else
    cat <<'EOF'

SAFE REMOVE COMPLETE
Removed:
  Gateway/Broker/package proxy runtime
  active temporary grants
Invalidated:
  previous local admin/static bearer credentials
Preserved:
  .env (with rotated local credentials)
  config/policy.yaml
  state/ audit and operation history
  integrated identity state on safe remove
  every VPS resource Portico MCP was allowed to manage
EOF
  fi
  exit 0
fi

# BEGIN VERIFIED PURGE RESOURCES
# Compose down -v can delete project-named volumes before a later ownership
# check. Validate every named removal target BEFORE invoking down -v. A
# foreign-owned or unlabeled volume is never a safe purge target.
volume_inventory="$(docker volume ls --format '{{.Name}}')" || {
  echo "REMOVE BLOCKED: cannot inventory Docker volumes before Compose purge." >&2
  exit 1
}
for volume in \
  mcp-vps-agent_broker-run \
  mcp-vps-agent_operator-run \
  mcp-vps-agent_zitadel-postgres-data \
  mcp-vps-agent_zitadel-bootstrap \
  mcp-vps-agent_vps-agent-letsencrypt
do
  if grep -Fxq -- "$volume" <<< "$volume_inventory"; then
    owner="$(docker volume inspect --format '{{ index .Labels "com.docker.compose.project" }}' "$volume")" || {
      echo "REMOVE BLOCKED: cannot inspect owner of volume before Compose purge: $volume" >&2
      exit 1
    }
    if [ "$owner" != "mcp-vps-agent" ]; then
      echo "REMOVE BLOCKED: volume not verified as Portico-owned before Compose purge: $volume" >&2
      exit 1
    fi
  fi
done

# Never ignore Compose failures or discard state needed to recover a purge.
if [ -f .env ]; then
  "${compose[@]}" down -v --remove-orphans
fi

# A previous interrupted run can strand volumes, including operator-run.
# Remove only fixed package volume names with the expected Compose owner.
for volume in \
  mcp-vps-agent_broker-run \
  mcp-vps-agent_operator-run \
  mcp-vps-agent_zitadel-postgres-data \
  mcp-vps-agent_zitadel-bootstrap \
  mcp-vps-agent_vps-agent-letsencrypt
do
  if docker volume inspect "$volume" >/dev/null 2>&1; then
    owner="$(docker volume inspect --format '{{ index .Labels "com.docker.compose.project" }}' "$volume")"
    if [ "$owner" != "mcp-vps-agent" ]; then
      echo "REMOVE BLOCKED: volume is not verified as Portico-owned: $volume" >&2
      exit 1
    fi
    docker volume rm "$volume" >/dev/null || {
      echo "REMOVE INCOMPLETE: failed to remove Portico volume: $volume" >&2
      exit 1
    }
  fi
done

# Only Compose-labeled private networks: the public Traefik edge is external
# and must not be removed, including when other services share it.
networks="$(docker network ls --filter label=com.docker.compose.project=mcp-vps-agent --format '{{.Name}}')"
while IFS= read -r network; do
  [ -n "$network" ] || continue
  if [ "$network" = "${VPS_AGENT_EDGE_NETWORK:-}" ] || [ "$network" = "traefik-public" ]; then
    continue
  fi
  owner="$(docker network inspect --format '{{ index .Labels "com.docker.compose.project" }}' "$network")"
  if [ "$owner" != "mcp-vps-agent" ]; then
    echo "REMOVE BLOCKED: unexpected network owner: $network" >&2
    exit 1
  fi
  docker network rm "$network" >/dev/null || {
    echo "REMOVE INCOMPLETE: failed to remove Portico network: $network" >&2
    exit 1
  }
done <<< "$networks"
# Final fence: a purge may not report success when an unrecognized
# Compose-labeled volume remains. Investigate manually instead of pruning.
remaining_volumes="$(docker volume ls --filter label=com.docker.compose.project=mcp-vps-agent --format '{{.Name}}')"
if [ -n "$remaining_volumes" ]; then
  echo "REMOVE INCOMPLETE: project volumes remain: $remaining_volumes" >&2
  exit 1
fi
remaining_networks="$(docker network ls --filter label=com.docker.compose.project=mcp-vps-agent --format '{{.Name}}')"
while IFS= read -r network; do
  [ -n "$network" ] || continue
  if [ "$network" != "${VPS_AGENT_EDGE_NETWORK:-}" ] && [ "$network" != "traefik-public" ]; then
    echo "REMOVE INCOMPLETE: project network remains: $network" >&2
    exit 1
  fi
done <<< "$remaining_networks"

# END VERIFIED PURGE RESOURCES

rm -rf -- state backups
rm -f -- .env config/policy.yaml

# These are package-owned default local build outputs. Never remove arbitrary
# images configured by the operator or images merely used by managed workloads.
docker image rm   mcp-vps-agent-gateway:local   mcp-vps-agent-broker:local   >/dev/null 2>&1 || true

# Legacy pre-Portico scaffold. The helper uses rmdir, never rm -rf:
# a non-empty directory is preserved rather than risking user data.
remove_legacy_sandbox

if vps_agent_is_pt_br; then
  cat <<EOF

PURGE COMPLETO CONCLUÍDO
Artefatos locais do Portico removidos:
  runtime/volumes
  imagens locais padrão do Gateway/Broker (quando presentes)
  .env
  config/policy.yaml
  state/
  backups/
  /opt/vps-agent-sandbox legado vazio (quando presente)
Preservados:
  aplicações, sites, bancos, imagens/contêineres de terceiros, serviços e arquivos delegados
EOF
else
  cat <<EOF

FULL PURGE COMPLETE
Deleted MCP-owned local artifacts:
  runtime/volumes
  default local Gateway/Broker images (when present)
  .env
  config/policy.yaml
  state/
  backups/
  empty legacy /opt/vps-agent-sandbox (when present)
Preserved:
  applications, sites, databases, third-party images/containers, services and delegated files
EOF
fi

if [ "$remove_source" -eq 1 ] && [ "$source_verified" -eq 1 ]; then
  parent="$(dirname "$repo_root")"
  echo "$(vps_agent_text "Deleting verified Portico MCP source checkout: $repo_root" "Apagando o checkout verificado do Portico MCP: $repo_root")"
  cd /
  rm -rf -- "$repo_root"
  if [ "$(basename "$parent")" = "vps-agent-lab" ]; then
    rmdir "$parent" >/dev/null 2>&1 || true
  fi
  echo "$(vps_agent_text 'Portico MCP source checkout removed.' 'Checkout do Portico MCP removido.')"
fi
