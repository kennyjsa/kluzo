# Contratos técnicos

Formatos e interfaces que o código do Kluzo implementa. Quando a SPEC e um contrato divergirem, **o contrato vale**, e a SPEC deve ser corrigida no mesmo PR.

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

Nome canônico em inglês (usado no código) e termo usado na documentação.

| Código | Documentação | Definição curta |
|---|---|---|
| `Project` | projeto | Repositório com `.kluzo/` |
| `Workspace` | workspace | Sessão de trabalho sobre uma milestone |
| `Milestone` | milestone | Unidade de entrega no tracker |
| `Node` | nó | Issue = branch = PR/MR, com gates próprios |
| `Subtask` | subtask | Item do checklist interno do nó |
| `Edge` (`blocks`, `contract`, `conflict`) | aresta (bloqueio, contrato, conflito) | Relação entre nós |
| `Area` | área | Nome + globs de caminho; gera conflito |
| `Role` | papel | Agente definido em `roles/*.md` |
| `Flow` | flow | Sequência de etapas |
| `Step` | etapa | Item de um flow |
| `Harness` / `Runtime` | harness | Ferramenta que executa um papel (Claude Code, OpenCode) |
| `Session` | sessão | Uma execução de um papel num harness |
| `Attempt` | tentativa | Uma passagem do nó pelo flow de nó |
| `Gate` | gate | Verificação determinística |
| `MergeQueue` | fila de merge | Integração serial na branch da milestone |
| `Decision` | decisão | Escolha registrada por agente ou humano |
| `Question` | escalação | Pergunta de um agente para o humano |
| `Proposal` | proposta | Nó novo sugerido durante a execução |
| `Plan` | plano | Grafo aprovado de uma milestone |
| `Profile` | perfil | Conjunto de overrides sobre os papéis |
| `Journal` | journal | Registro append-only de eventos |
