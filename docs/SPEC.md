# Kluzo — Especificação

> *Kluzo* é "eclusa" em esperanto: a estrutura que leva um barco de um nível do rio a outro passando por comportas que só abrem quando a câmara está pronta. É o desenho do projeto: o trabalho sai do upstream, atravessa portões e chega à `main`.

Status: rascunho v0 · 2026-11-07

## 1. Objetivo

O Kluzo é um orquestrador que **resolve uma milestone inteira** de forma autônoma.

O humano atua no **upstream**: refino, entrevistas e aprovação do plano. O **downstream**, que é a execução, roda sem intervenção até a milestone ficar pronta para o merge na `main`.

O Kluzo não orquestra sessões de agente. Ele orquestra o **estado da milestone**, e os agentes são os trabalhadores.

### Fluxo de branches suportado

```
issue/*  ──MR──►  milestone/<nome>  ──MR (humano)──►  main
```

- Toda issue vira uma branch, que nasce da ponta atual da branch da milestone.
- O Kluzo integra as issues na branch da milestone.
- O merge da milestone na `main` é sempre humano.

### Não-objetivos (v1)

- Substituir o tracker (GitLab/GitHub). O tracker continua sendo a fonte da verdade da **definição** do trabalho.
- Embutir um LLM próprio. Todo julgamento roda como um papel executado por um harness (ver §6).
- Fazer agentes conversarem diretamente entre si (ver §9).

## 2. Conceitos

| Conceito | Definição |
|---|---|
| **Projeto** | Um repositório com o diretório `.kluzo/` (configuração versionada). |
| **Workspace** | Uma sessão de trabalho sobre uma milestone. É dona da branch da milestone, dos worktrees, do journal, dos logs e do custo. Pode ser pausada e retomada. Vários workspaces podem coexistir no mesmo projeto. |
| **Milestone** | A unidade de entrega. Tem estado próprio e gate próprio. |
| **Nó** | Uma unidade de integração: uma issue = uma branch = um MR/PR, com gates próprios. |
| **Subtask** | Um item do checklist interno de um nó. O scheduler não agenda subtasks, só acompanha o progresso delas para poder retomar. |
| **Papel (role)** | Um agente definido por um markdown com frontmatter: harness, modelo, prompt, permissões, schema de saída e orçamento. |
| **Flow** | Uma sequência de etapas (papéis, gates, aprovações). O time de um flow é derivado dos papéis citados nele. |
| **Harness / Runtime** | A ferramenta que executa um papel: Claude Code, OpenCode e outras no futuro. |
| **Gate** | Uma verificação determinística (testes, lint, SAST, E2E). O agente nunca declara um gate como aprovado. |

### Regra nó × subtask

Uma parte do trabalho é **nó** se precisa de branch própria, de gate próprio ou de rodar em paralelo. Caso contrário, é **subtask**. Quando um nó falha além do limite, o replanejador pode promover as subtasks dele a nós novos.

### Hierarquia

```
Projeto (repo + .kluzo/)
 └── Workspace (milestone)
      └── Nós (issues)
           └── Sessões de agente (implementer, reviewer…)
```

## 3. Ciclo de vida

```
upstream → aprovação → downstream (N pipelines de nó) → fechamento → merge humano
              ▲__________ lacunas e falhas voltam como proposta ___________|
```

### 3.1 Upstream: refinar (nível milestone)

```
business → po → requirements → architect → planner ⇄ critic → [aprovação humana]
```

- As três primeiras etapas têm laços de entrevista com o humano, feitos pelo chat da web.
- O **planner** monta o grafo a partir das issues refinadas.
- O **critic**, numa sessão separada, revisa o grafo procurando lacunas: migrations e dados, infra e configuração, contratos front/back, testes E2E, observabilidade, segurança, feature flag e rollback, documentação e ADRs.
- O laço planner ⇄ critic repete até o crítico não encontrar lacuna relevante, com um limite de rodadas.
- **Saída:** uma proposta com o grafo, as issues novas e as alterações. **Nada é criado no tracker antes da aprovação.**

**Definition of Ready:** toda issue tem critérios de aceite, área, papel e dependências; o grafo não tem ciclos; o crítico não encontrou lacuna relevante.

### 3.2 Portão

A aprovação humana (label `plan/approved` ou o botão na web) faz três coisas:

1. Cria no tracker as issues e os links aprovados.
2. Faz commit do plano em `.kluzo/plans/<milestone>.yaml` na branch da milestone.
3. Libera o downstream.

### 3.3 Downstream: desenvolver (nível nó, repetido pelo grafo)

