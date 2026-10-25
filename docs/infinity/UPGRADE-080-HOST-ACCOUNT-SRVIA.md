# Upgrade controlado 080: for_shell / identidade do host (SRV-IA)

**Estado:** proposta de procedimento operador, não executar antes da CI integral da PR e revisão do SHA do próprio script. Requer comando independente por SSH/console **root** e confirmação explícita `--ack-operator-upgrade`. Não é UAT completo nem GO v1.0.

## Revisões fixadas

- Runtime **antes:** `48d8c15978a675090a052b977b47abd1fbdd87f9` (instalado, verificar novamente por SSH).
- Runtime **depois:** `53a6b14dae42bbe6cfd8eba9605468e7b28b54f7` (PR #80, 11/11 workflows SUCCESS).
- Alterações permitidas entre as revisões: identidade do shell não-root e testes/documentos; zero mudanças em `internal/state/`, `internal/jobs/`, `internal/securefs/`, `config/`, `Dockerfile` e overlays Compose. Qualquer divergência bloqueia a atualização.
- A pasta controladora `/srv/portico-infinity` permanece fora do teto MCP `/opt`, por projeto. Não executar upgrade por `shell.exec` do MCP, nem contornar políticas para entrar em `/srv`.

## Procedimento

Após CI e revisão administrativa, o operador deve buscar do GitHub a **versão revisada** do script `scripts/infinity-080-operator-upgrade.sh` usando um commit fixado do repositório (registrar o SHA daquele commit). Executar `bash -n` antes da invocação `sudo bash /CAMINHO/DO/SCRIPT --ack-operator-upgrade`. Não usar `curl|bash` nem apontar cegamente para `main` mutável.

O script falha fechado se: não for root no host `srv-ia`; houver SHA do checkout diferente, mudanças de arquivos versionados, diretório controlador não protegido, Compose inconsistente, serviços não saudáveis, jobs ativos/incertos perigosos, origem ou commit incompatíveis, esquema/Compose alterados ou falta da confirmação explícita.

Se o preflight passar, para **somente** Gateway e Broker da instância `portico-infinity`; cria arquivo privado `root:root` em `/var/backups/portico-infinity`, preservando configurações, `.env`, SQLite e SHA com `sha256sum`. Reconstrói **Broker e Gateway** do SHA de destino, sem recriar ZITADEL. Confere cinco healthchecks, IDs inalterados dos três serviços ZITADEL e cadeia de auditoria; registra inventário **somente leitura** de Compose e relatório de retenção **dry-run**. Em falha após a entrada na janela de manutenção, tenta reconstruir a revisão anterior exclusivamente nesses dois serviços; o backup fica intacto para revisão manual.

## Aceite posterior no MCP autenticado

1. Confirmar `system.health.ok`, `audit_chain_ok`, jobs ativos zero, escopo físico `/opt` e `full_enabled=false`.
2. `file.mkdir({path: "/opt/portico-infinity-forshell-uat-<ID_EXCLUSIVO>", for_shell: true})`: confirmar UID/GID do host `portico-infinity-exec` e permissão `0750`.
3. `job.start` com `cwd` na pasta recém-criada: provar escrita não-root e leitura por MCP; recusar uma segunda criação da mesma pasta, uma raiz autorizada e qualquer acesso fora do teto.
4. Remover somente os arquivos sintéticos e o diretório vazio, verificar auditoria; comentar a Issue #77 com SHA, evidências e decisão.
5. Guardar resultados do inventário para a Issue #12. A inspeção de registros `reconcile_required` é diagnóstica e jamais autoriza limpeza de unidades ou SQLite.

**Proibido sem gate independente:** reboot, revogação da conta OAuth humana, operação de rede WireGuard/VPS, desinstalação, `compose down` produtivo, publicação ou GO v1.0.
