# Pacotes e regras de dependência

## Layout do repositório

```
kluzo/
├── cmd/kluzo/                  # main e comandos Cobra
├── internal/
│   ├── app/                    # casos de uso (usados pela CLI e pela web)
│   ├── core/                   # domínio puro: máquina de estados, scheduler, flows, fila, triagem
│   ├── supervisor/             # laço por workspace: entradas → core → efeitos
│   ├── ports/                  # interfaces (Tracker, SCM, CI, Runtime, Notifier, CodeMapper) e tipos
│   │   ├── fakes/              # implementações em memória para testes
│   │   └── contracttest/       # suíte que todo adaptador deve passar
│   ├── store/                  # SQLite, migrations, Tx, journal, projeções, outbox
│   ├── gitops/                 # git local endurecido: worktree e preparo, checkout limpa dos gates, conferência pós-sessão, commit do nó, diff da rodada, rebase, merge; port Files
│   ├── config/                 # carga e validação de .kluzo/
│   ├── contracts/              # tipos de eventos e resultados, JSON Schemas embutidos
│   ├── gates/                  # port Gates: comando no worktree ou checks do CI; log, go test -json, JUnit
│   ├── guard/                  # política de ferramentas do agente (git, gh, caminhos protegidos e secretos), usada pelo hook e pela conferência do diff
│   ├── prompt/                 # montagem dos três segmentos de prompt, envelope e manifesto
│   ├── mcp/                    # servidor MCP por sessão
│   ├── tracker/github/         # Tracker + SCM + CI
│   ├── tracker/gitlab/
│   ├── runtime/claudecode/     # Runtime
│   ├── runtime/opencode/
│   ├── notify/{log,ntfy}/      # Notifier
│   ├── codemap/{cartog,lsp}/   # CodeMapper (v0.3, ADR 0021)
│   ├── web/                    # REST, SSE, tsnet, autorização, SPA embutida
│   ├── platform/               # utilitários técnicos sem domínio: clock, ids (ULID), exec, redact, sessionenv, hostcred, text
│   └── archtest/               # testes das regras de dependência
├── web/                        # frontend Vue (fonte) → web/dist (embutido)
├── testdata/
└── docs/
```

## Responsabilidades

| Pacote | Faz | Não faz |
|---|---|---|
| `cmd/kluzo` | Parse de flags; chama `app`; formata a saída | Regra de negócio |
| `app` | Um caso de uso por operação (`CreateWorkspace`, `ApprovePlan`, `Run`, `Pause`, `AnswerQuestion`…); monta as dependências | Lógica de transição (isso é do `core`) |
| `core` | `Apply(state, input) → (state, effects)`; validação do grafo; caminho crítico; avaliação de flows; regras de triagem e orçamento | I/O de qualquer tipo; relógio real; geração de ids aleatórios |
| `supervisor` | Laço do workspace: `planned → ready`, despacho, `core.Apply` e uma transação por decisão (transições, `node.flow`, efeitos); executor do outbox com a política de `data-model.md`; sessão de papel (worktree, prompt, MCP, início ou retomada, conferência pós-sessão, commit); etapas de gate pelo port `Gates`; registro de `mcp.call` e `guard.denied`; manifesto no `session.started`, custo das sessões e `budget.alert` da milestone; fatos da tentativa anterior a partir do journal; gates congelados no commit base da tentativa; conferência pós-rodada com as regras de `guard` (proposta ou finding automático); recusa de sessão com credenciais do host; retomada depois de um crash | Decidir transições |
| `ports` | Interfaces e tipos de fronteira | Implementação |
| `store` | Persistência, `Tx`, reconstrução das projeções a partir do journal | Regras de domínio |
| `gitops` | Executar `git` (endurecido: sem hooks, fsmonitor nem configuração global ou de sistema); criar, preparar (`worktree.include` e `worktree.setup` da ponta da milestone) e remover o worktree do nó; conferir e restaurar refs, `.git/config` e `.git/hooks` depois de cada sessão; commitar o trabalho do nó (o Kluzo commita, nunca o agente) e calcular o diff de uma rodada; implementar o port `Files` | Falar com o tracker; decidir o nome da branch do nó |
| `config` | Ler e validar `.kluzo/` (inclusive `plans/<milestone>.yaml`), com erro por arquivo e linha | Aplicar a precedência em tempo de execução (isso é do `core`) |
| `contracts` | Tipos e schemas compartilhados por todas as camadas | Lógica |
| `gates` | Implementar o port `Gates`: rodar o comando do gate no worktree, com o ambiente e o timeout recebidos, guardar o log redigido em `logs/gates/` e extrair falhas estruturadas (`go test -json`, JUnit); ou ler os checks do CI | Ler a definição do gate (o supervisor a passa congelada) |
| `guard` | Decidir se uma chamada de ferramenta, ou uma mudança de arquivo da rodada, quebra as regras do ADR 0020 e 0024 | Falar com o harness, com o Kluzo ou com o disco |
| `prompt` | Montar protocolo, papel e tarefa; sanear e envelopar texto externo; aplicar os limites; gerar o manifesto da sessão | Chamar LLM, fazer I/O ou gerar o nonce sozinho (o chamador injeta a fonte) |
| `mcp` | Servir as ferramentas `kluzo_*` e converter chamadas em entradas para o supervisor | Decidir o que acontece com elas |
| `tracker/*`, `runtime/*`, `notify/*`, `codemap/*` | Implementar um port | Importar `core` |
| `web` | HTTP, SSE, autenticação via tailnet; chama `app` | Regra de negócio |
| `platform` | Clock, gerador de ids, `exec` com grupo de processos, remoção de segredos, ambiente das sessões, credenciais visíveis do host (`hostcred`), corte de texto (`text.Clip`, o único) | Conhecer o domínio |

