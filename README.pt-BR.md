# Pórtico Infinity ♾️

**Edição pública experimental do Pórtico Community que permite delegar à IA, via MCP, acesso operacional permanente a um servidor Linux.**

> ⚠️ **PRÉ-LANÇAMENTO.** O perfil de operações privilegiadas permanentes limitadas a `/opt` ainda precisa ser configurado e validado; o root global será separado e opt-in. O código herdado do Community está disponível, mas a edição Infinity já teve integração real **parcial** no SRV-IA, mas ainda não foi homologada para uso geral ou instalações de terceiros.

[English](README.md) · [Community](https://github.com/josemirmoura/portico-community)

### Retomar o desenvolvimento com uma IA

Basta dizer: **"Leia o GitHub do Pórtico Infinity e siga o que foi comandado."** O agente deve começar por **[AGENTS.md](AGENTS.md)** e pelo **[Plano canônico de execução v1.0](docs/infinity/EXECUTION_PLAN.md)**, consultar issues, PRs e CI atuais e continuar a próxima tarefa comprovadamente pendente. **A publicação da v1.0 exige teste pessoal e autorização expressa do proprietário.**

## Objetivo

O proprietário poderá autorizar **uma única vez**, sem prazo de expiração:
- leitura, criação, edição e trabalho em `/opt`, inclusive novos projetos;
- operações administrativas **tipadas e restritas a `/opt`** pelo Broker, como `chmod` e `chown`, sem shell root genérico, após configuração e E2E;
- como opção avançada distinta, root global persistente, desativado por padrão e sujeito a uma decisão explícita do proprietário.

A autenticação HTTPS/OAuth continua obrigatória. A autorização não implica sessão infinita no ChatGPT nem elimina confirmações do cliente, limites de duração de jobs ou a validade dos tokens OAuth.

**⚠️ Operações sobre `/opt` também apresentam riscos**, especialmente se a pasta contiver configurações, executáveis ou arquivos que algum serviço privilegiado utilizará. Root global alcança todo o host, inclusive fora de `/opt`. O risco inclui perda de dados, comprometimento de credenciais, alterações nos serviços e execução de instruções maliciosas encontradas em arquivos. O desligamento de emergência e auditoria devem ter componentes externos à máquina administrada.

## Diretórios separados

- **`/srv/portico-infinity`**: instalação e plano de controle, com acesso restrito a root.
- **`/opt`**: projetos e arquivos administrados pela IA.

O diretório de instalação fica propositalmente fora da área delegada. Esse isolamento ainda exige testes E2E e revisão de permissões efetivas, incluindo contas de serviço.

## Avanço da integração OAuth

O E2E com **ZITADEL e token OAuth reais em ambiente descartável** foi aprovado ([GitHub Actions](https://github.com/josemirmoura/portico-infinity/actions/runs/38893160988)). O gateway e o Broker respeitaram o escopo `/opt` e negaram operações globais. Para instalações Infinity, usar o overlay `compose.infinity-auth.yaml` com rótulos e volumes próprios. Há registro de sessão MCP autenticada real no SRV-IA e execução não-root após a correção SHELL-001. A homologação pública completa de OAuth/revogação e dos demais gates permanece pendente; veja o [relato de campo](docs/infinity/RELATO-SRV-IA-2026-10-16.md).

## Estado da implementação

- ✅ Repositório público independente e histórico `main` do Community importado.
- ✅ Licença Apache-2.0 e atribuição original preservadas.
- ✅ Especificação do perfil permanente documentada.
- ✅ Entrada experimental `scripts/install-infinity.sh`, com política de operações de arquivo privilegiadas em `/opt` e controlador protegido em `/srv/portico-infinity`.
- ✅ `--local-only` não inicia contêineres; instalador de prévia antigo desativado por segurança.
- ✅ E2E Docker/OAuth em laboratório e testes parciais de arquivos e shell não-root no SRV-IA.
- ⏳ Homologação restante: revogação externa, reboot/reconexão, recuperação, instalação completa e testes adversariais; root global somente como modo avançado separado.
- ⏳ Correção de DELETE-001 para grandes árvores ([Issue #6](https://github.com/josemirmoura/portico-infinity/issues/6)).
- ⏳ Auditoria, revogação externa e E2E real no SRV-IA.

O instalador histórico `scripts/install.sh` ainda corresponde ao comportamento do Community. Evite instalá-lo como se o Infinity estivesse pronto.

## Documentação

- [Benchmark competitivo e plano de engenharia multi-IA](docs/infinity/BENCHMARK-E-PLANO-COMPETITIVO.md)
- [Arquitetura e fronteiras](docs/infinity/ARCHITECTURE.md)
- [Root persistente](docs/infinity/ROOT_PERSISTENTE.md)
- [Critérios de aceite E2E](docs/infinity/ACCEPTANCE.md)
- [Roteiro do primeiro E2E e instalação isolada](docs/infinity/FIRST_E2E.md)
- [Parada de emergência exclusiva do Infinity](docs/infinity/EMERGENCY_STOP.md)
- [Origem e detalhes da importação](docs/infinity/IMPORT.md)

## Interoperabilidade com outras IAs

O Infinity busca atender **qualquer cliente ou orquestrador com suporte MCP compatível**, sem dependência conceitual do ChatGPT. Claude, Cursor, VS Code/Copilot e hosts locais (por exemplo, Hermes/Nara) são alvos de verificação futura por versão, transporte, OAuth e operação auditada. Um LLM isolado precisa de um aplicativo/host capaz de consumir ferramentas MCP. A documentação não deve chamar clientes não testados de homologados.

## Autoria e licença

Projeto de Josemir Moura, derivado do [Pórtico MCP Community](https://github.com/josemirmoura/portico-community), sob a licença **Apache-2.0**. Manter créditos, LICENSE e NOTICE aplicáveis. O Community e seu plano de testes E2E não são alterados por este desenvolvimento.

## Contrato herdado de instalação (ainda sem homologação Infinity)

O instalador herdado precisa validar HTTPS/OAuth, conectar o cliente MCP **ChatGPT Web**, executar uma chamada real autenticada `system.info` e conferir a auditoria do Broker **antes** de emitir `INSTALLATION COMPLETE`. O Infinity exigirá ainda os testes específicos das operações privilegiadas no `/opt` e da revogação externa. O contrato do Community, isoladamente, não homologou a nova edição.

Documentação herdada em português:

- [Quick Start / Início rápido](docs/quick-start.pt-BR.md)
- [First installation acceptance / Aceite de primeira instalação](docs/first-install-acceptance.pt-BR.md)
- [Installer flow / Fluxo do instalador](docs/installer-flow.pt-BR.md)
- [Installation contract / Contrato de instalação](docs/installation-contract.pt-BR.md)
- [ChatGPT integration / Integração com ChatGPT](docs/chatgpt-integration.pt-BR.md)
