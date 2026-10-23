# Infinity I7 | Preparação para desinstalação integral

**Estado:** fases 1 (backup) e 2 (remoção **local** condicionada ao backup) implementadas e testadas em ambiente descartável. **A desinstalação multi-host completa permanece pendente**, incluindo worker instalado no host e dependências VPS/WireGuard/DNS. Nunca executar em SRV-IA/VPS sem autorização específica do proprietário.

## Contrato e fronteiras

O operador controla o ciclo de vida fora do MCP. Uma desinstalação completa precisa distinguir os seguintes domínios:

| Domínio | Tratamento |
| --- | --- |
| Gateway/Broker/ZITADEL/contêineres e redes Compose exclusivos | `scripts/infinity-lifecycle.py` identifica rótulos e encerra exclusivamente o runtime; o modo `--uninstall-runtime` preserva dados |
| Estado, código, auditoria, política e identidade em `/srv/portico-infinity` | Backup privado íntegro; antes da exclusão integral devem existir ensaio de restauração independente, inventário fixado e confirmação externa |
| Volumes Docker do projeto, incluindo ZITADEL/PostgreSQL e bootstrap | Inventário por rótulo do Compose, verificação de contêineres vinculados e backup byte a byte após parada consistente; remover somente em etapa independente posterior |
| Worker `systemd` instalado no host e usuário executor | Inventário e remoção manual exclusiva após certificar que não há jobs ativos, falhados ou incertos |
| Ponte Traefik na VPS, WireGuard, regra UFW, DNS e certificados | **Outros hosts/provedores ou compartilhados**; são gates manuais separados, nunca apagados automaticamente pelo desinstalador local |
| Todo o teto físico de trabalho, padrão `/opt` | **Preservar integralmente**, inclusive arquivos criados durante a utilização do Infinity, sem `rm -rf /opt` |
| Community e Cloud | **Nunca alterar** |

## Fase 1: inventário read-only e backup privado

O script `scripts/infinity-uninstall-backup.py` verifica root, diretórios e scripts não graváveis por terceiros, identidade de contêineres e volumes, ausência de worker ativo/incerto e todos os contêineres Infinity **parados**. Volumes compartilhados, remotos, com mountpoint não padrão ou rótulos incompatíveis bloqueiam o procedimento. Ele não executa ações de Docker que apaguem ou parem serviços.

**Exemplo de operação para host autorizado, após backup prévio independente e janela de manutenção aprovada:**

```bash
cd /srv/portico-infinity

# Desativação reversível, somente após inventário e autorização externa.
sudo python3 scripts/infinity-lifecycle.py --deactivate \
  --confirm DEACTIVATE-INFINITY

# O destino fica FORA de /srv e /opt, exclusivamente root:root 0700.
sudo install -d -o root -g root -m 0700 /var/backups/portico-infinity

sudo python3 scripts/infinity-uninstall-backup.py --inspect

# Rótulo deve ser único, ASCII seguro de 8 a 64 caracteres.
sudo python3 scripts/infinity-uninstall-backup.py \
  --backup INF-20261023-EXEMPLO \
  --confirm BACKUP-INFINITY-BEFORE-UNINSTALL

sudo python3 scripts/infinity-uninstall-backup.py \
  --verify INF-20261023-EXEMPLO
```

O arquivo `/var/backups/portico-infinity/infinity-uninstall-<LABEL>.tar` e seu JSON têm permissões **0600**. O diretório exige **0700**. Os dados contêm material confidencial de identidade e podem incluir segredos. **Nunca anexar ao GitHub Actions, publicar, enviar ao chat, copiar para repositório ou colocar em diretório web.** A verificação calcula SHA-256 de todo o arquivo e compara o inventário root-only.

**Importante:** validar SHA-256 confirma integridade do backup criado, mas **não prova que o serviço foi restaurado e voltou a operar**. Antes de exclusão irreversível, efetuar restauração em laboratório isolado, conferindo permissões, banco de identidade, auditoria, conteúdo de volumes e logins. Registrar hashes e evidências numéricas sem divulgar credenciais.

## Fase 2: remoção local após restauração independente

**Somente depois** de o operador ter confirmado o backup SHA-256, ensaiado a restauração em máquina isolada e autorizado especificamente a remoção local, é possível executar o módulo `scripts/infinity-uninstall-local.py`. Ele exige contêineres locais parados, manifesto sem alterações, ausência de workers incertos, ausência de mounts sob o controlador e hashes correspondentes.

```bash
cd /srv/portico-infinity
sudo python3 scripts/infinity-uninstall-local.py \
  --backup-label INF-20261023-EXEMPLO --dry-run

# Somente após conferir os destinos exibidos e comprovar a restauração:
# Não execute sem autorização específica. Os dois valores são derivados do
# SHA-256 do backup e aparecem no plano --dry-run.
sudo python3 scripts/infinity-uninstall-local.py \
  --backup-label INF-20261023-EXEMPLO \
  --remove-local \
  --confirm 'REMOVE-LOCAL-INFINITY-<20_DIGITOS_DO_SHA>' \
  --restore-attested 'RESTORE-TESTED-<20_DIGITOS_DO_SHA>'
```

A exclusão local usa `infinity-lifecycle.py` para interromper/remover exclusivamente contêineres e redes identificados, verifica cada volume privado antes de `docker volume rm`, e por último remove o controlador **somente no caminho literal `/srv/portico-infinity`** com `shutil.rmtree` resistente a ataques de links simbólicos. O arquivo de backup e um diário de intenção/conclusão `root:root 0600` permanecem fora do diretório removido, em `/var/backups/portico-infinity`. Se qualquer etapa falhar, **não há reexecução automática nem rollback implícito**; o operador deve reconciliar estado e recuperar os dados do backup.

A confirmação `RESTORE-TESTED` é uma declaração explícita do operador, **não uma verificação técnica da restauração em produção**. O fluxo E2E testou a extração real da configuração e do volume de identidade **sintéticos** antes da remoção.

**Exclusões proibidas/fora do escopo:** arquivos dentro do teto físico, modelos, outros projetos do usuário, volumes de outros projetos, worker fixo em `/usr/local/libexec`, conta do executor, rede WireGuard, regra de firewall UFW, proxy/TLS/DNS e bridge da VPS. Esses últimos são etapas independentes com inventário e decisão de proprietário específicos; não alegar desinstalação integral de dois hosts após `--remove-local`.

## Provas e falhas intencionais

O workflow `infinity-uninstall-private-backup-disposable` usa Docker/Compose reais, controlador sintético root-owned, volume privado de identidade com conteúdo fictício e contêiner estrangeiro. Ele exige:

- Recusa sem confirmação de backup e com contêiner ativo, rótulos/mountpoints falsos, volume compartilhado ou destino pré-existente.
- Backup root-only, SHA-256 íntegro e arquivo `tar` recuperável; extração independente do controlador e do volume.
- Preservação do volume original, serviço estranho e workspaces; nenhuma exclusão de dados pelo novo utilitário.
- Bloqueio contra reutilizar rótulo de backup, corrupção e mudança de inventário durante a captura.

**Etapas ainda pendentes:** inventário e desmontagem independente do worker do host, conta do executor, ponte VPS, WireGuard, UFW, DNS/TLS e outras dependências compartilhadas; ensaio de restauração integral de serviço OAuth/SQLite sob demanda e aceite operacional do proprietário. A remoção local testada **não equivale à desinstalação completa do sistema distribuído**.
