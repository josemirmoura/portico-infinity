# DELETE-001: atualização controlada e teste operacional no SRV-IA

**Issue:** [#6](https://github.com/josemirmoura/portico-infinity/issues/6)
**PR aprovada:** [#14](https://github.com/josemirmoura/portico-infinity/pull/14)
**SHA de implantação exato:** `1b2bfba22ca9d3a513470781f904adcc8d31472d`
**SHA identificado na instalação antes da operação (17/10/2026):** `a16bd9a805106d9cec8bd7597b0f75e44fdde0be`.
**Status:** preflight de leitura aprovado; backup, implantação e E2E de host ainda pendentes.

## Fronteira de autoridade

O controlador está em `/srv/portico-infinity` com dono root e **fora do teto `/opt` delegado à IA**. Somente o operador autenticado no **terminal ou SSH independente** realiza estes comandos. Nenhum passo deve ser executado via `shell.exec` do próprio Pórtico, via symlink /opt→/srv, Docker alternativo ou script hospedado em /opt. Não alterar Community, Cloud, VPS, Nara, Hermes ou Open WebUI.

Os contêineres `portico-infinity-broker-1`, `portico-infinity-gateway-1` e três serviços ZITADEL estavam healthy. Os overlays usados **por ambos Broker/Gateway** foram comprovados pelos labels Docker:
- `compose.yaml`
- `compose.infinity-auth.remote.yaml`

**Antes de qualquer mutação:** revalidar host `srv-ia`, remoto oficial, SHA anterior, git tracked clean, layout root-owned com `scripts/check-infinity-layout.py`, health, overlay exato, arquivo `.env`, arquivos de estado. Exigir conexão SSH/console independente como rota de reversão.

## Gate obrigatório: reconciliar jobs antes de reiniciar o Broker

Em 17/10/2026, `system.health.active_jobs=21`. O campo conta registros SQLite com estados `starting/running/active/activating/reloading/deactivating/cancelling`, **não comprova** que 21 processos reais estejam executando. Não apagar filas, SQLite, unidades systemd ou tentar marcar jobs como concluídos sem prova.

Inspecionar `/srv/portico-infinity/state/state.db` com SQLite **somente de leitura**, agrupando estados; comparar `unit_name` de cada job aparentemente ativo com `systemctl show --no-pager --property=LoadState,ActiveState,SubState,Result,ExecMainStatus`. Não revelar `detail`, comandos, `subject`, conteúdo, variáveis ou tokens ao chat. Se algum job estiver `active/running`, `activating`, `deactivating` ou `unknown`, **adiar atualização e investigar**. `active/exited` de unit oneshot pode ser resultado retido, não execução em andamento; verificar o resultado, não inferir sucesso só pelo estado. Unidades `not-found` não demonstram êxito.

## Backup e atualização pelo operador, somente após gate jobs

1. Criar snapshot restrito em `/srv/portico-infinity/backups` ou outro diretório protegido fora de `/opt`; `umask 077`, proprietário root. Não escrever segredos em GitHub ou chat. Nunca tirar `cp` do arquivo SQLite vivo.
2. Parar **somente gateway e broker**, preservando os três serviços ZITADEL, com os mesmos `-f` identificados, sem `down`, `down -v`, `--remove-orphans`, nem `--volumes`.
3. Com Broker parado, arquivar `.env`, `config/` e `state/` (incluindo WAL/SHM e auditoria) e demais arquivos privados necessários conforme os bind mounts. Confirmar `tar -tf`, SHA256 do arquivo, e guarda da versão anterior; se falhar, religar serviços originais sem prosseguir.
4. `git fetch origin main` e verificar `git cat-file -t 1b2bfba22ca9d3a513470781f904adcc8d31472d` == `commit`, assinatura/proveniência institucional e remoto oficial; confirmar que a versão antiga é ancestral da nova. Posicionar checkout root-owned no SHA exato, somente após backup, e repetir preflight/layout.
5. Validar em silêncio `docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml config --quiet`. Atualizar com `up -d --build --no-deps --force-recreate broker gateway`, **somente esses serviços**, sem tocar em ZITADEL.
6. Comprovar no SSH que todos os contêineres estão saudáveis, ZITADEL mantém IDs e uptime originais, `git rev-parse HEAD` igual ao SHA aprovado, auditoria íntegra. Depois testar `system.info` e `system.health` por MCP real. A string `gateway_version=v0.1.0-rc.7` pode não mudar: **SHA do checkout e digest das imagens reconstruídas são a prova**.
7. Se houver erro: parar somente Broker/Gateway, retornar ao SHA anterior e reconstruir com os mesmos overlays. Em caso de inconsistência de estado, restaurar backup consistente com ambos parados; preservar auditoria e nunca descartar dados silenciosamente.

## Aceite DELETE-001 em dados puramente sintéticos

**Não testar em `/opt/nara-vui-lab`.** Preparar uma pasta nova e exclusiva, por exemplo `/opt/portico-delete001-e2e-20261017`. Não reutilizar uma pasta que já existia, não executar scripts de projetos reais.

1. Criar via operador dados artificiais (>50.000 arquivos vazios) **somente** na pasta nova; criar fora dela uma sentinela de projeto vizinho também artificial.
2. Na árvore-alvo, inserir um `.env` artificial e tentar `file.remove(recursive=true)` via MCP real: deve negar **antes de apagar qualquer arquivo**; contar novamente os itens e verificar `.env` intacto.
3. Remover a sentinela `.env` artificial de maneira autorizada, confirmar a árvore limpa e tentar `file.remove(recursive=true)` com **operation_id novo**. Deve retornar sucesso e remover inteiramente somente o alvo; confirmar sentinela irmã intacta.
4. Confirmar auditoria íntegra, escopo `/opt`, ausência de nova autorização geral e bloqueio fora de `/opt`. Não testar a segurança com um segredo real.
5. Limpar **somente** a pasta descartável identificada e previamente conferida; se houver `reconcile_required`, parar e investigar, sem retry cego.

## Critério de encerramento

Issue #6 fecha somente quando a fonte do `main`, o SHA instalado e o teste de host acima estiverem registrados com evidências não sensíveis. O projeto assume que exclusão de árvore sob escritores externos não é atomicamente reversível; erros parciais ficam explicitamente pendentes para reconciliação, nunca são considerados sucesso. Não prometer rollback de arquivos removidos sem snapshot.

Referências: [runbook Compose](UPGRADE-COMPOSE-ASYNC-SRV-IA.md), [Gate 2](ACCEPTANCE.md), [issue #8](https://github.com/josemirmoura/portico-infinity/issues/8) (cópia GGUF multi-GB, trabalho futuro).
