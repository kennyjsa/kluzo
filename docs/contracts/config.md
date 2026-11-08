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
  language: pt-BR             # idioma de summary, message e rationale (ADR 0019)

tracker:
  kind: github                 # github | gitlab
  repo: kennyjsa/kluzo         # GitLab: grupo/projeto
  auth:
    kind: github_app           # github_app | token
    app_id_env: KLUZO_GH_APP_ID
    key_file_env: KLUZO_GH_APP_KEY
    # kind: token → token_env: KLUZO_GH_TOKEN (padrão; GitLab: KLUZO_GL_TOKEN)
    # installation_id do App: descoberto pela API
  poll_interval: 30s
  operators: [kennyjsa]        # quem pode aprovar e usar /kluzo pelo tracker

branches:
  main: main
  milestone_prefix: milestone/
  node_pattern: "issue/{number}-{slug}"

areas:                         # ADR 0011
  agenda: ["internal/agenda/**", "web/src/agenda/**"]
  auth: ["internal/auth/**"]
  # arquivos fora de qualquer área caem em 'shared'

protected_paths:                # ADR 0020: mudança aqui vira proposta para o humano
  - ".kluzo/**"
  - "AGENTS.md"                 # memória do projeto (ADR 0022)
  - "CLAUDE.md"                 # regras do projeto (ADR 0024)
  - ".claude/**"
  - ".mcp.json"
  - ".golangci.yml"
  - ".github/workflows/**"
  - "Makefile"

gates:                         # lidos da ponta da milestone no despacho, nunca do worktree (ADR 0020)
  - name: build
    run: go build ./... && go vet ./...
    fast: true                 # roda antes do review
  - name: lint
    run: golangci-lint run
    fast: true
  - name: test
    run: go test ./...
    timeout: 15m
    env_allow: [DATABASE_URL_TEST]   # gates não recebem segredos por padrão
  - name: ci                   # usa os checks do tracker em vez de um comando
    source: tracker
    required_checks: ["build", "test"]

milestone_gates:
  - name: e2e
    run: make e2e
    timeout: 40m

scheduler:
  max_parallel: 3              # v0.1 ignora e roda 1

code_map:                      # v0.3, opcional (ADR 0021)
  adapter: none                # none | cartog | lsp
  exclude: ["vendor/**", "web/dist/**"]

security:
  allow_host_credentials: false   # true só com isolamento próprio (ADR 0020)

budgets:                       # padrão; papéis e milestones podem sobrescrever
  node: { attempts: 3, minutes: 60, usd: 6 }
  milestone: { usd: 60, alert_at: 1.1 }

runtimes:
  claude-code:
    bin: claude
    min_version: 2.1.295        # ADR 0024
    env_allow: [DATABASE_URL_TEST]   # variáveis do projeto repassadas à sessão
  opencode:
    bin: opencode
    min_version: 1.4.0
    env_allow: []

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
protocol: 1
description: Implementa o nó e os testes
harness: claude-code
model: sonnet
advisor: opus
subagents: haiku
effort: high
tools: [read, edit, bash, test]
skills: [testing-conventions]
mcp: [kluzo_context, kluzo_changes, kluzo_code_map, kluzo_code_find, kluzo_code_refs, kluzo_decision, kluzo_ask, kluzo_propose_node, kluzo_result]
output: node-result
budget: { minutes: 45, usd: 4 }
max_turns: 80
fallback:
  - { model: opus }
  - { harness: opencode, model: openrouter/qwen3-coder }
