# Implantação protegida do Compose assíncrono no SRV-IA

**Escopo:** Pórtico Infinity isolado em `srv-ia`, Issue [#9](https://github.com/josemirmoura/portico-infinity/issues/9), PR [#11](https://github.com/josemirmoura/portico-infinity/pull/11).

**Estado:** procedimento preparado, **não executado** no SRV-IA. Preencher o SHA **do merge aprovado**, nunca usar um nome de branch de desenvolvimento como release. Seguir também o [runbook operacional de atualização anterior](UPGRADE-SHELL-001-SRV-IA.md).

## Segurança e fronteira de autoridade

O controlador está em `/srv/portico-infinity`, fora do teto `/opt` delegado à IA. Atualização requer operador root autenticado via **terminal ou SSH independente do próprio Pórtico**, com cópia consistente e restauração testada. **Não** usar `scripts/update.sh`, que pertence ao ciclo de vida do Community e falha fechado no Infinity. Não pedir permissões extras ao Pórtico nem copiar configuração, banco ou segredos para `/opt`, logs públicos ou GitHub.

A implantação altera **apenas Broker e Gateway do Pórtico Infinity**; ZITADEL, sua base, rede e demais serviços devem permanecer intactos. Nenhum comando automático de rollout será disparado pela PR.

## Gates antes da janela

1. PR #11 fundido somente após teste Go + race/vet, segurança, E2E Docker descartável e Docker Package Acceptance verdes no SHA final.
2. No terminal **srv-ia**, anotar `hostname -s`, `sudo git -C /srv/portico-infinity rev-parse HEAD`, `sudo git -C /srv/portico-infinity status --porcelain`, `sudo git -C /srv/portico-infinity remote -v`; exigir checkout root-owned sem alterações rastreadas e origem oficial.
3. Verificar `sudo python3 /srv/portico-infinity/scripts/check-infinity-layout.py --run-as portico-infinity-exec` e lista exata de arquivos Compose na **label Docker `com.docker.compose.project.config_files` dos contêineres broker/gateway**. Em 16/10, estava em uso a base `compose.yaml` mais `compose.infinity-auth.remote.yaml`. Não inferir que o overlay ainda é o mesmo sem conferi-lo.
4. Fazer inventário de serviços e volumes **sem imprimir variáveis de ambiente** e validar `docker compose ... config --quiet`. Verificar existência e espaço da área de backup protegida em `/srv/portico-infinity/backups`, evitando misturar snapshots Community e Infinity.

## Backup antes do Git checkout

- Garantir SSH alternativo. Interromper somente `gateway` e `broker` do Infinity; **não** executar `down`, `down -v`, `--remove-orphans`, nem parar ZITADEL.
- Fazer cópia protegida e consistente de `.env`, `config/policy.yaml`, `state/` e demais arquivos de estado (incluindo auditoria) conforme volumes efetivamente montados. Se o SQLite residir em volume Docker, usar backup SQLite consistente ou parar Broker antes de copiar os bytes. Não tomar `cp` de banco SQLite ativo como snapshot.
- Registrar SHA-256 do backup e executar teste de leitura/integridade antes de prosseguir. Backup deve permanecer proprietário root, modo restrito e **fora do escopo da IA**.
- Registrar SHA anterior e instrução de retorno. Qualquer falha no backup **interrompe a atualização**, e os serviços originais devem ser religados com os overlays originais.

## Implantação, somente por operador

1. Buscar `origin main` e verificar objeto `TARGET_SHA` como commit na origem oficial. `TARGET_SHA` deve ser **preenchido com o SHA do merge homologado**.
2. Com backups verificados e serviços parados, posicionar checkout root-owned em `TARGET_SHA` (por exemplo `git switch --detach "$TARGET_SHA"`) e repetir o preflight de layout.
3. Usar **exatamente** os overlays identificados. Exemplo apenas para o cenário base + WireGuard:
   ```bash
   cd /srv/portico-infinity
   sudo docker compose -p portico-infinity \
     -f compose.yaml -f compose.infinity-auth.remote.yaml config --quiet
   sudo docker compose -p portico-infinity \
     -f compose.yaml -f compose.infinity-auth.remote.yaml \
     up -d --build --no-deps --force-recreate broker gateway
   sudo docker compose -p portico-infinity \
     -f compose.yaml -f compose.infinity-auth.remote.yaml ps
   ```
4. Exigir `broker` e `gateway` **healthy**, ZITADEL sem recriação, `system.health.ok=true`, `audit_chain_ok=true` e acesso à Nara inalterado. Registrar SHA, versão/digest de Broker e Gateway e evidências não sensíveis.

## Aceite funcional

Em projeto descartável separado e previamente delegado:
- `compose.pull` com imagem não cacheada ou atraso controlado **maior que 15s** devolve `job_id` rapidamente;
- `job.status` evolui de `running` a `done`, com status `0`; `job.tail` preserva logs;
- desconectar/reconectar cliente e consultar o mesmo job por ID sem duplicar Docker;
- repetir com mesmo `operation_id`: mesma identidade, nunca segundo job;
- negar projeto fora de delegação, outra identidade, mutação concorrente, hardlinks/symlinks indevidos;
- `compose.up` e `compose.down` em **projeto descartável**, sem tocar em Nara, Docker de outros projetos ou dados persistentes.

Verificar diretamente as interfaces e auditoria. O CI de laboratório prova compatibilidade; **não** substitui o aceite no SRV-IA real.

## Reversão

Se surgirem erros, usar SSH/console do operador para parar somente os serviços do Infinity, voltar ao **SHA anterior registrado**, recompilar Broker e Gateway com os mesmos overlays e conferir health/auditoria. Se houver mudança incompatível do estado, restaurar **o snapshot íntegro** com os serviços parados. Não reinstalar Community nem apagar volumes, usuários, histórico ou autenticação.

## Pendências operacionais pós-merge

O systemd mantém unidades `oneshot` em `active/exited` para permitir consulta durável. Implementar política de retenção/coleta após persistência do resultado e revisão das regras de revogação/expiração antes de declarar a funcionalidade madura para hospedagem multiusuária. Não limpar unidades enquanto seu resultado ainda puder ser necessário para reconciliação.
