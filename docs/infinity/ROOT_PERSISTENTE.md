# Root/sudo persistente no Pórtico Infinity

**Decisão de produto de 16/10/2026. Estado: requisito aprovado, código pendente.**

## Escopo aprovado

O operador poderá conceder a uma **identidade MCP específica** (sujeito OAuth autenticado, audiência, emissor e instância fixados) autoridade administrativa equivalente a root sem renovação periódica. A aprovação será feita localmente fora do chat e poderá ser revogada pelo proprietário.

Não solicitar credenciais `sudo`, SSH ou `root` no chat. Usar preferencialmente um executor do Broker privilegiado, associado a uma identidade de serviço própria e sem login interativo, em vez de atribuir senha ou `sudo NOPASSWD:ALL` a uma conta pessoal.

## Regra funcional

- **Modo básico permanente:** raiz estática `/opt` read/write e shell `work`, sem grants com TTL.
- **Modo root persistente:** capacidade adicional administrativa, independente do diretório de trabalho, vinculada ao sujeito MCP de forma estrita.
- **Sessão OAuth:** continua expirável/revogável. Permissão estática persiste entre sessões autenticadas, reinicializações e atualizações compatíveis.
- **Revogação:** uma ação fora do MCP remove o privilégio. Também oferecer chave de emergência externa, como revogação no IdP e bloqueio do túnel.
- **Logs:** auditoria das solicitações/efeitos, com cópia externa. Um processo root local pode alterar ou apagar arquivos locais; portanto auditoria no host não pode ser tratada como inviolável.
- **Admin root:** pode ler qualquer arquivo e alterar o host fora de `/opt`. Nada impede tecnicamente um root legítimo de alterar o próprio Broker, os serviços ou a rede. A segurança residual depende de controles externos, backups e identidade.

## Mudança mínima do Broker

Adicionar uma forma explícita de autorização persistente controlada pelo operador, com campos tipados em política, testes de incompatibilidade e checagem por `subject + instance + capability` após autenticação; preservar o comportamento antigo de `shell.exec_admin` com grant temporário no Community. Separar os casos na API de decisão e auditoria, não introduzir TTL "infinito" como falso valor. Sujeito inesperado deve falhar fechado.

## Testes obrigatórios

1. Pedido root pelo sujeito autorizado: execução inofensiva em arquivo descartável e auditoria correspondente.
2. Sujeito diferente, token vencido, emissor/audiência errados: negar.
3. Sem política opt-in: negar mesmo com MCP conectado.
4. Tentativa da IA de editar política, de se autoautorizar ou de forjar solicitação do operador: negar.
5. Persistência após reboot/reconexão e revogação local.
6. Bloqueio externo encerra novas chamadas e jobs elevados devem ser revogáveis/canceláveis conforme o contrato.
7. Logs exportados e cópia de backup recuperável comprovados.
8. Operações que extrapolam `/opt`: documentar que root permite, não alegar sandbox.

**Bloqueio de publicação:** sem passar esses testes, não rotular o recurso como 'root permanente operacional'.
