# 0024. Adaptador do Claude Code: modo restrito, ambiente sintético e regras do projeto injetadas pelo Kluzo

- Status: aceito
- Data: 2026-11-08
- Origem: investigação 3 ([docs/research/claude-code-runtime.md](../../docs/research/claude-code-runtime.md))

## Contexto

O item 6 da v0.1 roda o Claude Code como subprocesso headless. O CLI carrega por padrão configurações, hooks e servidores MCP do usuário e do projeto, lê o `HOME` real e pula o diálogo de confiança no modo `-p`. Num worktree escrito por um agente, isso deixa o agente reescrever a própria guarda, alcançar credenciais do host e plantar código que roda no commit do Kluzo.

## Decisão

1. **Invocação** (argv congelado no manifesto da sessão):
   `claude -p --input-format stream-json --output-format stream-json --verbose --restricted --strict-mcp-config --disable-slash-commands --session-id <uuid> --model … --effort … --max-turns … --max-budget-usd <restante do nó> --append-system-prompt-file <arquivo 0600 com os segmentos 1, 2 e regras do projeto> --exclude-dynamic-system-prompt-sections --tools … --allowedTools … --permission-mode dontAsk --permission-prompts none --mcp-config <arquivo 0600> --settings <arquivo 0600> --include-hook-events`.
   Os arquivos de configuração ficam fora do worktree, e o token vale só para a sessão.
2. **Regras do projeto injetadas pelo Kluzo.** `AGENTS.md`, `CLAUDE.md` e as skills do papel são lidos da ponta da milestone no despacho, como os gates (ADR 0020), e entram no system prompt depois do papel. `.claude/**`, `CLAUDE.md` e `.mcp.json` entram no padrão de `protected_paths`.
3. **Ambiente sintético.** `HOME` próprio por workspace, credencial do modelo por variável (`CLAUDE_CODE_OAUTH_TOKEN` ou `ANTHROPIC_API_KEY`), sem `SSH_AUTH_SOCK`, com `GIT_TERMINAL_PROMPT=0` e `DISABLE_AUTOUPDATER=1`, mais o `env_allow` do runtime.
4. **Git do Kluzo endurecido.** Todo git do Kluzo roda sem hooks nem fsmonitor e sem configuração global ou de sistema. Depois de cada sessão, os refs, `.git/config` e `.git/hooks` são conferidos; divergência falha o nó e é restaurada.
5. **Ferramentas.** `read` → `Read, Glob, Grep`; `edit` → `Edit, Write, NotebookEdit`; `bash` → `Bash`; `test` → `Bash` restrito aos comandos de `Verify`, mas tratado como execução de código no modelo de ameaça. O `init` precisa listar exatamente as ferramentas esperadas mais `mcp__kluzo__kluzo_result`, senão a sessão é abortada. `advisor` e `subagents` são recusados pelo `roles check` neste adaptador até o mapeamento ser verificado.
6. **Guarda no harness.** Um hook `PreToolUse` HTTP para o servidor local do Kluzo aplica em Go as mesmas regras da conferência pós-rodada, com `onFailure: block`. Um canário aborta a sessão se um `tool_use` chegar sem o evento de hook correspondente. A garantia continua sendo a conferência do diff.
7. **Resultado.** Sucesso só com `subtype: success`, `is_error: false` e `kluzo_result` aceito; um resultado aceito vence um erro posterior. `error_max_turns` falha a etapa; `error_max_budget_usd` bloqueia por orçamento; 401/403 pausa o workspace e notifica; 429/529 e erros de execução são repetidos com backoff, até 2 sessões novas na mesma tentativa.
8. **Limites e custo contados pelo Kluzo.** Turnos e custo são somados no stream (custo por `modelUsage`), e o Kluzo chama `Cancel` ao estourar. As flags do CLI são uma segunda barreira.
9. **Retomada.** Só quando os hashes dos segmentos 1, 2 e das regras do projeto não mudaram; senão, sessão nova. UUID do harness = UUIDv5 de (`ss_…`, contador). Mesmo cwd, `HOME` e argv.
10. **Versão.** `min_version` padrão 2.1.295. O adaptador tem fixtures de stream por versão testada e grava a versão vista no `init`. Tipos de evento desconhecidos ficam só no log bruto.

## Consequências

- O agente não consegue alterar a guarda, os hooks do git nem as regras do projeto a partir do worktree; o que tenta fica registrado.
- A credencial do modelo continua legível pelo agente (mesmo usuário do sistema). O isolamento completo depende de usuário dedicado, container ou VM (ADR 0018).
- O aceite do item 6 inclui a lista de verificações empíricas da nota de pesquisa, rodada numa máquina autenticada.
- O adaptador do OpenCode (v0.2) segue o mesmo desenho: hook HTTP para o mesmo endpoint, ambiente sintético e regras do projeto injetadas.
