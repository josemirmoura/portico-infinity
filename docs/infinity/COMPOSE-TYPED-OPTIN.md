# Pórtico Infinity: habilitar Docker Compose tipado por projeto

**Diagnóstico 16/10/2026:** o MCP autenticado do SRV-IA tem `filesystem.read/write` e shell não-root no teto `/opt`, mas `compose.inspect/manage` estático vazio. A delegação dinâmica permanente de perfil `compose` foi aprovada para `/opt/open-webui` (pedido `apr_c53801e3a35dba73847309e6bb45645b`). Mesmo após a aprovação, `compose.validate` retornou `permission_denied: compose project is outside policy`.

**Causa comprovada pelo código:** `Broker.canCompose` verifica `Policy.CanComposeAction(action)` antes de consultar delegações. O instalador Infinity opt-privileged desativa intencionalmente `compose.actions`. A solução é uma autorização explícita do operador para as ações tipadas, sem liberar Docker irrestrito, shell root ou Compose de outros projetos.

## Utilitário versionado

- `scripts/enable-infinity-compose-actions.py`: opt-in limitado a `validate`, `pull`, `up`, `down`; exige teto `/opt`, modo `scoped`, `whole-host=0`, `full_mode_enabled=false`, `docker.manage/actions` e `compose.inspect/manage` vazios.
- Prévia sem escrita por padrão. Alteração exige `--apply --ack-typed-compose` e usuário root. Cria backup protegido da policy e substitui somente `compose.actions` de forma atômica.
- `tests/scripts/test_enable_infinity_compose_actions.py`: regressões de idempotência, limites de escopo, ausência de permissões globais e alteração mínima.

O script **não** reinicia serviços e **não** instala Docker. O checkout protegido em `/srv/portico-infinity` não é acessível ao MCP da IA. O operador deve aplicar somente no terminal administrativo autenticado.

## Procedimento do operador, após revisar a alteração

1. Confirmar hostname `srv-ia`, checkout protegido `/srv/portico-infinity`, política existente, Broker saudável e overlays em uso (no SRV-IA foi observado `compose.yaml` + `compose.infinity-auth.remote.yaml`). Não alterar a VPS Community.
2. Obter o script imutável do GitHub sem modificar o checkout ativo:

```bash
sudo bash -lc '
set -euo pipefail
cd /srv/portico-infinity
git fetch origin main
git show 2f50a2ee8292ee7208e7895fd39b8b6715d1619a:scripts/enable-infinity-compose-actions.py > /root/enable-infinity-compose-actions.py
python3 /root/enable-infinity-compose-actions.py --policy config/policy.yaml --env .env
'
```

Se a saída disser `DRY_RUN_OK`, a configuração é compatível e o operador pode executar o `--apply --ack-typed-compose`, seguido de **recriação somente do Broker**, preservando a mesma composição/overlays de produção:

```bash
sudo bash -lc '
set -euo pipefail
cd /srv/portico-infinity
python3 /root/enable-infinity-compose-actions.py --policy config/policy.yaml --env .env --apply --ack-typed-compose
docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml config --quiet
docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml up -d --no-deps --force-recreate broker
docker compose -p portico-infinity -f compose.yaml -f compose.infinity-auth.remote.yaml ps
'
```

**Atenção:** os overlays devem ser confirmados pelos labels dos contêineres antes do comando. Não executar `docker compose down`, nem substituir o Gateway/OAuth. O script preserva uma cópia anterior para rollback. Não publicar `.env`, tokens ou cópias da policy com dados sensíveis.

## Critério de aceite

- `permissions.list_root_access` ainda mostra delegação `compose` específica para `/opt/open-webui`.
- `compose.validate` passa somente nesse diretório; fora dele permanece negado.
- `compose.up` inicia `srv-ia-open-webui` sem alterar Community/Infinity ou dados preexistentes; verificações de healthcheck e logs.
- Docker global e `shell.exec_admin` permanecem negados.
- Exigências de perfil Hermes limitado, primeiro administrador Open WebUI e roteamento VPS são etapas independentes antes de publicar a Nara.

**Limitação residual:** Docker Compose pode ser tão privilegiado quanto Docker global quando o projeto autorizado contém montagens perigosas. O operador deve revisar o arquivo Compose de cada projeto antes de aprovar delegações do perfil `compose`.
