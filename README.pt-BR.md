# Pórtico Infinity ♾️

**Edição pública experimental do Pórtico Community que permite delegar à IA, via MCP, acesso operacional permanente a um servidor Linux.**

> ⚠️ **PRÉ-LANÇAMENTO.** O perfil root/sudo permanente ainda precisa ser implementado e auditado. O código herdado do Community está disponível, mas a edição Infinity ainda não está homologada para instalação no SRV-IA ou em servidores de terceiros.

[English](README.md) · [Código de origem: Community](https://github.com/josemirmoura/mcp-vps-agent-gateway)

## Objetivo

O proprietário poderá autorizar **uma única vez**, sem prazo de expiração:
- leitura, criação, edição e trabalho em `/opt`, inclusive novos projetos;
- posteriormente, **execução administrativa root persistente**, ativada por decisão local explícita e vinculada a uma identidade MCP específica.

A autenticação HTTPS/OAuth continua obrigatória. A autorização não implica sessão infinita no ChatGPT nem elimina confirmações do cliente, limites de duração de jobs ou a validade dos tokens OAuth.

**⚠️ Root irrestrito alcança todo o host**, inclusive fora de `/opt`. O risco inclui perda de dados, comprometimento de credenciais, alterações nos serviços e execução de instruções maliciosas encontradas em arquivos. O desligamento de emergência e auditoria devem ter componentes externos à máquina administrada.

## Estado da implementação

- ✅ Repositório público independente e histórico `main` do Community importado.
- ✅ Licença Apache-2.0 e atribuição original preservadas.
- ✅ Especificação do perfil permanente documentada.
- ⚠️ Wrapper de instalação experimental para `/opt` estático, **sem root permanente**.
- ⏳ Broker/instalador para root persistente vinculado ao sujeito MCP.
- ⏳ Auditoria, revogação externa e E2E real no SRV-IA.

O instalador histórico `scripts/install.sh` ainda corresponde ao comportamento do Community. Evite instalá-lo como se o Infinity estivesse pronto.

## Documentação

- [Arquitetura e fronteiras](docs/infinity/ARCHITECTURE.md)
- [Root persistente](docs/infinity/ROOT_PERSISTENTE.md)
- [Critérios de aceite E2E](docs/infinity/ACCEPTANCE.md)
- [Origem e detalhes da importação](docs/infinity/IMPORT.md)

## Autoria e licença

Projeto de Josemir Moura, derivado do [Pórtico MCP Community](https://github.com/josemirmoura/mcp-vps-agent-gateway), sob a licença **Apache-2.0**. Manter créditos, LICENSE e NOTICE aplicáveis. O Community e seu plano de testes E2E não são alterados por este desenvolvimento.

## Contrato herdado de instalação (ainda sem homologação Infinity)

O instalador herdado precisa validar HTTPS/OAuth, conectar o cliente MCP **ChatGPT Web**, executar uma chamada real autenticada `system.info` e conferir a auditoria do Broker **antes** de emitir `INSTALLATION COMPLETE`. O Infinity exigirá ainda os testes específicos do root persistente. O contrato do Community, isoladamente, não homologou a nova edição.

Documentação herdada em português:

- [Quick Start / Início rápido](docs/quick-start.pt-BR.md)
- [First installation acceptance / Aceite de primeira instalação](docs/first-install-acceptance.pt-BR.md)
- [Installer flow / Fluxo do instalador](docs/installer-flow.pt-BR.md)
- [Installation contract / Contrato de instalação](docs/installation-contract.pt-BR.md)
- [ChatGPT integration / Integração com ChatGPT](docs/chatgpt-integration.pt-BR.md)
