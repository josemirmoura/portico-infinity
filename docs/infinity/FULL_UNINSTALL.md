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

## Conta não-root do executor: inventário, não exclusão

O instalador Infinity aceita `--run-as USUARIO`, que **precisa existir antes da instalação**. Não executa `useradd` nem registra propriedade exclusiva da conta; ela pode ser pessoal, de automação ou compartilhada. Por isso, **uma desinstalação segura nunca deve executar `userdel`, `groupdel`, bloquear login, apagar o HOME ou encerrar processos dessa conta como efeito colateral**.

O operador pode realizar uma auditoria **somente leitura**, enquanto o controlador root-owned ainda está instalado:

```bash
cd /srv/portico-infinity
sudo python3 scripts/infinity-executor-account-audit.py --inspect
```

A rotina lê apenas o `shell.run_as` da política local, exige identidade não-root existente, lista grupos e conta processos vivos associados ao UID sem expor argumentos de processos. Reporta possíveis grupos administrativos e **sempre** informa `safe_automatic_account_deletion: false`. Não modifica contas, grupos ou processos, mesmo que o usuário escolhido se chame `portico-infinity-exec`.

O CI cria um usuário sintético **fora do Infinity**, inicia um processo pertencente a ele e executa o fluxo completo de backup e desinstalação local. O gate exige que a conta e o processo continuem intactos após remover o controlador, os contêineres e os volumes. A conta sintética só é eliminada na limpeza do runner descartável.

**Se o proprietário desejar encerrar uma conta dedicada depois do UAT:** isso será uma decisão de administração geral do host, após auditar quem mais a utiliza. Não faz parte dos comandos automáticos de desinstalação Infinity.

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

## Descomissionamento do worker privilegiado no host (antes da fase 2)

O [procedimento root-only](../../scripts/infinity-host-worker-lifecycle.py) protege o executável fixo `/usr/local/libexec/portico-infinity/portico-filecopy-worker`. Ele deve ser **retirado antes de excluir o controlador `/srv/portico-infinity`**; a remoção local passa a bloquear expressamente um worker ainda instalado.

Ele exige ausência de jobs `systemd` ativos, falhados, retidos ou com checkpoints inconclusivos, e verifica se nenhum processo Linux ainda executa o mesmo inode. A leitura exige root, proteção dos ancestrais e recusa links simbólicos/hardlinks e permissões inseguras. A remoção exige o **SHA-256 completo do binário instalado e confirmação correspondente aos 20 primeiros caracteres do hash**.

```bash
cd /srv/portico-infinity
sudo python3 scripts/infinity-host-worker-lifecycle.py --inspect

# SOMENTE após reconciliar/cancelar todos os trabalhos e aprovar a
# remoção externa: copie o SHA-256 completo retornado acima.
sudo python3 scripts/infinity-host-worker-lifecycle.py \
  --remove-worker \
  --expected-sha256 '<SHA256_COMPLETO_DO_EXECUTAVEL>' \
  --confirm 'REMOVE-INFINITY-HOST-WORKER-<20_PRIMEIROS_HEX>'
```

**Não executa `userdel`, `systemctl stop`, `docker rm` ou `rm -rf`**, não limpa o diretório de auditoria, e não altera o teto de trabalho, a conta do executor, serviços de terceiros, rede ou identidade. A conta de host `portico-infinity-exec` necessita inventário separado e remoção manual, se comprovado que não é compartilhada. Testes com `systemd` transitório real e binário do projeto ocorrem apenas em runner descartável.

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

## WireGuard e UFW: inventário independente, sem exclusão automática

A interface `wg-infinity`, a identidade privada `/etc/wireguard` e a regra UFW UDP `51822` foram criadas **fora do Docker e do ciclo de vida local**, por procedimentos de administração separados em dois hosts. Mesmo com um túnel configurado originalmente para o Infinity, não é possível concluir automaticamente que nenhum outro serviço passou a usá-lo depois.

