# F29 · CLI

- **Grupo:** G. Plataforma
- **Status:** planejada

- **O quê:** a interface de linha de comando, base de todas as outras superfícies.
- **Comportamento:**
  - Comandos: `init`, `doctor`, `plan`, `approve`, `run`, `status`, `pause`, `resume`, `answer` e `serve` (lista em [interfaces.md](../interfaces.md#cli)).
  - Usa a mesma camada de comandos (`internal/app`) que a web.
  - `doctor` verifica as CLIs dos harnesses (versão mínima), os tokens, o acesso ao tracker e a configuração.
  - Saída legível por padrão; `--json` em `status` para scripts.
  - Códigos de saída: 0 ok, 1 erro de uso, 2 configuração inválida, 3 falha de integração.
- **Aceite:** todo comando com efeito também funciona pela web (a partir da v0.3), e com o mesmo resultado.
- **Versão:** v0.1 (`serve` na v0.3).
