# Pórtico Infinity: arquitetura e fronteiras

Estado: **especificação**, baseada no Community de 13/10/2026. O perfil root permanente não está implementado.

## Componentes

```text
ChatGPT / cliente MCP com login OAuth
        |
        | HTTPS + MCP + autenticação de audiência/sujeito
        v
Gateway não-root, sem docker.sock
        |
        | Unix socket protegido
        v
Broker (autorização por ação/sujeito/host)
        |                 |
        |                 +--> auditoria externa / corte externo
        v
Linux SRV-IA, projetos e trabalhos em /opt
        |
        +--> executor privilegiado (somente opt-in, futuro)
```

1. **Acesso estático básico.** Política read/write e shell `/opt` ativa sem TTL. A política pode ser revogada por operador; não é delegação dinâmica renovável.
2. **Root persistente.** Exige extensão explícita de policy/Broker em separado, com identidade fixa e decisão local fora do MCP. Não usar `grant.max_ttl_minutes=0`: no código atual esse valor aplica o fallback de 1 hora.
3. **Separação de identidades.** Uma identidade Linux de serviço pode existir; preferir Broker privilegiado a `sudo NOPASSWD:ALL` em usuário SSH reutilizável. Mesmo com processo de Gateway não-root, a capacidade administrativa requer controle forte do Broker.
4. **Sem fronteira física para root.** Diretório `/opt` orienta trabalho cotidiano, mas root pode acessar `/`, rede, Docker, senhas e arquivos protegidos. Não prometer `/opt` como isolamento do root.
5. **Plano de controle.** Priorizar credenciais e configuração em diretórios protegidos e backups externos. Root pode adulterar controles locais; revogação via provedor OAuth, proxy/túnel externo ou console do host deve permanecer possível fora do canal da IA.
6. **Resiliência.** Shell pode ter limites por job. A autorização não expira, mas cada execução é limitada e recuperável; execução autônoma entre sessões exige agente/scheduler separado.
7. **Instalação.** Checkout de código de projeto proposto em `/opt/portico-infinity`, isolado da instalação `/opt/portico-community` e de quaisquer volumes/portas/usuários do Cloud. Não sobrescrever produção nem compartilhar segredos de instâncias.
8. **Rede doméstica.** SRV-IA sem IP fixo pode conectar-se por túnel HTTPS de saída; DNS público/túnel e OAuth serão validados no E2E.

## Segurança e avisos públicos

É obrigatório apresentar aviso ⚠️ em PT-BR/EN e exigir confirmação consciente em modo root. A IA não deve poder aprovar sua própria ampliação de autoridade. Não implantar root persistente enquanto revogação externa, logs fora da máquina e backup/restauração não forem testados.

## Não objetivos do MVP

Cloud SaaS, Windows/macOS, múltiplos nós, token OAuth eterno, autonomia 24h sem agente executor ou uso de credenciais SSH pessoais.