O novo `scripts/infinity-wg-decommission-audit.py` implementa **somente leitura**. Confere root, caminhos sem symlinks, arquivos de chave/configuração privados, uma única seção `[Peer]`, `AllowedIPs` estritamente locais, ausência de hooks do `wg-quick` (por exemplo `PostDown`, `SaveConfig`, `Table`, `DNS`) e correspondência entre a chave privada local e sua chave pública por `wg pubkey`. Também inventaria o estado da unidade `wg-quick@wg-infinity.service`, a interface, os peers e o endereço local **sem exibir chaves privadas ou endpoints**.

A verificação exige execução pelo operador em **cada host**, com a função correspondente:

```bash
# Na VPS, execução apenas após autorização de inspeção e conferência do SHA:
sudo python3 scripts/infinity-wg-decommission-audit.py --inspect --role vps

# No SRV-IA, leitura separada e autorização específica:
sudo python3 scripts/infinity-wg-decommission-audit.py --inspect --role srv-ia
```

A saída `shared_use_ruled_out: false` é **intencional**: mesmo uma configuração exata e um único peer não provam ausência de dependências externas. A rotina não modifica `wg-quick`, serviços, routes, WireGuard, UFW, certificados, DNS, arquivos `.conf` ou chaves.

**Gate ainda pendente para uma retirada real:** após o encerramento da ponte VPS e dos jobs, inventariar manualmente os consumidores do túnel, interfaces/rotas, política de boot nos dois peers e a regra exata UFW (incluindo comentário, duplicidade e finalidade). Só um procedimento com aprovação do proprietário, rollback de conectividade e verificação de isolamento poderá desativar o túnel. **Não apagar regras pelo número exibido no UFW sem novo inventário, nem excluir chaves privadas como efeito colateral de desinstalação Docker.**

## Fase 3: desativação isolada da ponte HTTPS na VPS

A ponte Infinity na VPS não é o Traefik principal. Ela tem projeto Compose próprio (`portico-infinity-edge`) e usa a rede Docker `traefik-public` **compartilhada**. O [procedimento da ponte](../../deploy/infinity-vps-edge/README.md) inclui `infinity-edge-lifecycle.py` para inventário, desativação reversível e remoção **apenas do contêiner `bridge`**.

A rotina exige etiquetas Docker de projeto, serviço e diretório de trabalho correspondentes, montagem de configuração read-only, ausência de portas publicadas ou redes inesperadas, além de confirmação explícita. Nunca altera a rede externa, o proxy Traefik principal, ACME/TLS, DNS, WireGuard, UFW, o controlador ou os dados no SRV-IA. Teste de Docker real em runner descartável verifica dois serviços estrangeiros preservados e a rede compartilhada com a mesma identidade.

**Importante:** o procedimento foi projetado para uma ação operacional independente, **não autorizada nesta rodada**. O restante do descomissionamento distribuído continua dependente de inventário e decisões explícitas para worker/conta do SRV-IA, WireGuard em ambos os peers, regras firewall e DNS/TLS.

## Provas e falhas intencionais

O workflow `infinity-uninstall-private-backup-disposable` usa Docker/Compose reais, controlador sintético root-owned, volume privado de identidade com conteúdo fictício e contêiner estrangeiro. Ele exige:

- Recusa sem confirmação de backup e com contêiner ativo, rótulos/mountpoints falsos, volume compartilhado ou destino pré-existente.
- Backup root-only, SHA-256 íntegro e arquivo `tar` recuperável; extração independente do controlador e do volume.
- Preservação do volume original, serviço estranho e workspaces; nenhuma exclusão de dados pelo novo utilitário.
- Bloqueio contra reutilizar rótulo de backup, corrupção e mudança de inventário durante a captura.

**Etapas ainda pendentes:** após inventário WireGuard root-only, desmontagem controlada e autorizada de WireGuard, UFW, DNS/TLS e quaisquer dependências compartilhadas; ensaio de restauração integral do serviço OAuth/SQLite sob demanda e aceite operacional do proprietário. A conta do executor é **preexistente e preservada**, não um artefato a remover; o worker e a ponte VPS possuem procedimentos separados com provas em runners descartáveis e autorização operacional ainda pendente. A remoção local testada **não equivale à desinstalação completa do sistema distribuído**.