---
You implement the node described in the task. Work test-first…
```

| Chave | Tipo | Obrigatória | Descrição |
|---|---|---|---|
| `version` | int | sim | Versão do formato (1) |
| `protocol` | int | sim | Versão do protocolo do Kluzo para a qual o papel foi escrito ([prompt.md](prompt.md)) |
| `description` | string | sim | Uma linha; aparece na tela Times |
| `harness` | enum | não | `claude-code` \| `opencode`; padrão em `defaults` |
| `model` | string | não | `claude-code`: alias (`opus`, `sonnet`, `haiku`) ou id `claude-*`. `opencode`: `provedor/modelo`. Validado por formato, sem catálogo embutido |
| `advisor` | string | não | Só `claude-code`; não pode ser mais fraco que `model` (família `haiku` < `sonnet` < `opus`; família não reconhecida não é comparada) |
| `subagents` | string | não | Modelo dos subagents; só `claude-code` |
| `effort` | enum | não | `low` \| `medium` \| `high` |
| `tools` | lista | sim | Vocabulário canônico do Kluzo: `read`, `edit`, `bash`, `test`. Cada adaptador traduz para as ferramentas do harness. A lista mora em `internal/contracts` e pode crescer com a investigação 3 |
| `skills` | lista | não | Pastas em `.agents/skills/` carregadas na sessão |
| `mcp` | lista | sim | Ferramentas `kluzo_*` permitidas ([mcp.md](mcp.md)) |
| `output` | string | sim | Nome do schema de saída ([results.md](results.md)) |
| `budget` | objeto | não | Limites desta etapa; somam ao orçamento do nó |
| `fallback` | lista | não | Overrides aplicados em ordem quando a triagem decide `switch_model` ou `switch_harness` |
| `max_turns` | int | não | Limite de turnos por sessão (padrão 80) |
| `prompt_limits` | objeto | não | Sobrescreve os limites de [prompt.md](prompt.md#limites). Chaves (inteiros; 0 = padrão): `findings`, `test_failures`, `failure_message_chars`, `log_head`, `log_tail`, `log_chars`, `changed_files`, `diff_lines` |

O corpo do papel é texto estático, sem variáveis, em qualquer idioma. Ele vira o segundo segmento do prompt ([prompt.md](prompt.md)).

Validações (também em `kluzo roles check`, que carrega a configuração inteira, papéis e flows, para aplicar as regras que cruzam os dois):
- Todo papel tem `kluzo_result` em `mcp`.
- O `protocol` é igual ao do binário.
- O corpo não contém marcadores reservados nem cita ferramenta `kluzo_*` fora de `mcp`.
- Um papel usado em etapa com `phase: in_review` (o `reviewer` no padrão) não pode ter `edit` nem `bash` (ADR 0020). A checagem é feita ao carregar os flows.
- O `advisor` é recusado se o harness não for `claude-code`, e também se for mais fraco que `model`.
- Toda ferramenta em `tools` pertence ao vocabulário canônico.

Obrigatórias em `kluzo.yaml`: `version`, `project.name`, `tracker.kind` e `tracker.repo`. `tracker.auth` só é exigido pelos comandos que falam com o tracker (`doctor`, `run`). Um papel sem harness ou modelo efetivo (nem nele, nem em `defaults`) é erro no papel.

Os vocabulários fechados do contrato (ferramentas, ferramentas `kluzo_*`, marcadores reservados, schemas embutidos, versão do protocolo) ficam em `internal/contracts`, para config, prompt, MCP e adaptadores usarem a mesma fonte.

## Flows

Arquivos: `flows/upstream.yaml`, `flows/node.yaml` e `flows/closing.yaml` (ADR 0010).

```yaml
# flows/node.yaml
version: 1
steps:
  - id: implement
    role: implementer
    phase: running              # estado do nó durante esta etapa
  - id: quick
    gate: [build, lint]         # gates rápidos antes do review (ADR 0020)
  - id: review
    role: reviewer
    phase: in_review
    on_reject: implement        # devolução
    max_rounds: 2
  - id: gates
    gate: [test]                # gates completos
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
| `gate` | `gate` (lista), `scope`? (`node` \| `milestone`), `on_fail`?, `max_retries`? (gates `fast`; padrão 3) | Roda os gates em sequência; falha devolve à etapa `on_fail`, que deve ser uma etapa `role` anterior (padrão: a primeira etapa `role`) |
| `approval` | `approval` (`plan`) | Portão humano; o workspace vai para `awaiting_approval` |
| `merge_queue` | `merge_queue` (`milestone`) | Entrega o nó à fila de merge |
| `loop` | `max_rounds`, `steps` | Repete as etapas internas até a última sair sem findings bloqueantes |
| `open_pr` | `open_pr` (`main`) | Abre o PR/MR da milestone com o relatório |

Restrições:
- `node.yaml` tem exatamente um `merge_queue`, como última etapa.
- `on_reject` só aponta para uma etapa anterior do mesmo flow.
- `loop` não pode ser aninhado.
- Nenhum flow é obrigatório no carregamento. Quem executa um flow recusa a ausência dele: `kluzo run` exige `node.yaml` a partir da v0.1; `upstream.yaml` e `closing.yaml` passam a ser exigidos quando os seus executores existirem (v0.3 e v0.4). Arquivo de flow com outro nome é erro.

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