```
implementer → reviewer → gates → fila de merge → integrado
     ▲_____________________|  (volta com os findings)
```

- O scheduler roda essa pipeline em cada nó pronto, com o paralelismo que o grafo permitir.
- O downstream nunca replaneja. Lacunas e falhas voltam ao upstream como proposta.
- Se uma lacuna **não bloqueia** o nó atual, ela vai para a fila de aprovação e o resto segue. Se **bloqueia**, o nó vai para `orch/blocked` e só ele espera.
- Em ambiguidade pequena, o agente **decide e registra** a decisão (`kluzo_decision`). Ele só escala o que muda escopo, contrato ou segurança.

### 3.4 Fechamento (nível milestone)

```
gate E2E → reporter → MR milestone→main → [merge humano]
```

O reporter gera o relatório da milestone: nós entregues, log de decisões, lacunas que viraram issues, custo e desvios entre o plano e a entrega.

**Definition of Done:** todos os nós integrados, todos os gates verdes, gate E2E verde e relatório publicado no MR.

### 3.5 Esteira

Enquanto a milestone N está no downstream, a N+1 pode estar no upstream. Cada uma é um workspace próprio.

### 3.6 Estados

**Milestone (workspace):** `refining → awaiting_approval → approved → running → closing → ready_to_merge → merged → archived` (mais `paused` e `failed`).

**Nó:** `planned → ready → running → in_review → gating → queued → integrated` (mais `blocked`, `failed` e `escalated`).

## 4. Grafo da milestone

### 4.1 Nós

Cada nó carrega: os critérios de aceite, o papel, overrides de modelo, a **área** (footprint previsto de módulos e arquivos), o orçamento e a referência à issue.

### 4.2 Arestas

| Tipo | Significado | Origem |
|---|---|---|
| **bloqueio** | B precisa do código de A já integrado. | Link nativo do tracker (GitHub: `blocked_by`; GitLab Premium: `blocks`) ou linha `Depende de: #12` na descrição. |
| **contrato** | B precisa só da interface de A (spec de API, schema), então pode começar antes de A terminar. | Linha `Contrato: #18` na descrição, ou o plano. |
| **conflito** | A e B mexem na mesma área e são serializados. | Derivada automaticamente de labels `area/*` iguais, ou do footprint declarado no plano. |

### 4.3 Scheduler

- É determinístico. Não chama LLM e não gasta tokens para agendar.
- **Pronto** = todas as dependências de bloqueio estão **integradas** na branch da milestone, não só concluídas na branch delas.
- Prioriza o **caminho crítico**: com paralelismo limitado, roda primeiro o que destrava mais trabalho.
- Valida o grafo antes de iniciar (ciclos, dependências fora da milestone, nós sem área) e recusa iniciar com uma explicação.

### 4.4 Grafo vivo

Um nó que estoura o orçamento é enviado para replanejamento: o upstream pode quebrá-lo em nós menores. Nós propostos durante a execução (`kluzo_propose_node`) entram no grafo só depois de aprovados.

## 5. Integração Git

1. **Início:** cria `milestone/<nome>` a partir da `main`, ou adota uma branch existente.
2. **Nó:** a branch da issue nasce da ponta da branch da milestone, num worktree isolado. O MR aponta para a branch da milestone.
3. **Fila de merge:** integra um nó por vez. Faz rebase na ponta da milestone e roda o **freshness gate** (se a ponta mudou depois da review, os gates rodam de novo). Depois faz o merge e destrava os dependentes.
4. **Sincronia com a `main`:** um hotfix que entra na `main` é trazido para a branch da milestone entre duas integrações, nunca no meio da fila, e os gates rodam em seguida.
5. **Fim:** abre o MR `milestone → main` com o relatório.

**Proteção de branches recomendada:** em `milestone/*`, só o bot do Kluzo faz merge. Na `main`, só humanos.

## 6. Papéis, flows e harnesses

### 6.1 Papel

É um markdown com frontmatter, no mesmo formato dos subagents do Claude Code:

```markdown
---
harness: claude-code
model: sonnet
advisor: opus
subagents: haiku
effort: high
tools: [edit, bash, test]
skills: [testing-conventions]   # de .agents/skills/
output: node-result             # schema
budget: {attempts: 3, minutes: 45, usd: 4}
fallback:
  - {model: opus}
  - {harness: opencode, model: <outro>}
---
Você é o implementador. Leia o contexto do nó com `kluzo_context`…
```

- O prompt é agnóstico de harness. O adaptador traduz o resto da configuração.
- A saída é JSON validado contra o schema do papel. Saída inválida conta como falha.
- O Kluzo recusa combinações impossíveis no carregamento (por exemplo, advisor num harness sem suporte, ou advisor mais fraco que o modelo principal).

