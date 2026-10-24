# Infinity | Limpeza durável das unidades Compose

**Estado:** implementação candidata, sujeita a CI e UAT. Nenhuma versão operacional SRV-IA foi atualizada por este documento.

## Segurança do ciclo de vida

O Pórtico executa operações `compose.pull/up/down` previamente autorizadas em unidades `systemd` transitórias `Type=oneshot,RemainAfterExit=yes`. A retenção do estado final evita perder o resultado, mas unidades concluídas e não consultadas podem permanecer carregadas indefinidamente.

Nesta evolução, o Broker Infinity usa um **outbox SQLite `compose_unit_cleanup`**. A conclusão comprovada (estado `done/failed` e `exit_code`) e um recibo `pending` são gravados na **mesma transação**. Se o banco falhar, nenhuma das duas mudanças é confirmada. A operação Compose original nunca é refeita pelo reconciliador.

A rotina protegida do Broker, habilitada apenas com `VPS_AGENT_INFINITY_PROTECTED_ROOT`, consulta até **64 jobs Compose ativos** por rodada com paginação SQL por `(created_at,job_id)` e avança o cursor para evitar starvation. Despachos novos e execução de contêineres não fazem parte desta rotina. Quando a evidência final da unidade não estiver disponível após prazo mais um minuto, o job é marcado `reconcile_required` e continua bloqueando operações posteriores nesse projeto pela cerca implementada no PR #74.

Em uma rodada separada, o Broker consulta até **64 recibos `pending`**. A única ação de host autorizada para o limpador é `systemctl stop` seguida de `systemctl reset-failed` sobre nomes `vps-agent-compose-` + 32 hexadecimais + `.service`, provenientes do banco root-owned. Antes da tentativa ele verifica que a unidade não está executando o processo Compose, e depois exige `LoadState=not-found`. Se o resultado for diferente ou `systemctl` falhar, o recibo permanece pendente para recuperação, com contador de tentativas; não há afirmação de limpeza bem-sucedida.

**Efeito importante:** `systemctl stop` retira somente a unidade transitória do comando `docker compose`; a rotina **não** envia `docker compose down`, não para contêineres de aplicações e não altera as permissões da pasta do projeto. Se a unidade estiver `active/running` ou em estado ambíguo, o limpador se recusa a pará-la.

## Fronteiras e recuperação

- Configurações Community e Cloud não passam a executar o reconciliador Infinity por efeito colateral.
- Jobs Compose históricos já terminados antes da implantação **não ganham recibo automaticamente**; revisar a trilha do sistema antes de qualquer limpeza manual.
- Nem uma unidade ausente nem um recibo pendente provam sucesso do comando original. O estado e o código de saída persistidos prevalecem; `reconcile_required` significa desconhecido.
- Em fila arbitrariamente grande, 64 itens por rodada é limite de carga, não SLA de latência. A rotina Compose usa contexto temporizado próprio, independente da varredura de jobs shell.
- A retomada após **reboot real do host**, o UAT pessoal do SRV-IA, a restauração distribuída, autenticação/revogação humana e a decisão GO/NO-GO continuam gates separados.
- A implementação trata apenas unidades criadas após a funcionalidade estar habilitada; não faz `docker down`, não revoga autorizações nem publica release.

## Testes mandatórios

Go/race: outbox atômico, falha/retentativa, reabertura SQLite, não sobrescrever estado terminal, não agir em outros tipos de job, seleção paginada, nomes de unidades válidos/inválidos e stub de falha de `systemctl`. CI privilegiado em runner efêmero: unidade `systemd` sintética com prefixo Compose, executando somente `/usr/bin/true`; comprovar persistência da conclusão, aposentadoria da unidade, reconsulta e resultado preservado. **Nenhum contêiner operacional é usado neste teste**.

Relacionadas: [Issue #12](https://github.com/josemirmoura/portico-infinity/issues/12), [Issue #2](https://github.com/josemirmoura/portico-infinity/issues/2), [COMPOSE_RESOURCE_FENCE.md](COMPOSE_RESOURCE_FENCE.md).
