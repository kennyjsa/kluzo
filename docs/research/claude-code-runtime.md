# Investigação 3 · Adaptador de runtime do Claude Code

Pesquisa de 08/11/2026, sobre o Claude Code 2.1.319.

## Fontes

- `claude --help` da versão 2.1.319, lido no ambiente.
- Uma execução real de `claude -p --output-format stream-json --verbose` sem credencial: ela não chegou ao modelo, mas mostrou o formato do stream e como uma falha aparece no evento `result`.
- Documentação oficial do Claude Code e do Agent SDK (via Context7): `ResultMessage`/`SDKResultMessage`, saída estruturada, configuração de MCP HTTP com cabeçalhos, hooks HTTP.
- Referências de terceiros sobre o modo headless, só para confirmar flags que não aparecem no `--help` (`--max-turns`).

## Fatos verificados

| Fato | Origem |
|---|---|
| `--output-format stream-json` exige `--verbose` | documentação e execução |
| O stream traz `system`/`init` (com `session_id`, `model`, `cwd`, `permissionMode`, `mcp_servers`, `claude_code_version`, `apiKeySource`), mensagens `assistant` e `user`, e termina com `result` | execução |
| Aparecem tipos não documentados no meio do stream (`active_goal`, `autocompact_state`, `system`/`status`) | execução |
| O `result` tem `subtype` ∈ `success`, `error_during_execution`, `error_max_turns`, `error_max_budget_usd`, `error_max_structured_output_retries`, mais `is_error`, `terminal_reason`, `stop_reason`, `api_error_status`, `num_turns`, `total_cost_usd`, `usage`, `modelUsage`, `permission_denials`, `errors`, `structured_output` | documentação |
| **`subtype: success` com `is_error: true`** acontece: erro de API, recusa, ferramenta indisponível. A execução sem credencial terminou assim (`terminal_reason: api_error`), com código de saída 1 | execução e documentação |
| `usage` cobre só o laço principal; `total_cost_usd` e `modelUsage` incluem subagentes | documentação |
| `--session-id <uuid>` deixa o chamador escolher o id da sessão; `--resume <id>` retoma | `--help` |
| `--system-prompt-snapshot` (padrão `on`) grava o system prompt na primeira requisição e o reutiliza na retomada | `--help` |
| `--append-system-prompt` mantém o system prompt do Claude Code e acrescenta o nosso; `--exclude-dynamic-system-prompt-sections` move cwd, ambiente e git status para a primeira mensagem, o que melhora o cache | `--help` |
| `--tools` define o conjunto de ferramentas embutidas disponíveis; `--allowedTools`/`--disallowedTools` aceitam padrões como `Bash(git *)` | `--help` |
| `--permission-mode dontAsk` e `--permission-prompts none`: o que pediria permissão é negado, sem travar a sessão | `--help` |
| `--mcp-config` aceita JSON com servidor `type: http`, `url` e `headers`; `--strict-mcp-config` ignora os demais servidores | `--help` e documentação |
| `--settings` aceita JSON; `--setting-sources` escolhe quais arquivos de configuração (`user`, `project`, `local`) carregar | `--help` |
| Hooks `PreToolUse` do tipo HTTP fazem POST do evento para uma URL, com cabeçalhos; `onFailure: block` (2.1.295+) bloqueia a ação quando o hook falha | documentação |
| `--max-budget-usd`, `--effort`, `--model`, `--fallback-model`, `--json-schema` | `--help` |
| `--max-turns` existe, mas não aparece no `--help` | documentação de terceiros |
| `--bare` ignora OAuth e exige `ANTHROPIC_API_KEY` | `--help` |

## Rascunho da decisão (antes da validação adversária, mantido para registro)

### Invocação

```
claude -p --output-format stream-json --verbose
  --session-id <uuid derivado do ss_<ulid>>
  --model <model> --effort <effort> --max-turns <max_turns> --max-budget-usd <budget>
  --append-system-prompt <segmentos 1 e 2> --exclude-dynamic-system-prompt-sections
  --tools <mapeamento do papel> --permission-mode dontAsk --permission-prompts none
  --mcp-config <json: servidor kluzo, http, Authorization: Bearer <token da sessão>> --strict-mcp-config
  --settings <json gerado: hooks e permissões>  --setting-sources project
```
O segmento 3 vai pelo stdin. Retomada: o mesmo comando com `--resume <uuid>` no lugar de `--session-id`.

### Mapeamento de ferramentas

