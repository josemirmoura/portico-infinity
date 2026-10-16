# Revisão de segurança | DELETE-001 | 16/10/2026

**Estado:** bloqueado para merge e implantação. Código experimental na [PR #7](https://github.com/josemirmoura/portico-infinity/pull/7), issue de acompanhamento [#6](https://github.com/josemirmoura/portico-infinity/issues/6).

## Objetivo

Permitir a remoção de subárvores autorizadas que excedam 50 mil entradas sem percorrer os outros projetos de `/opt` e sem enfraquecer proteções de segredos. A pasta real `/opt/nara-vui-lab` permanece preservada e **não** é alvo dos testes.

## Confirmado na revisão

- O preflight antigo `requireSensitiveSubtreeWork` varria todas as raízes autorizadas, inclusive projetos irmãos, via `ProtectedAliases` com teto global de 50 mil entradas. Isso reproduz a natureza do erro informado, sem evidência de exclusão parcial no episódio.
- A PR #7 usa `VisitProtectedSubtree` em lotes de 256 entradas, examinando apenas o alvo. Testes verificam árvore >50 mil entradas, segredo dentro da árvore, segredo em diretório irmão, hardlink e symlink.
- Foi adicionada classificação explícita de link simbólico denominado `.env` como caminho protegido, mesmo que a remoção seja apenas do link, sem seguir o destino.
- Todas as verificações do SHA `b08879b...` haviam passado, mas isso **não cobre** os riscos abaixo. Os commits posteriores exigem nova CI.

## Bloqueadores para aprovação

### 1. TOCTOU entre preflight e remoção

O preflight verifica uma subárvore; em seguida, `securefs.Manager.Remove` percorre e remove por nomes em uma operação separada. Um processo do host pode substituir uma pasta por symlink, renomear uma subárvore ou inserir um arquivo `.env` após a primeira inspeção. `os.Root` restringe saídas do root autorizado, mas **não garante** que a operação continue confinada à mesma árvore alvo na presença de renomeações internas. A rotina `removeTree` também usa `Readdir(-1)`, materializando listas grandes, e pode ter falha após exclusões parciais.

**Aceite:** percorrer e remover de forma ancorada a descritores estáveis de diretório, com `O_NOFOLLOW` por componente, validação de inodes e proteção de segredos no próprio fluxo destrutivo. Planejar staging/quarentena segura ou mecanismo equivalente que evite reportar exclusão completa se houver interrupção. Se não for possível assegurar consistência sob concorrência, exigir árvore quiescente por contrato e bloquear efetivamente mutações externas durante a operação. Testar substituição adversarial em corrida.

### 2. Bind mounts no mesmo dispositivo

Comparar apenas `st_dev` não distingue montagens bind no mesmo filesystem. O Linux expõe identificador de montagem (por exemplo, `statx(..., STATX_MNT_ID)`) e técnicas de resolução sem cruzar mounts (`openat2` com `RESOLVE_NO_XDEV`), que precisam ser avaliadas tanto na inspeção quanto na remoção. **Aceite:** qualquer transposição de mount, inclusive bind no mesmo device, falha fechado antes de afetar dados. Teste Linux controlado com privilégio apropriado; não interpretar `SKIPPED` como aprovação.

### 3. Integridade, autorizações e relatórios

Verificar aprovação `sensitive.work` exata, revogação durante operação longa, auditoria de intenção/resultado e comportamento após interrupção/erro de I/O. Interrupção após início pode deixar árvore parcialmente removida: documentar e reportar `reconcile_required`/recuperação supervisionada, nunca sucesso automático. Ponderar limites de tempo, ocupação de memória, caminhos e diretórios profundos.

## Fluxo de homologação

1. Resolver os dois bloqueadores em código e testes determinísticos, com CI completa.
2. Passar por revisão independente de segurança. Manter PR em rascunho até haver evidências.
3. E2E Docker em ambiente descartável, em diretório isolado com >50 mil entradas, segredos, mount bind, mutação concorrente e falha de I/O.
4. Merge na main apenas com aceite explícito; atualizar SRV-IA com backup e rollback de Broker/Gateway, preservando ZITADEL e VPS Community.
5. Validar MCP real em diretório experimental. Somente depois, planejar parada dos serviços de `/opt/nara-vui-lab` e eventual remoção autorizada, sem usar automaticamente seu `remove.sh`.

**Não executar `rm -rf`, `remove.sh` nem `file.remove(recursive=true)` no projeto Nara durante esse trabalho.**
