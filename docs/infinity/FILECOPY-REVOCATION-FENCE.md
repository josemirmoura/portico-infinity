# Filecopy: gate de revogação imediatamente anterior à publicação

**Escopo:** worker tipado binário do Pórtico Infinity; não altera Community e não habilita cópia MCP no ambiente operacional.

## Defeito e mitigação

A verificação da revogação externa já ocorria no início do job e em cada callback de progresso (chunks de 1 MiB). Entretanto, o último callback era seguido por verificações de integridade, `chown`, `chmod`, `fsync`, fechamento e só então `renameat2(RENAME_NOREPLACE)`. Se o operador revogasse a identidade nessa janela, o arquivo ainda poderia ser publicado.

A API `CopyFileStreamWithPreCommitFence` exige callback de autorização externo, consultado **depois do `fsync` do arquivo temporário e antes do `renameat2`**. O worker consulta novamente seu marcador privado root-owned de revogação. Marcador presente retorna `context.Canceled`; erro de consulta falha fechado e solicita reconciliação. Ambos impedem a publicação naquele ensaio e removem o temporário em saída normal. A função `CopyFileStream` anterior continua disponível para cópias de laboratório que não representem job autorizado por sujeito.

## Evidências e aceites

- Testes negativos determinísticos: revogação criada apenas na quinta consulta (após três chunks e verificação inicial), impossibilidade de publicar arquivo, status `cancelled`; indisponibilidade da consulta no mesmo momento retorna `reconcile_required`, sem destino publicado.
- Teste `securefs`: callback de fence chamado uma vez depois do último chunk, cancelamento ou falha de fence com ausência do destino e limpeza de temporários.
- Executar CI do PR, incluindo `go test ./internal/filecopyworker ./internal/securefs` e workflow descartável `infinity-filecopy-real-systemd-disposable`, na revisão exata antes de considerar aprovado.
- Nenhum dado em `/opt` do SRV-IA, servidor VPS, identidade real, DNS, WireGuard ou container operacional é usado.

## Limites explícitos

A consulta e a publicação **não formam uma transação atômica** com a revogação externa; uma revogação que aconteça depois da última consulta ainda pode concorrer com `renameat2`. O protocolo de suspensão externa deve criar primeiro o marcador durável, interromper unidades ativas e reconciliar estados, incluindo resultado de publicação incerto. O fechamento desse subgate depende de testes positivos/negativos e CI verde; não equivale a homologação operacional, refresh/login humano, retomada após perda de energia ou GO da v1.0.

**Rollback:** reverter o commit/PR desta API e do uso pelo worker, sem alterar jobs ou estado persistente no SRV-IA.
