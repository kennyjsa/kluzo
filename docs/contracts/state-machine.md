# Máquina de estados

Toda mudança de estado acontece no núcleo, numa transação SQLite, e gera um evento `workspace.transition` ou `node.transition` no journal ([events.md](events.md)). Uma transição que não está nestas tabelas é um bug, e o núcleo a recusa com erro.

O espelhamento no tracker (labels e comentário) acontece **depois** do commit da transação, de forma idempotente. Se o tracker falhar, o Kluzo tenta de novo, mas o estado do Kluzo não volta atrás.

## Workspace

```
refining → awaiting_approval → approved → running → closing → ready_to_merge → merged → archived
    ▲              │                         │  ▲
    └── rejected ──┘                     pause  resume
                                             ▼  │
                                           paused
qualquer estado ativo ── cancel ──► cancelled
qualquer estado ativo ── erro irrecuperável ──► failed
```

| De | Para | Gatilho | Condição | Efeito |
|---|---|---|---|---|
| — | `refining` | `kluzo workspace create` / web | milestone existe e sem workspace ativo | cria a branch `milestone/<nome>`; inicia o flow `upstream` |
| — | `awaiting_approval` | `kluzo plan` com plano escrito à mão (v0.1) | milestone existe e sem workspace ativo; plano válido | cria a branch `milestone/<nome>`; registra a proposta de plano |
| `refining` | `awaiting_approval` | flow `upstream` chega na etapa `approval` | resultado do planner válido | gera a proposta de plano; item na Inbox |
| `awaiting_approval` | `refining` | humano recusa algum item | — | devolve ao planner com os comentários |
| `awaiting_approval` | `approved` | humano aprova o plano | nenhum item pendente; grafo válido | cria issues e links aprovados; commit de `plans/<milestone>.yaml` |
| `approved` | `running` | imediato | efeito `git.milestone` concluído | scheduler começa a despachar nós |
| `approved` | `paused` | efeito `git.milestone` falha permanente (branch local e remota divergentes) | — | notificação; nenhum nó é despachado; o humano reconcilia a branch e retoma (`resume` reenfileira o efeito) |
| `running` | `paused` | efeito `git.milestone` reenfileirado por um `resume` falha permanente | — | igual à linha anterior |
| `running` | `paused` | humano pausa | — | sessões ativas recebem `Cancel`; nós voltam para o último estado estável |
| `running` | `paused` | credencial do runtime recusada (401/403, ADR 0024), ou credenciais do host visíveis às sessões sem `security.allow_host_credentials` (ADR 0020) | — | igual à pausa humana, com notificação; retomar exige `kluzo doctor` verde |
| `paused` | `running` | humano retoma | — | scheduler volta a despachar (só depois de `git.milestone` concluído) |
| `running` | `closing` | último nó vira `integrated` | todos os nós `integrated` ou `cancelled` | inicia o flow `closing` |
| `closing` | `ready_to_merge` | flow `closing` termina | gate E2E verde | abre o PR/MR `milestone → main` com o relatório |
| `closing` | `running` | gate E2E falha | — | triagem cria um nó de correção (proposta, exige aprovação) |
| `ready_to_merge` | `merged` | tracker informa merge na `main` | — | — |
| `merged` | `archived` | imediato ou humano | — | remove worktrees; mantém o journal |
| ativo | `cancelled` | humano cancela | — | `Cancel` em todas as sessões; branch mantida |
| ativo | `failed` | erro irrecuperável (repositório inacessível, config inválida no meio da execução) | — | item na Inbox com o diagnóstico |

O estado da branch da milestone é o estado do último efeito `git.milestone` no outbox (`pending`, `done`, `failed`); sem efeito, `pending`. O scheduler só despacha sessão, gate ou integração com o workspace `running` **e** esse estado `done`.

A pausa vale para o processo que estiver rodando o `kluzo run`, mesmo vinda de outro processo (CLI, web): a cada volta o supervisor relê o estado e, achando `paused`, envia `Cancel` às sessões ativas e devolve os nós ao último estado estável, como na linha `running → paused`.

Na v0.1 não existe upstream com agentes: `kluzo plan` cria o workspace direto em `awaiting_approval`, a partir de um plano escrito à mão.

## Nó

```
planned → ready → running → in_review → gating → queued → integrating → integrated
                     ▲          │           │                  │
                     └─ reject ─┴── fail ───┘                  │
                     ▲                                          │
                     └──────────── rebase conflict / freshness fail
qualquer estado não final ──► blocked ──► (estado anterior)
qualquer estado não final ──► failed | cancelled
```

| Estado | Significado | Label no tracker |
|---|---|---|
| `planned` | No plano; alguma dependência ainda não satisfeita | `orch/planned` |
| `ready` | Dependências satisfeitas; aguarda vaga no scheduler | `orch/ready` |
| `running` | Papel de implementação ativo (etapas anteriores ao primeiro `role` de revisão) | `orch/running` |
| `in_review` | Papel de revisão ativo | `orch/review` |
| `gating` | Gates rodando | `orch/gates` |
| `queued` | Aguardando a vez na fila de merge | `orch/queued` |
| `integrating` | Rebase, freshness gate e merge em andamento | `orch/queued` |
| `integrated` | Merge feito na branch da milestone (final) | `orch/integrated` |
| `blocked` | Parado por um motivo que exige ação (ver abaixo) | `orch/blocked` |
| `failed` | Desistência após triagem (final, até ação humana) | `orch/failed` |
| `cancelled` | Removido da milestone (final) | `orch/cancelled` |

