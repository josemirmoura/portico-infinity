# Gates de aceite: Pórtico Infinity

**Status:** especificação, não executados para o perfil `opt-privileged`.

| Gate | Requisito | Estado |
| --- | --- | --- |
| I0 | Histórico da `main` e Apache-2.0 preservados no repositório independente | concluído |
| I1 | Identidade e documentação distintas, sem alterar Community/Cloud | parcialmente concluído |
| I2 | Instalador opt-in e política persistente `/opt` auditável | preview existente, E2E pendente |
| I3 | Broker: operações de arquivo privilegiadas **tipadas**, confinadas a `/opt`; shell não-root | **E2E Docker local aprovado**; shell root bloqueado; E2E SRV-IA pendente |
| I4 | OAuth/identidade MCP, túnel e revogação externa | pendente |
| I5 | Adversarial: traversal, symlinks, hardlinks, montagens, segredos, `/proc`, TOCTOU e subject mismatch | pendente |
| I6 | `shell.exec_admin`, Docker host/root e gestão global negados por padrão | **bloqueios testados no Docker local**; ameaças indiretas pendentes |
| I7 | Reboot/reconexão mantêm autorização; revogação e restauração funcionam | pendente |
| I8 | E2E real no SRV-IA, após testes em laboratório descartável | pendente |
| I10 | Instalação de plano de controle em `/srv/portico-infinity`, proprietário root e usuário executor sem escrita | **aprovado no runner descartável**; SRV-IA pendente |
| I9 | Modo root global opcional, se implementado: gate independente | fora do MVP |

## Evidência E2E Docker real, 16/10/2026

**APROVADO somente o gate local descartável.** Execução [GitHub Actions #38891810390](https://github.com/josemirmoura/portico-infinity/actions/runs/38891810390), commit `863de4cdc1edc721b43d6a2d2785f4e81bccae9b`, resultado **SUCCESS**. GitHub runner `ubuntu-24.04` com Docker e Broker/Gateway reais; instância `portico-infinity` isolada. A autenticação foi um bearer **estático exclusivamente de laboratório**, não OAuth público.

- Controlador em `/srv/portico-infinity` root-owned, checado em 489 arquivos/diretórios após reinicialização, com conta de execução não-root distinta.
- Política estática `scoped`, teto `/opt`, operações de arquivo `file.write`, `file.read`, `file.patch`, `file.chmod`, `file.chown`, `file.remove` aprovadas no Broker real.
- Tentativas fora de `/opt` e contra `/srv/portico-infinity` bloqueadas; `package.install`, `shell.exec_admin`, `service.restart` e `docker.action` negadas.
- Token inválido rejeitado; cadeia de auditoria do Broker válida (**30 eventos**).
- Desligamento pelo operador confirmou interrupção dos dois contêineres, seguida de partida e healthchecks aprovados. Artefatos não secretos enviados pela Actions.
- Cleanup final do projeto Docker executado em runner descartável. **Nem VPS nem SRV-IA foram alterados.**

**Não coberto:** instalação guiada pública, OAuth/Zitadel e conexão ChatGPT real, reinicialização completa do host, revogação de tokens fora da máquina, efeitos indiretos de scripts em `/opt`, e instalação/desinstalação homologadas no SRV-IA. Esses gates permanecem pendentes e não devem ser descritos como aprovados.

## Provas mínimas

- Criar, ler, editar, `chmod`, `chown` e remover arquivo de teste dentro de `/opt`; verificar auditoria por operação.
- Negar operações fora de `/opt` e toda execução de shell root arbitrária.
- Verificar que escrever em `/opt` não altera componentes do plano de controle em `/srv/portico-infinity`; testar indireções perigosas de Docker/systemd e scripts privilegiados.
- Confirmar que a política persistente sobrevive a reboot sem permitir autoaprovação ou acesso a outro sujeito.
- Testar revogação externa e retorno ao estado seguro sem depender da IA.

**Release:** não chamar `opt-privileged` de aprovado antes de CI + E2E. Não gerar ciclos de auditoria sem novos defeitos concretos; usar critérios objetivos de encerramento.

## Regra

Pórtico Community e Pórtico Cloud permanecem isolados. Não reaproveitar estado, portas, identidade ou segredos de produção.
