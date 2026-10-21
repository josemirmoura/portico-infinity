# Pórtico Infinity: retenção e coleta controlada de unidades Compose

**Issue:** [#12](https://github.com/josemirmoura/portico-infinity/issues/12). **Escopo:** apenas o Infinity em `srv-ia`; não altera a instalação Community.

## Por que é necessário

Operações tipadas `compose.pull/up/down` são executadas como unidades transientes systemd com `Type=oneshot` e `RemainAfterExit=yes`, para permitir consultas confiáveis do resultado após reconexões. Unidades `active/exited` podem permanecer carregadas indefinidamente sem coleta. A partir deste patch, o prazo de execução do oneshot usa `TimeoutStartSec=7200`, porque `RuntimeMaxSec` não impõe limite a `Type=oneshot`.

## Operação segura

O **operador autenticado no terminal/SSH independente do próprio Infinity** deve usar a ferramenta `scripts/collect-infinity-compose-units.py` apenas após instalar a versão aprovada, com checkout root-owned em `/srv/portico-infinity`.

Leitura prévia, padrão *dry-run* (não escreve arquivos, não executa `systemctl stop/reset-failed`):

```bash
cd /srv/portico-infinity
sudo python3 scripts/collect-infinity-compose-units.py --retention-days 7 --limit 25
```

Após revisão explícita dos resultados, para coletar até 25 unidades elegíveis:

```bash
sudo python3 scripts/collect-infinity-compose-units.py --retention-days 7 --limit 25 --apply --ack-operator-cleanup
```

A ferramenta exige root e hostname `srv-ia`, sem redirecionar credenciais para a IA. Não usar `shell.exec` do Pórtico, tarefa de terceiros ou serviço automático do sistema sem governança. O Pórtico MCP permanece confinado a `/opt`, enquanto backup e controlador permanecem fora dele.

## Proteções e limites

- Apenas `compose.pull`, `compose.up`, `compose.down`, `done/failed`, com `exit_code` gravado no SQLite e `updated_at` mais antigo que o período configurado; padrão 7 dias e máximo 100 por lote.
- Apenas IDs `vps-agent-compose-<32-hex>`, nome da unidade exato, `Description=Portico authorized Docker Compose task`, `Type=oneshot`, `Transient=yes`, `WorkingDirectory` igual ao projeto persistido no SQLite, estado e resultado systemd correspondentes ao status terminal do banco.
- Falha fechada ou ignora registros `starting/running`, trabalhos estrangeiros, unidades desaparecidas após reboot, inconsistências de PID/resultado/saída ou mudança de status entre consultas.
- Primeiro grava evidências por unidade: metadados JSON e até 64 KiB de journal sob `/var/backups/portico-infinity/compose-job-retention` com permissões de root. Apenas depois chama `systemctl stop` na unidade `active/exited` ou `reset-failed` na `failed`.
- Nunca executa `docker compose down`, não cancela jobs ativos, não remove registros do SQLite, não altera a cadeia de auditoria e não recolhe unidades de outros serviços, pessoas ou projetos.
- Diretório de evidência existente não é sobrescrito. Falhas de escrita, journal inacessível ou divergências abortam a coleta e exigem revisão do operador, em vez de repetir cegamente.

### Observações de implantação

O controle é **manual sob demanda** nesta fase. Instalar timers automáticos e políticas multiusuárias deve exigir gate separado de segurança. A coleta só altera **unidades systemd de jobs Compose antigos e concluídos**, não contêineres ou dados de aplicação.

O script **não tenta reconciliar** automaticamente os 22 registros antigos de `shell.exec` no SQLite: são outra classe de jobs, e o estado `inactive/dead` não prova o código de saída do programa. Esses registros devem ser investigados com seu próprio protocolo de reconciliação.

Se não houver unidades com idade suficiente ou o status estiver incerto, o comando termina sem coletar nada; isso é esperado, não falha nem evidência falsa de sucesso.
