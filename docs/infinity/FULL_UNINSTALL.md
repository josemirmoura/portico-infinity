# Infinity I7 | Preparação para desinstalação integral

**Estado:** fase 1 implementada e testada em ambiente descartável. **Não há comando de remoção integral autorizado ou implementado neste procedimento.** Nunca executar em SRV-IA/VPS sem autorização específica do proprietário.

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

## Provas e falhas intencionais

O workflow `infinity-uninstall-private-backup-disposable` usa Docker/Compose reais, controlador sintético root-owned, volume privado de identidade com conteúdo fictício e contêiner estrangeiro. Ele exige:

- Recusa sem confirmação de backup e com contêiner ativo, rótulos/mountpoints falsos, volume compartilhado ou destino pré-existente.
- Backup root-only, SHA-256 íntegro e arquivo `tar` recuperável; extração independente do controlador e do volume.
- Preservação do volume original, serviço estranho e workspaces; nenhuma exclusão de dados pelo novo utilitário.
- Bloqueio contra reutilizar rótulo de backup, corrupção e mudança de inventário durante a captura.

**A segunda fase ainda está pendente**: implementar contrato de exclusão por manifesto fixado e confirmação de perdas, ensaiar exclusão total e restauração da identidade em runner/VM efêmero, certificar isolamento de VPS/Community/Cloud e obter autorização do proprietário para UAT em ambiente operacional. Nem `--uninstall-runtime` nem `--verify` equivalem à desinstalação completa.
