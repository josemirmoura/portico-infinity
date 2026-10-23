# Pórtico Infinity v1.0 | Pacote de aceite técnico e UAT do proprietário

**Estado:** pré-lançamento. Este documento é um roteiro de coleta e decisão, **não** atesta que a v1.0 foi homologada ou lançada. A revisão do candidato é a `main` consolidada após todas as PRs bloqueadoras passarem no respectivo SHA. Sem tag estável, deploy operacional ou anúncio até UAT pessoal + GO registrado.

## 1. Identificação inequívoca do candidato

Antes de iniciar qualquer UAT, preencher a partir de GitHub e do host **sem divulgar segredos**:

| Campo | Valor obrigatório |
| --- | --- |
| Revisão Git exata aprovada e verificada | `<SHA40_MAIN_CANDIDATA>` |
| PRs de último fechamento | [#63](https://github.com/josemirmoura/portico-infinity/pull/63), [#65](https://github.com/josemirmoura/portico-infinity/pull/65), [#64](https://github.com/josemirmoura/portico-infinity/pull/64) |
| Resultado CI da mesma revisão | `<RUN_URL>`; checks `<X>/<X> SUCCESS` |
| Host e instancia UAT isolados | `<OPERADOR_CONFIRMA>` |
| Escopo físico de trabalho autorizado | `<PATH_EXISTENTE>`, padrão `/opt` |
| Janela autorizada, backup, rollback e acesso externo | `<EVIDENCIA_OPERADOR>` |
| Versão de Docker/Compose/kernel/cliente MCP | `<COLETA_ANTES_TESTE>` |

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
