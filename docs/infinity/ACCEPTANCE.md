# Gates de aceite: Pórtico Infinity

**Status:** especificação, não executados para o perfil `opt-privileged`.

| Gate | Requisito | Estado |
| --- | --- | --- |
| I0 | Histórico da `main` e Apache-2.0 preservados no repositório independente | concluído |
| I1 | Identidade e documentação distintas, sem alterar Community/Cloud | parcialmente concluído |
| I2 | Instalador opt-in e política persistente `/opt` auditável | preview existente, E2E pendente |
| I3 | Broker: operações de arquivo privilegiadas **tipadas**, confinadas a `/opt`; shell não-root | base Community existente, testes Infinity pendentes |
| I4 | OAuth/identidade MCP, túnel e revogação externa | pendente |
| I5 | Adversarial: traversal, symlinks, hardlinks, montagens, segredos, `/proc`, TOCTOU e subject mismatch | pendente |
| I6 | `shell.exec_admin`, Docker host/root e gestão global negados por padrão | pendente |
| I7 | Reboot/reconexão mantêm autorização; revogação e restauração funcionam | pendente |
| I8 | E2E real no SRV-IA, após testes em laboratório descartável | pendente |
| I10 | Instalação de plano de controle em `/srv/portico-infinity`, proprietário root e usuário executor sem escrita | implementação inicial, aceite pendente |
| I9 | Modo root global opcional, se implementado: gate independente | fora do MVP |

## Provas mínimas

- Criar, ler, editar, `chmod`, `chown` e remover arquivo de teste dentro de `/opt`; verificar auditoria por operação.
- Negar operações fora de `/opt` e toda execução de shell root arbitrária.
- Verificar que escrever em `/opt` não altera componentes do plano de controle em `/srv/portico-infinity`; testar indireções perigosas de Docker/systemd e scripts privilegiados.
- Confirmar que a política persistente sobrevive a reboot sem permitir autoaprovação ou acesso a outro sujeito.
- Testar revogação externa e retorno ao estado seguro sem depender da IA.

**Release:** não chamar `opt-privileged` de aprovado antes de CI + E2E. Não gerar ciclos de auditoria sem novos defeitos concretos; usar critérios objetivos de encerramento.

## Regra

Pórtico Community e Pórtico Cloud permanecem isolados. Não reaproveitar estado, portas, identidade ou segredos de produção.
