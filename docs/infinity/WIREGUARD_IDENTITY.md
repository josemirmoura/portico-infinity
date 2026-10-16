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

## Etapa 3: preparar a configuração dos dois peers (sem ativação)

**Status da sessão 16/10/2026:** chaves públicas geradas nas duas máquinas; SRV-IA informa Docker Server 29.9.0 e DNS resolvendo `infinity.josemirmoura.com.br` para a VPS. Na VPS, UFW ativo com entrada padrão bloqueada, sem regra UDP 51822; WireGuard instalado e chave VPS gerada. **Há kernel novo pendente na VPS: não reiniciar durante este procedimento.**

O script `scripts/infinity-wg-configure.py` lê a chave privada **somente do arquivo local** protegido, confere o par com `wg pubkey`, recusa symlinks, caminhos existentes, interface ativa ou colisão com a rede `10.88.88.0/30`, e cria `/etc/wireguard/wg-infinity.conf` com `0600` de maneira exclusiva. Não modifica UFW, não ativa rede nem inicia serviço.

**Na VPS** (somente depois de obter a chave pública do SRV-IA, já recebida pelo operador nesta sessão):

```bash
curl -fsSLo /tmp/infinity-wg-configure.py \
  https://raw.githubusercontent.com/josemirmoura/portico-infinity/main/scripts/infinity-wg-configure.py
python3 -m py_compile /tmp/infinity-wg-configure.py
sudo python3 /tmp/infinity-wg-configure.py --role vps \
  --peer-public-key '<CHAVE_PUBLICA_SRV_IA>'
sudo stat -c '%n %a %U:%G' /etc/wireguard/wg-infinity.conf
```

**No SRV-IA** (somente depois de obter a chave pública da VPS):

```bash
curl -fsSLo /tmp/infinity-wg-configure.py \
  https://raw.githubusercontent.com/josemirmoura/portico-infinity/main/scripts/infinity-wg-configure.py
python3 -m py_compile /tmp/infinity-wg-configure.py
sudo python3 /tmp/infinity-wg-configure.py --role srv-ia \
  --peer-public-key '<CHAVE_PUBLICA_VPS>' \
  --endpoint-ip <IP_PUBLICO_VPS>
sudo stat -c '%n %a %U:%G' /etc/wireguard/wg-infinity.conf
```

Substitua os parâmetros entre sinais de maior e menor pelos valores públicos já conhecidos apenas na sessão operacional; não publique identificadores do laboratório no repositório. Nenhuma chave privada foi colocada neste documento. A configuração `wg-infinity.conf` contém a chave privada local, portanto **não executar `cat` nesse arquivo nem enviá-lo ao chat**. `sudo wg-quick strip wg-infinity >/dev/null` valida o formato do arquivo sem revelar a chave (só depois de a configuração existir).

## Etapa 4: ativação controlada, depois de revisar o UFW

Antes de qualquer mudança, salvar a saída de `sudo ufw status verbose` da VPS e conferir também o UFW do SRV-IA. Na VPS, exclusivamente para WireGuard, a regra necessária é:

```bash
sudo ufw allow 51822/udp comment 'Portico Infinity WireGuard'
sudo ufw status numbered
```

Esse comando altera apenas UFW e deve ser realizado **após conferir o arquivo de configuração nos dois hosts** e assegurar que a hospedagem/provedor não bloqueia UDP. Não alterar portas 22, 80, 443, Tailscale ou regras atuais.

1. Na **VPS**: `sudo systemctl start wg-quick@wg-infinity` (sem `enable` inicial).
2. No **SRV-IA**: `sudo systemctl start wg-quick@wg-infinity` (sem `enable` inicial).
3. No SRV-IA: `sudo wg show wg-infinity latest-handshakes` e `sudo wg show wg-infinity transfer`; na VPS as mesmas consultas. Um handshake recente e tráfego autenticado nos dois sentidos confirmam a primeira comunicação. Se necessário, enviar um pacote de prova com `ping -c 2 -W 2 10.88.88.1` no SRV-IA; eventual ausência de ping não prova ausência de handshake, pois pode haver filtro ICMP.
4. Confirmar que a rota padrão e o link direto i7↔SRV-IA permanecem intactos: `ip -4 route` e `ip -4 route get 10.77.77.1`.

**Plano de reversão imediato** caso haja comportamento inesperado:

```bash
sudo systemctl stop wg-quick@wg-infinity
```

Na VPS, se necessário, também remover **exclusivamente** a regra UFW criada para o Infinity mediante inspeção de `sudo ufw status numbered`, sem limpar regras preexistentes.

Somente após handshake e teste de tráfego estáveis decidir se os dois serviços devem iniciar no boot com `systemctl enable wg-quick@wg-infinity`. A atualização de kernel pendente da VPS deve ser agendada separadamente, não como parte deste gate.

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
