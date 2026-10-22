# Infinity SCALE-001: contrato de execução durável de cópias GGUF

**Implementação inicial:** [Issue #8](https://github.com/josemirmoura/portico-infinity/issues/8). A API MCP `file.copy.start` e o acompanhamento via `job.status`/`job.cancel` estão integrados em código, sujeitos a flag de operador e a homologação operacional ainda incompleta.

## Teto físico selecionado pelo operador (evolução v1.0)

Para instalações novas com escopo privilegiado alternativo, o Broker obtém o teto físico exclusivamente de `VPS_AGENT_PHYSICAL_SCOPE_ROOT` (controlado pelo operador) e persistir o valor no manifesto root-only do job como `scope_root`. O trabalhador confere que origem e destino permanecem abaixo dele, e a unidade systemd confina `ReadWritePaths` ao **mesmo teto** e ao diretório de estado root-only. A compatibilidade com manifestos anteriores sem `scope_root` usa `/opt`, mantendo jobs legítimos anteriores interpretáveis sem ampliar permissões.

Caminhos relativos, escapes, diretórios críticos (incluindo `/srv`, `/etc`, `/var` e `/root`), `.env*` e caminhos de origem/destino fora do teto são recusados. O valor do root jamais é fornecido como argumento MCP. O executável instalado no host continua sob decisão independente do operador, **desabilitado até habilitação explícita** e aceite operacional.

**Evidências em 22/10/2026:** o motor foi provado com fonte esparsa de 2.147.487.744 bytes, SHA-256, cancelamento e replay negado em runner descartável (PR #28, 21/21 checks); o worker foi executado por systemd real com confinamento, SHA, manifesto malicioso recusado e SIGTERM com checkpoint `cancelled` (PR #30, 20/20 checks). As PRs #28/#30 não comprovam sozinhas o percurso MCP, o reboot, a retomada em offset nem a ativação em produção. A feature permanece opt-in e **OFF no SRV-IA**.

## Evidência rastreável de laboratório

- [PR #28](https://github.com/josemirmoura/portico-infinity/pull/28), [E2E de arquivo >2 GiB](https://github.com/josemirmoura/portico-infinity/actions/runs/39825831727), merge `5c4da681903512cd747d9ba328da14ae0def0bfe`: SHA em origem/destino, bytes/progresso, cancelamento após 128 MiB, replay negado.
- [PR #30](https://github.com/josemirmoura/portico-infinity/pull/30), [E2E systemd real](https://github.com/josemirmoura/portico-infinity/actions/runs/39835994798), merge `07e401d8092782d917f055fea150c1a011e763f4`: cópia de 128 MiB através de unidade systemd, rejeição de `.env`, cancelamento de uma fonte esparsa de 8 GiB e bloqueio de reexecução.
- [PR #31](https://github.com/josemirmoura/portico-infinity/pull/31), [E2E Docker + MCP + systemd](https://github.com/josemirmoura/portico-infinity/actions/runs/39837703649), merge `7a118efbc5c046cf834bedab142bd376efe167b1`: cliente MCP com **bearer estático de laboratório** → Gateway → Broker → systemd host → worker. Cópia de 128 MiB, SHA, `job.status`, idempotência, recusa fora do teto e cadeia de auditoria; **17/17 checks CI** no SHA `5b48523351587efcf297aa7633b6fe2f352e1c33`.
- **Limite das provas acima:** dados sintéticos e hosts descartáveis; OAuth real foi comprovado em workflow independente e ainda não combinado com o worker em um único ensaio. Reinício de host, revogação de jobs ativos, recuperação/reconciliação e UAT do proprietário permanecem obrigatórios.

## Domínios de confiança

| Camada | Acesso e responsabilidade |
|---|---|
| ChatGPT → OAuth MCP | Solicita cópia tipada e recebe ID do job; não recebe shell, root, segredos ou bytes binários |
| Gateway/Broker | Confirma sujeito OAuth, política do teto selecionado, autorização por origem/destino, quota e intenção durável antes de despachar |
| Banco SQLite do Broker | Mantém job/subject/operation_id, início, terminalidade e referências para reconciliação |
| Manifesto root-only em `/srv/portico-infinity/state/filecopy-jobs` | Contém campos estritos `job_id,subject,source,destination,scope_root,max_bytes,expected_sha256`, com 0600 |
| Executável independente `portico-filecopy-worker` | Recebe **apenas job_id**, verifica manifesto, executa `CopyFileStream`, escreve progresso e recebe SIGTERM |
| systemd host | Controla job tipado e independente de reinício do Broker; só o operador instala/atualiza o executável |
| Relatório de progresso root-only | `job_id.progress.json` contém fase, bytes feitos/total, SHA-256 final, horário UTC; não guarda conteúdo de arquivo |

O controlador `/srv` e o executável host fora do teto selecionado **não são modificáveis pelo MCP**. O worker recebe acesso de leitura/escrita apenas ao teto fixado pelo operador para aquela instalação. Ele recusa caminhos fora do escopo, traversal, symlinks, hardlinks, `.env*`, origem=destino e cota inexistente/superior a 1 TiB.

## Revogação externa de cópias longas (implementação candidata, sujeita a CI)

A desativação de um usuário no ZITADEL bloqueia novas solicitações OAuth, mas **não cancela automaticamente** um worker `systemd` despachado antes da revogação. O fluxo técnico acrescenta um *fence persistente* por sujeito, criado pelo operador fora do MCP, e cancelamento tipado dos jobs daquele sujeito.

- O marcador `revoked-subject-<sha256(UTF8(subject))>.marker` reside em `/srv/portico-infinity/state/filecopy-jobs` (no Broker montado em `/var/lib/vps-agent/filecopy-jobs`). Nunca é escrito dentro do teto delegado ou por um pedido MCP; verificação de dono, tipo, permissões e hardlinks falha fechada.
- O Broker nega `file.copy.start` de sujeitos marcados antes de criar novos manifestos. O worker também consulta o marcador antes de iniciar e em cada callback de progresso do fluxo binário, abortando com estado durável `cancelled`, quando a interrupção é confirmada.
- Para parar operações em andamento de forma determinística, o console externo executa `scripts/infinity-revoke-filecopy-subject.py` com o sujeito exato e confirmação específica. O utilitário primeiro persiste o fence, depois identifica manifestos root-owned vinculados à identidade, para somente unidades `vps-agent-filecopy-<32hex>.service` e lê o progresso durável. **Resultados duvidosos são relatados como incertos**, exigindo investigação externa. Nenhuma cópia de usuário é excluída por essa rotina.
- A operação é idempotente. O marcador permanece ativo durante reinícios; sua remoção ou restauração de privilégios requer protocolo de operador separado. Revogação de OAuth e sessões/refresh tokens deve acontecer **também por canal externo ao MCP**, com verificação de token antigo negado. O marcador é específico de cópias longas, não representa revogação geral do Gateway.
- O commit atômico do destino pode competir com o cancelamento. Se já estiver publicado, o resultado pode ser `done`; se os registros forem contraditórios, permanecer em `reconcile_required`. Nenhuma camada pode prometer que `systemctl stop` sempre significa que o destino não foi publicado.

Exemplo de operação independente, **somente após revisão do sujeito** e em instalação autorizada (ainda não executar no SRV-IA):

```bash
cd /srv/portico-infinity
sudo python3 scripts/infinity-revoke-filecopy-subject.py --subject 'SUJEITO-OAUTH-EXATO' --dry-run
sudo python3 scripts/infinity-revoke-filecopy-subject.py --subject 'SUJEITO-OAUTH-EXATO' --revoke --confirm REVOKE-INFINITY-FILECOPY-SUBJECT
```

**Limites do gate atual:** sem prova, por esta alteração isolada, de revogação interativa/refresh tokens no ZITADEL real, reboot de host ou cancelamento de um worker de produção. Exigir CI positivo/negativo no SHA, E2E OAuth + cópia longa combinado em runner isolado, e UAT antes de considerar o requisito integral concluído.

## Ciclo do job

`validated → manifest_persisted → job_sqlite_persisted → systemd_dispatch_pending → running → done | failed | cancelled | reconcile_required`

- **Preparação:** Broker deve validar permissão estática ou delegação, confirmar sujeito e gerar ID determinístico a partir de operação+identidade. Manifesto root-only precisa estar persistido **antes** do dispatch.
- **Despacho:** somente `systemd-run` de binário fixo root-owned, com flags pré-definidas, sem comando do usuário. Um erro entre envio e confirmação torna o dispatch **incerto**: não repetir por novo operation_id.
- **Acompanhamento:** Worker mantém `flock` exclusivo, realiza cópia binária em chunks de 1 MiB, salva progresso em intervalos (64 MiB/1 s) usando arquivo temporário, `fsync` e rename. A persistência de progresso deve ocorrer antes de seguir copiando. O Broker cruza o estado do systemd com o progresso, nunca inferindo sucesso de `inactive/dead` isolado.
- **Conclusão:** arquivo temporário no mesmo filesystem, SHA-256 opcional confirmado, `fchown` validado, `fsync` do arquivo, `renameat2(RENAME_NOREPLACE)` e `fsync` do diretório. Só depois sucesso; nenhuma sobrescrita.
- **Cancelamento:** operação tipada do dono OAuth provoca parada da unidade; SIGTERM cancela contexto do worker, que tenta salvar estado terminal. Cancelamento pode competir com publicação, portanto requer reconciliação se resultado estiver incerto.
- **Reinício:** estado `running` no arquivo ou unidade ausente após reboot **não** autoriza reexecutar a cópia; o operador inspeciona destino/manifesto/SQLite e decide retomar somente com protocolo futuro que confirme origem estável e checkpoint íntegro.

## Estado de entrega e limitações

O Gateway/Broker já incluem `file.copy.start` e `job.status`/`job.cancel`, manifestos duráveis e despacho systemd tipado, com recurso **desligado por padrão**. O trabalhador host instalado depende do procedimento externo do operador; uma conexão MCP pode não expor a ferramenta até atualizar seu catálogo.

A transferência interrompida não possui retomada em offset comprovada: estados incertos exigem reconciliação por operador, sem reexecução cega. A v1.0 permanece bloqueada pelos E2E, pelo aceite real dos arquivos grandes e pela aprovação humana. **Não instalar uma unidade systemd a partir deste documento sozinho.**

## Aceite mínimo futuro

Próximo gate ainda necessário: validar OAuth → Broker → systemd com arquivo >512 MiB sob cota explícita no mesmo ensaio, e a recuperação/reconciliação após interromper/reiniciar Broker/host. Os testes unitários e E2E segmentados acima devem ser reaproveitados, sem repetições indiscriminadas. Sem copiar modelos reais da Nara, sem leitura MCP de bytes binários e sem modificar Community. Ativar no SRV-IA somente após o operador verificar backup e SHA do worker, como etapa independente do MCP.