| Canônica | Claude Code |
|---|---|
| `read` | `Read`, `Glob`, `Grep` |
| `edit` | `Edit`, `Write`, `NotebookEdit` |
| `bash` | `Bash` |
| `test` | `Bash` permitido só para os comandos da seção `Verify` (`--allowedTools "Bash(<cmd>)"`) |

Sem `WebFetch`, `WebSearch` nem `Agent`, a não ser que o papel declare `subagents`.

### Saída

O resultado oficial continua sendo o `kluzo_result` via MCP (ADR 0019). O `--json-schema` não é usado, para não ter duas saídas concorrentes. Uma sessão só é bem-sucedida com `subtype: success`, `is_error: false` e um `kluzo_result` aceito.

| Fim da sessão | Tratamento |
|---|---|
| `error_max_turns` | falha da etapa, causa `max_turns` |
| `error_max_budget_usd` | `blocked` (`budget`) |
| `success` com `is_error` e `stop_reason: refusal` | falha da etapa, causa `refusal`; triagem |
| `error_during_execution`, `terminal_reason: api_error`, processo morto sem `result` | erro de runtime repetível; nova sessão na mesma tentativa, até 2 vezes |
| `success` sem `kluzo_result` aceito | falha da etapa (saída ausente) |

### Normalização de eventos

| Stream | Evento do Kluzo |
|---|---|
| `system`/`init` | `session.started` (versão, modelo, modo de permissão). Se o servidor MCP `kluzo` não estiver `connected`, a sessão é abortada |
| `assistant` texto | `session.message` |
| `assistant` `tool_use` | `tool.call`; `Edit`/`Write` → `file.edited`; `Bash` → `command.run` |
| `user` `tool_result` | `tool.result` |
| `result` | `cost` (com `total_cost_usd` e `modelUsage`) e `session.finished` |
| tipo desconhecido | ignorado, contado no manifesto |

### Guarda dentro do harness

Um hook `PreToolUse` HTTP, gerado no `--settings`, faz POST para o próprio servidor local do Kluzo (mesma porta e token do MCP). O Go aplica as mesmas regras de ADR 0020 que a conferência pós-rodada: bloqueia git de escrita, `gh`, escrita em caminho protegido, teste apagado ou com skip novo. `onFailure: block` mantém a guarda fechada se o Kluzo não responder. A garantia continua sendo a conferência do diff depois da rodada; o hook só avisa cedo.

### Processo e ambiente

- Grupo de processos próprio; `Cancel` manda SIGTERM ao grupo e SIGKILL depois de 10 s.
- Ambiente mínimo: `PATH`, `HOME` (credenciais do próprio Claude Code), `LANG`, mais o `env_allow` do runtime.
- O stream bruto vai para `logs/sessions/<id>.jsonl`, depois do redact.

### Versão mínima e verificação

`min_version` padrão sobe para 2.1.295 (hooks HTTP com `onFailure`). O `Check` lê `claude --version`, confere as flags exigidas no `--help`, e roda `claude auth status` para saber se há credencial.

## Validação adversária

Um agente sem acesso ao rascunho anterior leu o rascunho, os contratos e os ADRs 0018, 0019, 0020 e 0023, conferiu o `claude --help` e procurou onde o desenho falharia. Todas as críticas foram aceitas:

