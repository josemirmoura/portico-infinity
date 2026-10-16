# Pórtico Infinity | Preparação do primeiro E2E

**Status:** roteiro operacional, **não executado**. Destinado somente a laboratório descartável antes do SRV-IA. A suíte CI do código deve estar integralmente verde antes do gate de instalação.

## Decisão de diretórios e identidades

| Recurso | Destino | Autoridade |
| --- | --- | --- |
| Código e controle Infinity | `/srv/portico-infinity` | `root:root`; sem escrita de grupo/outros |
| Configuração, tokens e política | `/srv/portico-infinity/.env`, `config/policy.yaml` | arquivos `0600` |
| Estado, auditoria local e socket | diretórios/volumes exclusivos Infinity | Broker/controlador, não shell da IA |
| Projetos e artefatos trabalhados | `/opt` | leitura/escrita e operações de arquivo tipadas via Broker |
| Execução de shell normal | `portico-infinity-exec` | conta separada, não-root, sem grupos `sudo`, `docker`, `wheel`, `lxd`, `libvirt` |
| Shell root e `apt` | fora do MVP | **desativados** |

O código do controlador permanece fora de `/opt`, inclusive nos testes. Não instalar a cópia Infinity em `/opt` nem misturar `.env`, volumes, identidade ou portas com Community/Cloud.

## Resultado experimental já executado

Os E2E **Docker local e OAuth real em laboratório** concluídos com sucesso estão documentados em [Critérios de aceite](ACCEPTANCE.md). O workflow reproduz contêineres reais e usa autenticação estática de laboratório. **Ele não substitui a instalação pública/OAuth nem a validação no SRV-IA.**

## Gate 0: verificar a máquina descartável

Antes de alterar a máquina, registrar hostname, arquitetura, Docker/Compose, portas ocupadas, versão do kernel, disco disponível e ponto de restauração. A primeira execução não deve ocorrer na VPS de produção ou no SRV-IA.

Conferir que `/srv` e seus ancestrais pertencem a root e não são graváveis por terceiros. Não confiar apenas no `pwd`: bloquear symlinks, ACLs inesperadas, pontos de montagem e arquivos com dono inesperado.

Criar a conta de serviço exclusiva e obter o repositório público como root são ações **do operador**, executadas apenas após revisão do ambiente; por exemplo, na VM descartável:

```bash
sudo useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin portico-infinity-exec
sudo git clone https://github.com/josemirmoura/portico-infinity.git /srv/portico-infinity
cd /srv/portico-infinity
sudo python3 scripts/check-infinity-layout.py --run-as portico-infinity-exec
```

Não executar os comandos se o usuário já existir, o diretório já estiver ocupado, ou houver qualquer instalação anterior. **Não apagar pastas existentes para preparar o teste**. Inspecionar e fazer snapshot antes.

## Gate 1: configuração e instalação da VM descartável

Validar que a CI está verde no SHA a ser testado. O fluxo interativo aprovado pelo proprietário é:

```bash
cd /srv/portico-infinity
sudo bash scripts/install-infinity.sh --run-as portico-infinity-exec --lang pt-BR
```

A palavra de confirmação local é `AUTORIZO INFINITY /opt`. Não usar `--yes` por conveniência no primeiro E2E. O instalador não pode modificar configuração antiga silenciosamente.

O perfil `--local-only` é apenas **prévia de política, sem inicialização de Docker**; não certifica integração MCP, OAuth nem instalação concluída. Atualmente a prévia cria arquivos de estado local do bootstrap, e o instalador real exige checkout inicial limpo. Portanto, não executar essa prévia sobre a futura instalação real nem descartá-la como teste inofensivo de produção.

## Gate 2: isolamento e privilégios

- [ ] O serviço Broker usa política `scoped` com teto físico `/opt`, `Full` desativado e usuário de shell separado.
- [ ] `/opt` permite operações tipadas `file.write`, `file.patch`, `file.chmod`, `file.chown` em arquivos descartáveis.
- [ ] `/srv/portico-infinity`, `/etc`, `/usr`, `/root` e `/var` negam todas as mutações do MCP.
- [ ] Shell comum não obtém `sudo`, `docker.sock`, `nsenter` privilegiado nem grupo administrativo.
- [ ] `shell.exec_admin`, `package.install`, `service.*` global e administração Docker irrestrita continuam negados.
- [ ] Validar `../`, symlink, hardlink, bind mount, `/proc`, TOCTOU, arquivos protegidos e consumidor root indireto dentro de `/opt`.
- [ ] Confirmar que o controlador mantém segredos `0600`, source root-owned, e o Broker registra e audita as operações.

**Atenção:** permitir editar scripts em `/opt` pode alterar programas executados posteriormente por root. A proteção por diretório não impede esse efeito indireto, e o plano de controle também depende de proteção do canal OAuth/túnel externo.

## Gate 3: conectividade real e revogação

- [ ] HTTPS, identidade OAuth específica, audience/issuer/scopes e autenticação ChatGPT válidos.
- [ ] Auditoria correspondente a uma chamada autenticada de `system.info`.
- [ ] Reinício/reconexão mantém a política estática, não a sessão OAuth.
- [ ] Operador para apenas os serviços Gateway/Broker Infinity por meio de `scripts/infinity-emergency-stop.py --execute`, com etiquetas Docker de proprietário verificadas; consultar [runbook de emergência](EMERGENCY_STOP.md).
- [ ] Revogação externa interrompe chamadas futuras, com recuperação verificada do serviço e snapshot.
- [ ] Testar remoção/rollback isolados, sem atingir Community, Cloud ou outros projetos `/opt`.

Somente após registrar esses resultados a versão estará apta a um primeiro E2E no SRV-IA. Cada gate tem critério objetivo: falhou, corrigir o defeito específico e repetir apenas os testes relacionados, evitando loops de auditoria indefinidos.

## Trabalhos ainda pendentes

1. Homologar a validação do checkout root-owned e o perfil inicial em ambiente descartável com Docker real.
2. Configurar endpoint HTTPS/túnel do SRV-IA sem IP fixo e OAuth independente.
3. Validar auditoria e revogação fora da máquina.
4. Formalizar instalação limpa, atualização e remoção específica do Infinity.

Referências: [Arquitetura](ARCHITECTURE.md), [Política privilegiada](ROOT_PERSISTENTE.md), [Gates de aceite](ACCEPTANCE.md).
