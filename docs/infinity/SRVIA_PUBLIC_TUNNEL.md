# Pórtico Infinity | Entrada pública via VPS e túnel para SRV-IA

**Situação em 16/10/2026:** plano aprovado conceitualmente; **NÃO instalado**. O SRV-IA permanece privado na rede doméstica. O E2E Docker e o E2E OAuth/ZITADEL foram aprovados **separadamente em runners descartáveis**; não houve conexão com a conta ChatGPT nem com o SRV-IA.

## Topologia escolhida

```text
ChatGPT remoto
    │  HTTPS + OAuth (domínio distinto do Community)
    ▼
infinity.josemirmoura.com.br         [DNS A proposto; NÃO configurado aqui]
    │
    ▼
VPS pública srv678294                 [Traefik existente, TLS, porta 443]
    │          serviços de entrada e controle de revogação NÃO delegáveis à IA
    │  WireGuard (iniciado PELO SRV-IA, usando keepalive)
    ▼
SRV-IA (host local, sem IP público fixo; sem port-forwarding doméstico)
    ├── Gateway MCP Infinity           [serviço HTTP acessível só pela VPN]
    ├── OAuth ZITADEL/Login/PostgreSQL  [identidade distinta do Community]
    ├── Broker protegido               [somente socket Unix, não na Internet]
    └── /srv/portico-infinity           [controller root-owned]
        /opt                            [área de trabalho delegada à IA]
```

### Valores propostos, pendentes de verificação de colisões

- DNS: `infinity.josemirmoura.com.br`, registro A → IP público da VPS atualmente observado `85.209.93.51`. **Não criar outro domínio pago**; apenas subdomínio independente de `mcp.josemirmoura.com.br`, que é o Community.
- WireGuard (nova interface independente, exemplos a validar antes do uso): `10.88.88.1/30` para VPS e `10.88.88.2/30` para SRV-IA, UDP `51822` na VPS. Não atribuir essas redes sem confirmar que estão livres.
- Somente o SRV-IA conhece o endpoint público da VPS e inicia o túnel. Atrás do NAT residencial, `PersistentKeepalive = 25` permite manter o mapeamento ativo. A configuração do WireGuard e as chaves ficam fora de `/opt`, root-owned.
- O serviço de entrada HTTP do SRV-IA escutará **somente no IP do túnel**. Não publicar portas do Gateway nem do provedor OAuth na Internet, na rede doméstica ou no Docker host `0.0.0.0`.
- O Traefik da VPS encaminhará rotas por **File Provider ou configuração equivalente**, porque labels Docker de contêineres no SRV-IA **não são descobertas** pelo Traefik da VPS. Verificar acesso TCP a partir **do contêiner Traefik**, não apenas do host VPS.
- O Tailscale já está ativo na VPS segundo inspeção somente leitura. Pode ser considerado reaproveitamento caso SRV-IA participe da mesma rede autorizada, mas a decisão do laboratório para SRV-IA/Nara foi **WireGuard próprio**. Nenhum novo VPN foi instalado por este plano.

## Rotas públicas por domínio

Todas utilizam o **mesmo hostname OAuth**, com TLS emitido na VPS. O Traefik decide pelo prefixo do caminho, preservando Host, URI, método e cabeçalhos relevantes:

| Caminho | Serviço no SRV-IA pela VPN | Responsabilidade |
| --- | --- | --- |
| `/mcp`, `/.well-known/oauth-protected-resource`, `/healthz` | Gateway MCP | MCP e descoberta de recurso |
| `/oauth/v2`, `/oidc/v1`, `/.well-known/openid-configuration`, `/.well-known/oauth-authorization-server`, `/v2` | ZITADEL API | Authorization, DCR, token, introspection e identidade |
| `/ui/v2/login` | ZITADEL Login | Login interativo |
| `/` (se exigido pelo provedor) | ZITADEL Login | Redirecionamento de tela inicial |

**Cuidado:** o overlay `compose.infinity-auth.yaml` atualmente presume Traefik descobrindo labels Docker localmente. Na topologia proposta o Traefik de borda está em OUTRO host. **É necessário implementar e testar um overlay sem edge local**, expondo apenas os backends por IP da VPN (sem exposição `0.0.0.0`) e publicar regras **File Provider na VPS**. Não rodar `setup-integrated-auth.sh` em produção até essa separação estar validada. Não copiar arquivos/tokens de `mcp.josemirmoura.com.br`.

## Etapas de execução, com critérios objetivos

1. **Verificação apenas leitura**: Docker/Traefik funcionando na VPS, interface WG existente, sub-redes e UDP livres, conectividade do SRV-IA, presença de WireGuard e restrições de firewall. Não coletar chaves privadas em chat ou no repositório.
2. **Configuração privada**: criar interface WireGuard em VPS e SRV-IA; usar chaves geradas **localmente em cada máquina**; só trocar chaves públicas; validar handshake e TCP só entre pares. Nem DNS nem Traefik são alterados nessa etapa.
3. **Backend SRV-IA**: instalar Infinity root-owned em `/srv/portico-infinity`; Gateway/Identity publicados **somente** em `10.88.88.2` com portas distintas; Broker continua privado. Só após snapshot e aceite do proprietário.
4. **Traefik VPS**: criar router/serviços separados e de nome exclusivo Infinity; HTTPS Let’s Encrypt já disponível, apontando ao túnel. Testar toda a cadeia de OAuth a partir de rede externa. Não modificar as rotas Community/Cloud.
5. **DNS**: criar registro A do subdomínio separado para a VPS pública, após confirmar controle da zona e disponibilidade.
6. **Prova real ChatGPT**: conectar `https://infinity.josemirmoura.com.br/mcp`; dono faz login OAuth; validar `system.info`, um arquivo experimental em `/opt`, hash de auditoria, token inválido e revogação.
7. **Reboot e corte externo**: simular queda e reconexão do túnel, expiração de sessão, reinício dos serviços, desligamento externo dos contêineres e revogação pela VPS (fora das permissões MCP). Confirmar que sem túnel/OAuth o ChatGPT não acessa o laboratório.

## Limites de segurança

- O privilégio persistente sobre `/opt` é operacional, não equivale a `sudo` confinado. O **root shell**, `apt`, `docker.sock` e edição do controlador devem continuar negados.
- O túnel não substitui autenticação OAuth. HTTPS termina na VPS, tráfego de backend via WireGuard criptografado; ZITADEL deve reconhecer o issuer público e cabeçalhos encaminhados.
- Scripts em `/opt` executados mais tarde como root são um risco indireto: revisar qualquer consumidor privilegiado desses arquivos.
- Túnel e controle de revogação devem estar na VPS/SRV-IA sob contas de administrador separadas, não dentro da área controlada pelo agente.
- Restringir tráfego WireGuard ao **peer único** e às portas internas necessárias, evitando converter o túnel em roteador de toda a rede doméstica.

**Não interpretar este documento como configuração aplicada.** Nenhum DNS, firewall, WireGuard, Traefik ou SRV-IA foi alterado.
