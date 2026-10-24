# Infinity | Cerca de segurança de jobs Docker Compose

**Implementação candidata**, aguardando CI e homologação do operador. Não há implantação no SRV-IA.

## O problema

Antes desta correção, a tentativa de iniciar `compose.pull/up/down` pesquisava **todos os jobs ativos** do SQLite. Um job Compose encerrado no estado `reconcile_required` já não estava na lista de ativos e poderia, em certas condições, deixar uma segunda operação atravessar a mesma pasta de projeto sem reconciliar a primeira.

## Regra corrigida

1. Recusar diretórios Compose relativos ou com grafia não canônica (pontos e barras redundantes). As permissões de projeto, `canonicalComposeProject`, isolamento de volumes e autorização seguem sendo verificadas na execução normal.
2. Consultar apenas o projeto exato no SQLite, com índice parcial. O filtro contém jobs `compose.pull/up/down` ativos **e `reconcile_required`**. Jobs de shell, transferência binária e outros projetos não ocupam a consulta.
3. Diante de uma unidade ainda ativa, status ausente ou `reconcile_required`, retornar `resource_busy` **sem iniciar outro Compose**. Nenhuma autorização externa é emitida e a intenção da nova operação é liberada para uma tentativa posterior.
4. Caso o status da unidade seja verificavelmente terminal, persistir a mudança por comparação atômica de estado; não sobrescrever terminal previamente confirmado por consulta concorrente. Repetir a busca até não haver bloqueador, com teto de 64 verificações por requisição, falhando fechado ao exceder.
5. O proprietário deve revisar unidades com status desconhecido pelo canal de operador. Não aplicar `compose down` ou `systemctl stop` automaticamente nessa etapa.

**Limites:** não normaliza automaticamente registros históricos gravados sob grafias alternativas do mesmo caminho; exigem revisão antes de uma migração. Esta correção trata conflito e segurança de replay, **não a coleta durável de unidades Compose** descrita na [Issue #12](https://github.com/josemirmoura/portico-infinity/issues/12). Não executa deploy, revogações ou atualização de contêineres no SRV-IA.

**Aceite:** CI Go/race, testes de 600 jobs shell e 150 Compose externos, caso `reconcile_required` entre sujeitos distintos, rejeição de alias de caminho, idempotência/retentativa sem efeitos, empacotamento e prova efêmera. UAT do host e GO v1.0 continuam separados.
