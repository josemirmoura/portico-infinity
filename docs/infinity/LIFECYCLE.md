# Pórtico Infinity | Ciclo de vida do runtime (pré-v1.0)

**Estado atualizado em 22/10/2026:** implementação de desativação e remoção **restrita ao runtime** aprovada em CI e Docker real descartável (PR #27, merge `c3a8a154fc3d9a1c4b1d20c2d78869471906ff9b`, 20/20 checks; [workflow](https://github.com/josemirmoura/portico-infinity/actions/runs/39824785485)). Não executada no SRV-IA; não constitui desinstalação completa, revogação OAuth nem autorização de release.

O utilitário `scripts/infinity-lifecycle.py` é independente do `scripts/remove.sh` herdado do Community. Opera exclusivamente na instância **`portico-infinity`**, com diretório de controle **`/srv/portico-infinity`**. Requer console/root do proprietário fora do MCP e conferência das etiquetas Docker de contêineres, redes e volumes **antes de qualquer alteração**. Serviços desconhecidos, projeto estrangeiro, rede compartilhada e workers de cópia ativa causam bloqueio.

## Procedimento seguro

Executar em console local/SSH independente do Pórtico, **somente depois de revisão do inventário**, jamais como teste destrutivo no SRV-IA sem janela e autorização próprias:

```bash
cd /srv/portico-infinity
sudo python3 scripts/infinity-lifecycle.py --dry-run
# Desativar de forma reversível (impede reinício automático dos contêineres atuais)
sudo python3 scripts/infinity-lifecycle.py --deactivate --confirm DEACTIVATE-INFINITY
# Remover apenas contêineres e redes privadas do Infinity, sem apagar dados
sudo python3 scripts/infinity-lifecycle.py --uninstall-runtime --confirm UNINSTALL-INFINITY-RUNTIME
```

**Prévia:** consulta Docker; nenhum container, volume, rede, arquivo ou segredo é alterado.

**Desativação reversível:** muda a política de reinício dos contêineres para `no` e os para. Mantém todos os contêineres parados, volumes, banco de identidade, política e trilha de auditoria. Para reativar, o proprietário deve revisar o estado e executar o procedimento de inicialização Compose específico da instalação. Não equivale a revogação OAuth e não garante inatividade de automações externas.

**Desinstalação do runtime:** para e remove os contêineres comprovadamente pertencentes ao Infinity e suas redes privadas, se estiverem sem outros clientes conectados. Preserva deliberadamente os volumes Docker, `/srv/portico-infinity`, `.env`, `state/`, `backups/`, banco OAuth, trilha de auditoria, configuração, WireGuard/Traefik/DNS externos e **todo o teto de trabalho**, incluindo `/opt`. Nem imagens globais nem recursos do Community/Cloud são removidos. Operação idempotente se os recursos do Infinity já estiverem ausentes.

## Segurança e escopo do aceite

- O script exige operador root, checkout em `/srv/portico-infinity` e controlador/script protegidos, de propriedade de root. Identidades, nomes, serviço, diretório de origem e redes são validados. A ação não aceita recursos de propriedade incerta.
- A verificação de workers systemd `portico-filecopy-*.service` é fail-closed. Quando houver cópia ativa, o operador precisa cancelar/reconciliar o job separadamente, **antes** da desativação/desinstalação.
- Falhas parciais do Docker interrompem as próximas etapas. Conferir inventário e estado reais para diagnóstico.
- Os volumes continuam intactos para permitir rollback e recuperação. **A remoção irreversível de dados de identidade/estado deve ser uma etapa separada**, condicionada a inventário de volumes, backup, exportação da auditoria, revogação de sessões e confirmação explícita.
- Revogação criptográfica de OAuth, despublicação de Traefik/DNS/VPN, remoção dos serviços systemd persistentes e limpeza final do controlador ainda **não** são realizadas automaticamente por este utilitário. Portanto, não descreva `--uninstall-runtime` como desinstalação completa da v1.0.
- A inspeção usa Docker e systemd do host e exige ambiente Linux compatível. Os testes unitários são simulados; o workflow `infinity-lifecycle-disposable.yml` executa contêineres reais e um projeto de terceiros sintético num runner descartável.

## Gates que permanecem

1. **PASS (runtime-only):** CI e Docker E2E descartável da PR #27 no SHA exato. Revalidar apenas se a implementação ou ambiente correspondente mudar.
2. Desinstalação **completa** segura e recuperação pós-falha: inventário + backups + destruição verificada de recursos exclusivamente Infinity, preservando os dados do teto; rotas/identidade externas sob autoridade do operador.
3. E2E global OAuth/revogação, worker multigigabyte/recovery, reboot, isolamento e critérios do `EXECUTION_PLAN.md`.
4. Teste pessoal do proprietário e **GO/NO-GO expresso** antes de qualquer publicação v1.0.

O utilitário não substitui `infinity-emergency-stop.py`, cuja finalidade é uma interrupção pontual e sem alteração de política de restart.
