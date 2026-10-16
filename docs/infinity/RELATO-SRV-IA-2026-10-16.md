# Diário de campo | Primeiro ChatGPT ↔ Pórtico Infinity ↔ SRV-IA

**Data:** 16/10/2026 · **Estado:** E2E parcial, não certificado · **Host:** `srv-ia`.

## Objetivo e contexto
Derivação **pública e independente** do Pórtico Community para dar à IA autenticada por MCP acesso persistente a projetos Linux em `/opt`, sem renovações periódicas de delegação de diretório. O Community e o Cloud não devem ser modificados. O controle administrativo do Infinity fica protegido fora do teto da IA.

## Evidências de execução real no SRV-IA
Na sessão ChatGPT de 16/10:
- `system.info`: hostname `srv-ia`, Linux amd64, 12 CPUs, gateway `v0.1.0-rc.7`, instance `portico-infinity`, teto `/opt`.
- `permissions.status`: `mode=scoped`, `full_enabled=false`.
- `permissions.list_root_access`: `filesystem_read=["/opt"]`, `filesystem_write=["/opt"]`, `shell_cwd_roots=["/opt"]` como raízes estáticas; sem delegação dinâmica.
- `system.health`: `ok=true`, `audit_chain_ok=true`, `jobs_configured=true`, `docker_configured=true`.
- Evidência experimental preexistente reaproveitada: `/opt/portico-infinity-e2e-20261016/PROVA.txt`, contendo marcador de teste sem segredo.
- `file.read` da prova: PASS; `file.patch` com SHA-256 esperado: PASS; nova leitura: PASS; `file.stat`: PASS.
- `file.write` de `TEMP-DELECAO.txt`: PASS; `file.chmod` (0640): PASS; `file.chown` (0:0): PASS; `file.remove` somente do arquivo temporário: PASS.
- `file.read("/etc/passwd")`: negado (`permission_denied: path outside authorized roots`): PASS.
- `file.read("/opt/../etc/passwd")`: negado (`permission_denied: path traversal is forbidden`): PASS.
- `shell.exec` com comando não destrutivo em `cwd=/opt/portico-infinity-e2e-20261016`: **FAIL**, antes de executar, por `sensitive_path_check_failed: cannot safely prepare shell secret mask: sensitive-path scan exceeded safety limit`.

A prova `PROVA.txt` foi preservada, atualizada com a etapa `editado e relido via MCP autenticado`. Não houve shell executado nem alteração deliberada em outros projetos do host. A chamada MCP autenticada demonstra conexão funcional, mas não equivale a teste independente da cadeia OAuth nem prova de revogação externa.

## Correção prioritária (defeito reproduzido)
**SHELL-001:** O scanner de caminhos sensíveis excede seu limite de segurança ao preparar um `shell.exec` mínimo em diretório experimental. Investigar escopo de varredura, limites de recursão, custo e estratégia de máscara. A correção deve manter falha fechada para segredos; **não** desativar varredura, expor paths sensíveis ou conceder shell root. Reproduzir em laboratório descartável, cobrir com teste de regressão, repetir em SRV-IA após revisão.

**Aceite objetivo:** `id -u; id -Gn; pwd` conclui via conta não-root sob `/opt`; comandos fora da política seguem negados; testes de caminhos sensíveis seguem aprovados; auditoria do Broker verificável.

## Pendências antes do release
1. Corrigir SHELL-001 e retestar shell não-root, sem alargar permissões.
2. Testar controles adversariais adicionais (symlinks, hardlinks, bind mounts, TOCTOU, caminhos protegidos e efeitos indiretos).
3. Confirmar persistência da política após reboot completo e reconexão.
4. Confirmar OAuth específico, revogação externa e parada de emergência pelo operador no ambiente real.
5. Homologar instalação limpa, atualização, desinstalação e isolamento de Community/Cloud.
6. Atualizar README, FIRST_E2E, ACCEPTANCE e guia de instalação após evidência, nunca antecipando aprovação.

## Aprendizados para quem instalar em servidor próprio
- Distinguir **teto do projeto** (`/opt`) de **plano de controle** protegido (`/srv/portico-infinity`).
- A autorização persistente do diretório não remove expiração OAuth nem confirmações exigidas pelo cliente.
- O Broker deve impor permissões, e a IA não pode aprovar sua própria elevação.
- Testes inofensivos de leitura, escrita e tentativa de escape são úteis; aprovação de shell exige verificação separada.
- Evitar utilizar um servidor de produção como laboratório de exploração; manter um plano externo de parada e recuperação.
- Este documento registra uma sessão específica, não autoriza divulgar o produto como estável.
