# F29 · CLI

- **Grupo:** G. Plataforma
- **Status:** parcial (v0.1 item 10a: `init`, `roles check`, `doctor`, `plan`, `approve`, `run`, `status`, `pause`, `resume`)

- **O quê:** a interface de linha de comando, base de todas as outras superfícies.
- **Comportamento:**
  - Comandos: `init`, `doctor`, `plan`, `approve`, `run`, `status`, `pause`, `resume`, `answer` e `serve` (lista em [interfaces.md](../interfaces.md#cli)).
  - Usa a mesma camada de comandos (`internal/app`) que a web.
  - Na v0.1, `plan` recebe o **número** da milestone no tracker e lê o plano `.kluzo/plans/<título>.yaml`; o workspace fica em `~/.local/share/kluzo/workspaces/<projeto>-<título>/` (ou sob `$XDG_DATA_HOME`). Os outros comandos recebem o **título** da milestone, que nomeia o workspace.
  - `approve` revalida o plano contra o projeto e o tracker e põe no outbox o commit do plano na branch da milestone (`git.plan`).
  - `run` roda o supervisor até o workspace terminar (merged, cancelled ou failed) ou até Ctrl-C; o que estava em andamento fica no outbox para o próximo `run`.
  - `pause` e `resume` valem com o `run` parado ou rodando: o laço lê o estado a cada passo. `resume` exige `doctor` verde.
  - `doctor` verifica a configuração (papéis incluídos), o `git` (2.38 ou mais novo) e as CLIs dos harnesses usadas pelos papéis (versão mínima, flags e credencial do modelo), o token do tracker (`Whoami`) e as credenciais do host (ADR 0020). Worktrees órfãos, recursos Docker e disco ficam para o item 10b.
  - Saída legível por padrão; `--json` em `status` para scripts.
  - Códigos de saída: 0 ok, 1 erro de uso, 2 configuração inválida, 3 falha de integração.
- **Aceite:** todo comando com efeito também funciona pela web (a partir da v0.3), e com o mesmo resultado.
- **Versão:** v0.1 (`serve` na v0.3).
