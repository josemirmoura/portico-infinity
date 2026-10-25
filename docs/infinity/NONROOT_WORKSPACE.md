# Infinity | Criação segura de workspace para shell não-root

**Estado em 25/10/2026:** `file.mkdir(for_shell=true)` está disponível no catálogo ChatGPT após atualização da integração; o SHA de código instalado no SRV-IA `48d8c15978a675090a052b977b47abd1fbdd87f9` **ainda falha operacionalmente** com `configured shell account does not exist`. Identificou-se que `os/user.Lookup` consultava o `/etc/passwd` do contêiner Broker, enquanto o executor `shell.run_as` existe no host. A correção proposta usa a consulta host-only `hostexec`/`getent passwd` sob timeout e parser restrito, preservando identidade definida na política. **Ainda não implantada nem aprovada em UAT**.

## Motivação e fluxo

O `file.mkdir` tradicional cria uma subpasta com permissão `0750` e herda o dono/grupo do diretório pai. Em instalações nas quais `/opt` pertence a um usuário diferente da conta isolada `shell.run_as`, o `shell.exec(cwd=novo_diretorio)` pode falhar com `systemd status=200/CHDIR`. Nunca ampliar todas as pastas para `0777` nem transferir a árvore `/opt` ao executor.

Somente no Infinity com `shell.enabled=true` e acesso estático ou delegado de escrita **e shell** à pasta escolhida:

1. Crie ou identifique um **pai autorizado já existente** ao qual a conta `shell.run_as` consiga chegar; a operação não muda a propriedade desse pai.
2. Chame `file.mkdir` com `path: /opt/projeto-novo` e `for_shell: true`.
3. O Broker **resolve a conta de `shell.run_as` na política, não no cliente**, usando a identidade do host Linux via consulta tipada `getent passwd`/`hostexec` (não o cadastro de usuários interno do contêiner); verifica que ela não é root e faz criação do diretório **novo** (perm `0750`), `fchown` no descritor do inode recém-criado e verificação de identidade/perm. `path` não pode ser raiz autorizada, arquivo existente, link simbólico, diretório existente nem caminho com pais ausentes. A operação não altera nenhum arquivo/diretório preexistente.
4. Só então execute `shell.exec` com `cwd` exatamente nessa pasta. O sandbox segue limitado ao workspace e mascara nomes de segredos.

Se o diretório já existir, `for_shell: true` **falha fechado**. Não serve como ferramenta para ajustar recursivamente propriedade de projetos existentes. A migração de diretórios legítimos exige intervenção operacional consciente e controle de serviços consumidores root.

## Segurança e recuperação

- O modo padrão `file.mkdir(path)` permanece idêntico; Community sem a variável de plano de controle Infinity não aceita a opção.
- Acesso MCP a `/srv`, identidades root e caminhos fora da política continuam negados; não há acesso a Docker administrativo.
- Se `fchown`, `fchmod`, abertura ou inspeção falharem **após o mkdir**, a pasta parcial pode permanecer. Não executar rollback por nome sob corrida TOCTOU. O erro explicita `MANUAL RECOVERY`, exigindo inspeção do operador antes de reusar.
- Acesso/execução também depende das permissões de travessia de todos os ancestrais existentes, dos grupos suplementares, mounts e da política do sandbox. A criação tipada não modifica esses pré-requisitos.
- Corridas com mutadores externos após criação não são transação atômica de ownership para sempre; o caminho é reverificado, mas uma alteração posterior pode exigir reconciliação.

## Validação exigida antes de declarar entregue

Testes automatizados: recusa de raiz, escapes, `..`, symlinks, pais ausentes, diretórios existentes e conta root; manutenção de arquivos anteriores; criação e verificação UID/GID/permissões; replay/idempotência; isolamento Community.

Em CI efêmero, exercitar ciclo `file.mkdir(for_shell=true) → shell.exec(cwd=projeto) → arquivo criado por UID shell`, incluindo negação de shell fora do teto e do modo normal. O UAT no SRV-IA exige **autorização específica posterior para implantar a correção de identidade no Broker**: a opção existe no código instalado, mas a resolução da conta no contêiner falha. Depois do upgrade autorizado, testar somente diretório sintético novo, UID/GID do host, execução/arquivo não-root, negação de root/fora de `/opt`, persistência e limpeza sem alterar projetos reais.

**Rollback:** reverter o merge antes de implantar; sem alteração na API padrão, políticas estáticas ou infraestrutura. Nunca reconfigurar `/opt` inteiro como remediação.

## Diagnóstico adicional e mitigação

Falha reproduzida após atualização do catálogo do conector ChatGPT em 25/10/2026: o campo `for_shell` apareceu como opcional e `file.mkdir(for_shell=true)` retornou `permission_denied: configured shell account does not exist`; nenhuma pasta foi criada. O host tem conta executor `portico-infinity-exec` (UID 999/GID 987) confirmada por UAT anterior, mas a imagem `broker` Debian não cria essa conta e `os/user.Lookup` consultava erroneamente sua base de contas interna. A biblioteca `internal/hostexec` já expõe comandos tipados no namespace do host sob `VPS_AGENT_HOST_NSENTER=1`; a correção usa `getent passwd` com nome **somente** de `shell.run_as` da política root-owned, sem comando arbitrário, sem cliente fornecer UID/GID e sem criar ou alterar usuários. Falha/timeout/parsing inválido negam a operação. Os fluxos Community e `file.mkdir` comum permanecem inalterados. Não executar `chown` generalizado ou criar conta homônima dentro da imagem como solução.
