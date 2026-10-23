# Infinity | Reconciliação de shell jobs sem polling

**Estado:** implementação candidata, aguardando CI completo e UAT. Este documento não comprova instalação operacional.

## Contexto

A correção [#69](https://github.com/josemirmoura/portico-infinity/pull/69) preserva a evidência terminal de jobs shell do systemd até o cliente consultar `job.status`. Em clientes que nunca consultam, a unidade concluída pode permanecer em `active/exited`, gerando acúmulo. Jobs que pertencem a versões antigas e tiveram a unidade coletada prematuramente precisam de reconciliação manual, nunca inferência de sucesso.

## Política do reconciliador

- **Somente Infinity:** iniciado pelo Broker root-owned quando `VPS_AGENT_INFINITY_PROTECTED_ROOT` estiver definido; Community não ganha novo job sweep.
- **Periodicidade interna:** um tick a cada 60 segundos, com máximo 64 registros de jobs shell inspecionados por passagem e limite global de 25 segundos de trabalho por tick.
- **Origem de autoridade:** `State.ListActiveJobs` do SQLite protegido. Seleciona apenas `shell.exec`, `job.start` e `shell.exec_admin`, em registro com idade superior a 20 segundos. Não toca jobs Compose, cópias binárias, serviços arbitrários, Docker ou unidades descobertas fora da trilha persistente.
- **Sucesso comprovado:** usa o status detalhado `LoadState/ActiveState/SubState/Result/ExecMainCode/ExecMainStatus` da unidade fixa. Persiste `done` ou `failed` com `ExitCode` antes da coleta `systemctl stop/reset-failed` best-effort.
- **Evidência ausente:** antes do prazo do job mais 60 segundos de tolerância, não presume nada. Se a unidade faltar, for ambígua ou continuar sem resultado confiável depois do prazo, grava `reconcile_required` **sem** exit code, sem matar ou repetir a tarefa. Exige análise do operador.
- **Segurança concorrente:** atualização SQL `UpdateActiveJobState` com comparação atômica sobre estados ativos. Não sobrescreve checkpoints já `done/failed/cancelled/reconcile_required`, mesmo com polls concorrentes.
- **Privacidade:** telemetria do Broker registra apenas contagens de inspeções, conclusões e casos que exigem operador; sem comandos, conteúdos ou credenciais.

## Diagnóstico operacional

`job.status(job_id)` retorna checkpoint durável da tarefa quando concluída ou marcada para reconciliação. `reconcile_required` significa **estado desconhecido**, não falha comprovada do comando e certamente não sucesso. O operador deve inspecionar unidade, journal, efeitos no workspace e trilha de auditoria antes de decidir qualquer repetição.

Nenhuma operação de recuperação de arquivos, desinstalação, reboot, revogação ou rollback é disparada automaticamente. O contador `system.health.active_jobs` pode incluir registros históricos que serão reconciliados progressivamente; não equivale a processos vivos.

## Limitações e rollback

A reconciliação não é substituto de execução transacional. Se o Broker estiver parado, os jobs não são varridos até sua recuperação. Se `systemctl stop/reset-failed` falhar depois de persistir o resultado, a coleta ainda pode exigir tratamento do operador: esta versão não implementa controle persistente separado de `cleanup_completed`. O teto de 64 itens por tick é uma medida de carga, não uma garantia de latência sob filas arbitrariamente grandes.

Antes de reverter uma implantação futura, preservar backup SQLite + auditoria, e verificar se o binário anterior compreende `reconcile_required`. A reversão do código é feita por Git, mas não autoriza converter registros desconhecidos em sucesso nem excluir unidades remanescentes.

## Testes e aceite

Em runner descartável: teste de reconciliação de shell concluído sem polling do cliente, teste real de systemd, unidade não verificável após prazo, registro jovem, tipos de job estrangeiros, monotonicidade CAS, persistência e coleta única. **Teste em SRV-IA exige autorização própria para deploy/UAT**, não concedida pela aprovação anterior restrita a arquivos sintéticos.

Relacionado: [#68](https://github.com/josemirmoura/portico-infinity/issues/68), [EXECUTION_PLAN.md](EXECUTION_PLAN.md).