**Precedência:** padrão global → papel → override da milestone → label no nó (por exemplo `model/opus`).

**Papéis iniciais:**
- **Upstream:** `business`, `po`, `requirements`, `architect`, `planner`, `critic`.
- **Downstream:** `implementer`, `reviewer`, `qa`, `triage`.
- **Fechamento:** `reporter`.

### 6.2 Flow

```yaml
# .kluzo/flows/node.yaml
steps:
  - role: implementer
  - role: reviewer
    on_reject: implementer
  - gate: ci
  - merge_queue: milestone
```

O time é derivado dos papéis citados nos flows. Não existe uma declaração separada de time. A troca de elenco inteiro, se um dia for necessária, entra como **perfil** (`profiles/*.yaml` com overrides).

### 6.3 Contrato de runtime

```go
type Runtime interface {
    Start(ctx context.Context, task Task, ws Worktree, role Role) (Session, error)
    Resume(ctx context.Context, id SessionID) (Session, error)
}

type Session interface {
    Events() <-chan Event   // tool call, edição, mensagem, custo
    Result() (Result, error) // validado contra o schema do papel
    Cancel() error           // mata o grupo de processos
}
```

**Adaptadores v1:**
- **Claude Code:** `claude -p` com `--output-format stream-json`, `--resume`, e advisor e subagent configurados por papel.
- **OpenCode:** `opencode run` ou o modo servidor (`opencode serve`). Útil para papéis com modelos mais baratos, por ser multi-provedor.

A versão de cada CLI fica fixada em `kluzo.yaml`. Flags e formatos de evento mudam entre versões.

## 7. Núcleo sem LLM

O núcleo (scheduler, máquina de estados, fila de merge, gates) é código determinístico. Os pontos de julgamento são papéis:

| Julgamento | Como |
|---|---|
| Plano e lacunas | `planner` e `critic` (upstream) |
| Falha de nó | Primeiro a **tabela de regras** (teste flaky → retry; lint → volta ao implementador; 3ª falha → fallback de modelo). Só o caso ambíguo vai para o papel `triage`, que responde `retry`, `switch_model`, `split` ou `escalate`. |
| Relatório | `reporter` |

O orquestrador **chama** LLMs, mas não **é** um LLM. Ele aplica regras sobre as respostas estruturadas.

## 8. Tracker e SCM

**Interfaces:** `Tracker` (issues, links, labels, comentários), `SCM` (branches, MR/PR, merge) e `CI` (status dos gates). O adaptador do GitLab vem primeiro; o do GitHub vem em seguida.

| Conceito | GitLab | GitHub |
|---|---|---|
| Milestone | Milestone | Milestone |
| Nó | Issue / work item | Issue |
| Subtask | Task filha ou checklist | Sub-issue ou checklist |
| Bloqueio | `blocks` (Premium) ou descrição | `blocked_by` nativo |
| Gates | Pipeline | Checks / Actions |

**Fontes da verdade:**
- **Definição do trabalho:** o tracker.
- **Estado da execução:** o SQLite do workspace.

O estado é **espelhado** para o tracker (labels e um comentário por transição) para dar visibilidade no app do tracker.

**Labels têm dono.** Uma label escrita por quem não é o dono dela é ignorada.

| Família | Dono |
|---|---|
| `plan/*` (exceto `plan/approved`) | planner |
| `plan/approved` | humano |
| `orch/*` | Kluzo |
| `area/*`, `role/*`, `model/*` | refino (agentes de upstream ou humano) |

**Eventos:** começa por polling (funciona atrás de NAT). Webhooks ficam para depois.

**Identidade:** um usuário bot com project access token no GitLab, e um GitHub App no GitHub.

## 9. Canal agente → Kluzo (MCP)

Os agentes **não conversam entre si**. Toda comunicação passa pelo Kluzo, em estrela, e fica no journal.

O binário expõe um **servidor MCP**, injetado em cada sessão e já identificado com o nó e o papel dela:

| Ferramenta | Função |
|---|---|
| `kluzo_context` | Issue, critérios, grafo ao redor e decisões anteriores. |
| `kluzo_decision` | Registra uma decisão tomada sob ambiguidade. |
| `kluzo_propose_node` | Propõe uma lacuna como nó novo (exige aprovação). |
| `kluzo_ask` | Escala uma pergunta para o humano (vira card na web). |
| `kluzo_result` | Entrega o resultado final, validado pelo schema. |

As permissões são por papel. Nenhum papel tem acesso a aprovação ou merge.