| # | Crítica | Decisão |
|---|---|---|
| 1 | `--setting-sources project` carrega `.claude/settings.json` e hooks que o agente escreveu na rodada anterior, sem diálogo de confiança no `-p`; o reviewer executaria esse código | `--restricted` (ignora settings de user, project e local, mantém `--settings`, confina arquivos aos diretórios de trabalho, trava escrita em settings, git e configuração de ferramentas), `--strict-mcp-config` e `--disable-slash-commands`. `.claude/**`, `CLAUDE.md` e `.mcp.json` entram em `protected_paths`. As regras do projeto passam a ser lidas pelo Kluzo da ponta da milestone e injetadas no prompt |
| 2 | O `.git` compartilhado fica fora do diff: hooks, `core.hooksPath`, `core.fsmonitor`, filtros e refs podem ser alterados pelo Bash do agente e rodar no commit do Kluzo, com o token do bot | O git do Kluzo roda com `core.hooksPath=/dev/null`, `core.fsmonitor=false`, `GIT_CONFIG_NOSYSTEM=1` e `GIT_CONFIG_GLOBAL` neutro. Depois de cada sessão, o Kluzo confere os refs contra o journal e o hash de `.git/config` e `.git/hooks`; divergência falha o nó e restaura |
| 3 | Passar o `HOME` real expõe `~/.ssh`, `gh`, credential helper e as credenciais do próprio Claude Code, contra o ADR 0020 | `HOME` sintético por workspace (persistente, porque guarda os transcripts da retomada). Credencial do modelo por `CLAUDE_CODE_OAUTH_TOKEN` ou `ANTHROPIC_API_KEY`. Sem `SSH_AUTH_SOCK`; `GIT_TERMINAL_PROMPT=0`. Limite documentado: a credencial do modelo continua legível pelo agente |
| 4 | Tokens do MCP e do hook no argv aparecem em `/proc/*/cmdline` | `--mcp-config` e `--settings` como arquivos `0600` fora do worktree; o token vale só para a sessão e o estado dela no servidor |
| 5 | Com `dontAsk`, `--tools` só disponibiliza; sem `--allowedTools` tudo é negado. `Bash(<cmd>)` não é fronteira: `go test` executa código do agente | `--allowedTools` explícito. `test` conta como execução de código no modelo de ameaça. No `init`, a lista de ferramentas tem de ser exatamente a esperada, senão a sessão é abortada |
| 6 | Um `--settings` inválido é ignorado em silêncio no `-p`, e a guarda some | Canário com `--include-hook-events`: um `tool_use` sem o evento de hook correspondente aborta a sessão. O hook é defesa adicional; a garantia é a conferência pós-rodada |
| 7 | A retomada não tem regras: snapshot do system prompt, colisão de UUID, transcript cortado no meio de um `tool_use` | Retoma só se os hashes dos segmentos 1, 2 e das regras do projeto forem iguais; senão, sessão nova. UUID da sessão do harness = UUIDv5 de (`ss_…`, contador). Retomada com mesmo cwd, `HOME` e argv congelado no manifesto. Retomada após SIGKILL entra no teste |
| 8 | `Session.Send` não funciona com prompt por stdin de texto | `--input-format stream-json`, com o fechamento do stdin controlado pelo adaptador |
| 9 | Classificação incompleta: 401/403, 429/529, resultado aceito antes de um erro, orçamento | 401/403: o workspace vai para `paused` (credencial do runtime) e notifica, sem retry. 429/529: backoff. Um `kluzo_result` aceito vence um erro posterior. `--max-budget-usd` recebe o orçamento restante do nó |
| 10 | `advisor` e `subagents` não têm flag no CLI | O adaptador recusa essas chaves no `roles check` até o mapeamento ser verificado; `Agent` só entra em `--tools` quando o mapeamento existir |
| 11 | Custo errado com subagentes ou retomada | Tokens e custo de `modelUsage`; conferir se `total_cost_usd` acumula na retomada; sem `result`, estimar pelo `usage` de cada mensagem |
| 12 | `--max-turns` oculto; `Check` por leitura do `--help` é frágil; `min_version` divergente entre documentos | Turnos e custo contados no stream, com `Cancel` determinístico; `--max-turns` passado também. Faixa de versões testada com fixtures de stream; `DISABLE_AUTOUPDATER=1`; versão gravada do `init`. `min_version` padrão único: 2.1.295 |
| 13 | O status do MCP no `init` pode vir `pending` | Exigir `mcp__kluzo__kluzo_result` na lista de ferramentas do `init` |
| 14 | Tipo de evento desconhecido: o rascunho ignora, `events.md` não diz | Fica só no log bruto e é contado no manifesto; não vira evento no journal |

## Decisão final

[ADR 0024](../../.agents/adr/0024-adaptador-claude-code.md).

## Resultado das verificações locais (08/11/2026, Claude Code 2.1.319)

Rodadas na máquina do Kenny, com haiku e orçamento baixo. Todas confirmaram o ADR 0024; os fatos novos estão marcados.

