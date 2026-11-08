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
| `workspace.transition` | `{from, to, reason}` |
| `node.transition` | `{from, to, reason, attempt, round, blocked_reason?}` |
| `plan.approved` | `{plan_version, created_issues: [n], by}` |
| `edge.added` | `{from, to, kind, source}` |
| `area.deviation` | `{declared: [..], touched: [..], files: [..]}` (ADR 0011) |
| `gate.started` / `gate.finished` | `{gate, sha, state?, log_ref?}` |
| `merge.rebased` | `{base_sha, head_sha}` |
| `merge.freshness` | `{reviewed_sha, tip_sha, rerun: bool}` |
| `merge.done` | `{merge_sha}` |
| `main.synced` | `{main_sha, merge_sha}` |
| `decision.recorded` | `{decision_id, author, title}` |
| `question.asked` / `question.answered` | `{question_id, blocking?, by?}` |
| `proposal.created` / `proposal.decided` | `{proposal_id, kind, state?}` (`kind` inclui `lesson`, ADR 0022) |
| `pattern.detected` | `{pattern_id, kind: finding\|gate_failure\|question, occurrences: [node], sample}` |
| `budget.alert` | `{scope: node\|milestone, spent_usd_micros, limit_usd_micros}` |
| `tracker.sync_failed` | `{op, error, retry_in}` |

## Eventos de sessão (normalizados pelo runtime)

Cada adaptador de runtime traduz o stream do seu harness para estes eventos. A web e o journal nunca veem o formato original do harness, que fica guardado à parte em `logs/sessions/<id>.jsonl`.

| `kind` | Payload | Origem no Claude Code (`stream-json`) |
|---|---|---|
| `session.started` | `{role, harness, model, attempt, round}` | mensagem `system` de init |
| `session.message` | `{role: assistant\|user, text}` | mensagens de texto |
| `tool.call` | `{tool, input_summary}` | `tool_use` |
| `tool.result` | `{tool, ok, output_summary}` | `tool_result` |
| `file.edited` | `{path, added, removed}` | `tool_use` de edição |
| `command.run` | `{command, exit_code?, duration_ms?}` | `tool_use` de shell |
| `subagent.started` / `subagent.finished` | `{model, task_summary}` | chamadas de subagent |
| `advisor.consulted` | `{model, advice_summary}` | chamada do advisor |
| `mcp.call` | `{tool, ok}` | chamadas `kluzo_*` |
| `cost` | `{input_tokens, output_tokens, cost_usd_micros}` | totais de uso |
| `session.finished` | `{state, duration_ms, cost_usd_micros}` | mensagem `result` |

Regras:
- Os campos `*_summary` têm no máximo 300 caracteres. O conteúdo completo fica só no log bruto.
- Antes de gravar, o adaptador remove segredos (valores das variáveis de ambiente marcadas como secretas e padrões de token).
- Um evento que o adaptador não reconhece vira `session.message` com `text` vazio e é registrado como aviso. Ele nunca derruba a sessão.

## SSE

- Endpoint: `GET /api/workspaces/{id}/events`, com `Last-Event-ID` opcional.
- Também há um canal global, `GET /api/events`, que transmite só eventos de workspace e os itens da Inbox.
- O SSE envia o envelope completo. A web atualiza o estado aplicando os eventos de transição sobre o snapshot inicial (`GET /api/workspaces/{id}`).
