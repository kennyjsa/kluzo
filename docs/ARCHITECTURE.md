# Arquitetura do código

Como o código do Kluzo é organizado e quais dependências entre pacotes são permitidas. Os conceitos estão na [SPEC](SPEC.md) e os formatos em [contracts/](contracts/).

## Visão geral

```
                 ┌──────────── cmd/kluzo (CLI) ─────────────┐
                 │                                           │
                 ▼                                           ▼
          internal/app  ◄──────── internal/web (HTTP, SSE, tsnet, SPA embutida)
     (comandos: plan, approve, run, pause, answer…)
                 │
                 ▼
          internal/core ────────► internal/ports (interfaces) ◄──── adaptadores
   (máquina de estados, scheduler,                                  internal/tracker/github
    fila de merge, flows, triagem                                   internal/tracker/gitlab
    por regras, orçamento)                                          internal/runtime/claudecode
                 │                                                  internal/runtime/opencode
                 ▼                                                  internal/notify/ntfy
          internal/store (SQLite, journal, projeções)
          internal/gitops (worktree, rebase, merge local)
          internal/mcp (servidor MCP por sessão)
          internal/config (carga e validação de .kluzo/)
          internal/contracts (tipos e JSON Schemas embutidos)
```

## Pacotes

| Pacote | Responsabilidade |
|---|---|
| `cmd/kluzo` | Entrada da CLI (Cobra). Só faz parse de flags e chama `internal/app`. |
| `internal/app` | Casos de uso: um comando por operação (`CreateWorkspace`, `ApprovePlan`, `Run`, `Pause`, `AnswerQuestion`…). É usado pela CLI e pela web. Monta as dependências (injeção manual, sem framework). |
| `internal/core` | Regras do domínio: máquina de estados ([state-machine.md](contracts/state-machine.md)), scheduler, avaliação de flows, fila de merge, conferência de áreas, triagem por regras e orçamento. **Determinístico: não chama LLM, não faz I/O direto.** |
| `internal/ports` | As interfaces de [interfaces.md](contracts/interfaces.md) e os tipos que elas trocam. Os fakes ficam em `internal/ports/fakes`. |
| `internal/store` | SQLite, migrations, `store.Tx` (grava o evento e atualiza as projeções juntos) e reconstrução das projeções a partir do journal. |
| `internal/gitops` | Git local via binário `git`: worktrees, rebase, `merge --no-ff`, diff por área. |
| `internal/config` | Carga e validação de `.kluzo/` (YAML, frontmatter), com erro por arquivo e linha. |
| `internal/contracts` | Tipos Go dos resultados e eventos, e os JSON Schemas embutidos (`schemas/*.json`). |
| `internal/mcp` | Servidor MCP: um endpoint e um token por sessão, e permissões por papel. |
| `internal/tracker/{github,gitlab}` | Implementam `Tracker`, `SCM` e `CI`. |
| `internal/runtime/{claudecode,opencode}` | Implementam `Runtime`; normalizam os eventos do harness. |
| `internal/notify/{log,ntfy}` | Implementam `Notifier`. |
| `internal/web` | API REST, SSE, `tsnet`, autorização por `WhoIs` e a SPA embutida (`web/dist` via `embed`). |
| `internal/supervisor` | Laço principal: polling do tracker, despacho de sessões, timeouts, retomada ao subir. Liga `core` aos ports. |
| `web/` | Código-fonte Vue (fora do Go). O build gera `web/dist`, que é embutido. |

## Regras de dependência

1. **`internal/core` importa só `internal/ports` e `internal/contracts`.** Nunca adaptadores, nunca `store`, nunca `net/http`, nunca `os/exec`. O núcleo recebe dados e devolve decisões. Quem executa as decisões é o `supervisor`.
2. **Adaptadores não importam `core`.** Eles implementam interfaces de `ports` e não sabem nada do domínio além dos tipos dessas interfaces.
3. **`internal/web` e `cmd/kluzo` só falam com `internal/app`.** Nenhuma regra de negócio na camada HTTP ou na CLI.
4. **Só o `store` toca o SQLite.** Só o `gitops` executa `git`. Só os `runtime/*` executam os harnesses.
5. **Sem estado global.** Nada de `init()` com efeito colateral nem singletons. Tudo é passado por construtor.

Essas regras são verificadas no CI com um teste de imports (`go list -deps`), em `internal/archtest`.

## O núcleo como função

O núcleo é modelado como "estado + evento → novo estado + efeitos":

```go
// internal/core
func (m *Machine) Apply(s State, e Input) (State, []Effect, error)
```

- `Input` é algo que aconteceu: resultado de sessão, gate terminado, resposta humana, tick do scheduler.
- `Effect` é algo a fazer: iniciar uma sessão, rodar um gate, trocar uma label, rebase, merge, notificar.
- O `supervisor` grava o `State` e os eventos com `store.Tx` e só então executa os `Effect`s pelos ports.
- Cada `Effect` tem uma chave de idempotência. Se o processo morrer depois do commit e antes do efeito, a retomada reexecuta os efeitos pendentes sem duplicar nada.

Isso torna cada transição testável como função pura, com tabelas de casos, sem GitHub e sem LLM.

## Concorrência

- Um `supervisor` por workspace, numa goroutine. Uma única goroutine aplica `Input`s no `Machine`, em fila; não há lock no núcleo.
- As sessões de agente e os gates rodam em goroutines próprias e devolvem o resultado como `Input` pela fila.
- Um processo do Kluzo pode supervisionar vários workspaces (esteira, v0.4). Cada workspace tem o seu banco, e não há estado compartilhado entre eles.
