# Pórtico Infinity v1.0 | Pacote de aceite técnico e UAT do proprietário

**Estado:** pré-lançamento. Em 24/10/2026 o proprietário autorizou e executou no SRV-IA o deploy operacional controlado do candidato identificado abaixo, com backup e rollback preparados. Isso **não** é GO para v1.0, não certifica todos os gates de UAT e não autoriza tag, GitHub Release, promoção adicional ou anúncio público. O SHA de referência é o código efetivamente implantado; mudanças posteriores somente documentais da `main` não mudam retroativamente a revisão instalada.

## 1. Identificação inequívoca do candidato

Antes de iniciar qualquer UAT, preencher a partir de GitHub e do host **sem divulgar segredos**:

| Campo | Valor obrigatório |
| --- | --- |
| Revisão de **código candidata instalada** | `48d8c15978a675090a052b977b47abd1fbdd87f9` (merge [#75](https://github.com/josemirmoura/portico-infinity/pull/75)); não é tag v1.0 |
| PRs de último fechamento | [#73](https://github.com/josemirmoura/portico-infinity/pull/73) (shell outbox), [#74](https://github.com/josemirmoura/portico-infinity/pull/74) (Compose fence), [#75](https://github.com/josemirmoura/portico-infinity/pull/75) (Compose outbox) |
| Evidência CI por **head SHA** de cada PR incluída no commit final | #73: `e53f1f72bbc81e5101ea672fe3f46d7a39ae9d13`, **9/9** workflows SUCCESS; #74: `2c5c051ac35df5afe6b2875805c972dfbdaeeac0`, **12/12**; #75: `2f70737f670e47a52e159ea20666d244aedde0e8`, **13/13**. Conferir [Actions](https://github.com/josemirmoura/portico-infinity/actions); estes são heads testados, não claims de uma suíte disparada no SHA squash. |
| Host e instância operacionais do UAT controlado | `srv-ia`, `portico-infinity`; Gateway e Broker próprios, ZITADEL não recriado na atualização. **Não** são ambiente efêmero |
| Escopo físico de trabalho autorizado | `/opt`, `mode=scoped`, `full_enabled=false`; controlador protegido `/srv/portico-infinity` fora do teto |
| Autorização, backup e rollback | Proprietário autorizou atualização de Broker/Gateway e testes descartáveis sem reboot/alteração de rede; backup consistente validado e ensaio de restauração SQLite em `/var/backups/portico-infinity/uat-20261024T013805Z`; backup do deploy `/var/backups/portico-infinity/deploy-20261024T015226Z`; SHA anterior `c269d980d91392c2eca9ab92ab2eaa132efc6752`. **Rollback real pós-deploy não foi executado** |
| Versão Docker/Compose/kernel/cliente MCP | Docker Server `29.9.0`, Compose `5.6.0`; cliente ChatGPT com conector MCP autenticado; kernel e detalhes de refresh de sessão **não coletados** |

Nenhuma aprovação pode ser transferida silenciosamente para um SHA ou instalação diferente. Se um teste altera o código, repetir só os ensaios afetados e registrar nova revisão.

## 2. Matriz de evidências pré-UAT

| Cenário | Evidência de laboratório já existente | Lacuna a validar |
| --- | --- | --- |
| CRUD, `chmod/chown` e negações /srv e /etc | [E2E Docker #38891810390](https://github.com/josemirmoura/portico-infinity/actions/runs/38891810390) | Cliente e conta reais do proprietário |
| TLS, OAuth ZITADEL e MCP | [E2E OAuth #38893160988](https://github.com/josemirmoura/portico-infinity/actions/runs/38893160988) | Login interativo, expiração, refresh e revogação de sessão humana |
| Reboot do **mesmo kernel guest** com OAuth/ZITADEL/Postgres/Broker/SQLite | [PR #56](https://github.com/josemirmoura/portico-infinity/pull/56) e [run #39982720921](https://github.com/josemirmoura/portico-infinity/actions/runs/39982720921) | Reboot operacional com janela e rollback específicos |
| Máscaras de segredo em 1 milhão de entradas sintéticas | [PR #58](https://github.com/josemirmoura/portico-infinity/pull/58) | Cargas reais diferentes do hardware CI |
| Identidade mutável em cópia de binário | [PR #63](https://github.com/josemirmoura/portico-infinity/pull/63) | Corridas residuais; nenhuma garantia de snapshot transacional |
| Checkpoint terminal em falha de inicialização | [PR #65](https://github.com/josemirmoura/portico-infinity/pull/65), 12/12 workflows SUCCESS | Deploy, UAT e observação real do operador |
| Revogação antes do commit da cópia | [PR #64](https://github.com/josemirmoura/portico-infinity/pull/64) | Marcar PASS somente após CI integral e merge da revisão combinada |
| Inventário de consumidores root de /opt | [PR #62](https://github.com/josemirmoura/portico-infinity/pull/62) | Referências indiretas e serviços reais requerem revisão do operador |
| Desinstalação local isolada e componentes host/VPS | [PRs #45 a #49](https://github.com/josemirmoura/portico-infinity/pulls) e [FULL_UNINSTALL.md](FULL_UNINSTALL.md) | Restauração funcional integral de identidade e desmontagem multi-host exclusiva, com autorização |

**Interpretação:** o ambiente descartável prova mecanismos específicos, e os resultados não devem ser generalizados para a infraestrutura do proprietário.


## 2A. Evidência operacional SRV-IA vinculada ao SHA instalado (24/10/2026)

Os resultados abaixo se referem **somente** ao host operacional e testes sintéticos explicitamente executados; não equivalem a um E2E multi-host, a uma certificação geral de segurança nem ao UAT pessoal completo. Evidências detalhadas em [Issue #2](https://github.com/josemirmoura/portico-infinity/issues/2), [#12](https://github.com/josemirmoura/portico-infinity/issues/12) e [#68](https://github.com/josemirmoura/portico-infinity/issues/68).

| Caso | Evidência no host real | Parecer |
| --- | --- | --- |
| Atualização controlada | Checkout SHA `48d8c15978a675090a052b977b47abd1fbdd87f9`; Broker image `sha256:4e8e9f230572e54e2a1e67f4025286ef57764a95fbbb2e5bf006ecbcec54e632`; Gateway `sha256:9577d46ec5dfd4107532dbcf572aba5108356de4a21df937e99f21b149a155b3`; ambos healthy; ZITADEL mantido; `audit_chain_ok=true` | **PASS** para rollout específico, **não** para rollback em falha real |
| Migração SQLite | Backup consistente e extração isolada; binário Broker candidato iniciou isolado contra cópia do estado (sem rede); schema v2 com `shell_unit_cleanup` e `compose_unit_cleanup`; 81 jobs preservados, `integrity_check=ok` | **PASS** na cópia e no banco operacional |
| Reconciliação de histórico incerto | 40 `shell.exec` + 2 `job.start` anteriormente `running` com deadline expirado e unidade `systemd not-found` foram classificados `reconcile_required` sem replay ou sucesso inventado; depois `active_jobs=0` | **PASS** da classificação conservadora; recuperação histórica do resultado original **impossível sem evidência adicional** |
| Shell não-root e aposentadoria | Job sintético em UID 999, `done/exit_code=0`; falha separada `200/CHDIR` comprovou erro de permissão da pasta UAT; correção apenas no diretório sintético; recibo `shell_unit_cleanup=(done,1)` e unidade `LoadState=not-found` | **PASS** no host |
| Compose autenticado temporário | `compose.validate` negado antes do grant e validado após decisão SSH; projeto sintético sem rede/volumes/portas e sem pull remoto; `compose.up` e `compose.down` `done/0`, logs systemd confirmados | **PASS** no host |
| Aposentadoria durável Compose | Dois jobs sintéticos mantiveram `done/exit_code=0`; recibos `compose_unit_cleanup=(done,1)`; `LoadState=not-found`, `ActiveState=inactive`, `SubState=dead` em ambos | **PASS**, evidência operador em [#12](https://github.com/josemirmoura/portico-infinity/issues/12) |
| Controle de escopo e CRUD | `file.write/read/patch/copy/move/hash/chmod/chown` em diretório sintético, SHA-256 idêntico na cópia; `file.read` fora de `/opt` negado; artefatos removidos individualmente | **PASS** para operações específicas, não para todas as classes adversariais |
| Revogação da delegação UAT Compose | Delegação temporária de uma pasta criada após aprovação SSH; removida ao final, `revoked_dynamic_delegations=1`, sem jobs dependentes; delegação anterior para Open WebUI preservada | **PASS** para root delegation, **não** prova revogação OAuth da identidade humana |
| Estado final | `system.health.ok=true`, `audit_chain_ok=true`, `active_jobs=0`, `mode=scoped`, `full_enabled=false`; sem release estável | **PASS** no momento da coleta |

**Gates ainda abertos:** renovação/refresh e revogação real da identidade humana, restauração funcional multi-host, reboot operacional se autorizado separadamente, desinstalação integral sem perdas, revisão de consumidores root de `/opt`, benchmark grande no ambiente alvo, UAT pessoal completo e GO/NO-GO. Sem nova autorização específica, **não** reiniciar o SRV-IA, revogar identidades reais, desinstalar, mexer na VPS ou publicar a v1.0.

## 3. Roteiro mínimo de UAT pessoal, sem intervenção desnecessária

**Pré-condição bloqueadora:** proprietário selecionou e autorizou expressamente host, escopo, janela, rollback e backups. Jamais converter este roteiro em script para ser executado automaticamente em SRV-IA/VPS. Registrar `PASS`, `FAIL` ou `SKIPPED` com motivo e evidências sem segredos para cada etapa.

1. **Inventário sem mutação:** hostname, SHA instalado, controle root-owned em `/srv/portico-infinity`, permissões, composição Docker real e serviços estrangeiros; validar snapshot independente e console/SSH fora do MCP.
2. **Login humano:** conectar cliente MCP escolhido pelo proprietário, verificar token emitido para o sujeito e audiência corretos, `system.info` e evento correlato na auditoria. Repetir após renovação de sessão. Registrar resultado quando login/refresh for testado de fato.
3. **Trabalho comum:** criar subárvore UAT isolada, escrever/ler/editar/mover/copiar/renomear e remover **apenas artefatos criados para o teste**; confirmar permissões e auditoria. Nada de operações destrutivas em projetos Nara ou outros dados reais.
4. **Controles negativos:** negar traversal, symlink, hardlink e tentativa fora do teto, acesso a `.env` protegido, `/srv/portico-infinity`, shell root/admin, Docker irrestrito, sujeito/token inválido; provar resposta explícita sem vazamento.
5. **Job grande e cancelamento:** criar arquivo sintético esparso e copiação autorizada somente se o recurso estiver explicitamente habilitado para UAT; confirmar progresso, SHA, parada e ausência de destino parcial. **Não transferir GGUF de produção como teste.**
6. **Revogação externa:** operador revoga identidade em canal administrativo independente; confirmar novos pedidos negados, jobs ativos suspensos/reconciliados, e sessão browser/refresh inválidos; não relatar sucesso com base somente em client-credentials.
7. **Recuperação controlada:** realizar restart/reboot apenas se houver autorização específica e janela, checando `boot_id`, recuperação de OAuth/Postgres, jobs incertos, auditoria e acesso posterior com nova sessão válida. Sem autorização: `SKIPPED` e gate aberto.
8. **Reversão/remoção:** validar modo dry-run e lista de recursos exclusivos. Desinstalação distribuída/revogação de WireGuard/UFW/DNS/TLS exige confirmação separada por host e prova de restauração funcional. Nunca apagar `/opt`, projetos ou recursos compartilhados.
9. **Aceite de experiência:** registrar clareza de mensagens, fricção de permissões legítimas, histórico de jobs, robustez e limites de recursos observados.

## 4. Evidências, riscos e decisão

Cada caso registra `ID, SHA, host, método, início/fim, PASS/FAIL/SKIPPED, artefato sem credenciais, responsável`. Os resultados de GitHub Actions devem incluir URL da execução, SHA do *head* efetivamente testado e, após merge, SHA final da `main`.

**NO-GO obrigatório** se: alguma regressão relevante falhar; política permitir escape/elevação; acesso/refresh permanecer ativo após revogação sem explicação; trabalho interrompido publicar destino indevido; restore funcional não for demonstrado; ou o proprietário não tiver executado UAT pessoal. `SKIPPED` em requisito bloqueador não equivale a PASS.

**Riscos residuais conhecidos:** transações de arquivos/identidade externamente concorrentes, scripts de `/opt` consumidos por serviços root, componentes multi-host compartilhados, confiança em operações de manutenção e variação de throughput em dados reais. Riscos aceitos devem ter mitigação e responsável explícitos.

**Decisão exclusiva do proprietário, somente depois de UAT:**

- `GO` com SHA exato, evidências, escopo e data, ou
- `NO-GO` com defeitos e critérios para novo aceite.

Sem essa decisão documentada, manter `PRE-RELEASE`. A ponte IA local ↔ IA remota ([Issue #21](https://github.com/josemirmoura/portico-infinity/issues/21)) permanece pós-v1.0 e não impede o aceite.

**Documentos normativos:** [AGENTS.md](../../AGENTS.md), [EXECUTION_PLAN.md](EXECUTION_PLAN.md), [ACCEPTANCE.md](ACCEPTANCE.md), [FIRST_E2E.md](FIRST_E2E.md), [FULL_UNINSTALL.md](FULL_UNINSTALL.md).
