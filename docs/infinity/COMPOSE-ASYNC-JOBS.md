# Operações Compose assíncronas e duráveis no Infinity

**Situação (16/10/2026):** PR [#11](https://github.com/josemirmoura/portico-infinity/pull/11) fundido ao `main` no commit imutável `a16bd9a805106d9cec8bd7597b0f75e44fdde0be`, após **12/12 workflows aprovados** no head final. **NÃO implantado nem homologado no SRV-IA de produção**. Próximo passo: [runbook de atualização protegida](UPGRADE-COMPOSE-ASYNC-SRV-IA.md). Issue relacionada: [#9](https://github.com/josemirmoura/portico-infinity/issues/9).

## Problema

O Gateway usa timeout IPC de 15 s. Antes desta mudança, `compose.pull/up/down` executavam comandos Docker Compose de forma síncrona, fazendo downloads longos retornarem `read unix ... broker.sock: i/o timeout` embora o processo no host continuasse. Repetir operação nesses casos é perigoso.

## Solução nesta branch

- Mantém ferramentas oficiais `compose.pull/up/down` e autorização pelo Broker; não expõe Docker socket nem shell root no MCP.
- Após autorização por política e delegação exclusiva do projeto, o Broker gera uma unidade systemd transiente com comando **fixo e tipado** `docker compose -f FILE --project-directory DIR pull|up -d --remove-orphans|down`, sem executar uma string shell arbitrária.
- O Broker persiste o job no SQLite **antes** de despachar ao systemd. `systemd-run --no-block` retorna rapidamente, evitando manter o IPC aberto pelo tempo do download.
- Resposta: `{job_id, state: "running", project_dir, action, status_tool: "job.status", log_tool: "job.tail"}`.
- `job.status` verifica `systemctl show` e interpreta `ActiveState/Result/ExecMainStatus` sem confundir unidade ausente com sucesso. `job.tail` usa o runner existente e mantém limites de saída. `job.cancel` usa o mesmo escopo por proprietário, conforme a API existente.
- Um `operation_id` estável gera identificador de job determinístico (hash do sujeito e ID da operação). Retry com o mesmo ID retorna o resultado cacheado, ou identifica o job anterior sem reiniciá-lo se houve falha no retorno.
- Um segundo `compose.*` para o mesmo diretório é negado enquanto houver job ativo ou status incerto. Até as unidades que falharam na confirmação ficam pendentes para reconciliação manual.
- Jobs têm limite de execução de **2 horas**, memória da unidade limitada e usam `systemd-run` com `Type=oneshot` e `RemainAfterExit=yes` (sem `--collect`), preservando uma unidade concluída em `active/exited` e o código de saída para consulta posterior mesmo após reconexão ou reinício do Broker. Sem esta retenção, a unidade curta pode desaparecer (`LoadState=not-found`) antes do primeiro `job.status`.
- Com `DockerManager` falso de testes, caminho antigo síncrono continua possível. Em produção, `DockerCLI` implementa o iniciador assíncrono.

## Testes adicionados

`go test ./internal/broker ./internal/jobs ./internal/state ./internal/gateway` cobre:
- retorno imediato com job persistido, idempotência, colisão de IDs e isolamento por sujeito;
- falha na confirmação de dispatch **não** libera uma nova tentativa com outro operation ID;
- classificação de unidade ativa, concluída, falha, timeout e estado não confiável;
- rejeição de ação Compose não autorizada.

**Estes testes ainda devem ser executados pelo GitHub Actions antes do merge.** Não inferir sucesso apenas porque os arquivos estão versionados.

## Homologação obrigatória antes de produção

1. Revisar o diff e CI do PR. Executar testes existentes (incluindo autorizações, auditoria, operações sincronizadas e integração MCP).
2. Em máquina de homologação isolada, delegar um projeto Compose descartável via fluxo de aprovação e iniciar pull lento com imagem não cacheada. Confirmar resposta em poucos segundos, `job.status` evoluindo de `running` para `done`, `job.tail` retornando progresso e nenhuma segunda execução.
3. Testar duas identidades, operação ID repetido, diretório não delegado, revogação, concorrência, cancelamento e reinício do Gateway/Broker enquanto o job continua.
4. Testar job falho e timeout de 2 h. Confirmar que nenhum sucesso é relatado sem código de saída validado.
5. Backup de código/configuração/estado, build de binários e rollout somente do Broker e Gateway compatíveis, preservando OAuth e rotas, com rollback conhecido.
6. Verificar que funções legacy do Community não foram alteradas e que arquivos grandes usam sua própria solução de streaming (#8), não esta função de Compose.

## Limitações conhecidas desta implementação

- Unidades systemd não coletadas automaticamente podem acumular ao longo de meses. Planejar coleta explícita supervisionada e retenção de audit logs após sucesso/erro.
- Revogação de delegação após o início não cancela automaticamente um job que já recebeu autorização. Isto exige teste e decisão de política, antes de liberação geral multiusuário.
- Existe janela entre persistência SQLite e despacho systemd. Quando a existência da unidade é incerta, o projeto fica bloqueado para reconciliação manual. É intencional e evita duplicar efeitos.
- O Compose autorizado é potencialmente privilegiado em sua própria capacidade (montagens e root no contêiner). Manter aprovação humana prévia para o projeto, arquivos de configuração restritos, validação de symlinks e auditoria.
- Esta correção resolve somente operações Compose. Transferências de modelos LLM multigigabyte e exclusão recursiva >50.000 entradas continuam nas issues #8 e #6.

## Falha descoberta em CI real e regressão

O [Docker package acceptance](https://github.com/josemirmoura/portico-infinity/actions/runs/38978432836) recebeu rapidamente `job_id` para `compose.up`, mas o `job.status` seguinte encontrou a unidade descarregada com `LoadState=not-found`. Foi corrigido na branch para execução systemd `Type=oneshot`/`RemainAfterExit=yes`, com classificação `active/exited`, código de saída zero e `Result=success` como concluído. Adicionados testes de status confiável e estado incerto. **A versão corrigida aguarda novo CI real antes de qualquer merge.**
