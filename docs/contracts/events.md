# Eventos

Um único formato de evento serve para três coisas:
- o **journal** (tabela `events`);
- a **normalização** dos streams dos harnesses;
- o **SSE** da web.

## Envelope

```json
{
  "v": 1,
  "id": "ev_01JC8Z…",
  "seq": 1842,
  "ts": "2026-11-07T14:41:03.120Z",
  "kind": "tool.call",
  "workspace": "ws_01JC…",
  "node": "nd_01JC…",
  "session": "ss_01JC…",
  "payload": { }
}
```

`node` e `session` são opcionais, conforme o `kind`. O `seq` é monotônico por workspace. O cliente SSE usa o `seq` como `Last-Event-ID` para retomar sem perder eventos.

## Eventos do núcleo

| `kind` | Payload |
|---|---|
| `workspace.created` | `{id, branch, base_sha, profile?, budget_usd_micros?, milestone_ref?}` (dados iniciais; o estado nasce vazio e a transição `create` o leva a `refining`) |
| `workspace.transition` | `{from, to, reason}` |
| `node.added` | `{issue_ref, title, areas, budget}` (o nó nasce `planned`; o id vai no `node` do envelope) |
| `node.transition` | `{from, to, reason, attempt, round, blocked_reason?}` |
| `node.flow` | `{step, run}` (progresso do nó no flow de nó — etapa, findings, sessão a retomar, commits da rodada — gravado a cada decisão; projetado em `nodes.step_index` e `nodes.flow`) |
| `plan.approved` | `{plan_version, created_issues: [n], by}` |
| `edge.added` | `{from, to, kind, source}` |
| `area.deviation` | `{declared: [..], touched: [..], files: [..]}` (ADR 0011) |
| `gate.started` / `gate.finished` | `{gate, sha, state?, log_ref?}` |
| `merge.rebased` | `{base_sha, head_sha}` |
| `merge.freshness` | `{reviewed_sha, tip_sha, rerun: bool}` |
| `merge.done` | `{merge_sha}` |
| `pr.opened` | `{ref, url}` (PR/MR `milestone → main`; projetado em `workspace.flow_cursor`, de onde o supervisor observa o merge humano) |
| `main.synced` | `{main_sha, merge_sha}` |
| `decision.recorded` | `{decision_id, author, title}` |
| `question.asked` / `question.answered` | `{question_id, blocking?, by?}` |
| `proposal.created` / `proposal.decided` | `{proposal_id, kind, state?, blocking?, payload?, by?}` (`kind` inclui `lesson`, ADR 0022; `payload` só no `created`, e para `change` é `{changes: [{path, reason, sha256}]}`, com o SHA-256 do conteúdo do caminho no head da rodada, vazio quando apagado; `by` só no `decided`, com `state` `approved` ou `rejected`) |
| `pattern.detected` | `{pattern_id, kind: finding\|gate_failure\|question, occurrences: [node], sample}` |
| `effect.failed` | `{effect_id, kind, attempts, last_error}` |
| `budget.alert` | `{scope: node\|milestone, spent_usd_micros, limit_usd_micros}` (`milestone`: gravado uma vez, com notificação, quando o custo do workspace passa de `limit × budgets.milestone.alert_at`, na transação do `session.finished` que o fez passar) |
| `tracker.sync_failed` | `{op, error, retry_in}` |

## Eventos de sessão (normalizados pelo runtime)

Cada adaptador de runtime traduz o stream do seu harness para estes eventos. A web e o journal nunca veem o formato original do harness, que fica guardado à parte em `logs/sessions/<id>.jsonl`.

| `kind` | Payload | Origem no Claude Code (`stream-json`) |
|---|---|---|
| `session.started` | `{role, harness, model, attempt, round, harness_version, permission_mode, argv, harness_session_ref, manifest}` (`argv`: linha de comando congelada, com o system prompt como hash; `harness_session_ref` e `manifest` ([prompt.md](prompt.md#manifesto-da-sessão)) são acrescentados pelo supervisor, que conhece a conversa do harness e o prompt enviado) | mensagem `system` de init |
| `session.message` | `{role: assistant\|user, text}` | mensagens de texto |
| `tool.call` | `{tool, input_summary}` | `tool_use` |
| `tool.result` | `{tool, ok, output_summary}` | `tool_result` |
| `file.edited` | `{path, added, removed}` | `tool_use` de edição |
| `command.run` | `{command, exit_code?, duration_ms?}` | `tool_use` de shell |
| `subagent.started` / `subagent.finished` | `{model, task_summary}` | chamadas de subagent |
| `advisor.consulted` | `{model, advice_summary}` | chamada do advisor |
| `mcp.call` | `{tool, ok}` | chamadas `kluzo_*` |
| `guard.denied` | `{tool, rule, reason, input_summary}` | hook `PreToolUse` do Kluzo que negou a chamada (ADR 0024); `rule` é `git_write`, `hosting_cli`, `protected_path`, `secret_path` ou `outside_worktree` |
| `cost` | `{input_tokens, output_tokens, cost_usd_micros}` | totais de uso da sessão: `modelUsage` do `result` menos o acumulado das sessões anteriores da mesma conversa, que o harness soma entre retomadas |
| `session.finished` | `{state, duration_ms, cost_usd_micros, cause?, result?}` (`result`: o resultado aceito pelo `kluzo_result` (schema e cobertura), quando a sessão terminou com um; a conferência pós-sessão ainda pode recusar a sessão; o custo vai para a sessão, o nó e o workspace na projeção deste evento, antes da decisão sobre a sessão) | mensagem `result` |

Regras:
- `session.started` e `session.finished` alimentam a tabela `sessions` ([data-model.md](data-model.md)). Uma sessão do Kluzo (`ss_<ulid>`, uma por efeito `session.start`) pode rodar mais de uma sessão do harness (as repetições do outbox): cada `session.started` assume a linha, e cada `session.finished` soma o seu custo.
- Tipos de evento do harness que o adaptador não conhece ficam só no log bruto e são contados no manifesto; não viram evento no journal.
- Os campos `*_summary` têm no máximo 300 caracteres. O conteúdo completo fica só no log bruto.
- Antes de gravar, o adaptador remove segredos (valores das variáveis de ambiente marcadas como secretas e padrões de token).
- Um evento que o adaptador não reconhece vira `session.message` com `text` vazio e é registrado como aviso. Ele nunca derruba a sessão.

## SSE

- Endpoint: `GET /api/workspaces/{id}/events`, com `Last-Event-ID` opcional.
- Também há um canal global, `GET /api/events`, que transmite só eventos de workspace e os itens da Inbox.
- O SSE envia o envelope completo. A web atualiza o estado aplicando os eventos de transição sobre o snapshot inicial (`GET /api/workspaces/{id}`).
