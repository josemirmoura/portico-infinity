# Infinity | Reconciliação de shell jobs sem polling

**Estado:** reconciliação integrada ao código, com paginação em validação no GitHub. Nenhuma revisão comprova implantação no SRV-IA.

## Contexto

A correção [#69](https://github.com/josemirmoura/portico-infinity/pull/69) preserva a evidência terminal de jobs shell do systemd até o cliente consultar `job.status`. Em clientes que nunca consultam, a unidade concluída pode permanecer em `active/exited`, gerando acúmulo. Jobs que pertencem a versões antigas e tiveram a unidade coletada prematuramente precisam de reconciliação manual, nunca inferência de sucesso.

## Política do reconciliador

- **Somente Infinity:** iniciado pelo Broker root-owned quando `VPS_AGENT_INFINITY_PROTECTED_ROOT` estiver definido; Community não ganha novo job sweep.
- **Periodicidade interna:** um tick a cada 60 segundos, com máximo 64 registros de jobs shell inspecionados por passagem e limite global de 25 segundos de trabalho por tick. **A seleção é limitada no SQLite**, com índice parcial, ordenação por `(created_at,job_id)` e cursor que avança inclusive quando o job permanece ativo. Ao chegar ao fim, a busca reinicia pelos registros ativos mais antigos; Compose e cópia binária não ocupam vagas do lote.
- **Origem de autoridade:** `State.ListActiveJobs` do SQLite protegido. Seleciona apenas `shell.exec`, `job.start` e `shell.exec_admin`, em registro com idade superior a 20 segundos. Não toca jobs Compose, cópias binárias, serviços arbitrários, Docker ou unidades descobertas fora da trilha persistente.
- **Sucesso comprovado:** usa o status detalhado `LoadState/ActiveState/SubState/Result/ExecMainCode/ExecMainStatus` da unidade fixa. Persiste `done` ou `failed` com `ExitCode` antes da coleta `systemctl stop/reset-failed` best-effort.
- **Evidência ausente:** antes do prazo do job mais 60 segundos de tolerância, não presume nada. Se a unidade faltar, for ambígua ou continuar sem resultado confiável depois do prazo, grava `reconcile_required` **sem** exit code, sem matar ou repetir a tarefa. Exige análise do operador.
- **Segurança concorrente:** atualização SQL `UpdateActiveJobState` com comparação atômica sobre estados ativos. Não sobrescreve checkpoints já `done/failed/cancelled/reconcile_required`, mesmo com polls concorrentes.
- **Privacidade:** telemetria do Broker registra apenas contagens de inspeções, conclusões e casos que exigem operador; sem comandos, conteúdos ou credenciais.

## Diagnóstico operacional

`job.status(job_id)` retorna checkpoint durável da tarefa quando concluída ou marcada para reconciliação. `reconcile_required` significa **estado desconhecido**, não falha comprovada do comando e certamente não sucesso. O operador deve inspecionar unidade, journal, efeitos no workspace e trilha de auditoria antes de decidir qualquer repetição.

Nenhuma operação de recuperação de arquivos, desinstalação, reboot, revogação ou rollback é disparada automaticamente. O contador `system.health.active_jobs` pode incluir registros históricos que serão reconciliados progressivamente; não equivale a processos vivos.

## Limitações e rollback

A reconciliação não é substituto de execução transacional. Se o Broker estiver parado, os jobs não são varridos até sua recuperação. No perfil Infinity, o Broker registra a intenção de limpeza na mesma transação que persiste o estado terminal e o código de saída. Falha em `systemctl stop/reset-failed` deixa recibo `pending` para nova tentativa limitada. O resultado terminal original permanece inalterado. A limpeza só é marcada `done` após inspeção do estado `LoadState=not-found`. Se o nome da unidade não corresponder ao prefixo determinístico `vps-agent-job-` + 16 caracteres hexadecimais, o sistema recusa a ação. O perfil Community não habilita essa rotina. O teto de 64 itens por tick agora também limita quantos registros são transferidos do SQLite, sem materializar o backlog inteiro; ainda não é garantia de latência em filas arbitrariamente grandes. O cursor é local ao Broker e se reinicia após restart, sem alterar checkpoints duráveis.

Antes de reverter uma implantação futura, preservar backup SQLite + auditoria, e verificar se o binário anterior compreende `reconcile_required`. A reversão do código é feita por Git, mas não autoriza converter registros desconhecidos em sucesso nem excluir unidades remanescentes.

## Testes e aceite

Em runner descartável: teste de reconciliação de shell concluído sem polling do cliente, teste real de systemd, unidade não verificável após prazo, registro jovem, tipos de job estrangeiros, monotonicidade CAS, persistência e coleta única. **Teste em SRV-IA exige autorização própria para deploy/UAT**, não concedida pela aprovação anterior restrita a arquivos sintéticos.

Relacionado: [#68](https://github.com/josemirmoura/portico-infinity/issues/68), [EXECUTION_PLAN.md](EXECUTION_PLAN.md).

## Prova específica de escala da reconciliação

O teste de consulta por keyset semântica usa centenas de jobs Compose que não devem ocupar páginas, 135 shell ativos com o mesmo timestamp e limite de 32. Verifica exclusão de jobs novos/terminais, inexistência de duplicação, término da paginação e abertura de banco SQLite persistente. O teste do Broker usa 80 shell em execução prolongada, 20 concluídos não consultados e 150 Compose; os 20 precisam ser alcançados no segundo lote, comprovando que um backlog de jobs vivos não bloqueia jobs posteriores. Também valida retorno ao começo e preservação dos resultados já salvos.

Esta prova não substitui benchmark com milhões de jobs ou teste de operações reais no SRV-IA.

## Outbox de retirada de unidades, revisão posterior

O SQLite tem tabela aditiva `shell_unit_cleanup` com `job_id`, `unit_name`, `status` (`pending` ou `done`), contador de tentativas e erro sanitizado. O Broker só cria recibo quando a mudança de estado `running → done/failed` e o recibo **confirmam juntos** via transação. Se qualquer escrita falhar, nenhum dos dois estados é declarado persistido. Os jobs das versões antigas com término sem recibo **não são apagados automaticamente** e devem ser reconciliados pelo operador.

A cada ciclo de 60 segundos o Broker Infinity varre até 64 recibos em aberto, sem depender de novos pedidos MCP. Cada tentativa só aciona comandos tipados `systemctl stop` e `systemctl reset-failed` na unidade determinística própria, após verificar que ela não está rodando. A aposentadoria só recebe confirmação após nova consulta do `systemctl show` indicando `LoadState=not-found`. Se o comando falhar, a unidade permanecer `loaded` ou o estado for desconhecido, o recibo continua pendente para revisão/tentativa futura. O campo `exit_code` original nunca é reescrito na limpeza; a tarefa original nunca é executada novamente.

A implementação é protegida pelo indicador `VPS_AGENT_INFINITY_PROTECTED_ROOT`, sem ativação equivalente no Community. Testes de estado verificam retomada após reabrir o banco, falhas transitórias, idempotência, rejeição de jobs Compose/sem código de saída e isolamento Community; smoke test executa um comando falho em unidade `systemd` efêmera e confirma aposentadoria e resultado persistido.

**Limites da prova:** ainda não substitui auditoria exportada, UAT autorizado no SRV-IA, teste completo de reboots, nem a limpeza de unidades Compose tratada na Issue #12. O outbox não repara automaticamente registros terminados antes da mudança. O lançamento v1.0 continua sujeito à aprovação pessoal.
