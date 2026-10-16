# Pórtico Infinity ♾️

**Edição pública experimental do Pórtico Community para administração persistente de um Linux por uma IA autenticada via MCP.**

> ⚠️ **PRÉ-LANÇAMENTO.** O perfil Infinity de operações privilegiadas confinadas a `/opt` ainda precisa de validação E2E; o root global irrestrito será opcional e ficará desativado por padrão. Este repositório preserva o código histórico do Community, mas não comprova uma instalação Infinity pronta para uso. Não execute em um servidor de produção sem validação E2E independente.

[Português (Brasil)](README.pt-BR.md) · [Community original](https://github.com/josemirmoura/mcp-vps-agent-gateway)

## Target

- Operator-owned, opt-in, **non-expiring** authorization for read/write and ordinary work inside `/opt`.
- **Default design:** persistent, path-checked privileged filesystem operations inside `/opt` via the Broker. No generic root shell. Existing operations such as `file.chmod` and `file.chown` are available in Community but need an explicitly reviewed Infinity policy and E2E acceptance.
- HTTPS, OAuth/OIDC, local Broker enforcement, audit trail, out-of-band emergency shutdown and revocation.
- Ubuntu/Linux as the first target; optional outbound secure tunnel for machines without fixed public IP.
- Distinct deployments, state, service identities and configuration from Pórtico Community and Cloud.

**Optional future advanced mode:** persistent, unrestricted root shell behind a separate explicit operator decision, disabled by default. There is no secure generic `sudo` shell restricted only to `/opt`. Persistent server authorization does not disable OAuth token expiry, ChatGPT/tool confirmations, execution timeouts or chat session limits.

## Documents

- [Architecture and threat boundaries](docs/infinity/ARCHITECTURE.md)
- [Persistent administrative authority specification](docs/infinity/ROOT_PERSISTENTE.md)
- [Acceptance gates](docs/infinity/ACCEPTANCE.md)
- [Disposable E2E and protected installation checklist (PT-BR)](docs/infinity/FIRST_E2E.md)
- [Owner-only emergency stop runbook (PT-BR)](docs/infinity/EMERGENCY_STOP.md)
- [Import provenance and remaining references](docs/infinity/IMPORT.md)

## Installation layout

The protected control-plane checkout lives in **`/srv/portico-infinity`**, owned by root and inaccessible for writes by the AI shell account. AI projects reside inside **`/opt`**. This separation is a defense-in-depth decision, pending full E2E verification.

## Disposable real OAuth evidence

The independent Infinity OAuth E2E with real ZITADEL, PostgreSQL, HTTPS, issued OAuth token, Gateway and `/opt`-scoped Broker **passed** ([GitHub Actions](https://github.com/josemirmoura/portico-infinity/actions/runs/38893160988)). Infinity uses `compose.infinity-auth.yaml` for dedicated Traefik labels and bootstrap volumes. The public ChatGPT/SRV-IA OAuth login is **not yet tested**.

## Implementation status

Community baseline copied from `josemirmoura/mcp-vps-agent-gateway`, commit `42a1bbe29660edf61e737fc882e7c126ebc10de3`. The **existing Community runtime remains intact** in the copy. Its current `scripts/install.sh` still implements Community behavior, not the completed Infinity feature.

The experimental entrypoint is now **`scripts/install-infinity.sh`**. It adds an opt-in `/opt` privileged-file policy, requires an isolated root-owned control plane in `/srv/portico-infinity`, and still **does not grant sudo/root shell or apt**. `--local-only` is a configuration preview that never starts privileged Docker. The previous `scripts/install-infinity-preview.sh` is permanently disabled because it did not enforce the `/srv` boundary. **No Infinity E2E acceptance has been completed.**

## License and origin

Derived from [Pórtico MCP Community](https://github.com/josemirmoura/mcp-vps-agent-gateway), by Josemir Moura, under Apache-2.0. Source LICENSE and attribution remain preserved. Git history is retained in `main`. Public repo access is not permission to access an operator's machine: the operator must authenticate and consent locally.

## Inherited Community installation contract (not yet Infinity certification)

The inherited setup must present HTTPS/OAuth, connect a compatible **ChatGPT Web** MCP client, obtain a real authenticated `system.info` call, and verify the matching Broker audit record **before** displaying `INSTALLATION COMPLETE`. Portico Infinity adds further root-specific gates; this legacy contract alone does not approve Infinity installation.

Localized inherited documentation (PT-BR):

- [Quick Start / Início rápido](docs/quick-start.pt-BR.md)
- [First installation acceptance / Aceite de primeira instalação](docs/first-install-acceptance.pt-BR.md)
- [Installer flow / Fluxo do instalador](docs/installer-flow.pt-BR.md)
- [Installation contract / Contrato de instalação](docs/installation-contract.pt-BR.md)
- [ChatGPT integration / Integração com ChatGPT](docs/chatgpt-integration.pt-BR.md)
