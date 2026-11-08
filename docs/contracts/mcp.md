# Ferramentas MCP do Kluzo

O binário do Kluzo expõe um servidor MCP (ADR 0007). Cada sessão de agente recebe o seu próprio endpoint, já identificado com o workspace, o nó, a sessão e o papel. O agente não informa quem é: o servidor sabe, pelo endpoint.

- **Transporte:** HTTP no host local (`127.0.0.1`), com um token por sessão no cabeçalho. O token é revogado quando a sessão termina.
- **Permissões:** o servidor só lista e aceita as ferramentas declaradas em `mcp:` no papel. Uma chamada fora disso retorna o erro `forbidden`.
- **Disponibilidade:** a v0.1 expõe só `kluzo_result`; as demais ferramentas entram na v0.2 (ADR 0019). As de navegação de código (`kluzo_code_*`) entram na v0.3 e só são listadas quando `code_map.adapter` está configurado (ADR 0021).
- **Texto externo:** todo campo com texto vindo de fora do Kluzo (issue, comentários, findings, logs, diff) é entregue como `{"trust": "untrusted", "source": "...", "text": "..."}`, saneado como em [prompt.md](prompt.md#envelope-de-dado-não-confiável).
- **Journal:** toda chamada gera um evento `mcp.call`. As chamadas com efeito também geram os eventos próprios (`decision.recorded`, `question.asked`…).

## `kluzo_context`

Retorna o contexto do trabalho atual. Não tem efeito colateral.

Entrada: `{}`

Saída:
```json
{
  "workspace": { "milestone": "v2.0", "branch": "milestone/v2.0" },
  "node": {
    "issue": 42, "title": "API de agendamentos", "body": "…",
    "acceptance": ["…"], "areas": ["agenda"], "subtasks": ["…"],
    "attempt": 1, "round": 0
  },
  "neighbors": {
    "blocked_by": [{ "issue": 41, "state": "integrated", "summary": "…" }],
    "contracts": [{ "issue": 43, "path": "openapi/agenda.yaml" }],
    "dependents": [{ "issue": 45, "title": "Lembrete por WhatsApp" }]
  },
  "previous": {
    "review_findings": [],
    "gate_failures": [],
    "triage_note": null
  },
  "decisions": [{ "node": 41, "title": "Slots de 15 minutos", "rationale": "…" }],
  "budget": { "remaining_usd_micros": 1900000, "remaining_minutes": 22 }
}
```

`previous` traz o motivo da tentativa ou rodada atual: os findings do reviewer, o log resumido do gate que falhou, ou a nota da triagem.

## `kluzo_changes`

Retorna o que mudou na milestone desde que o nó foi despachado. Não tem efeito colateral.

Entrada: `{}`

Saída:
```json
{
  "since": "c0ffee1",
  "tip": "f00dbab",
  "integrated": [
    {
      "issue": 41, "title": "Modelo de slots",
      "files": ["internal/agenda/slot.go"],
      "symbols": ["agenda.Slot", "agenda.NewSlot"],
      "summary": { "trust": "untrusted", "source": "node-result:41", "text": "…" }
    }
  ],
  "active_in_my_areas": [{ "issue": 44, "state": "running", "areas": ["agenda"] }]
}
```

- `since` é o commit da milestone em que o worktree do nó foi criado; `tip` é a ponta atual.
- `symbols` só vem preenchido quando há um `CodeMapper` configurado.
- `active_in_my_areas` lista nós em execução que tocam as mesmas áreas (por arestas de conflito, isso só acontece com a área `shared` ou com uma área não declarada no plano).

## `kluzo_decisions`

Busca decisões de milestones anteriores do mesmo projeto (ADR 0022). Não tem efeito colateral.

Entrada: `{ "query": "slots", "areas": ["agenda"], "limit": 10 }` (todos opcionais; sem `areas`, usa as do nó)

Saída: lista de `{ "milestone": "v1.4", "node": 41, "title": "…", "rationale": { "trust": "untrusted", "source": "decision:…", "text": "…" } }`, da mais recente para a mais antiga.

## Navegação de código (`kluzo_code_*`)

Consultas ao índice do `CodeMapper` (ADR 0021). Não têm efeito colateral. O índice é o da ponta da milestone, mais as mudanças do worktree do nó quando o adaptador suporta atualização incremental. Todo trecho de código retornado vai no envelope não confiável.

| Ferramenta | Entrada | Saída |
|---|---|---|
| `kluzo_code_map` | `{ "paths": ["internal/agenda/**"], "max_tokens": 2000 }` (ambos opcionais; sem `paths`, usa as áreas do nó) | Visão resumida: arquivos e símbolos principais, ordenados por centralidade, dentro do orçamento |
| `kluzo_code_find` | `{ "query": "NewSlot", "kind": "func" }` (`kind` opcional) | Lista de `{ symbol, kind, path, line, signature }` |
| `kluzo_code_refs` | `{ "symbol": "agenda.NewSlot", "direction": "callers" }` (`callers` ou `callees`) | Lista de `{ symbol, path, line }` |

Se o índice ainda está sendo construído, a resposta é o erro `unavailable`, e o agente segue com as ferramentas do harness.

## `kluzo_decision`

Registra uma decisão tomada sob ambiguidade, sem parar o trabalho.

Entrada:
```json
{ "title": "Horários em UTC no banco", "rationale": "Conversão só na borda da API." }
```

Saída: `{ "decision_id": "dc_01J…" }`

## `kluzo_ask`

Escala uma pergunta para o humano. Use para o que muda escopo, contrato ou segurança. Em ambiguidade pequena, a regra é decidir e registrar com `kluzo_decision`.

Entrada:
```json
{
  "question": "Um profissional pode ver a agenda de outro profissional da mesma clínica?",
  "why": "Os critérios do #46 não definem visibilidade entre profissionais.",
  "options": ["Não", "Sim, na mesma clínica", "Só gestor e recepção veem todas"],
  "snippet": { "file": "internal/agenda/acl.go", "start": 12, "end": 16 },
  "blocking": true
}
```

Saída: `{ "question_id": "qs_01J…", "state": "pending" }`

Comportamento:
- Com `blocking: true`, o nó vai para `blocked` (motivo `question`). A sessão é encerrada de forma limpa e retomada com `Resume` quando chega a resposta, que é injetada como mensagem.
- Com `blocking: false`, a sessão continua. A resposta é entregue na próxima chamada a `kluzo_context`, em `answers`.

## `kluzo_propose_node`

Propõe uma lacuna como nó novo, ou uma mudança no plano.

Entrada:
```json
{
  "kind": "new_node",
  "title": "Idempotência no webhook da Zoop",
  "why": "Reentregas criam pagamentos duplicados.",
  "areas": ["payments"],
  "blocking": false
}
```

`kind`: `new_node` · `split` (com `subtasks_as_nodes`) · `edge` (com `from`, `to`, `edge_kind`).

Saída: `{ "proposal_id": "pp_01J…", "state": "pending" }`

Nenhuma issue é criada antes da aprovação humana. Com `blocking: true`, o nó atual vai para `blocked` (motivo `proposal`).

## `kluzo_result`

Entrega o resultado final da etapa.

Entrada: o JSON do schema declarado em `output` do papel ([results.md](results.md)).

Saída:
- Válido: `{ "accepted": true }`. A sessão deve terminar em seguida.
- Inválido: `{ "accepted": false, "errors": ["$.findings[0].severity: valor fora do enum"] }`. O agente pode corrigir e chamar de novo uma única vez.

## Erros

| Código | Quando |
|---|---|
| `forbidden` | Ferramenta não declarada no papel |
| `invalid_input` | Entrada fora do schema da ferramenta |
| `session_closed` | Sessão já terminou ou foi cancelada |
| `unavailable` | Índice de código ainda em construção ou adaptador fora do ar |
| `rate_limited` | Mais de 20 chamadas por minuto da mesma ferramenta |
