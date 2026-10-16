# Pórtico Infinity | SRV-IA por WireGuard e Traefik externo

**Status 16/10/2026:** o túnel VPS ↔ SRV-IA foi comprovado pelo usuário nos dois sentidos e ativado para boot nas duas máquinas. Traefik VPS `v2.11` usa apenas Docker Provider e está na `traefik-public`. A ponte dedicada passou no teste descartável de Gateway + API h2c + Login. **A instalação OAuth/MCP real no SRV-IA ainda NÃO foi executada.**

## Arquitetura de publicação sem modificar Traefik principal

```text
ChatGPT
  -> https://infinity.josemirmoura.com.br
  -> Traefik VPS existente [Docker provider; certResolver letsencrypt]
  -> container bridge separado: portico-infinity-edge [arquivo interno, sem docker.sock]
  -> VPN WireGuard [VPS 10.88.88.1; SRV-IA 10.88.88.2]
  -> Gateway 10.88.88.2:18081
  -> ZITADEL API h2c 10.88.88.2:18082
  -> ZITADEL Login 10.88.88.2:18083
```

O Docker Provider no Traefik principal já existe. Portanto NÃO editar `/opt/traefik/data/traefik.yml` nem reiniciar o proxy que atende os sites existentes. Não tocar em `/opt/traefik/data/acme.json`.

A ponte usa o projeto Compose `portico-infinity-edge`, separado do Community; ver [compose e readme da ponte](../../deploy/infinity-vps-edge/README.md). No lado SRV-IA, `compose.infinity-auth.remote.yaml` contém apenas redes locais e bindings aos IPs VPN; não utiliza `traefik-public`.

## Ordem de execução futura

**Não executar ainda como lote único:** estas fases precisam ser validadas uma a uma, com rollback e captura de resultados antes de avançar.

1. **Gate de inventário SRV-IA**: confirmar livre o conjunto TCP 18081/18082/18083, a interface `wg-infinity` com IP `10.88.88.2` e a presença de Docker 29.9.0/Compose. Garantir snapshot dos dados relevantes.
2. **Gate do proxy de borda VPS**: copiar só dois arquivos de `deploy/infinity-vps-edge` para `/opt/portico-infinity-edge` root-owned; renderizar Compose com `INFINITY_PUBLIC_DOMAIN=infinity.josemirmoura.com.br`; verificar que não há ports de host, não há mount do Docker socket e só o router `portico-infinity-public` corresponde ao domínio. A ativação só após checar roteadores conflitantes. Não iniciar proxy global, não interromper o Community.
3. **Gate controlador SRV-IA**: instalar checkout root-owned em `/srv/portico-infinity`, conferir `scripts/check-infinity-layout.py` e usuário executor `portico-infinity-exec` distinto. Não instalar código privilegiado em `/opt`.
4. **Gate instalação SRV-IA**: novo instalador usa explicitamente `--remote-edge --wg-bind-address 10.88.88.2`, com hostname público, login próprio ZITADEL e confirmação `AUTORIZO INFINITY /opt`. O parâmetro é validado contra IP na interface `wg-infinity`. O Gateway inicia somente ligado à VPN; API e Login só usam a VPN. O bootstrap OAuth usa o domínio público roteado pela VPS.
5. **Gate ChatGPT real**: conectar endpoint MCP, autenticar via OAuth e testar `system.info`, operações tipadas descartáveis dentro de `/opt`, negação de acessos fora do teto e trilha de auditoria, com revogação externa.
6. **Gate recuperação**: verificar desligamento seguro e restabelecimento com interferência limitada somente à ponte/Infinity, sem reboot da VPS agora (kernel pendente).

## Interface do instalador (não iniciar sem gate anterior)

```bash
cd /srv/portico-infinity
sudo bash scripts/install-infinity.sh \
  --run-as portico-infinity-exec \
  --lang pt-BR \
  --domain infinity.josemirmoura.com.br \
  --remote-edge \
  --wg-bind-address 10.88.88.2
```

O instalador solicita consentimento do escopo `/opt` e o login OAuth separado. O usuário NÃO deve enviar senha do operador, chave WireGuard ou .env no chat. Ao instalar, manter Docker sem conceder grupo docker ao usuário executor.

**Limite de evidência:** testes Docker da ponte e OAuth integrado descartáveis **não constituem ainda** E2E do instalador completo com VPS+SRV-IA em produção. Ainda há riscos indiretos de consumo de arquivos em `/opt` por root. O plano protege as rotas, mas não substitui revisão dos serviços que executam scripts privilegiados.

## Rollback

- Ponte na VPS: `docker compose -f /opt/portico-infinity-edge/compose.yaml down` apenas no projeto `portico-infinity-edge` e com a variável de domínio configurada.
- SRV-IA: interromper só Gateway/Broker Infinity pelo procedimento de emergência, não remover containers/volumes de Community; não desativar WireGuard antes de inspecionar a conexão.
- Não editar roteadores de outros domínios, não apagar volumes/dados do host.

Referências: [túnel e DNS](SRVIA_PUBLIC_TUNNEL.md), [WireGuard](WIREGUARD_IDENTITY.md), [aceite](ACCEPTANCE.md).
