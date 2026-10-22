# Infinity SCALE-001: contrato de execução durável de cópias GGUF

**Implementação inicial:** [Issue #8](https://github.com/josemirmoura/portico-infinity/issues/8). Esta documentação descreve o **executável interno e seus invariantes**; **não significa que haja uma ferramenta MCP disponível**.

## Domínios de confiança

| Camada | Acesso e responsabilidade |
|---|---|
| ChatGPT → OAuth MCP | Solicita cópia tipada e recebe ID do job; não recebe shell, root, segredos ou bytes binários |
| Gateway/Broker | Confirma sujeito OAuth, política de `/opt`, autorização por origem/destino, quota e intenção durável antes de despachar |
| Banco SQLite do Broker | Mantém job/subject/operation_id, início, terminalidade e referências para reconciliação |
| Manifesto root-only em `/srv/portico-infinity/state/filecopy-jobs` | Contém campos estritos `job_id,subject,source,destination,max_bytes,expected_sha256`, com 0600 |
| Executável independente `portico-filecopy-worker` | Recebe **apenas job_id**, verifica manifesto, executa `CopyFileStream`, escreve progresso e recebe SIGTERM |
| systemd host | Controla job tipado e independente de reinício do Broker; só o operador instala/atualiza o executável |
| Relatório de progresso root-only | `job_id.progress.json` contém fase, bytes feitos/total, SHA-256 final, horário UTC; não guarda conteúdo de arquivo |

O controlador `/srv` e o futuro executável host fora de `/opt` **não são modificáveis pelo MCP**. O worker proposto tem acesso a arquivos de origem/destino exclusivamente sob `/opt`. Ele recusa caminhos fora do escopo, traversal, symlinks, hardlinks, `.env*`, origem=destino e cota inexistente/superior a 1 TiB.

## Ciclo do job

`validated → manifest_persisted → job_sqlite_persisted → systemd_dispatch_pending → running → done | failed | cancelled | reconcile_required`

- **Preparação:** Broker deve validar permissão estática ou delegação, confirmar sujeito e gerar ID determinístico a partir de operação+identidade. Manifesto root-only precisa estar persistido **antes** do dispatch.
- **Despacho:** somente `systemd-run` de binário fixo root-owned, com flags pré-definidas, sem comando do usuário. Um erro entre envio e confirmação torna o dispatch **incerto**: não repetir por novo operation_id.
- **Acompanhamento:** Worker mantém `flock` exclusivo, realiza cópia binária em chunks de 1 MiB, salva progresso em intervalos (64 MiB/1 s) usando arquivo temporário, `fsync` e rename. A persistência de progresso deve ocorrer antes de seguir copiando. O Broker cruza o estado do systemd com o progresso, nunca inferindo sucesso de `inactive/dead` isolado.
- **Conclusão:** arquivo temporário no mesmo filesystem, SHA-256 opcional confirmado, `fchown` validado, `fsync` do arquivo, `renameat2(RENAME_NOREPLACE)` e `fsync` do diretório. Só depois sucesso; nenhuma sobrescrita.
- **Cancelamento:** operação tipada do dono OAuth provoca parada da unidade; SIGTERM cancela contexto do worker, que tenta salvar estado terminal. Cancelamento pode competir com publicação, portanto requer reconciliação se resultado estiver incerto.
- **Reinício:** estado `running` no arquivo ou unidade ausente após reboot **não** autoriza reexecutar a cópia; o operador inspeciona destino/manifesto/SQLite e decide retomar somente com protocolo futuro que confirme origem estável e checkpoint íntegro.

## O que esta PR NÃO entrega

A versão inicial do worker não registra `file.copy.start` no Gateway, não cria manifesto via Broker, não instala binário host e não integra `job.status`/cancelamento. A habilitação deve continuar desativada até que essas dependências estejam implementadas, cobertas por CI e homologadas com um projeto sintético. **Não instalar uma unidade systemd a partir deste documento sozinho.**

## Aceite mínimo futuro

Uma rodada curta em runner descartável com arquivo esparso acima de 512 MiB: cópia com hash correto, status/progresso, cancelamento sem publicação, parada/retorno do Broker e reconciliação de estado incerto. Sem copiar modelos reais da Nara, sem leitura MCP de bytes binários e sem modificar Community. Ativar no SRV-IA somente após o operador verificar backup e SHA do worker, como etapa independente do MCP.