Qual etapa do flow corresponde a `running` ou a `in_review` é definido pelo campo `phase` de cada etapa `role` ([config.md](config.md#flows)).

### Transições do nó

| De | Para | Gatilho | Condição |
|---|---|---|---|
| `planned` | `ready` | uma dependência vira `integrated` | todas as arestas `blocks` de entrada `integrated`; todas as `contract` de entrada com o contrato publicado |
| `ready` | `running` | scheduler despacha | vaga livre; nenhum nó em execução com área em comum |
| `running` | `in_review` | resultado do implementer com `status: done` | resultado válido pelo schema |
| `in_review` | `running` | veredito derivado `request_changes` ([results.md](results.md#review-result-reviewer)) | `rounds < max_rounds` |
| `in_review` | `blocked` (`triage`) | veredito derivado `request_changes` | `rounds == max_rounds`: as rodadas acabaram com finding `blocking` aberto. A Inbox mostra os findings abertos e as respostas do implementer |
| `in_review` | `gating` | veredito derivado `approve` | — |
| `gating` | `running` | algum gate falha | tentativas restantes |
| `gating` | `queued` | todos os gates verdes | conferência de áreas feita (ADR 0011) |
| `queued` | `integrating` | chega a vez na fila | nenhum outro nó `integrating` no workspace |
| `integrating` | `integrated` | merge feito | freshness gate verde |
| `integrating` | `running` | conflito no rebase ou freshness gate vermelho | tentativas restantes; nova tentativa com o contexto do erro |
| qualquer não final | `blocked` | ver motivos | — |
| `blocked` | estado anterior | motivo resolvido | — |
| `blocked` | `running` | humano pede nova tentativa (`kluzo node retry`) | o nó já foi despachado; tentativas restantes; a tentativa nova começa da primeira etapa, com worktree novo |
| qualquer não final | `failed` | triagem decide desistir | — |
| qualquer não final | `cancelled` | humano remove o nó | nenhum dependente ativo, ou dependentes também cancelados |

Os **gates rápidos** (`fast: true`, ADR 0020) rodam com o nó ainda em `running`, entre o implementer e o reviewer. Uma falha volta ao implementer na mesma sessão e na mesma tentativa. Ela tem contador próprio, limitado pelo `max_retries` da etapa `gate` (padrão 3), que zera a cada tentativa (não a cada rodada) e não consome as rodadas de review. Esgotado o limite, o nó vai para `blocked` (`triage`). O gatilho `implementer_done` só é emitido depois que os gates rápidos passam.

No desempate do scheduler, referências numéricas são comparadas como números (ordem de criação no tracker); só referências não numéricas usam a ordem de texto.

Uma **tentativa** (`attempt`) começa a cada entrada em `running` vinda de `ready`, `gating` ou `integrating`. Uma devolução do reviewer (`in_review → running`) é uma **rodada** dentro da mesma tentativa.

### Motivos de bloqueio

| `blocked_reason` | Quando | Como resolve |
|---|---|---|
| `question` | `kluzo_ask` | resposta humana |
| `proposal` | `kluzo_propose_node` com `blocking: true`; ou a conferência depois de uma rodada do implementer encontra mudança em `protected_paths`, teste apagado ou teste desativado (ADR 0020), com `proposal.created` (`kind: change`) | proposta aprovada (e o nó novo integrado) ou recusada |
| `budget` | orçamento do nó estourou | humano aumenta o orçamento, troca o modelo ou cancela |
| `triage` | triagem decide `escalate` | ação humana indicada no item da Inbox |
| `dependency_failed` | uma dependência virou `failed` ou `cancelled` | dependência resolvida, ou o nó é replanejado |
| `paused` | workspace pausado | workspace retomado |

Enquanto a triagem automática não existe (v0.2), toda etapa que falha vai direto para `blocked` (`triage`), com notificação e o motivo no comentário: `CauseGuard`, `CauseMaxTurns`, `CauseRefusal`, `CauseNoResult`, `status: failed` do implementer, as repetições esgotadas de `CauseRateLimit` e `CauseRuntime` e o `worktree.setup` que continua falhando. `CauseAuth` leva o nó a `blocked` (`paused`) e o workspace a `paused`; `CauseBudget`, e o orçamento do nó esgotado antes de uma sessão, levam a `blocked` (`budget`). A conferência pós-sessão que encontra adulteração leva a `failed`. A saída de `blocked` na v0.1 é humana, pela CLI ([v0.1, item 10b](../planning/milestones/v0.1.md)): `kluzo node unblock` volta ao estado anterior e a etapa atual roda de novo (um nó que estava integrando refaz o rebase; a contagem dos gates rápidos zera); `kluzo node retry` começa uma tentativa nova; `kluzo node cancel` cancela o nó, fora de `running`, `in_review`, `gating` e `integrating`. Um nó bloqueado por `proposal` sai pela decisão da proposta (`kluzo proposal approve|reject`): aprovar segue para a etapa seguinte; recusar o devolve ao implementer, na mesma tentativa, com um finding automático (`blocking`) por mudança pedindo a reversão.

## Labels no tracker

- Todas as labels `orch/*` pertencem ao Kluzo. O Kluzo ignora qualquer `orch/*` aplicada por outro usuário e a reverte.
- Um nó tem exatamente uma label `orch/*` por vez. A troca é feita removendo a anterior e aplicando a nova.
- Cada transição gera um comentário curto na issue: o estado novo, o motivo e um link para a tela do nó, quando a web existir.
