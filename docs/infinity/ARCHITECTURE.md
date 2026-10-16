# Pórtico Infinity: arquitetura e fronteiras

**Estado:** especificação em 16/10/2026; execução E2E pendente.

```text
ChatGPT / IA autenticada via MCP + OAuth
          |
          v
Gateway não-root (HTTPS; sem docker.sock)
          |
          | Unix socket de acesso controlado
          v
Broker privilegiado + política estática de /opt
          |                       |
          |                       +--> auditoria / revogação externa
          |
          +--> operações de arquivo tipadas: read/write/patch/chmod/chown/etc.
          |
          +--> shell NÃO-root confinado a /opt
          |
          x  shell root global, Full, host Docker irrestrito (desativados)
```

## Contrato padrão: `opt-privileged`

1. Autorização permanente do proprietário para `/opt`, sem TTL; expiração dos tokens OAuth e dos jobs é independente.
2. Caminhos e ferramentas são verificados pelo Broker, com proteção contra traversal/symlinks e auditoria obrigatória.
3. Privilégios administrativos existem **apenas dentro das operações de arquivo específicas do Broker**. Nenhuma conta `sudo NOPASSWD:ALL` e nenhuma execução root arbitrária são delegadas à IA.
4. A identidade autenticada é fixa e a política não é expansível por ferramentas MCP.
5. Proteções específicas para arquivos sensíveis, administração e acesso de rede continuam ativas.
6. Regras de serviço e Docker precisam considerar efeitos indiretos fora de `/opt`. Docker com montagem arbitrária de host é root-equivalente e não integra o perfil padrão.

## Root global avançado (opcional, futuro)

O proprietário autoriza explicitamente uma instância/sujeito diferente ou política avançada isolada. O acesso ultrapassa `/opt`; exige aviso ostensivo, revogação fora do host, auditoria externa e E2E próprio. Nunca usar como fallback silencioso nem anunciar como root restrito por diretório.

## Instalação e separação de projetos

**Decisão de instalação:** código-fonte e plano de controle em `/srv/portico-infinity`, fora do teto de trabalho da IA (`/opt`), com propriedade `root:root` e acesso negado ao usuário de execução. Os arquivos `.env`, `config/policy.yaml`, estado e credenciais pertencem exclusivamente ao runtime protegido. `/opt` permanece reservado a projetos da IA. Community (`/opt/portico-community`) e Cloud (`/opt/portico-cloud`) continuam independentes. A infraestrutura externa de revogação deve permanecer fora da autoridade do MCP.

No SRV-IA sem IP fixo, preferir túnel HTTPS de saída e identidade OAuth dedicada. O runtime da VPS atual e o Community permanecem intocados.

## Segurança e limites operacionais

O padrão impede que uma solicitação normal execute qualquer comando root, mas não garante isolamento absoluto contra arquivos de `/opt` consumidos por serviços privilegiados. Não basta impedir `../`: examinar montagem, Docker, hooks, automações e serviços executados como root.

Mesmo com a permissão persistente, a conexão MCP, os limites das chamadas ChatGPT e a execução de tarefas continuam sujeitos às restrições dos respectivos serviços. Um agente/scheduler separado será necessário para trabalho autônomo contínuo.
