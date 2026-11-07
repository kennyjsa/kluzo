# Contratos técnicos

Formatos e interfaces que o código do Kluzo implementa. Quando outro documento e um contrato divergirem, **o contrato vale**, e o outro documento é corrigido no mesmo PR.

| Contrato | Conteúdo |
|---|---|
| [state-machine.md](state-machine.md) | Estados e transições do workspace e do nó; labels espelhadas no tracker |
| [data-model.md](data-model.md) | Tabelas do SQLite e o journal |
| [config.md](config.md) | `kluzo.yaml`, papéis, flows e perfis |
| [plan.md](plan.md) | Formato do plano (`.kluzo/plans/<milestone>.yaml`) |
| [results.md](results.md) | Saídas estruturadas de cada papel |
| [events.md](events.md) | Eventos normalizados (runtime, núcleo, SSE) |
| [interfaces.md](interfaces.md) | Interfaces Go: `Tracker`, `SCM`, `CI`, `Runtime`, `Notifier` |
| [mcp.md](mcp.md) | Ferramentas `kluzo_*` do servidor MCP |

Regras gerais:
- Nomes de chaves, estados, eventos e labels são em inglês (ADR 0008).
- Todo formato tem versão (`version: 1` nos YAML, `"v": 1` nos JSON). Uma mudança incompatível sobe a versão, e o Kluzo recusa versões que não conhece, com mensagem clara.
- IDs internos são ULIDs (`ws_01J…`, `nd_01J…`, `ss_01J…`, `ev_01J…`). O número da issue é o identificador externo do nó.
- Timestamps em UTC, RFC 3339 com milissegundos.
- Dinheiro:
  - Nos YAML escritos por humanos, em dólares (`usd: 6`).
  - No banco, nos eventos e nos JSON, em micro-dólares inteiros (`cost_usd_micros`), para não acumular erro de ponto flutuante.

## Glossário

Os nomes canônicos em inglês de cada conceito estão no [glossário](../product/concepts.md#glossário).
