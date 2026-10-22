# Pórtico Infinity | Plano canônico de execução até a v1.0

**Natureza:** instrução operacional para agentes de engenharia. **Estado:** pré-lançamento; este plano não é evidência de funcionalidade implementada nem de homologação.

**Entrada para uma IA:** ler o `AGENTS.md` na raiz e este documento, consultar a `main`, as issues e as execuções de CI atuais, e **continuar o trabalho técnico pendente**. Não tratar documentação, histórico de chat ou checkbox isolado como prova de que algo funciona.

## 1. Autoridade, limites e fontes

- **Produto-alvo:** [Pórtico Infinity](https://github.com/josemirmoura/portico-infinity). Esta edição é independente do Pórtico Community/Cloud. Não alterar repositórios, instalações, políticas ou infraestrutura de outros produtos como efeito colateral.
- **Fonte de verdade técnica:** código da revisão exata, testes/CI executados naquele SHA e evidências E2E. Issues descrevem requisitos, decisões e falhas, mas podem ficar defasadas; reconciliar com código e commits atuais. Para segurança, a exigência mais restritiva prevalece até decisão fundamentada.
- **Fontes normativas:** [Issue #2](https://github.com/josemirmoura/portico-infinity/issues/2), especialmente os comentários de requisitos e de **GO/NO-GO** de 22/10/2026; [ACCEPTANCE.md](ACCEPTANCE.md), [FIRST_E2E.md](FIRST_E2E.md), [ROOT_PERSISTENTE.md](ROOT_PERSISTENTE.md), [ARCHITECTURE.md](ARCHITECTURE.md). Para escala: [#8](https://github.com/josemirmoura/portico-infinity/issues/8); remoção recursiva: [#6](https://github.com/josemirmoura/portico-infinity/issues/6).
- **Ambiente:** laboratório descartável para ensaios destrutivos, de escala, segurança e instalação limpa; SRV-IA e VPS são ambientes operacionais. **Não executar deploy, reboot, revogação real, limpeza, desinstalação ou testes destrutivos em SRV-IA/VPS sem autorização específica do proprietário.** Autorização prévia para atuar em `/opt` não equivale à aprovação de qualquer ensaio ou lançamento.
- **Separação:** plano de controle em `/srv/portico-infinity`, protegido e fora da autoridade da IA; teto de trabalho inicialmente `/opt`. Segredos, estado de identidade, revogação externa e auditoria precisam permanecer protegidos contra mutação pelo próprio agente.
- **Regra de release inviolável:** **NENHUMA** publicação, tag/release pública estável v1.0, anúncio de disponibilidade, ou promoção para produção antes de **teste pessoal do proprietário (UAT) seguido de autorização explícita de GO**. CI verde, E2E ou conclusão de código não substituem GO.

## 2. Contrato funcional-alvo da v1.0

1. **Instalação e conexão simples:** procedimento Linux assistido ou CLI, com pré-requisitos e diagnóstico compreensíveis; OAuth/HTTPS/MCP funcionando com cliente compatível, prova de `system.info` autenticado e registro na auditoria. Não alegar compatibilidade universal com qualquer LLM sem host MCP.
2. **Teto configurável no instalador:** sugerir `/opt` por padrão e permitir caminho absoluto alternativo indicado pelo proprietário, validado de forma segura. Controlador, segredos e políticas devem continuar fora do teto; rejeitar sobreposições, aliases e caminhos inseguros. O modo Infinity privilegiado atual pode exigir `/opt`: **implementar e testar** a mudança, não declará-la pronta por existir escopo customizável no Standard.
3. **Autorização duradoura e revogável:** acesso a todas as profundidades de subpastas e arquivos novos dentro do teto concedido, com operações de leitura, criação, edição e exclusão tipadas autorizadas. Sem prazo de expiração da **política estática do proprietário**, mas com revogação externa efetiva, inclusive suspensão segura de novas operações e tratamento dos jobs em curso. Não confundir isso com duração de tokens OAuth, sessões, chamadas ou confirmação externa da plataforma.
4. **Segurança silenciosa:** OAuth por identidade, TLS, policy no Broker, confinamento de caminhos, proteção contra traversal, links, mounts, TOCTOU, acesso a segredos, prompt injection e efeitos indiretos de scripts em diretórios graváveis consumidos por serviços privilegiados. Evitar interrupções repetitivas para operações já permitidas; exigir intervenção fora do MCP para mudança de autoridade ou riscos excepcionais. Nenhum shell root irrestrito, `sudo` genérico ou Docker host root por padrão.
5. **Arquivos e escala:** todos os tipos de extensões sem filtro arbitrário; grandes binários (incluindo GGUF, modelos e backups) processados por stream/chunks, com consumo de memória limitado, integridade verificável, progresso, cancelamento, timeouts, retomada segura e limites de recursos documentados. Não despejar gigabytes no contexto MCP, nem prometer tamanho matematicamente ilimitado. Evitar teto global de 50 mil entradas na varredura de árvores.
6. **Trabalho operacional auditável:** histórico consultável de tarefas e jobs, com identidade, ação, início/fim, duração, estado/progresso, resultado, cancelamento, correlação e erros. Não vazar credenciais, prompts sensíveis ou conteúdos protegidos nos logs; manter trilha íntegra e exportável.
7. **Ciclo de vida completo:** instalador, atualização/rollback, parada de emergência, **desativação reversível** com preservação de dados, e **desinstalação segura própria do Infinity**. Inventário, simulação/dry-run e confirmação explícita para perdas irreversíveis. Nunca apagar projetos do usuário no teto nem afetar Community, Cloud ou serviços não pertencentes ao Infinity. O `scripts/remove.sh` herdado não serve para desinstalar Infinity; o emergency-stop não equivale a uninstall.
8. **Experiência ponta a ponta:** falhas recuperáveis, estados claros, comandos/documentação PT-BR, operação normal sem microaprovações redundantes e logs suficientes para diagnóstico. A proteção no backend não deve bloquear trabalho legítimo silenciosamente: erros precisam ser explicáveis e tratáveis.

Este é o **contrato desejado**, sujeito a implementação e prova objetiva. Recursos comprovados parcialmente não tornam a v1.0 aceita.

## 3. Ordem de execução para o agente

### Etapa A | Diagnóstico incremental, sem refazer trabalho aprovado

- Ler `AGENTS.md`, este plano, `ACCEPTANCE.md`, issues abertas relacionadas e histórico recente de PRs/CI. Verificar o SHA atual; listar entregas comprovadas, falhas reproduzíveis e bloqueios de produção, distinguindo **feito / parcial / pendente / não testado**.
- Preservar evidências existentes do E2E Docker e OAuth reais em ambiente descartável e da conexão MCP no SRV-IA. Não repetir baterias inteiras de teste somente por insegurança subjetiva. Reexecutar testes relevantes quando código ou ambiente relacionado mudar.
- Selecionar o próximo bloqueador v1.0 por risco/impacto/dependência. Trabalhar em mudanças pequenas, coerentes e revisáveis; não deixar pendências em forma de sugestões genéricas quando é possível implementá-las.
- Critérios permanentes: testes negativos de segurança, regressão dos fluxos afetados, CI no SHA exato, rollback identificável e documentação alinhada ao código. Não alterar Community/Cloud nem eliminar proteções para fazer os testes passarem.

### Etapa B | Fechar lacunas da v1.0

Priorizar, segundo dependências e estado atual verificado: teto configurável do Infinity; revogação externa efetiva e reconexão pós-reboot; isolamento de plano de controle e vias indiretas de privilégio; fluxos de arquivos grandes e árvores extensas (#8 e #6); histórico de tarefas/progresso; instalação/upgrade/rollback; desativação e desinstalação isolada; UX/documentação e diagnóstico. Abrir ou atualizar issues objetivas, com critérios de aceite verificáveis, quando necessário.

Cada entrega deve conter: o defeito ou requisito, implementação, teste positivo/negativo, comando/workflow de verificação, resultado do SHA testado, riscos residuais e links. Não marcar como concluído só por escrever documentação.

### Etapa C | E2E completo em ambiente efêmero

Executar uma **instalação limpa real** em VM/runner/container devidamente isolado, com identidades sintéticas e dados descartáveis. Cobrir no mínimo:

- Configuração de `/opt` e teto alternativo; autenticação TLS/OAuth e MCP; autorização duradoura + negação fora do escopo.
- CRUD, chmod/chown conforme política, profundidade elevada, formatos variados, arquivos multigigabyte esparsos e grandes árvores; medir custo e limites reais sem consumir volume de produção.
- Controles negativos: traversal, symlink/hardlink, mount, corrida, secrets, identidade inválida, prompt injection e caminhos indiretos de escalada; auditoria verificável.
- Progresso, cancelamento, jobs interrompidos/repetidos, integridade SHA, timeout e recuperação; revogação externa, reconexão e reboot do ambiente de ensaio.
- Atualização e rollback; desativação preservando estado; desinstalação completa que **preserva os arquivos do teto** e demais instalações/serviços.

Publicar matriz com PASS/FAIL/SKIPPED e evidências por comando/CI/SHA, explicando tudo que o laboratório não consegue demonstrar. Corrigir falhas bloqueadoras e repetir os testes afetados. E2E aprovado não significa instalação operacional autorizada.

### Etapa D | Pacote de aceite ao proprietário

Entregar **release candidate identificada por SHA**, changelog, instruções reproduzíveis de instalação/upgrade/rollback/uninstall, inventário de dados preservados, resultados E2E, limites, problemas conhecidos, riscos residuais e roteiro curto de teste humano. Não usar a palavra "homologado" para itens sem evidência.

### Etapa E | UAT controlado pelo proprietário

Solicitar autorização específica para o ambiente/instância do teste real. O proprietário verifica conexão com ChatGPT ou outro cliente MCP escolhido, tarefas comuns e longas, logs/progresso, revogação, recuperação, UX e desinstalação conforme janela planejada. Registrar achados; corrigir e repetir verificações diretamente afetadas. **Não interpretar o aceite de uma operação isolada como aceite para lançar o produto.**

### Etapa F | Gate humano GO/NO-GO

Apresentar evidências e solicitar decisão expressa **GO ou NO-GO** do proprietário. Até haver GO verificável, manter pré-lançamento e **não criar release/tag pública estável v1.0**, não promover/atualizar ambiente operacional automaticamente. Em NO-GO, corrigir e reapresentar para novo UAT/decisão. Mesmo com GO, seguir o checklist final de CI, assinatura/empacotamento e publicação vinculado ao SHA aprovado.

## 4. Fora do escopo bloqueador da v1.0

**AI-BRIDGE-001 [#21](https://github.com/josemirmoura/portico-infinity/issues/21):** ChatGPT/IA remota delegar a agente/LLM local, e agente local iniciar pedido à IA remota, com fila, correlação, orçamento e autorização. É uma evolução **pós-v1.0**, sem promessa de escrever espontaneamente numa conversa aberta do ChatGPT. Pode evoluir arquitetura/documentação separadamente, mas **não atrasar a v1.0**.

Também ficam fora do perfil padrão: root global irrestrito, `sudo` amplo, Docker host irrestrito e exigência de compatibilidade não testada com todos os provedores MCP.

## 5. Convenção de continuidade, evidência e relato

Ao receber a instrução **"Leia o GitHub do Pórtico Infinity e siga o que foi comandado"**, o agente deve:

1. Abrir a `main`, seguir `AGENTS.md` → este documento → evidências/issues, sem pedir ao proprietário que repita os requisitos.
2. Identificar a **próxima tarefa executável** e implementá-la dentro das permissões efetivamente disponíveis. Não confundir intenção do usuário com autorização de uma ferramenta/plataforma ou com acesso à máquina.
3. Rodar testes relevantes e registrar resultados; atualizar docs/issues com links verificáveis; declarar explicitamente o que ainda não foi executado.
4. Continuar para o próximo bloqueador enquanto houver acesso, tempo e permissões adequados; em bloqueio real, apontar precisamente a ação do proprietário necessária, sem inventar sucesso nem pedir novamente informações já decididas.
5. Encerrar cada sessão com **SHA/links**, entregas verificadas, testes e falhas, pendências priorizadas, próximo passo e eventual autorização específica requerida. Não declarar a v1.0 concluída antes do gate F.

**Atalho para o usuário:** "Leia o GitHub do Pórtico Infinity e siga o que foi comandado." O roteiro reside neste repositório, não no histórico de uma conversa.