### Sessões e gates no laço do supervisor

O [overview](overview.md) prevê sessões e gates em goroutines próprias, devolvendo o resultado como entrada. Com `max_parallel: 1` na v0.1, o laço roda a sessão e os gates dentro do executor do outbox, sem goroutine: enquanto uma sessão roda, labels e comentários de outros nós esperam. Com o workspace fora de `running`, o laço não inicia sessão nem gate, mas labels, comentários e notificações continuam saindo. Quem ligar o paralelismo move a sessão e os gates para goroutines e mantém a decisão numa transação só.

## Regras de dependência

1. **`core` importa só `contracts`, `ports` (só tipos) e a biblioteca padrão pura.** Nunca `store`, adaptadores, `net/*`, `os/exec`, `os` (arquivos), `time.Now` nem `math/rand`. O relógio e os ids chegam como dados na entrada.
2. **Adaptadores (`tracker/*`, `runtime/*`, `notify/*`, `gitops`, `gates`) não importam `core`, `app`, `supervisor` nem `store`.**
3. **`web` e `cmd/kluzo` importam só `app`, `contracts` e `platform`.**
4. **Só `store` importa o driver SQLite. Só `gitops` executa `git`. Só `runtime/*` executa os harnesses.** Todos usam `platform/exec`.
5. **Sem estado global:** nada de variáveis de pacote mutáveis e nada de `init()` com efeito colateral. Dependências entram por construtor.
6. **`internal/` é privado.** Não há API Go pública na v1.

As regras são verificadas por `internal/archtest`, que roda `go list -deps` e falha o CI quando uma regra é violada. Uma regra nova entra no mesmo PR que o teste correspondente.

## Frontend (`web/`)

```
web/
├── src/
│   ├── app/            # bootstrap, roteador, layout responsivo (barra inferior / trilho / lateral)
│   ├── api/            # cliente REST + SSE tipados (gerados dos contratos)
│   ├── stores/         # Pinia: snapshot + aplicação de eventos
│   ├── features/       # uma pasta por área: inbox, projects, workspace, progress, node, upstream
│   ├── components/     # componentes visuais reutilizáveis (card de nó, tarja de estado…)
│   ├── design/         # tokens (cores de estado, tipografia, espaçamento)
│   └── i18n/           # pt-BR (padrão) e en
└── tests/              # Vitest (unidade) e Playwright (e2e em 390/768/1280)
```

Regras:
- `features/*` não importam umas às outras. O que é compartilhado vai para `components/` ou `stores/`.
- Só `api/` faz chamadas HTTP.
- Nenhuma regra de estado no front: o front aplica eventos sobre o snapshot, e quem decide transições é o servidor.
