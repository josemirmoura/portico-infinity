#!/usr/bin/env bash
# Pórtico Infinity WireGuard identity bootstrap.
# Run separately AS ROOT on SRV-IA or VPS. Generates ONLY local keys and prints
# the PUBLIC key; does not configure a tunnel, networking, DNS or firewall.
set -euo pipefail
umask 077

usage() {
  cat <<'EOF'
Uso: sudo bash scripts/infinity-wg-keygen.sh srv-ia
  ou sudo bash scripts/infinity-wg-keygen.sh vps

Gera duas identidades WireGuard distintas, uma por máquina, em:
  /etc/wireguard/infinity-srv-ia.key / .pub
  /etc/wireguard/infinity-vps.key / .pub

Nunca imprime chaves privadas. Não cria interface, túnel nem regras de firewall.
Recusa sobrescrever chaves existentes.
EOF
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  usage
  exit 0
fi

if [[ "$#" -ne 1 ]]; then
  usage >&2
  exit 2
fi
case "$1" in
  srv-ia) label=infinity-srv-ia ;;
  vps) label=infinity-vps ;;
  *) echo "ERRO: identidade inválida. Use srv-ia ou vps." >&2; exit 2 ;;
esac

if (( EUID != 0 )); then
  echo "ERRO: execute com sudo/root. Nenhuma chave gerada." >&2
  exit 2
fi
if ! command -v wg >/dev/null 2>&1; then
  echo "ERRO: WireGuard ausente. Instale wireguard-tools primeiro." >&2
  exit 2
fi

directory=/etc/wireguard
private="$directory/$label.key"
public="$directory/$label.pub"
if [[ -L "$directory" ]]; then
  echo "ERRO: /etc/wireguard é um symlink, revisar antes de prosseguir." >&2
  exit 2
fi
if [[ ! -e "$directory" ]]; then
  install -d -m 0700 -o 0 -g 0 -- "$directory"
fi
if [[ ! -d "$directory" || "$(stat -c '%u' -- "$directory")" != 0 ]]; then
  echo "ERRO: /etc/wireguard não é um diretório de root." >&2
  exit 2
fi
if [[ -e "$private" || -L "$private" || -e "$public" || -L "$public" ]]; then
  echo "ERRO: identidade já existe, chaves NÃO serão substituídas." >&2
  echo "Para conferir somente a chave pública: sudo cat $public" >&2
  exit 2
fi

temp_dir="$(mktemp -d "$directory/.infinity-keygen.XXXXXXXX")"
cleanup() { rm -f -- "$temp_dir/key" "$temp_dir/pub"; rmdir -- "$temp_dir" 2>/dev/null || true; }
trap cleanup EXIT

wg genkey > "$temp_dir/key"
wg pubkey < "$temp_dir/key" > "$temp_dir/pub"
[[ -s "$temp_dir/key" && -s "$temp_dir/pub" ]] || {
  echo "ERRO: não foi possível gerar par de chaves." >&2
  exit 2
}

# The root-owned directory is not writable by the delegated AI shell user.
install -m 0600 -o 0 -g 0 -- "$temp_dir/key" "$private"
install -m 0644 -o 0 -g 0 -- "$temp_dir/pub" "$public"
echo "OK: identidade WireGuard $label gerada neste host."
echo "CHAVE PUBLICA (compartilhável):"
cat -- "$public"
echo "CHAVE PRIVADA: guardada apenas em $private, modo 0600. NÃO compartilhe."
echo "Rede, túnel e firewall NÃO foram alterados."
