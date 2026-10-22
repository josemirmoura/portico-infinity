# Gates de aceite: Pórtico Infinity

## Atualização verificável em 22/10/2026 (pós-PR #31)

A tabela de 16/10 abaixo é histórica. Os novos ensaios ocorreram exclusivamente em runners descartáveis do GitHub; não autorizam atualização do SRV-IA nem publicação.

| Gate incremental | Evidência | Resultado |
| --- | --- | --- |
| Ciclo de vida do runtime | [PR #27](https://github.com/josemirmoura/portico-infinity/pull/27), [Docker CI](https://github.com/josemirmoura/portico-infinity/actions/runs/39824785485) | 20/20 checks; desativação reversível, remoção exclusiva de contêineres/redes, dados e /opt preservados |
| Cópia >2 GiB | [PR #28](https://github.com/josemirmoura/portico-infinity/pull/28), [CI](https://github.com/josemirmoura/portico-infinity/actions/runs/39825831727) | 21/21 checks; fonte sintética 2.147.487.744 bytes, SHA-256, progresso, cancelamento e replay negado |
| Revogação externa da identidade | [PR #29](https://github.com/josemirmoura/portico-infinity/pull/29), [ZITADEL real](https://github.com/josemirmoura/portico-infinity/actions/runs/39828400590) | 16/16 checks; token ativo antes e inativo após desativação administrativa de conta de serviço sintética |
| Worker executado via systemd | [PR #30](https://github.com/josemirmoura/portico-infinity/pull/30), [CI](https://github.com/josemirmoura/portico-infinity/actions/runs/39835994798) | 20/20 checks; cópia, hash, manifesto inseguro negado, cancelamento SIGTERM sem publicação |
| MCP até worker real | [PR #31](https://github.com/josemirmoura/portico-infinity/pull/31), [Docker E2E](https://github.com/josemirmoura/portico-infinity/actions/runs/39837703649) | 17/17 checks; token estático de laboratório, Gateway → Broker → systemd → cópia de 128 MiB, auditoria e negações |

**Bloqueadores reais da v1.0:**

1. Revogação de sessões humanas/refresh tokens e efeitos em jobs ativos; a revogação de conta de serviço sintética cobre apenas parte do requisito.
2. Reboot, restauração e reconciliação de trabalhos incertos no laboratório e, sob autorização específica, no SRV-IA.
3. Desinstalação integral isolada, incluindo identidade, volumes, plano de controle e dependências externas, com backup verificável, inventário e confirmação independente. A rotina de remoção de runtime é deliberadamente parcial.
4. Segurança de efeitos indiretos em serviços root consumidores de arquivos em /opt, testes de escala extrema e integração OAuth + cópia longa num único cenário.
5. Release candidate vinculada ao SHA, UAT pessoal do proprietário e decisão expressa GO/NO-GO. Nenhuma tag/release estável antes do GO.

**Operação real:** o MCP do SRV-IA respondeu em 22/10, mas estas PRs não foram implantadas nele. Não foi autorizada execução de deploy, reboot, revogação ou desinstalação.

---

> **Atualização SRV-IA 16/10/2026:** conexão MCP e operações tipadas de arquivo dentro de `/opt` funcionaram; bloqueios fora do teto e de traversal confirmados. **`shell.exec` falhou antes da execução** (`sensitive-path scan exceeded safety limit`). E2E SRV-IA **parcial, ainda pendente**, sem comprovação de reboot/revogação. Evidências em [Relato SRV-IA](RELATO-SRV-IA-2026-10-16.md) e [Issue #4](https://github.com/josemirmoura/portico-infinity/issues/4).

**Status (16/10/2026):** E2E Docker real e OAuth real em laboratório descartável **aprovados**. Correção SHELL-001 incorporada no commit `76dbeec0f8b1eae992617ab008726273b026eca5`, com regressões, CI e E2E de shell real **aprovados**. Implantação do Broker corrigido, reteste autenticado e homologação completa no SRV-IA **pendentes**; VPS Community permanece inalterada. [Procedimento do operador](UPGRADE-SHELL-001-SRV-IA.md).

| Gate | Requisito | Estado |
| --- | --- | --- |
| I0 | Histórico da `main` e Apache-2.0 preservados no repositório independente | concluído |
| I1 | Identidade e documentação distintas, sem alterar Community/Cloud | parcialmente concluído |
| I2 | Instalador opt-in e política persistente `/opt` auditável | E2E Docker local aprovado; fluxo público do instalador pendente |
| I3 | Broker: operações de arquivo privilegiadas **tipadas**, confinadas a `/opt`; shell não-root | **E2E Docker local aprovado**; shell root bloqueado; E2E SRV-IA pendente |
| I4 | OAuth/identidade MCP, túnel e revogação externa | **OAuth real em laboratório aprovado**; túnel público e revogação externa pendentes |
| I5 | Adversarial: traversal, symlinks, hardlinks, montagens, segredos, `/proc`, TOCTOU e subject mismatch | pendente |
| I6 | `shell.exec_admin`, Docker host/root e gestão global negados por padrão | **bloqueios testados no Docker local**; ameaças indiretas pendentes |
| I7 | Reboot/reconexão mantêm autorização; revogação e restauração funcionam | pendente |
| I8 | E2E real no SRV-IA, após testes em laboratório descartável | pendente |
| I10 | Instalação de plano de controle em `/srv/portico-infinity`, proprietário root e usuário executor sem escrita | **aprovado no runner descartável**; SRV-IA pendente |
| I9 | Modo root global opcional, se implementado: gate independente | fora do MVP |

## Evidência E2E Docker real, 16/10/2026

**APROVADO somente o gate local descartável.** Execução [GitHub Actions #38891810390](https://github.com/josemirmoura/portico-infinity/actions/runs/38891810390), commit `863de4cdc1edc721b43d6a2d2785f4e81bccae9b`, resultado **SUCCESS**. GitHub runner `ubuntu-24.04` com Docker e Broker/Gateway reais; instância `portico-infinity` isolada. A autenticação foi um bearer **estático exclusivamente de laboratório**, não OAuth público.

- Controlador em `/srv/portico-infinity` root-owned, checado em 489 arquivos/diretórios após reinicialização, com conta de execução não-root distinta.
- Política estática `scoped`, teto `/opt`, operações de arquivo `file.write`, `file.read`, `file.patch`, `file.chmod`, `file.chown`, `file.remove` aprovadas no Broker real.
- Tentativas fora de `/opt` e contra `/srv/portico-infinity` bloqueadas; `package.install`, `shell.exec_admin`, `service.restart` e `docker.action` negadas.
- Token inválido rejeitado; cadeia de auditoria do Broker válida (**30 eventos**).
- Desligamento pelo operador confirmou interrupção dos dois contêineres, seguida de partida e healthchecks aprovados. Artefatos não secretos enviados pela Actions.
- Cleanup final do projeto Docker executado em runner descartável. **Nem VPS nem SRV-IA foram alterados.**

**Não coberto:** instalação guiada pública, OAuth/Zitadel e conexão ChatGPT real, reinicialização completa do host, revogação de tokens fora da máquina, efeitos indiretos de scripts em `/opt`, e instalação/desinstalação homologadas no SRV-IA. Esses gates permanecem pendentes e não devem ser descritos como aprovados.

## Evidência OAuth real, 16/10/2026

**APROVADO somente em laboratório descartável.** Workflow [infinity-real-oauth-disposable-acceptance #38893160988](https://github.com/josemirmoura/portico-infinity/actions/runs/38893160988), commit `c0513c155450188193ab5b1516817f9516fb2dfe`: **SUCCESS**.

- ZITADEL/PostgreSQL/Login V2 e Dynamic Client Registration reais, com token OAuth real, CA de laboratório e validação TLS.
- Traefik HTTPS → Gateway MCP → Broker via socket Unix → política `scoped` com teto `/opt`, leitura/escrita e auditoria válidas.
- Negados token inválido, operações em `/srv/portico-infinity`, fuga para `/etc`, instalação de pacotes e shell administrativo.
- Identidade Docker do ensaio: `portico-infinity-oauth`, isolada do Community. Na produtização, `compose.infinity-auth.yaml` substitui os nomes dos roteadores/serviços Traefik; `setup-integrated-auth.sh` seleciona volumes Infinity e recusa proxy público automático sem Traefik reaproveitável.
- Não foi realizado login interativo no ChatGPT nem exposto endereço público de produção. OAuth/ChatGPT do SRV-IA e revogação de sessões continuam pendentes. Teste de integração OAuth não é teste de renovação/revogação.

## Provas mínimas

- Criar, ler, editar, `chmod`, `chown` e remover arquivo de teste dentro de `/opt`; verificar auditoria por operação.
- Negar operações fora de `/opt` e toda execução de shell root arbitrária.
- Verificar que escrever em `/opt` não altera componentes do plano de controle em `/srv/portico-infinity`; testar indireções perigosas de Docker/systemd e scripts privilegiados.
- Confirmar que a política persistente sobrevive a reboot sem permitir autoaprovação ou acesso a outro sujeito.
- Testar revogação externa e retorno ao estado seguro sem depender da IA.

**Release:** não chamar `opt-privileged` de aprovado antes de CI + E2E. Não gerar ciclos de auditoria sem novos defeitos concretos; usar critérios objetivos de encerramento.

## Regra

Pórtico Community e Pórtico Cloud permanecem isolados. Não reaproveitar estado, portas, identidade ou segredos de produção.
