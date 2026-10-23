# Checkpoint terminal quando a preparação do worker falha

**Escopo:** worker de cópia binária do Infinity, ainda sujeito a autorização e UAT. Nenhuma alteração no SRV-IA/VPS.

O worker registra `running` antes de abrir a raiz segura do filesystem. Se `securefs.NewWithHostRoot` falhar por raiz indisponível, insegura ou configuração inválida, o job ainda não iniciou a cópia. Antes desta correção, o processo saía deixando `running` durável, forçando diagnóstico ambíguo de um job que nunca transferiu bytes.

**Novo contrato:** nesse ponto específico, registrar `failed`, com 0 bytes, e impedir o replay automático pelo mesmo ID. A falha de gravação do status é reportada como incerteza e exige reconciliação do operador; jamais marcar como `done`. Uma falha ocorrida **após** iniciar a transferência segue as classificações existentes e não pode ser presumida sem publicação.

**Teste negativo reproduzível:** `TestWorkerWorkspaceInitializationFailureIsTerminal` usa raiz host inválida apenas no laboratório, comprova `failed`, bytes=0, destino ausente e segunda execução recusada. Executar CI do SHA exato do PR, incluindo testes Go do worker e o E2E real systemd descartável.

**Rollback:** reverter o commit desta correção; não modificar dados persistentes nem reexecutar automaticamente jobs antigos. O sucesso desse gate não certifica retomada após crash, recuperação de jobs incertos, refresh humano ou lançamento.
