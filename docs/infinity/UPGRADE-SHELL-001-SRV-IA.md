# Operação controlada | Atualização SHELL-001 no SRV-IA

**Data:** 16/10/2026. **Estado:** fonte corrigida e validada no CI; aplicação em `srv-ia` **pendente de execução pelo operador**.

- Repositório: https://github.com/josemirmoura/portico-infinity
- PR #5 incorporada: commit imutável `76dbeec0f8b1eae992617ab008726273b026eca5`
- CI principal: https://github.com/josemirmoura/portico-infinity/actions/runs/38931417373 (**SUCCESS**)
- E2E Docker com shell real e bloqueio de `.env`: https://github.com/josemirmoura/portico-infinity/actions/runs/38931417374 (**SUCCESS**)
- Empacotamento/rollback herdado, repetição aprovada: https://github.com/josemirmoura/portico-infinity/actions/runs/38931417435 (**SUCCESS** na tentativa 2)
- Defeito rastreado em https://github.com/josemirmoura/portico-infinity/issues/4

## Fronteira de autorização

A IA tem leitura/escrita estática em `/opt`, e **não tem autoridade sobre** `/srv/portico-infinity`, Docker global, shell administrativo nem `sudo`. **Nunca** autorizar um script executado a partir de `/opt` a atualizar o controlador protegido. Essa separação é intencional. A execução a seguir é exclusivamente uma tarefa do operador autenticado no console/SSH. Não modificar Pórtico Community na VPS `srv678294`.

## Pré-checagem obrigatória pelo operador, no terminal do SRV-IA

Realizar numa janela de manutenção com acesso SSH/console **independente do próprio Infinity**. NÃO executar na VPS.

```bash
hostname
sudo test -d /srv/portico-infinity
sudo test ! -L /srv/portico-infinity
sudo git -C /srv/portico-infinity remote -v
sudo git -C /srv/portico-infinity rev-parse HEAD
sudo git -C /srv/portico-infinity status --short
sudo python3 /srv/portico-infinity/scripts/check-infinity-layout.py --run-as portico-infinity-exec
cd /srv/portico-infinity
sudo docker compose -p portico-infinity ps
sudo docker compose -p portico-infinity config --quiet
```

**Condições de avanço:** hostname `srv-ia`; remote oficial esperado; alterações rastreadas inexistentes; layout control-plane `root:root`, conta não-root; contêineres `broker` e `gateway` identificados como exclusivos do Infinity, com diretório Compose correspondente a `/srv/portico-infinity`. Se houver sobreposições Compose de OAuth/túnel, IDENTIFICAR as mesmas opções `-f` usadas na instalação antes de qualquer comando de atualização. Se qualquer verificação divergir, parar e investigar. Não substituir configurações locais, credenciais, volumes ou contas.

## Preparação e atualização, somente após a pré-checagem

1. Registrar o SHA anterior, a lista de arquivos de configuração e a composição ativa. Preparar backup protegido de `.env`, `config/policy.yaml`, estado e auditoria com proprietário root e modo restrito. Para consistência de banco de dados, interromper Gateway/Broker do Infinity durante a cópia de estado, ou usar mecanismo consistente de backup SQLite devidamente testado. **Nunca** copiar segredos para `/opt`, para o chat ou para artefatos públicos.
2. Buscar apenas o repositório oficial e conferir o commit exato:
   ```bash
   cd /srv/portico-infinity
   sudo git fetch origin main
   sudo git cat-file -t 76dbeec0f8b1eae992617ab008726273b026eca5
   sudo git show --no-patch --format='%H %s' 76dbeec0f8b1eae992617ab008726273b026eca5
   ```
   O tipo deve ser `commit`. Verificar se o SHA anterior é ancestral e se não existem mudanças rastreadas locais.
3. Após backup e serviço parado na janela de manutenção, o operador poderá posicionar o checkout no **SHA imutável aprovado**:
   ```bash
   cd /srv/portico-infinity
   sudo git switch --detach 76dbeec0f8b1eae992617ab008726273b026eca5
   sudo python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
   ```
   Se a instalação utiliza arquivos Compose extras, incluir exatamente os mesmos `-f` aqui. Na composição básica:
   ```bash
   sudo docker compose -p portico-infinity config --quiet
   sudo docker compose -p portico-infinity up -d --build broker gateway
   sudo docker compose -p portico-infinity ps
   sudo docker compose -p portico-infinity exec -T broker /usr/local/bin/vps-agent audit-status --socket /run/vps-agent/broker.sock
   ```
   **Atenção:** o comando acima é um exemplo para instalações com apenas `compose.yaml`; não rodar cegamente se a instalação usa overlays de autenticação, rede ou túnel. Nunca executar `docker compose down -v` ou remover volumes como parte deste patch.
4. No ChatGPT conectado ao Infinity, conferir `system.health` e `system.info`; executar na pasta experimental `/opt/portico-infinity-e2e-20261016` um `shell.exec` não-root que escreva um arquivo sentinela; verificar conteúdo, UID não-root, `job.status`/`job.tail` e trilha de auditoria. Testar leitura negada de `.env` sintético (sem segredos) e bloqueio fora de `/opt`. A versão nominal pode permanecer `v0.1.0-rc.7` porque este patch não altera VERSION: o SHA do checkout e digest do contêiner reconstruído são as evidências de atualização.
5. Somente após esses testes, registrar SHELL-001 como corrigido no SRV-IA e fechar Issue #4.

## Rollback

Em caso de saúde ruim, falha de segurança, diferença nos overlays ou falha no shell real, executar restauração **pelo console/SSH do operador**, nunca pelo MCP. Retornar ao SHA original anotado na etapa 1, preservar o backup, reconstruir os serviços Infinity com a mesma composição e confirmar saúde/auditoria. Se o estado tiver mudado por migração incompatível, restaurar o backup consistente protegido após parada dos contêineres; não fazer downgrade com banco incompatível. Não alterar Community/Cloud.

## Impacto funcional da mudança

A autorização de operações de arquivos tipadas continua abrangendo `/opt`. **Cada shell não-root do Infinity passa a montar somente o projeto escolhido como cwd**, com varredura dos arquivos sensíveis limitada a essa workspace. Para projetos com mais de 50 mil entradas, o shell ainda falha fechado; a abordagem não desativa o teto de segurança nem autoriza execução root. A implantação real requer E2E; aprovação em laboratório não substitui homologação no SRV-IA.
