# Ferramentas MCP do Kluzo

O binário do Kluzo expõe um servidor MCP (ADR 0007). Cada sessão de agente recebe o seu próprio endpoint, já identificado com o workspace, o nó, a sessão e o papel. O agente não informa quem é: o servidor sabe, pelo endpoint.

- **Transporte:** HTTP no host local (`127.0.0.1`), com um token por sessão no cabeçalho. O token é revogado quando a sessão termina.
- **Permissões:** o servidor só lista e aceita as ferramentas declaradas em `mcp:` no papel. Uma chamada fora disso retorna o erro `forbidden`.
- **Disponibilidade:** a v0.1 expõe só `kluzo_result`; as demais ferramentas entram na v0.2 (ADR 0019).
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
- Inválido: `{ "accepted": false, "errors": ["$.verdict: valor fora do enum"] }`. O agente pode corrigir e chamar de novo uma única vez.

## Erros

| Código | Quando |
|---|---|
| `forbidden` | Ferramenta não declarada no papel |
| `invalid_input` | Entrada fora do schema da ferramenta |
| `session_closed` | Sessão já terminou ou foi cancelada |
| `rate_limited` | Mais de 20 chamadas por minuto da mesma ferramenta |