| Verificação | Resultado |
|---|---|
| `--restricted` + `--settings` | Hook HTTP dispara com o cabeçalho; hooks do projeto e do usuário ignorados |
| `--settings` inválido | JSON malformado vira caminho de arquivo e falha com saída 1, sem stream. **Hook com `type` inválido é ignorado em silêncio**: o canário é necessário |
| `onFailure: block` | Conexão recusada, timeout, 500 e JSON inválido bloqueiam; aparece em `permission_denials` |
| Hook em subagente | Dispara no `Agent` e nas ferramentas do subagente, com `agent_id`. **O `init` lista `Task`, mas o `tool_use` se chama `Agent`** |
| `dontAsk` sem `--allowedTools` | Negado; o hook roda antes da negação |
| `Bash(go test *)` | `; touch …` e `$(…)` negados, nada criado |
| `init` | Lista exata de ferramentas e MCP `connected` |
| Sessões | `--session-id` repetido falha (saída 1, sem stream). Retomada após SIGKILL no meio de um `tool_use` funciona. **Retomada com `--append-system-prompt` diferente ignora o novo em silêncio** |
| Custo | **`total_cost_usd` e `modelUsage` acumulam entre retomadas**; `usage` vale só a invocação. Custo de execução morta por SIGKILL se perde. Subagentes entram em `total_cost_usd` e `modelUsage` |
| Limites | `--max-turns` aceito (`error_max_turns`); **`--max-budget-usd` é checado depois da chamada e pode estourar** |
| 401/429 (servidor falso) | `success`, `is_error: true`, `terminal_reason: api_error`, `api_error_status` preenchido, saída 1 |
| `HOME` sintético | Sem variável de token: não logado. Caminho com `CLAUDE_CODE_OAUTH_TOKEN` **ainda não verificado** (`claude setup-token` é interativo) |
| Prompt grande e `stream-json` | 238 KB pelo stdin ok. **Cada mensagem gera `init` e `result` próprios no mesmo processo**, com custo acumulado: `result` não marca o fim do processo |
| Recusa | `success`, `is_error: true`, `stop_reason: refusal` |
| `--max-budget-usd` na retomada (08/11/2026, item 7c, Q40) | **O limite vale para a invocação, não para o total acumulado da conversa.** Numa conversa com US$ 0,0041 acumulados, uma retomada de uma volta com limite de US$ 0,002 terminou em `success`; outra, com limite de US$ 0,003, passou da primeira volta (invocação de ~US$ 0,0006, acumulado já acima do limite), rodou as ferramentas e só parou (`error_max_budget_usd`, `terminal_reason: budget_exhausted`, saída 1) depois da segunda volta, cuja invocação custou ~US$ 0,23. O adaptador passa à retomada o que resta ao nó, como no início. **A segunda volta criou 231 mil tokens de cache numa conversa de 17 mil**, sem causa visível no transcript: o estouro de uma volta pode ser grande, e o orçamento do nó é conferido pelo Kluzo depois de cada sessão |
| Corpo do hook HTTP (08/11/2026, item 7c, Q41) | O `PreToolUse` traz `tool_use_id`, igual ao `id` do `tool_use` no stream: o canário confere por id. Um `Edit` sem `Read` antes passa pelo hook (a checagem de leitura é posterior). Chamadas recusadas pela validação do próprio harness (entrada inválida, ferramenta inexistente) voltam com `<tool_use_error>` e, por não terem rodado, não abortam a sessão sem hook. **A volta custou US$ 0,13 em haiku por criação de cache anômala**, como na verificação de Q40 |
| `--append-system-prompt-file` (08/11/2026, item 6e) | Funciona (o texto do arquivo entra no system prompt), mas **não aparece no `--help`**, como `--max-turns`. Arquivo inexistente: `Append system prompt file not found`, saída 1, antes de qualquer chamada; flag desconhecida: `unknown option`. O `Check` sonda a flag assim |

Consequências no adaptador: custo calculado como diferença entre retomadas; `--max-budget-usd` da retomada é o que resta ao nó; system prompt por `--append-system-prompt-file`; formato real dos eventos de hook nos fixtures; alias `Task`/`Agent` quando subagentes forem mapeados.

## Verificações obrigatórias numa máquina autenticada (aceite do item 6)

- `--restricted` com `--settings`: o hook HTTP dispara; o `.claude/settings.json` do projeto é ignorado.
- `--settings` inválido: o canário aborta a sessão.
- `onFailure: block` com conexão recusada, timeout, 500 e JSON inválido.
- O hook dispara nas ferramentas de subagente (quando houver).
- `dontAsk` com `--tools Edit` e sem `--allowedTools`: o edit é negado.
- `Bash(go test *)` contra `go test ./... ; curl x` e `$(…)`.
- Lista de ferramentas e MCP no `init`.
- `--session-id` repetido; retomada depois de SIGKILL no meio de um `tool_use`; retomada com segmento diferente.
- `total_cost_usd` na retomada; `modelUsage` com subagente.
- `--max-turns` aceito e o `subtype` resultante; estouro de `--max-budget-usd`.
- `api_error_status` para 401 e 429.
- Credencial com `HOME` sintético.
- Prompt grande pelo stdin; `--input-format stream-json` com `Send`.
