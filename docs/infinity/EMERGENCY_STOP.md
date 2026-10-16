# Pórtico Infinity | Desligamento de emergência (operador)

**Estado:** script e testes automatizados implementados. A execução com Docker real será validada na VM descartável antes de qualquer instalação no SRV-IA.

O controlador Infinity reside em `/srv/portico-infinity`, fora da área delegada à IA (`/opt`). O script `scripts/infinity-emergency-stop.py` faz inventário de contêineres Docker e **para exclusivamente Gateway e Broker da instância Infinity**. Não toca nos serviços Community/Cloud, não remove dados e não executa comandos provenientes da IA.

## Procedimento em console do proprietário

Antes de utilizar, verificar identidade da máquina e o diretório de controle. Executar a prévia para conferir os IDs verificados:

```bash
cd /srv/portico-infinity
sudo python3 scripts/infinity-emergency-stop.py --dry-run
```

Para interromper as entradas de controle da IA, fora do MCP:

```bash
cd /srv/portico-infinity
sudo python3 scripts/infinity-emergency-stop.py --execute
```

O script recusa funcionar fora do checkout esperado ou sem root, exige `/srv` e `/srv/portico-infinity` root-owned e sem escrita de grupo/terceiros, consulta a identificação Docker, verifica o nome do projeto **`portico-infinity`** e a etiqueta de diretório `/srv/portico-infinity` de *todos* os contêineres encontrados, e somente após essa validação para os serviços `gateway` e `broker`. Se qualquer contêiner tiver propriedade não verificável, **aborta antes de executar um único stop**.

Verifica ainda que cada contêiner de destino ficou parado e retorna erro se a operação falhar. Em caso de falha parcial, o operador deve inspecionar a máquina manualmente. Não executa `docker compose down`, remove volumes, apaga arquivos, redes ou imagens. Não usa `scripts/remove.sh` nem `scripts/update.sh` herdados, atualmente incompatíveis com os nomes de projeto Infinity.

## Garantias e limitações

- Esta é uma **parada de emergência do runtime**, não uma revogação criptográfica de tokens, nem desabilitação permanente de contas OAuth.
- O dono deverá manter controle do console/SSH fora do MCP. Se o Docker reiniciar os processos por uma política externa, o operador deve bloqueá-los também nesse plano de controle externo.
- Não impede serviços do host que sejam independentes do Infinity. Não reverte operações previamente executadas.
- A instalação Infinity não deve ser retomada até conferir configuração, política, auditoria e motivo da interrupção.
- Teste obrigatório no laboratório descartável: subir contêineres Infinity isolados, comprovar identificação de projeto e checkout, parar exclusivamente os dois serviços, verificar estado final, examinar serviços de terceiros intocados.

## Referências

- [Primeiro E2E](FIRST_E2E.md)
- [Política /opt privilegiada](ROOT_PERSISTENTE.md)
- [Critérios de aceite](ACCEPTANCE.md)
