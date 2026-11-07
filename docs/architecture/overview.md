# Visão da arquitetura

Como o Kluzo é construído por dentro. O comportamento do produto está em [product/](../product/), e os formatos exatos em [contracts/](../contracts/).

## Componentes

```
                ┌──── cmd/kluzo (CLI) ────┐        navegador (tailnet)
                │                          │               │
                ▼                          ▼               ▼
          internal/app  ◄─────────────── internal/web (REST, SSE, tsnet, SPA)
     (casos de uso: plan, approve, run, pause, answer…)
                │
                ▼
        internal/supervisor ── laço por workspace: entradas → núcleo → efeitos
           │         │                       │
           ▼         ▼                       ▼
   internal/core   internal/store      internal/ports ◄── adaptadores
   (máquina de     (SQLite: journal,   (interfaces)       tracker/github, tracker/gitlab
    estados,        projeções,                            runtime/claudecode, runtime/opencode
    scheduler,      efeitos)                              notify/ntfy
    flows, fila,                                          gitops (git local)
    triagem)                                              mcp (servidor por sessão)
```

O desenho segue **ports & adapters** com **núcleo funcional e casca imperativa** (ADR 0012). Os padrões estão detalhados em [patterns.md](patterns.md), e os pacotes em [packages.md](packages.md).

## O laço do supervisor

Cada workspace tem um supervisor, numa goroutine, que repete este ciclo:

```
1. recebe uma entrada (resultado de sessão, gate terminado, resposta humana,
   mudança no tracker, tick do relógio)
2. core.Apply(estado, entrada) → novo estado + efeitos     [puro, sem I/O]
3. store.Tx: grava os eventos, atualiza as projeções e
   enfileira os efeitos (outbox)                           [uma transação]
4. executa os efeitos pendentes pelos ports                [idempotentes]
5. marca cada efeito como concluído
```

- Se o processo morrer entre o passo 3 e o passo 5, a retomada reexecuta os efeitos pendentes. A chave de idempotência evita duplicidade: nada de label aplicada duas vezes, comentário repetido ou merge em dobro (ADR 0013).
- As sessões de agente e os gates rodam em goroutines próprias e devolvem o resultado como uma nova entrada.
- Uma única goroutine por workspace aplica as entradas, em fila. Por isso o núcleo não usa locks.

## Estado

| Onde | O quê |
|---|---|
| `~/.local/share/kluzo/kluzo.db` | Registro de projetos e workspaces |
| `~/.local/share/kluzo/workspaces/<projeto>-<milestone>/state.db` | Journal, projeções e efeitos do workspace (ADR 0014) |
| `…/worktrees/<issue>/` | Um worktree por nó ativo |
| `…/logs/` | Streams brutos das sessões e logs dos gates |
| `.kluzo/` no repositório | Configuração e planos aprovados (versionados) |

O esquema completo está em [contracts/data-model.md](../contracts/data-model.md).

## Integrações

- **Tracker, SCM e CI:** polling primeiro, porque funciona atrás de NAT (ADR 0015). Os detalhes estão em [integrations.md](integrations.md).
- **Harnesses:** processos filhos (`claude -p`, `opencode run`). Os eventos deles são normalizados ([contracts/events.md](../contracts/events.md)).
- **Agentes → Kluzo:** um servidor MCP local, com um endpoint e um token por sessão ([contracts/mcp.md](../contracts/mcp.md)).
- **Web:** REST para leitura e ações; SSE para o tempo real (ADR 0015).

## Robustez

- **Retomada:** journal mais outbox de efeitos. Matar o processo em qualquer ponto não perde nem duplica trabalho.
- **Orçamento:** limites por nó e por milestone (tentativas, tempo e custo). Quando estoura, o nó vai para `blocked`, e o resto do grafo segue.
- **Agente descontrolado:** timeout por etapa e kill do grupo de processos.
- **Tracker instável:** o espelhamento é assíncrono e repetido com backoff. O estado do Kluzo nunca volta atrás por causa de uma falha no tracker.
- **Pré-requisito do projeto alvo:** um ambiente autossuficiente, com banco com seed, sandboxes ou mocks dos serviços externos e segredos disponíveis para os gates.

## Escala

A versão 1 roda num único processo, numa única máquina (notebook ou VPS), com vários workspaces. A distribuição de workers por várias máquinas (NATS JetStream) só entra se aparecer a necessidade. Ela não está no roadmap.