**Skills** (`.agents/skills/`) continuam servindo para o conhecimento do projeto. O papel declara no frontmatter quais carrega.

## 10. Interfaces

1. **Web** (principal, **responsiva e mobile-first**): Vue + Vue Flow, embutida no binário, com tempo real via SSE. O layout é desenhado primeiro para o celular e se expande em três faixas:

   | Faixa | Largura | Navegação | Conteúdo |
   |---|---|---|---|
   | Celular | < 768 px | barra inferior | uma coluna; detalhe em tela própria ou painel inferior |
   | Tablet | 768–1199 px | trilho lateral recolhido | lista + detalhe lado a lado |
   | Desktop | ≥ 1200 px | barra lateral | grafo/kanban/estrutura + painel de detalhe fixo; kanban com todas as colunas |

   - Lista de workspaces.
   - **Progresso em duas visões alternáveis:**
     - **Grafo ao vivo:** estados por cor, arestas por tipo, caminho crítico, progresso e custo.
     - **Kanban:** colunas por estado do nó (Planejado, Pronto, Executando, Review/Gates, Fila de merge, Integrado), com Bloqueado/Escalado destacados. No celular, uma coluna por vez, com navegação por abas.
   - Fila de merge e log de decisões.
   - **Detalhe do nó:** esteira de papéis, chamadas do advisor, subagents, stream de ferramentas e diff.
   - **Ações:** iniciar, pausar, retomar, retry, trocar modelo, chat das entrevistas do upstream, aprovação item a item do plano e resposta a escalações.
   - **Acesso via `tsnet`:** o Kluzo é um nó da tailnet, com HTTPS da própria tailnet e identidade via `WhoIs`. Sem porta pública e sem sistema de login próprio. Escrita e leitura liberadas por usuário da tailnet.
2. **CLI** `kluzo`: `plan`, `approve`, `run`, `status`, `pause`, `resume`. A web usa os mesmos comandos internos.
3. **Tracker:** labels, comentários e o grafo em Mermaid na milestone.
4. **Notificações:** só quando a ação humana é necessária. Via ntfy ou Telegram, com link para a tela correspondente.

## 11. Layout

### No repositório

```
.kluzo/
├── kluzo.yaml        # tracker, branches, gates, runtimes, web, notificações
├── roles/            # um .md com frontmatter por papel
├── flows/            # upstream.yaml, node.yaml, closing.yaml
├── schemas/          # opcional: sobrescreve os schemas embutidos
└── plans/            # planos aprovados, um por milestone
```

### Estado (fora do repositório)

```
~/.local/share/kluzo/workspaces/<projeto>-<milestone>/
├── state.db          # SQLite: grafo, estados, journal, custo
├── worktrees/
└── logs/
```

## 12. Stack

- **Go** no núcleo, num binário único.
- **SQLite** (`modernc.org/sqlite`, sem cgo) para estado e journal, com WAL.
- **Vue + Vue Flow** na web, embutida via `embed`, com layout automático (ELK ou dagre).
- **tsnet** para o acesso.
- **NATS JetStream** só quando houver workers distribuídos em mais de uma máquina.

## 13. Robustez

- **Journal por transição:** um crash retoma do último passo concluído.
- **Orçamento** por nó e por milestone (tentativas, tempo e custo). Quando estoura, o nó vai para `orch/blocked` e o resto do grafo segue.
- **Kill do grupo de processos** de um agente descontrolado.
- **Ambiente autossuficiente** (pré-requisito do projeto alvo): seed de banco, sandboxes ou mocks dos serviços externos, e secrets disponíveis para o Runner.

## 14. Roadmap

| Versão | Escopo |
|---|---|
| **v0.1** | CLI; GitLab; um workspace; grafo com arestas de bloqueio; execução **sequencial**; adaptador Claude Code; pipeline de nó implementer → reviewer → gates; SQLite e journal; resume. |
| **v0.2** | Paralelismo; arestas de conflito e contrato; fila de merge com freshness gate; adaptador OpenCode; MCP `kluzo_*`. |
| **v0.3** | Upstream completo com planner ⇄ critic e portão de aprovação; web com grafo ao vivo; tsnet; notificações. |
| **v0.4** | Adaptador GitHub; perfis; esteira de workspaces; relatório da milestone. |

## 15. Questões abertas

- O footprint (área) de um nó é declarado pelo planner ou inferido do código? Possivelmente as duas coisas: uma estimativa do planner e a confirmação pelo diff real.
- Merge na milestone: fast-forward após o rebase ou merge commit?
- O schema de `node-result` e dos demais papéis.
- O formato exato de `flows/*.yaml`: laços, condições e paralelismo dentro de um flow.
