# Gates de aceite: Pórtico Infinity

## Bloqueio preventivo do publicador herdado

O workflow `.github/workflows/release.yml` veio do Pórtico Community e publica tags,
imagens GHCR e GitHub Releases. No repositório Infinity, esse caminho deve
**falhar fechado**, mesmo se acionado pelo proprietário: a confirmação genérica
`PUBLICAR <tag>` não comprova o UAT e o GO específicos da edição Infinity.
O job de validação confere a identidade do repositório antes do checkout;
os jobs que publicam imagens e GitHub Releases possuem travas adicionais
`github.repository`. Regressões negativas verificam a rejeição de Infinity,
aliases e forks. Um processo próprio de release Infinity somente poderá ser
habilitado depois do UAT do proprietário e de seu GO explícito sobre o SHA.
O bloqueio não interfere no código nem na instalação do Community.


## Atualização verificável em 23/10/2026 (pós-PR #52)

A tabela de 16/10 abaixo é histórica. Os novos ensaios ocorreram exclusivamente em runners descartáveis do GitHub; não autorizam atualização do SRV-IA nem publicação.

| Gate incremental | Evidência | Resultado |
| --- | --- | --- |
| Ciclo de vida do runtime | [PR #27](https://github.com/josemirmoura/portico-infinity/pull/27), [Docker CI](https://github.com/josemirmoura/portico-infinity/actions/runs/39824785485) | 20/20 checks; desativação reversível, remoção exclusiva de contêineres/redes, dados e /opt preservados |
| Cópia >2 GiB | [PR #28](https://github.com/josemirmoura/portico-infinity/pull/28), [CI](https://github.com/josemirmoura/portico-infinity/actions/runs/39825831727) | 21/21 checks; fonte sintética 2.147.487.744 bytes, SHA-256, progresso, cancelamento e replay negado |
| Revogação externa da identidade | [PR #29](https://github.com/josemirmoura/portico-infinity/pull/29), [ZITADEL real](https://github.com/josemirmoura/portico-infinity/actions/runs/39828400590) | 16/16 checks; token ativo antes e inativo após desativação administrativa de conta de serviço sintética |
| Worker executado via systemd | [PR #30](https://github.com/josemirmoura/portico-infinity/pull/30), [CI](https://github.com/josemirmoura/portico-infinity/actions/runs/39835994798) | 20/20 checks; cópia, hash, manifesto inseguro negado, cancelamento SIGTERM sem publicação |
| MCP até worker real | [PR #31](https://github.com/josemirmoura/portico-infinity/pull/31), [Docker E2E](https://github.com/josemirmoura/portico-infinity/actions/runs/39837703649) | 17/17 checks; token estático de laboratório, Gateway → Broker → systemd → cópia de 128 MiB, auditoria e negações |
| Cancelamento pelo MCP em job ativo | [PR #33](https://github.com/josemirmoura/portico-infinity/pull/33) | Ensaio sintético: job.cancel de cópia longa e ausência de publicação parcial; limitado a token de laboratório |
| Reconciliação após restart do Broker | [PR #34](https://github.com/josemirmoura/portico-infinity/pull/34), [E2E](https://github.com/josemirmoura/portico-infinity/actions/runs/39847946672) | 17/17 checks; job systemd preservado, reconexão e cancelamento MCP após restart do contêiner; **não prova reboot do host** |
| Desativação com worker ativo | [PR #35](https://github.com/josemirmoura/portico-infinity/pull/35), [E2E](https://github.com/josemirmoura/portico-infinity/actions/runs/39850331376) | 20/20 checks; bloqueia desativação e remoção quando worker real está `activating`, sem afetar projetos externos |
| Desativação com worker falhado | [PR #36](https://github.com/josemirmoura/portico-infinity/pull/36), [E2E](https://github.com/josemirmoura/portico-infinity/actions/runs/39862020998) | 8/8 workflows no head `8ea9e1cf`; falha de worker `systemd` detectada e bloqueada até reconciliação externa |
| Fence externo de cópias longas por sujeito | [PR #38](https://github.com/josemirmoura/portico-infinity/pull/38), [E2E de revogação](https://github.com/josemirmoura/portico-infinity/actions/runs/39871198739), [E2E Docker/MCP](https://github.com/josemirmoura/portico-infinity/actions/runs/39871198832) | 14/14 workflows SUCCESS no SHA `2b4883a1`; marcador root-owned, rejeição de novos despachos e cancelamento com checkpoint de worker sintético real `systemd`. Prova de OAuth integrado registrada separadamente na PR #40 | 
| Revogação OAuth real + worker ativo no mesmo E2E | [PR #40](https://github.com/josemirmoura/portico-infinity/pull/40), [GitHub Actions](https://github.com/josemirmoura/portico-infinity/actions/runs/39874940886) | 8/8 workflows SUCCESS no head `315b7985`; identidade ZITADEL sintética, MCP autenticado inicia cópia 8 GiB, unidade `systemd` ativa, fence externo, estado durável `cancelled`, destino ausente, novas cópias recusadas; conta desativada e **mesmo token → HTTP 401** |
| Recuperação do Broker/Gateway e SQLite após reboot real do kernel de VM | [PR #52](https://github.com/josemirmoura/portico-infinity/pull/52), [QEMU real](https://github.com/josemirmoura/portico-infinity/actions/runs/39949281265) | **9/9 workflows SUCCESS** no SHA `b56cfb3e`; reboot efetivo do kernel Ubuntu 24.04 com `boot_id` diferente, Broker e Gateway reais reiniciados pelo `systemd`, Gateway não-root, SQLite e arquivos persistentes, auditoria íntegra e crescida, MCP autorizado antes/depois, escapes administrativos e bearer inválido negados. **Token sintético estático, não ZITADEL** |
| Recuperação de Broker/Gateway com bearer OAuth e SQLite persistentes | [PR #51](https://github.com/josemirmoura/portico-infinity/pull/51), [OAuth E2E](https://github.com/josemirmoura/portico-infinity/actions/runs/39946289952) | **8/8 workflows SUCCESS** no SHA `e975532b`; mesmo bearer ZITADEL antes/depois de reiniciar os processos Gateway/Broker, arquivo persistente, controle fora do teto negado, bearer falso 401 e auditoria SQLite íntegra e crescente. **Reinício dos processos Docker, não do kernel** |
| Reboot real do kernel guest com worker `systemd` ativo | [PR #42](https://github.com/josemirmoura/portico-infinity/pull/42), [QEMU E2E](https://github.com/josemirmoura/portico-infinity/actions/runs/39879379953) | 13/13 workflows SUCCESS no head `095bdb48`; imagem Ubuntu 24.04 oficial verificada por SHA256, `/proc/sys/kernel/random/boot_id` alterado após reset QMP, manifesto/checkpoint sobreviventes, unidade transitória ausente, destino não publicado e replay negado. **É reboot real de VM, não SRV-IA** |
| Ciclo de vida pós-reboot com checkpoints incertos | [PR #43](https://github.com/josemirmoura/portico-infinity/pull/43) | 8/8 workflows SUCCESS no head `08451f0c`; o operador não desativa nem remove o runtime com manifesto sem checkpoint ou job `queued/running/reconcile_required`, mesmo sem unidade systemd; arquivos/volumes externos preservados |
| Preparação para desinstalação integral, sem exclusão | [PR #45](https://github.com/josemirmoura/portico-infinity/pull/45), [procedimento](FULL_UNINSTALL.md) | Inventário read-only de volumes e contêineres exclusivos, backup privado root-only com SHA-256 e prova de restauração de configuração/identidade sintética em runner. **Não remove volumes, controlador ou identidade operacional**; fase de exclusão final continua pendente |
| Remoção local isolada com backup e restauração prévios | [PR #46](https://github.com/josemirmoura/portico-infinity/pull/46), [CI descartável](https://github.com/josemirmoura/portico-infinity/actions/runs/39884191674) | Remoção real **somente em runner descartável** de contêineres/redes/volumes privados e controlador após inventário imutável, checksum e dupla confirmação; contêiner estrangeiro e backup permanecem. **Não é desinstalação multi-host e não foi executada no SRV-IA/VPS** |
| Desativação/remoção isolada da ponte HTTPS VPS | [PR #47](https://github.com/josemirmoura/portico-infinity/pull/47), [CI Docker/Traefik](https://github.com/josemirmoura/portico-infinity/actions/runs/39932078464) | **7/7 workflows SUCCESS**, SHA final `6b86ab14`; contêiner único `bridge` removido, rede `traefik-public`, Traefik principal, configs, TLS e serviços estrangeiros preservados. Nenhuma ação na VPS operacional |
| Descomissionamento do binário privilegiado do host | [PR #48](https://github.com/josemirmoura/portico-infinity/pull/48), [CI real systemd](https://github.com/josemirmoura/portico-infinity/actions/runs/39933839281) | 9/9 workflows SUCCESS, SHA final `4b3272a0`. Binário root-owned retirado com negação de unidade ativa, checkpoints reconciliados e confirmação SHA-256. Nenhuma ação no SRV-IA |
| Preservação da conta não-root preexistente do executor | [PR #49](https://github.com/josemirmoura/portico-infinity/pull/49), [E2E de desinstalação](https://github.com/josemirmoura/portico-infinity/actions/runs/39942489368) | **8/8 workflows SUCCESS** no head `c6ab82d0`; conta `--run-as` preexistente e processo independente preservados após backup, restauração e desinstalação local em runner |
| Inventário independente de WireGuard/UFW, sem desconexão | [procedimento](FULL_UNINSTALL.md), [CI](../../.github/workflows/infinity-wg-decommission-readonly-disposable.yml) | Auditoria read-only em cada host confere configuração gerada, permissões 0600, keypair, peer e estado systemd, sinaliza dependências não comprovadas e nunca remove interfaces, chaves ou regras UFW. **Aguardando CI desta PR para comprovar o gate** |


**Bloqueadores reais da v1.0:**

1. Revogação de **sessões humanas**, sessões browser e **refresh tokens** com cliente interativo; a conta de serviço e o bearer do mesmo fluxo MCP + cópia ativa já passaram no E2E da PR #40, mas não substituem ensaio humano.
2. **Recuperação pós-reboot em instalação integral:** a PR #42 provou reboot real do kernel com worker; a PR #51 provou OAuth/ZITADEL e SQLite após reinício dos processos; a PR #52 provou kernel reboot real com Gateway/Broker/SQLite/MCP em **uma única VM**, mas com **bearer estático de laboratório**. Continua faltando provar **ZITADEL/PostgreSQL + OAuth/MCP + Gateway/Broker/SQLite após reboot real do kernel do mesmo host**, reconciliação de destinos incertos e, sob autorização específica, homologação no SRV-IA.
3. Desinstalação integral **distribuída**: PRs #45/#46 (runtime/volumes/controlador), #47 (ponte VPS), #48 (worker host) e #49 (conta preexistente preservada) têm CI aprovado. Inventário read-only WireGuard está em validação; ainda faltam ensaio de restauração funcional integral OAuth/SQLite, comprovação de não compartilhamento e desmontagem **autorizada** de WireGuard, UFW e DNS/TLS, sem afetar terceiros.
4. Segurança de efeitos indiretos em serviços root consumidores de arquivos em /opt e escala extrema, integridade de origem mutável e recuperação do arquivo após falha; o ensaio OAuth + cópia longa no mesmo cenário **já passou** para a identidade sintética.
5. Release candidate vinculada ao SHA, UAT pessoal do proprietário e decisão expressa GO/NO-GO. Nenhuma tag/release estável antes do GO.

**Operação real:** o MCP do SRV-IA respondeu em 22/10 e permaneceu sem implantação das PRs #42/#43. Não houve autorização de deploy, reboot, revogação ou desinstalação operacional. **A decisão GO/NO-GO segue reservada ao proprietário após UAT.**

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
