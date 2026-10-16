# Gates de aceite: Pórtico Infinity

**Status:** planejado; nenhum E2E Infinity completado.

| Gate | Requisito | Estado |
| --- | --- | --- |
| I0 | Git histórico do Community em repositório próprio e Apache-2.0 preservado | base main importada |
| I1 | Identidade visual/docs independentes; nenhum efeito colateral Community | documentação inicial |
| I2 | Instalador com confirmação ⚠️ e raiz `/opt` estática persistente | experimental, sem aceite |
| I3 | Broker root persistente por sujeito fixo, sem autoaprovação | pendente |
| I4 | Autenticação OAuth e portal HTTPS / túnel testados | pendente |
| I5 | Leitura, escrita e shell em laboratório descartável; fora do teto negado para operações scoped | pendente |
| I6 | Root: teste inofensivo, auditoria externa e revogação de emergência | pendente |
| I7 | Reconexão/reboot mantém autoridade e recuperação de jobs | pendente |
| I8 | E2E real com ChatGPT no SRV-IA depois dos testes descartáveis | pendente |

## Regra de manutenção

Pórtico Community segue separado e não recebe commits Infinity. Nunca migrar estado/segredos da Community ou do Cloud automaticamente. Instalação em ambiente existente exige processo de migração independente.
