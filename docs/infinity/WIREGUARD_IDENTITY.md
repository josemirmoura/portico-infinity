# Pórtico Infinity | Geração de identidade WireGuard, sem ativar túnel

**Situação:** DNS do Infinity confirmado no SRV-IA em 16/10/2026, resolução A para a VPS. No SRV-IA, WireGuard e Tailscale estão ausentes, Docker instalado mas o usuário comum não consulta o daemon. A VPS possui Tailscale ativo e a porta UDP 51822 não apareceu como ocupada na inspeção somente leitura. **Ainda não existe túnel.**

**Escolha arquitetural:** WireGuard próprio, com o SRV-IA iniciando a conexão para a VPS. A VPN será dedicada ao Infinity. A rede `10.88.88.0/30` e UDP `51822` são **propostas**, ainda dependentes de conferir rotas/firewall em ambos os hosts.

## Etapa 1: gerar apenas a identidade do SRV-IA

No terminal `josemir@srv-ia`:

```bash
sudo apt-get update
sudo apt-get install -y wireguard-tools

curl -fsSLo /tmp/infinity-wg-keygen.sh \
  https://raw.githubusercontent.com/josemirmoura/portico-infinity/main/scripts/infinity-wg-keygen.sh
bash -n /tmp/infinity-wg-keygen.sh
sudo bash /tmp/infinity-wg-keygen.sh srv-ia

# Confirmação de acesso administrativo ao Docker; não altera configurações:
sudo docker version --format 'Docker Server: {{.Server.Version}}'
```

O script imprime somente a chave pública. A chave privada fica em `/etc/wireguard/infinity-srv-ia.key`, root-only, modo `0600`; a pública em `/etc/wireguard/infinity-srv-ia.pub`. Se o par já existir, aborta sem substituir. **Não envia a chave privada ao ChatGPT, GitHub ou VPS.** É seguro encaminhar somente a chave pública ao operador que configurará o peer.

Não há ativação automática de rede, firewall ou rota e nenhuma mudança na interface direta `10.77.77.2` do i7.

## Etapa 2: gerar a identidade da VPS

Somente após conferir as permissões, o host correto, o estado do firewall e a ausência de configurações WireGuard prévias, um operador com acesso à VPS executa:

```bash
sudo apt-get update
sudo apt-get install -y wireguard-tools
curl -fsSLo /tmp/infinity-wg-keygen.sh \
  https://raw.githubusercontent.com/josemirmoura/portico-infinity/main/scripts/infinity-wg-keygen.sh
bash -n /tmp/infinity-wg-keygen.sh
sudo bash /tmp/infinity-wg-keygen.sh vps
```

A chave privada será `/etc/wireguard/infinity-vps.key`, root-only, distinta da chave do SRV-IA.

## Etapa 3: parear sem divulgar segredos

Trocar **apenas** as chaves públicas dos dois peers. Usar `wg-quick` e interface `wg-infinity` em ambos os hosts, com endereços ainda sujeitos a confirmação:

- VPS: `10.88.88.1/30`, `ListenPort = 51822`, `AllowedIPs` para SRV-IA `10.88.88.2/32`.
- SRV-IA: `10.88.88.2/30`, `Endpoint = <IP_PUBLICO_VPS>:51822`, `AllowedIPs = 10.88.88.1/32`, `PersistentKeepalive = 25`.
- Não configurar `0.0.0.0/0` como `AllowedIPs`: não desviar a internet doméstica ou a rota para o i7.
- Habilitar no firewall da VPS somente a entrada UDP 51822 (proposta), preservando Traefik, SSH, Tailscale e regras existentes. Não abrir porta na residência.
- Não incluir chaves privadas nem arquivos `.conf` reais em repositório ou logs de CI.

Só depois de testar handshake, latência, roteamento e acesso TCP **dentro do túnel** devem ser publicados Gateway/OAuth no IP VPN do SRV-IA e rotas Traefik dedicadas.

## Atenção

O script `infinity-wg-keygen.sh` **gera chaves e não configura serviço**, nem tenta instalar o Pórtico Infinity. Ele verifica proprietário/permissões do diretório antes de criar novos arquivos. Se houver chave existente, não a substitui automaticamente.

Referência: [Topologia e plano completo](SRVIA_PUBLIC_TUNNEL.md).
