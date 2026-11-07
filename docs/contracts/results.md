# Resultados dos papéis

Cada papel declara em `output` o schema da sua saída ([config.md](config.md#papéis-rolesrolemd)). A saída chega por `kluzo_result` (a partir da v0.2) ou pela saída estruturada do harness (v0.1). Em qualquer caso, ela é validada contra o JSON Schema embutido no binário (`internal/contracts/schemas/*.json`), que um projeto pode sobrescrever em `.kluzo/schemas/`.

**Saída inválida conta como falha da etapa.** A sessão recebe o erro de validação uma vez para corrigir. Se falhar de novo, a triagem decide.

Os exemplos abaixo mostram a forma. O schema formal fica no arquivo `.json` com o mesmo nome.

Campos comuns a todos os schemas:

| Campo | Tipo | Descrição |
|---|---|---|
| `v` | int | Versão do schema (1) |
| `summary` | string | Uma ou duas frases para o journal e para a web |
| `decisions` | lista de `{title, rationale}` | Decisões tomadas sob ambiguidade; equivalem a chamar `kluzo_decision` |
| `gaps` | lista de `{title, why, blocking}` | Lacunas encontradas; viram propostas (equivalem a `kluzo_propose_node`) |

## `node-result` (implementer)

```json
{
  "v": 1,
  "status": "done",
  "summary": "Endpoints de criação e listagem com checagem de conflito.",
  "subtasks": [
    { "title": "Modelo e repositório", "state": "done" },
    { "title": "Cancelamento e remarcação", "state": "done" }
  ],
  "files_changed": ["internal/agenda/service.go", "internal/agenda/conflict.go"],
  "tests": { "added": 9, "command": "go test ./internal/agenda/..." },
  "decisions": [{ "title": "Horários em UTC no banco", "rationale": "Conversão só na borda da API." }],
  "gaps": []
}
```

`status`: `done` (vai para a revisão) · `needs_input` (houve `kluzo_ask` bloqueante) · `failed` (o agente desistiu; precisa de `reason`).

## `review-result` (reviewer)

```json
{
  "v": 1,
  "verdict": "request_changes",
  "summary": "Falta tratar remarcação para horário já ocupado.",
  "findings": [
    {
      "severity": "blocking",
      "category": "correctness",
      "file": "internal/agenda/handler.go",
      "line": 88,
      "message": "Remarcar não chama CheckConflict."
    }
  ],
  "criteria": [
    { "criterion": "Não permite dois agendamentos no mesmo slot", "met": false }
  ]
}
```

- `verdict`: `approve` · `request_changes`.
- `severity`: `blocking` · `major` · `minor`. Só `blocking` e `major` justificam `request_changes`.
- `category`: `correctness`, `tests`, `security`, `duplication`, `dead_code`, `style`, `scope`. Código duplicado e código morto são `blocking`.
- Cada critério de aceite da issue aparece em `criteria`.

## `triage-result` (triage)

```json
{
  "v": 1,
  "action": "switch_harness",
  "summary": "Três falhas de build por timeout do harness; código está correto.",
  "reason": "runtime_instability",
  "params": { "harness": "opencode" }
}
```

`action`: `retry` · `switch_model` · `switch_harness` · `split` · `escalate`. Um `split` traz `params.subtasks_as_nodes` e vira proposta. Um `escalate` traz `params.question`.

## `plan-result` (planner, v0.3)

```json
{
  "v": 1,
  "summary": "8 nós; caminho crítico #41 → #42 → #45 → #47.",
  "nodes": [
    { "issue": 41, "title": "Schema da agenda", "areas": ["agenda"], "subtasks": ["Migração", "Índices"], "estimate_hours": 1.5 },
    { "new": "n1", "title": "Migração: preferências de lembrete", "areas": ["agenda"], "why": "lacuna do critic, rodada 1" }
  ],
  "edges": [
    { "from": 41, "to": 42, "kind": "blocks" },
    { "from": 43, "to": 44, "kind": "contract" }
  ],
  "changes": [
    { "kind": "split", "issue": 51, "into": ["n2", "n3"], "why": "isolar o reenvio com backoff" }
  ],
  "estimate_usd": 26
}
```

Um nó novo usa `new` (um id local) até ser aprovado e receber o número da issue.

## `critique-result` (critic, v0.3)

```json
{
  "v": 1,
  "summary": "2 lacunas bloqueantes.",
  "findings": [
    {
      "severity": "blocking",
      "category": "data",
      "title": "Nenhuma issue cria a tabela de preferências",
      "suggestion": "Nó de migração antes do #51"
    }
  ]
}
```

`category` segue o checklist do critic: `data`, `infra`, `contract`, `e2e`, `observability`, `security`, `rollout`, `docs`. O laço planner ⇄ critic termina quando a resposta não traz nenhum finding `blocking`.

## `refinement-result` (business, po, requirements, v0.3)

```json
{
  "v": 1,
  "summary": "5 issues refinadas com critérios de aceite.",
  "issues": [
    {
      "issue": 60,
      "title": "Relatório de atendimentos por profissional",
      "acceptance": ["Gestor filtra por profissional", "Exporta CSV"],
      "areas": ["reports"],
      "role_hint": "backend"
    }
  ],
  "open_questions": []
}
```

## `architecture-result` (architect, v0.3)

```json
{
  "v": 1,
  "summary": "ADR 0012 e contrato OpenAPI da agenda.",
  "adrs": [{ "path": "docs/adr/0012-conflito-no-banco.md", "title": "Conflito de horário validado no banco" }],
  "contracts": [{ "path": "openapi/agenda.yaml", "issue": 43 }]
}
```

## `report-result` (reporter)

```json
{
  "v": 1,
  "summary": "12/12 nós, US$ 31,20 de 34.",
  "markdown": "## Relatório da milestone v0.4\n…"
}
```

O campo `markdown` vai na descrição do PR/MR `milestone → main`. Os números do relatório (custo, tempo, tentativas) vêm do journal, não do agente. O reporter recebe esses números prontos e só redige o texto.
