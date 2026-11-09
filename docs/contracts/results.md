# Resultados dos papéis

Cada papel declara em `output` o schema da sua saída ([config.md](config.md#papéis-rolesrolemd)). A saída chega por `kluzo_result` (MCP, desde a v0.1; ADR 0019). Para harness sem MCP, há a reserva do bloco cercado `kluzo-result` ([prompt.md](prompt.md#segmento-1--protocolo)). Em qualquer caso, ela é validada contra o JSON Schema embutido no binário (`internal/contracts/schemas/*.json`), que um projeto pode sobrescrever em `.kluzo/schemas/`.

**Saída inválida ou ausente conta como falha da etapa**, nunca como resultado vazio: um reviewer ou critic que não entregou não revisou. A sessão recebe o erro de validação uma vez para corrigir. Se falhar de novo, a triagem decide.

Os exemplos abaixo mostram a forma. O schema formal fica no arquivo `.json` com o mesmo nome. Os schemas são estritos (`additionalProperties: false`): um campo desconhecido é erro, e o agente tem a correção única para tirá-lo.

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
  "criteria": [
    { "id": "AC1", "evidence": ["internal/agenda/conflict.go:41", "internal/agenda/conflict_test.go:TestOverlap"] }
  ],
  "responses": [
    { "id": "F1", "action": "fixed", "note": "Remarcar agora chama CheckConflict." }
  ],
  "files_changed": ["internal/agenda/service.go", "internal/agenda/conflict.go"],
  "tests": { "added": 9, "command": "go test ./internal/agenda/..." },
  "decisions": [{ "title": "Horários em UTC no banco", "rationale": "Conversão só na borda da API." }],
  "gaps": []
}
```

`status`: `done` (vai para a revisão) · `failed` (o agente desistiu; precisa de `reason`) · `needs_input` (houve `kluzo_ask` bloqueante; só a partir da v0.2, quando a ferramenta existe).

Validação feita pelo Kluzo, além do schema (ADR 0023):
- `criteria` cobre exatamente os ids de critério do nó (`AC1..n`, atribuídos pelo Kluzo a partir do plano). Com `status: done`, todo critério tem ao menos uma evidência.
- Toda evidência `caminho:linha` aponta para um arquivo que existe no worktree, com a linha dentro do arquivo. Evidência `caminho:Nome` aponta para um arquivo que existe. A partir da v0.2, um nome de teste citado precisa constar como executado e aprovado no relatório estruturado do gate `test`; se não constar, o nó volta com a causa `evidence_unverified`.
- `responses` cobre exatamente os ids de findings abertos recebidos. `action`: `fixed` ou `contested`. Um finding automático (`auto: true`) não aceita `contested`.

## `review-result` (reviewer)

```json
{
  "v": 1,
  "summary": "Falta tratar remarcação para horário já ocupado.",
  "criteria": [
    { "id": "AC1", "met": false, "evidence": ["internal/agenda/handler.go:88"] }
  ],
  "previous": [
    { "id": "F1", "status": "open", "note": "A contestação não procede: o handler ainda chama Save direto." }
  ],
  "findings": [
    {
      "severity": "blocking",
      "category": "correctness",
      "file": "internal/agenda/handler.go",
      "line": 88,
      "message": "Remarcar não chama CheckConflict.",
      "check": "Um teste que remarca para um slot ocupado falha com ErrConflict.",
      "caused_by": null
    }
  ]
}
```

- **O reviewer não declara veredito.** O Kluzo o deriva (ADR 0023): `approve` quando não há finding `blocking` ou `major` novo, nenhum `previous` está `open` e todo critério tem `met: true`. Caso contrário, `request_changes`. Na última rodada (`round == max_rounds`), findings `major` não bloqueiam: viram `gaps` registrados e o nó segue. `blocking` sempre bloqueia.
- `severity`: `blocking` · `major` · `minor`. Desempate: critério sem evidência executável é `blocking`.
- `category`: `correctness`, `tests`, `security`, `duplication`, `dead_code`, `style`, `scope`. Código duplicado e código morto são `blocking`.
- `check` é obrigatório em `blocking` e `major`.
- Os ids dos findings (`F1`, `F2`…) são atribuídos pelo Kluzo depois da validação, estáveis por nó e através das tentativas.
- Os findings que o próprio Kluzo gera (supressão de lint nova, ADR 0020) vêm com `auto: true`, `severity: blocking` e não são contestáveis. O reviewer não envia `auto`; o campo não existe no schema de saída dele. Teste removido ou desativado vira proposta, não finding (state-machine.md).
- O estado de um finding automático é do Kluzo: a conferência depois de cada rodada do implementer o reabre enquanto a supressão estiver no trabalho da tentativa e o resolve quando ela sai. O `previous` do reviewer não muda esse estado.

Validação feita pelo Kluzo, além do schema:
- `criteria` cobre exatamente os ids de critério do nó.
- Da rodada 2 em diante, `previous` cobre exatamente os findings `blocking` e `major` abertos (inclusive os de tentativas anteriores).
- Da rodada 2 em diante, um finding fora dos trechos alterados na rodada exige `caused_by` (o `caminho:linha` alterado que o causou). Sem isso, ou com um `caused_by` que não aponta para uma linha alterada na rodada, é rebaixado para `minor`, exceto `security`. Vale para todas as categorias, inclusive `duplication` e `dead_code`: o que não foi apontado na rodada em que apareceu não volta como bloqueio.
- A evidência dos critérios do reviewer passa pela mesma conferência de arquivo e linha que a do implementer.
- Na última rodada, critério com `met: false` leva a `blocked` (`triage`), como um `blocking` aberto. Os `major` só viram `gap` quando o nó é aprovado; se ele vai para a triagem, continuam abertos para a próxima tentativa.
- Saída que falha em qualquer regra é inválida: o reviewer recebe o erro uma vez para corrigir.

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
