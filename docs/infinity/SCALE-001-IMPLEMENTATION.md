# SCALE-001: workspaces massivos e arquivos LLM multigigabyte

Estado de execução (21/10/2026): [Issue #8](https://github.com/josemirmoura/portico-infinity/issues/8). Não ampliar escopo do Community nem mutar dados de produção da Nara.

## Etapa A: scanner escalável de shell (PR #16)

O perfil Infinity executa `shell.exec` apenas dentro de um workspace específico em `/opt`. Seu scanner anterior ainda percorria `ProtectedAliases` com o teto arbitrário de **50.000 entradas**, embora o workspace fosse legitimamente maior. Nova rotina `ProtectedAliasesStreaming`:

- Usa `openat2`, `RESOLVE_BENEATH`, `RESOLVE_NO_SYMLINKS`, `RESOLVE_NO_XDEV`, com varredura por `Readdirnames(256)` e sem materializar diretórios inteiros.
- Escaneia somente o workspace realmente montado no shell; não retorna a `/opt` global. Não abre links simbólicos nem atravessa bind mounts.
- Mascara nomes `.env*` e arquivos com múltiplos hardlinks. Não fixa limite de **entradas comuns**; limita **as máscaras** enviadas ao systemd a 1.024 e cerca de 64 KiB; excesso provoca falha fechada, não execução com proteção incompleta.
- Não altera a API `ProtectedAliases` legada do Community nem sua política.
- Testa uma árvore puramente sintética de 50.033 arquivos, hardlink, `.env` e sibling; não faz benchmark de 1 milhão de entradas em SRV-IA.

**Limitação consciente:** como toda varredura pré-execução, não é uma transação atômica contra alteração concorrente do workspace por outro escritor. O Broker continua obrigando `cwd` confinado e identidade shell não-root.

## Etapa B: cópia binária durável, ainda não implementada

O limite de leitura MCP textual (`DefaultMaxBytes=8 MiB`) é necessário e **não deve ser aumentado**. `file.hash` já usa streaming; `file.move` entre arquivos no mesmo root usa rename, sem recópia de dezenas de GiB. O `file.copy` atual faz `io.Copy` em memória constante, mas rejeita arquivos acima de **512 MiB** e é síncrono. **Remover apenas `MaxCopyBytes` não resolve**: um GGUF de 20–100 GiB pode exceder timeout de IPC/cliente e deixar operação incerta.

Implementação proposta para PR independente, com validação específica:

1. API `file.copy.start` com `source`, `destination`, `expected_sha256` opcional e `operation_id` obrigatório; resposta rápida com `job_id` determinístico. `file.copy.status` ou `job.status` informa `bytes_done`, `bytes_total`, `phase`, `sha256` final e erro, mas nunca dados binários.
2. Worker tipado **independente do ciclo de vida do Broker**, instalado root-owned exclusivamente via console do operador e sem comando shell fornecido pelo MCP. Confinar a `/opt`, com raízes do sujeito revalidadas; não permitir `docker exec` como atalho privilegiado. Persistir autorização, ID, origem/destino e progresso antes de iniciar.
3. Copiar em chunks pequenos com `pread/pwrite` ou `io.CopyBuffer`, atualizando progresso sem WAL por chunk individual. Verificar tamanho disponível e quotas configuráveis; default conservador, teto ajustável pelo operador. Nunca alocar o tamanho total do arquivo em RAM.
4. Destino temporário exclusivo no mesmo filesystem, modo restrito, symlink/mount/hardlink e ownership checados por descritores, `fsync` e **rename atômico apenas após verificar SHA-256 e propriedade**. Recusar origem mutável/sem estabilidade, destino ambíguo e path fora do root.
5. Cancelamento tipado e checkpoint validado, com retomada apenas após nova verificação de origem (inode, tamanho, mtime, hash/manifest). Se houve reinício ou dúvida na confirmação, estado `reconcile_required`, sem `retry` cego e sem substituir o destino final.
6. Retenção limitada de metadados e logs fora de `/opt`; cancelar sempre sujeito correto; não compartilhar job entre OAuth subjects.

**Gate mínimo futuro:** um arquivo esparso artificial >512 MiB em runner descartável, prova de chunking/timeout/restart/sha256; não copiar LLMs reais, não repetir os testes de 50.033 entradas.

## Etapa C: aceitação final

Somente após ambas as etapas implementadas e CI final aprovada, atualizar Broker/Gateway no SRV-IA com backup consistente, usando terminal SSH independente e conferindo `system.health`/auditoria. Evitar testes de carga longos na infraestrutura produtiva.

Issue #8 **permanece aberta** até a etapa B e o gate mínimo da C serem comprovados. Este documento não representa um recurso já entregue.
