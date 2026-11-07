# Conceitos

## Glossário

O nome em código (em inglês, conforme o ADR 0008) é o nome canônico. A documentação usa o termo em português.

| Código | Documentação | Definição |
|---|---|---|
| `Project` | projeto | Um repositório com o diretório `.kluzo/` (configuração versionada). |
| `Workspace` | workspace | Uma sessão de trabalho sobre uma milestone. É dona da branch da milestone, dos worktrees, do journal, dos logs e do custo. Pode ser pausada e retomada. Vários workspaces podem coexistir no mesmo projeto. |
| `Milestone` | milestone | A unidade de entrega. Tem estado próprio e gate próprio. |
| `Node` | nó | Uma unidade de integração: uma issue = uma branch = um PR/MR, com gates próprios. |
| `Subtask` | subtask | Item do checklist interno de um nó. O scheduler não agenda subtasks; só acompanha o progresso delas para poder retomar. |
| `Edge` | aresta | Relação entre nós: `blocks` (bloqueio), `contract` (contrato) ou `conflict` (conflito). |
| `Area` | área | Nome curto com uma lista de globs de caminho. Nós com área em comum não rodam ao mesmo tempo. |
| `Plan` | plano | O grafo aprovado de uma milestone, versionado em `.kluzo/plans/`. |
| `Role` | papel | Um agente definido por um markdown com frontmatter: harness, modelo, prompt, permissões, schema de saída e orçamento. |
| `Flow` | flow | Uma sequência de etapas (papéis, gates, aprovações). |
| `Step` | etapa | Um item de um flow. |
| `Harness` / `Runtime` | harness | A ferramenta que executa um papel: Claude Code, OpenCode. |
| `Session` | sessão | Uma execução de um papel num harness. |
| `Attempt` | tentativa | Uma passagem do nó pelo flow de nó. |
| `Gate` | gate | Uma verificação determinística (testes, lint, SAST, E2E). |
| `MergeQueue` | fila de merge | A integração serial dos nós na branch da milestone. |
| `Decision` | decisão | Uma escolha registrada por um agente ou pelo humano. |
| `Question` | escalação | Uma pergunta de um agente para o humano. |
| `Proposal` | proposta | Um nó novo ou uma mudança no plano sugerida durante a execução. |
| `Profile` | perfil | Um conjunto de overrides sobre os papéis (por exemplo, "econômico"). |
| `Journal` | journal | O registro append-only de eventos de um workspace. |

## Hierarquia

```
Projeto (repo + .kluzo/)
 └── Workspace (milestone)
      └── Nós (issues)
           ├── Subtasks (checklist)
           └── Sessões de agente (implementer, reviewer…)
```

## Nó ou subtask?

Uma parte do trabalho é **nó** se precisa de branch própria, de gate próprio ou de rodar em paralelo. Caso contrário, é **subtask**. Quando um nó falha além do limite, as subtasks dele podem ser promovidas a nós novos, por uma proposta aprovada pelo humano.

## O grafo da milestone

Os nós formam um grafo com três tipos de aresta:

| Tipo | Significado | Exemplo |
|---|---|---|
| **bloqueio** (`blocks`) | B precisa do código de A já **integrado** na branch da milestone | A API precisa do schema |
| **contrato** (`contract`) | B precisa só da interface de A (spec de API, schema), então começa antes de A terminar | A tela começa com o contrato OpenAPI |
| **conflito** (`conflict`) | A e B mexem na mesma área e são serializados | Duas mudanças em `internal/agenda` |

Bloqueio e contrato vêm do plano e do tracker. Conflito é derivado das áreas (ADR 0011). O formato está em [contracts/plan.md](../contracts/plan.md).

## Papéis

| Fase | Papéis |
|---|---|
| Upstream | `business`, `po`, `requirements`, `architect`, `planner`, `critic` |
| Downstream | `implementer`, `reviewer`, `qa`, `triage` |
| Fechamento | `reporter` |

O **time** de um workspace é derivado dos papéis citados nos flows. Não existe uma declaração separada de time. O formato dos papéis está em [contracts/config.md](../contracts/config.md).

## Labels têm dono

O Kluzo espelha o estado no tracker usando labels. Cada família de labels tem um único dono, e uma label aplicada por quem não é o dono é ignorada.

| Família | Dono |
|---|---|
| `orch/*` | Kluzo (estado do nó) |
| `plan/*`, exceto `plan/approved` | planner |
| `plan/approved` | humano |
| `area/*`, `role/*`, `model/*` | refino (agentes de upstream ou humano) |

## Fontes da verdade

| O quê | Onde |
|---|---|
| Definição do trabalho (o que fazer, critérios de aceite) | Tracker (issues) |
| Plano aprovado (grafo, áreas, orçamento) | `.kluzo/plans/<milestone>.yaml` |
| Estado da execução (tentativas, sessões, custo) | Journal do workspace (SQLite) |
| Configuração (papéis, flows, gates) | `.kluzo/` |
