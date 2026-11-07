# Configuração `.kluzo/`

```
.kluzo/
├── kluzo.yaml
├── roles/<role>.md
├── flows/{upstream,node,closing}.yaml
├── profiles/<profile>.yaml      # v0.4
├── schemas/<name>.json          # opcional: sobrescreve os schemas embutidos
└── plans/<milestone>.yaml       # escrito pelo Kluzo na aprovação (plan.md)
```

A validação acontece ao carregar a configuração e antes de criar ou retomar um workspace. Cada erro informa o arquivo, a linha e a chave. Chaves desconhecidas são erro, não aviso, para pegar erros de digitação.

## `kluzo.yaml`

```yaml
version: 1

project:
  name: agiclin

tracker:
  kind: github                 # github | gitlab
  repo: kennyjsa/kluzo         # GitLab: grupo/projeto
  auth:
    kind: github_app           # github_app | token
    app_id_env: KLUZO_GH_APP_ID
    key_file_env: KLUZO_GH_APP_KEY
  poll_interval: 30s

branches:
  main: main
  milestone_prefix: milestone/
  node_pattern: "issue/{number}-{slug}"

areas:                         # ADR 0011
  agenda: ["internal/agenda/**", "web/src/agenda/**"]
  auth: ["internal/auth/**"]
  # arquivos fora de qualquer área caem em 'shared'

gates:
  - name: test
    run: go test ./...
    timeout: 15m
  - name: lint
    run: golangci-lint run
  - name: ci                   # usa os checks do tracker em vez de um comando
    source: tracker
    required_checks: ["build", "test"]

milestone_gates:
  - name: e2e
    run: make e2e
    timeout: 40m

scheduler:
  max_parallel: 3              # v0.1 ignora e roda 1

budgets:                       # padrão; papéis e milestones podem sobrescrever
  node: { attempts: 3, minutes: 60, usd: 6 }
  milestone: { usd: 60, alert_at: 1.1 }

runtimes:
  claude-code:
    bin: claude
    min_version: 2.1.186
  opencode:
    bin: opencode
    min_version: 1.4.0

defaults:
  harness: claude-code
  model: sonnet

notify:                        # v0.3
  kind: ntfy
  url_env: KLUZO_NTFY_URL
```

## Papéis: `roles/<role>.md`

Um papel é um markdown com frontmatter YAML. O corpo é o prompt, que deve ser agnóstico de harness.

```markdown
---
version: 1
description: Implementa o nó e os testes
harness: claude-code
model: sonnet
advisor: opus
subagents: haiku
effort: high
tools: [read, edit, bash, test]
skills: [testing-conventions]
mcp: [kluzo_context, kluzo_decision, kluzo_ask, kluzo_propose_node, kluzo_result]
output: node-result
budget: { minutes: 45, usd: 4 }
fallback:
  - { model: opus }
  - { harness: opencode, model: openrouter/qwen3-coder }
---
Você é o implementador deste nó. Comece chamando `kluzo_context`…
```

| Chave | Tipo | Obrigatória | Descrição |
|---|---|---|---|
| `version` | int | sim | Versão do formato (1) |
| `description` | string | sim | Uma linha; aparece na tela Times |
| `harness` | enum | não | `claude-code` \| `opencode`; padrão em `defaults` |
| `model` | string | não | Alias (`opus`, `sonnet`, `haiku`) ou id completo |
| `advisor` | string | não | Só `claude-code`; não pode ser mais fraco que `model` |
| `subagents` | string | não | Modelo dos subagents; só `claude-code` |
| `effort` | enum | não | `low` \| `medium` \| `high` |
| `tools` | lista | sim | Ferramentas do harness permitidas; o adaptador traduz os nomes |
| `skills` | lista | não | Pastas em `.agents/skills/` carregadas na sessão |
| `mcp` | lista | sim | Ferramentas `kluzo_*` permitidas ([mcp.md](mcp.md)) |
| `output` | string | sim | Nome do schema de saída ([results.md](results.md)) |
| `budget` | objeto | não | Limites desta etapa; somam ao orçamento do nó |
| `fallback` | lista | não | Overrides aplicados em ordem quando a triagem decide `switch_model` ou `switch_harness` |

Validações:
- O papel tem `kluzo_result` em `mcp` ou usa a saída estruturada do harness (v0.1).
- O `advisor` é recusado se o harness não for `claude-code`, e também se for mais fraco que `model`.
- Toda ferramenta em `tools` deve existir no mapeamento do adaptador do harness.

## Flows

Arquivos: `flows/upstream.yaml`, `flows/node.yaml` e `flows/closing.yaml` (ADR 0010).

```yaml
# flows/node.yaml
version: 1
steps:
  - id: implement
    role: implementer
    phase: running              # estado do nó durante esta etapa
  - id: review
    role: reviewer
    phase: in_review
    on_reject: implement        # devolução
    max_rounds: 2
  - id: gates
    gate: [test, lint]          # nomes em kluzo.yaml
  - id: merge
    merge_queue: milestone
```

```yaml
# flows/upstream.yaml
version: 1
steps:
  - { id: business, role: business, interview: true }
  - { id: po, role: po, interview: true }
  - { id: requirements, role: requirements, interview: true }
  - { id: architect, role: architect }
  - id: plan
    loop:                       # repete até o critic não ter findings bloqueantes
      max_rounds: 3
      steps:
        - { id: planner, role: planner }
        - { id: critic, role: critic }
  - { id: approve, approval: plan }
```

```yaml
# flows/closing.yaml
version: 1
steps:
  - { id: e2e, gate: [e2e], scope: milestone }
  - { id: report, role: reporter }
  - { id: pr, open_pr: main }
```

Tipos de etapa:

| Tipo | Chaves | Comportamento |
|---|---|---|
| `role` | `role`, `phase`?, `on_reject`?, `max_rounds`?, `interview`? | Roda o papel. Com `interview: true`, a sessão conversa com o humano pela web até o papel entregar o resultado. |
| `gate` | `gate` (lista), `scope`? (`node` \| `milestone`) | Roda os gates em sequência; falha devolve ao `on_fail` (padrão: a primeira etapa `role`) |
| `approval` | `approval` (`plan`) | Portão humano; o workspace vai para `awaiting_approval` |
| `merge_queue` | `merge_queue` (`milestone`) | Entrega o nó à fila de merge |
| `loop` | `max_rounds`, `steps` | Repete as etapas internas até a última sair sem findings bloqueantes |
| `open_pr` | `open_pr` (`main`) | Abre o PR/MR da milestone com o relatório |

Restrições:
- `node.yaml` tem exatamente um `merge_queue`, como última etapa.
- `on_reject` só aponta para uma etapa anterior do mesmo flow.
- `loop` não pode ser aninhado.

## Perfis: `profiles/<profile>.yaml` (v0.4)

```yaml
version: 1
description: Econômico — Sonnet e Haiku, Opus só no arquiteto
roles:
  implementer: { model: sonnet, advisor: null }
  reviewer: { model: sonnet }
  critic: { model: haiku }
```

Ordem de precedência: `defaults` → papel → perfil → override da milestone (no plano) → label `model/<alias>` no nó.
