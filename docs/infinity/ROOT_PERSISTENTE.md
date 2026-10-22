# Privilégios persistentes confinados a /opt: decisão técnica do Pórtico Infinity

**Decisão revisada em 16/10/2026. Estado: especificação e código de base existente, sem aceite Infinity E2E.**

## Requisito do proprietário

No laboratório SRV-IA, dar à IA acesso persistente, sem renovação temporal, para trabalhar em todos os projetos atuais e futuros em `/opt`, inclusive operações que requerem privilégios administrativos sobre arquivos. O proprietário aceita root global em último caso, mas prefere o limite real ao diretório `/opt` quando viável.

## Teto selecionável para novas instalações (implementação em avaliação)

O caminho padrão continua sendo `/opt`. Para um host Linux novo, o operador pode fornecer `--scope /mnt` (exemplo de diretório existente e protegido), que passa a ser o teto físico e a única raiz estática de leitura, escrita e shell não-root no perfil Infinity.

A seleção é explícita, exige caminho absoluto canônico, ancestrais protegidos e ausência de links simbólicos, e recusa diretórios de sistema/identidade, incluindo `/srv`, `/etc`, `/usr`, `/var`, `/root` e `/home`. O plano de controle permanece em `/srv/portico-infinity`, fora do teto.

Exemplo de prévia **apenas em laboratório descartável**, sem iniciar serviços:

```bash
bash scripts/install-infinity.sh --scope /mnt --local-only \
  --run-as portico-infinity-exec --yes --ack-infinity-opt
```

A confirmação interativa, quando presente, inclui o diretório escolhido. Uma instalação existente não pode mudar de teto reaplicando o instalador; isso requer migração controlada própria.

**Atualização do código (22/10/2026):** a PR #26 (merge `41970625c53eb15626a49222bd5bf746a847edea`) adaptou o manifesto do worker, a validação de origem/destino e o `ReadWritePaths` da unidade ao **teto físico selecionado pelo operador**. Há testes sintéticos de teto alternativo e isolamento. A ativação de cópia durável permanece **OFF por padrão**, e o ciclo completo MCP/systemd, recuperação após reboot e aceite em host com teto alternativo ainda exigem homologação. A implementação em código não autoriza ativação no SRV-IA nem release.

## Decisão de engenharia

**Padrão Infinity: `opt-privileged`, sem shell root arbitrário. Instalação do controlador em `/srv/portico-infinity`.**

- `/opt` é raiz estática de leitura/escrita e de shell **não-root**; não depende de concessões dinâmicas com TTL.
- O **Broker privilegiado** recebe operações *tipadas*, aplica política por identidade, ferramenta, ação e caminho e restringe acesso aos descritores permitidos em `/opt`. A implementação herdada já inclui `file.chmod`, `file.chown`, `file.write`, `file.patch`, `file.remove` e operações de leitura; a permissão de cada ação deve ser habilitada conscientemente e validada.
- `shell.exec` continua rodando com identidade não-root confinada. **Não** criar `sudo NOPASSWD:ALL`, não disponibilizar `bash`/Python root com simples `cwd=/opt`, não usar `chroot` sozinho como garantia de isolamento.
- Todas as chamadas mantêm autenticação OAuth, identidade MCP fixa, auditoria, limites por execução, controle de concorrência e revogação via operador externo ao MCP.
- `shell.exec_admin` global, `Full`, pacotes APT, firewall, gestão de usuários, systemd global, Docker privilegiado e escape de rede permanecem desativados no perfil padrão. Se necessário, uma **edição avançada root global** será um modo separado, opt-in explícito, jamais fallback automático.
- O checkout e o plano de controle ficam em `/srv/portico-infinity` com proprietário root e sem permissão de escrita do usuário da IA; o Broker só recebe o bind de trabalho de `/opt`. Segredos protegidos continuam sujeitos às políticas específicas do Broker, e a revogação deve ter componente externo.

Isso atende à maior parte das tarefas de desenvolvimento, criação, edição, propriedade e manutenção de projetos. Não permite todos os comandos possíveis com sudo. Quando uma atividade exigir alterar `/usr`, `/etc`, o kernel ou outro serviço global, ela será deliberadamente tratada à parte.

## Por que "sudo apenas em /opt" não é uma propriedade garantível por cwd

`sudo /bin/bash` com diretório atual `/opt` continua permitindo `cd /`, abrir `/etc`, carregar bibliotecas e executar processos externos. Regras `sudoers` que aceitam scripts ou editores com argumentos genéricos também não impõem confinamento de caminhos. Capabilities, montagens, dispositivos, sockets e Docker podem ampliar autoridade. Um chroot simples tampouco é um isolamento robusto para um processo root.

O isolamento implementável consiste em **não conceder root genérico** e expor operações previamente definidas, cujos caminhos são validados pelo Broker. Os mecanismos de resolução seguros (por exemplo `openat2`, `RESOLVE_BENEATH`, `RESOLVE_NO_MAGICLINKS` e operações por descritor) servem para evitar travessia de caminhos, *não* para transformar um processo root arbitrário em root restrito.

### Riscos residuais importantes

- Um arquivo editável em `/opt` pode ser consumido **posteriormente por um serviço com privilégios globais**, permitindo impacto fora de `/opt`; isso inclui scripts, automações, unidades de serviço, configurações e hooks. O limite por caminho não protege contra esse efeito indireto.
- `chmod`/`chown` devem proteger hardlinks, symlinks, montagens, caminhos reservados e operações concorrentes. Avaliar se a IA pode escrever em projetos de infraestrutura de controle.
- Docker/Compose de host é normalmente root-equivalente quando operações arbitrárias permitem montar `/` do servidor. Não incluir `docker.sock` como acesso irrestrito.
- Proteções de identidade, auditoria e revogação precisam estar fora do conjunto que a IA pode editar. Um desligamento externo (túnel/proxy, provedor OAuth ou console) é indispensável para este perfil avançado.

## Condições de implementação e aceite

1. Política `opt-privileged` instalável com advertência PT-BR/EN e confirmação do proprietário **fora do MCP**; padrão fail-closed.
2. Identidade fixa: autenticação de emissor, audiência, sujeito e instância, com revisão no Broker antes de executar.
3. Acesso persistente estático a `/opt`, sem abuso de `max_ttl_minutes=0`, que atualmente significa fallback de uma hora.
4. Negar leitura/escrita e operações administrativas fora de `/opt`; testar `../`, symlink, hardlink, `/proc`, mount bind e alterações concorrentes.
5. Testar leitura, escrita, exclusão deliberada, `chmod` e `chown` sobre dados descartáveis com auditoria e resultados idempotentes onde couber.
6. Bloquear `shell.exec_admin`, `Full`, `sudo` root, Docker privilegiado e rotas indiretas de elevação no perfil padrão.
7. Comprovar reconexão/reboot sem expirar política, ao lado de revogação/recuperação pelo operador externo.
8. Instalar o Pórtico em `/srv/portico-infinity`, separado do `/opt` delegável, e comprovar negação de acesso do usuário executor ao checkout.
9. Executar a suíte em ambiente descartável e depois o E2E no SRV-IA.

**Situação:** configuração conceitual definida; operações privilegiadas tipadas e infraestrutura de confinamento já existem no Community, mas o perfil Infinity ainda não foi homologado. Nenhuma permissão nova foi aplicada ao SRV-IA.

## Fontes técnicas

- `sudoers(5)`: https://man7.org/linux/man-pages/man5/sudoers.5.html
- `openat2(2)`: https://man7.org/linux/man-pages/man2/openat2.2.html
- Segurança Docker: https://docs.docker.com/engine/security/
