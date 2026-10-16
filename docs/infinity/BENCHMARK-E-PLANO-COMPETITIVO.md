# Benchmark aplicado e plano competitivo — Pórtico Infinity

**Data:** 2026-10-16 · **Estado:** plano de engenharia público aprovado, implementação sujeita a testes. Não certifica release nem concede permissões adicionais.

O Pórtico Infinity é uma edição pública **experimental e separada** do Pórtico Community. Seu objetivo é permitir trabalho persistente de uma IA autenticada sobre projetos em `/opt` do Linux, com Broker local, política estática, auditoria e controle externo do proprietário. Não deve afetar instalações Community/Cloud.

## 1. Benchmark útil para o Infinity

| Referência | O que estudar | Condição para adotar |
| --- | --- | --- |
| [Open Machine](https://github.com/OpenMachineLabs/openmachine) | contratos de tarefas com estágio/evidência e recuperação por ID; leitura/escrita condicionada a hash | preservar a autorização do Broker; não tratar workspace de software como sandbox de SO |
| [DeskMCP](https://github.com/edmen12/deskmcp) | edições protegidas contra concorrência; Task Rooms; instalador e atualizador verificáveis | usar como inspiração de interface/controle, não ampliar poderes do agente |
| [chatgpt-mcp Linux](https://github.com/alexcodeplace/chatgpt-mcp) | `exec.start/status/output/cancel/list`, jobs duráveis, resultados incertos e rollback | integrar aos jobs atuais, evitando executor duplicado |
| [SSH MCP Bridge](https://github.com/shashikanth-gs/mcp-ssh-bridge) | arquivos transferidos por partes e verificação SHA-256; capacidade de múltiplos hosts | conservar a reautorização local; SSH não substitui o Broker |
| [Aegis SSH MCP](https://github.com/nextgridit-team/Aegis-SSH-MCP) | perfis mínimos e validação de comandos/argumentos | regex isolada não constitui isolamento de shell/arquivos |

**Limite da análise:** documentação e código selecionado, sem instalação e ensaio adversarial independente dos concorrentes. Referência estratégica adicional é mantida no canônico privado do ecossistema; este documento contém somente requisitos públicos específicos do Infinity.

## 2. Prioridade 0 — DELETE-001, operações seguras em árvores volumosas

**Incidente comprovado:** a exclusão de `/opt/nara-vui-lab` falhou com `sensitive-path scan exceeded safety limit`; o diretório ficou preservado. Ver [Issue #6](https://github.com/josemirmoura/portico-infinity/issues/6) e [Relato SRV-IA](RELATO-SRV-IA-2026-10-16.md).

`DefaultScanLimit = 50000` limita **número de entradas varridas**, não tamanho em bytes de arquivos. A implementação existente percorria raízes autorizadas como `/opt`, inclusive diretórios irmãos do alvo. Assim, um projeto pequeno pode falhar se outra árvore em `/opt` for muito grande.

**Requisitos de implementação:**
1. Resolver e validar o destino com primitivas seguras; verificar **a subárvore afetada** em vez de varrer indiscriminadamente `/opt`.
2. Projetar preflight seguro e de memória limitada; enfrentar árvores sintéticas >50 mil entradas sem simplesmente aumentar o teto numérico.
3. Manter bloqueios de `.env` e demais segredos, hardlinks ambíguos, symlinks, traversal, mount/bind mounts e permissões especiais. Testar corridas TOCTOU/mutações concorrentes.
4. Evitar exclusões parcialmente executadas silenciosamente: preflight completo antes da fase destrutiva; para mudanças posteriores usar estratégia e estados explícitos de invalidação/abort/quarentena quando necessária.
5. E2E primeiro em laboratório **descartável** com árvores grandes, casos negativos e auditoria; depois teste de pasta **sintética** no SRV-IA.
6. Não apagar `/opt/nara-vui-lab`, não executar seu `remove.sh` e não afetar sistemas produtivos como efeito incidental da correção.

**Aceite:** árvore elegível removida conforme contrato, operação proibida bloqueada antes de efeitos destrutivos, sem leitura de arquivos sensíveis, memória/tempo observados e auditoria íntegra.

## 3. Prioridade 0 — fechar homologação operacional

A correção do shell SHELL-001 teve execução real não-root confirmada no SRV-IA após atualização de 16/10; o [Issue #4](https://github.com/josemirmoura/portico-infinity/issues/4) está encerrado. A certificação Infinity permanece pendente dos demais gates documentados em [ACCEPTANCE.md](ACCEPTANCE.md).

Concluir: verificação de checkout/backups/overlays; reboot/reconexão; OAuth/revogação externa; testes adversariais para montagem, links, segredos e permissões; instalação limpa/update/rollback/remoção; E2E real e preservação de Community/Cloud.

**Gate de encerramento:** evidência de cada cenário com SHA/build/runtime e evento de auditoria correspondente; falhas específicas geram issue com reproducer e teste de regressão, sem auditorias abertas indefinidamente.

## 4. Prioridade 1 — arquivos e artefatos grandes (LLMs, datasets, binários)

**Atenção:** DELETE-001 e payload de arquivo têm causas distintas. Corrigir a varredura de 50 mil entradas **não** significa permitir automaticamente upload/download de modelos multi-GB no contexto do ChatGPT.

Propor contrato tipado, sujeito a ADR, política do Broker e testes:
- operação `stat`/hash; início de transferência; leitura/gravação por chunks ou stream com offset e cursor; status, cancelamento e commit;
- tamanho e quota independentes de limite de resposta MCP; SHA-256 e verificação final; arquivo temporário + publicação atômica quando suportada;
- retomada após desconexão sem duplicar chunks; ids associados a sujeito, nó, recurso e escopo; expiração e limpeza; evitar exposição de arquivos de segredo.
- quando necessário, operação de transferência diretamente entre armazenamento/nós autorizados, retornando apenas metadados/resultados via MCP.

Ensaios candidatos: 1 GiB e 10 GiB de dados artificiais, medição de memória/throughput, perda de conexão, falta de espaço, hash divergente, symlink/bind mount hostil e negação por política. Volume de teste e orçamento dependem de planejamento, não devem ser executados no SRV-IA produtivo sem preparo.

## 5. Prioridade 1 — continuidade de tarefas e UX

- Avaliar extensões aos jobs, locks, journal e idempotência **já existentes** antes de criar novo banco/worker.
- Vincular solicitações a ids duráveis, capturar aceite/etapas/evidências e permitir recuperar `unknown` sem executar operação duas vezes.
- Diferenciar jobs que sobrevivem à conversa de agentes que **decidem novas ações em background**; o último requer scheduler externo autorizado e não é consequência de acesso MCP persistente.
- Instalador em PT-BR/EN, verificação de health/auth/audit e rollback, sempre preservando controle do operador.
- Atualização de binários/código do plano de controle protegido em `/srv/portico-infinity` permanece exclusiva de operador autorizado, fora do teto `/opt`.

## 6. Interoperabilidade além do ChatGPT

Infinity deve ter **protocolo MCP agnóstico de IA**. Compatibilidade depende do host MCP, do transporte, do OAuth/OIDC e das ferramentas expostas. Destinos de ensaio: ChatGPT; Claude; Cursor; VS Code/Copilot; host MCP local de orquestrador como Hermes/Nara; cliente MCP de referência. Nenhum é automaticamente homologado.

Para cada combinação registrar versão, transporte, autenticação, descoberta, `system.info`, tentativa permitida, tentativa negada, auditoria e revogação. Preservar URLs e identidades diferentes entre instalações. UI de autorização externa continua obrigatória onde a UX do cliente não oferece prova confiável de proprietário.

## 7. Sequência e não-escopo

1. Corrigir DELETE-001 com regressões em runner efêmero.
2. Confirmar SHA/backup e terminar gates reais do Infinity.
3. Reavaliar, junto ao núcleo Community, contratos de transferência grande e tarefa durável.
4. Homologar clientes adicionais um de cada vez, com evidência; não publicar `supported` antes do E2E.

Não autorizado por este plano: root shell irrestrito, Docker host irrestrito, ampliar escopo além de `/opt`, modificar Community/Cloud, desproteger segredos ou automatizar exclusão de `nara-vui-lab`.

## Fontes internas

- [ACCEPTANCE.md](ACCEPTANCE.md)
- [ARCHITECTURE.md](ARCHITECTURE.md)
- [RELATO-SRV-IA-2026-10-16.md](RELATO-SRV-IA-2026-10-16.md)
- [Issue #6 — DELETE-001](https://github.com/josemirmoura/portico-infinity/issues/6)
