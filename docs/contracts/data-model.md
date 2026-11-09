# Modelo de dados

Cada workspace tem o seu próprio banco SQLite em `~/.local/share/kluzo/workspaces/<project>-<milestone>/state.db`. Um banco global, `~/.local/share/kluzo/kluzo.db`, guarda só o registro de projetos e workspaces.

Configuração do SQLite: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`. As migrations ficam em `internal/store/migrations/`, numeradas e sem volta (só para frente).

## Regras

- **O journal (`events`) é a fonte da verdade.** As tabelas de estado (`nodes`, `sessions`…) são projeções mantidas na mesma transação que grava o evento. Reconstruir as projeções a partir do journal deve dar o mesmo resultado; existe um teste para isso.
- Toda escrita de estado passa por `store.Tx`, que grava o evento, atualiza as projeções e enfileira os efeitos (`effects`) juntos.
- Nada é apagado do journal. Cancelar ou arquivar é mudança de estado. Worktrees, caches de build, recursos Docker marcados e logs antigos seguem o ciclo de limpeza do ADR 0025.

## Banco global (`kluzo.db`)

```sql
CREATE TABLE projects (
  id            TEXT PRIMARY KEY,          -- prj_<ulid>
  name          TEXT NOT NULL,
  repo_path     TEXT NOT NULL UNIQUE,      -- caminho local do clone
  tracker       TEXT NOT NULL,             -- 'github' | 'gitlab'
  remote        TEXT NOT NULL,             -- 'kennyjsa/kluzo'
  created_at    TEXT NOT NULL
);

CREATE TABLE workspaces (
  id            TEXT PRIMARY KEY,          -- ws_<ulid>
  project_id    TEXT NOT NULL REFERENCES projects(id),
  milestone     TEXT NOT NULL,             -- título da milestone no tracker
  milestone_ref TEXT NOT NULL,             -- id/número da milestone no tracker
  state         TEXT NOT NULL,             -- espelho do estado no state.db
  db_path       TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  UNIQUE (project_id, milestone_ref)
);
```

## Banco do workspace (`state.db`)

```sql
-- Journal: append-only. seq é a ordem total dentro do workspace.
CREATE TABLE events (
  seq           INTEGER PRIMARY KEY AUTOINCREMENT,
  id            TEXT NOT NULL UNIQUE,      -- ev_<ulid>
  ts            TEXT NOT NULL,
  kind          TEXT NOT NULL,             -- ver events.md
  node_id       TEXT,                      -- nulo para eventos do workspace
  session_id    TEXT,
  payload       TEXT NOT NULL              -- JSON, schema por kind
);
CREATE INDEX events_node ON events(node_id, seq);

-- Outbox de efeitos (ADR 0013): gravado na mesma transação dos eventos.
CREATE TABLE effects (
  id            TEXT PRIMARY KEY,          -- ef_<ulid>
  idem_key      TEXT NOT NULL UNIQUE,      -- derivada do evento de origem
  event_seq     INTEGER NOT NULL REFERENCES events(seq),
  kind          TEXT NOT NULL,             -- catálogo abaixo
  payload       TEXT NOT NULL,             -- JSON
  state         TEXT NOT NULL,             -- 'pending' | 'done' | 'failed'
  attempts      INTEGER NOT NULL DEFAULT 0, -- execuções iniciadas (contadas antes de rodar)
  next_at       TEXT,                      -- backoff
  last_error    TEXT,
  created_at    TEXT NOT NULL,
  done_at       TEXT
);
CREATE INDEX effects_pending ON effects(state, next_at);
```

Política do outbox, aplicada pelo supervisor (o store só oferece as operações):
- Um efeito tem até 8 tentativas. Entre elas, o atraso é exponencial a partir de 1 s, dobrando a cada falha, com teto de 5 min. Sem jitter, para manter a execução reproduzível.
- Um erro que o adaptador classifica como permanente (credencial inválida, recurso inexistente) vai direto para `failed`.
- Ao virar `failed`, o supervisor grava `effect.failed` no journal e notifica. O nó ou workspace que dependia do efeito não avança sozinho.
- Um efeito iniciado e não concluído (o processo morreu) continua `pending` e roda de novo; o adaptador deduplica pela `idem_key`.
- A conclusão de um efeito cujo resultado vira entrada do núcleo (sessão, gates) é gravada na mesma transação da decisão: depois dela, a retomada não roda o efeito de novo.
- Sessões têm política própria (ADR 0024, item 7): `CauseRateLimit` e `CauseRuntime` repetem com o mesmo backoff, em até 2 sessões novas na mesma tentativa; esgotadas, a etapa falha.

Catálogo de efeitos (`kind`). Um tipo novo entra aqui no mesmo PR que o cria:

| `kind` | Dono | Observação |
|---|---|---|
| `session.start` | nó | sessão de papel |
| `gate.run` | nó | gates de etapa; com `freshness`, os gates completos de novo no rebase |
| `tracker.set_label` / `tracker.comment` | nó | espelho no tracker |
| `notify` | nó ou workspace | notificação |
| `git.milestone` | workspace | cria ou adota `milestone/<nome>`; falha permanente (branches divergentes) pausa o workspace (state-machine.md) |
| `git.rebase` / `git.merge` | nó | fila de merge (ADR 0009) |
| `scm.open_pr` | workspace | PR `milestone → main`; grava `pr.opened` |
| `cleanup.node` / `cleanup.workspace` | nó / workspace | limpeza por ciclo de vida (ADR 0025), regras abaixo |

Regras da limpeza (ADR 0025):
- Imagens que os gates constroem levam os labels `kluzo.workspace=<id do workspace>` e `kluzo.node=<id do nó>` (o id `nd_…`, não o número da issue). `cleanup.node` remove por esses dois labels; `cleanup.workspace`, só pelo primeiro.
- Docker indisponível é o CLI ausente **ou** o daemon inalcançável (`docker version` sem a parte do servidor). Nos dois casos a limpeza de Docker é pulada com aviso no log e o efeito conclui; o que ficou para trás é achado depois pelo `kluzo clean`, pelos mesmos labels e nomes de projeto. Qualquer outra falha do Docker segue a política do outbox.
- A branch do nó no remoto é apagada por `cleanup.node` quando o nó termina `integrated` (o conteúdo já está na milestone), com a credencial do bot; branch já ausente conta como sucesso. Nó `cancelled` mantém a branch remota, que é a única cópia do trabalho. `cleanup.workspace` apaga as branches remotas de nós que restarem quando o workspace termina `merged`; em `cancelled`, mantém e as lista na notificação.

`gate_runs` após queda do processo: ao projetar um `gate.started`, uma linha `running` anterior do mesmo `(node_id, gate)` vira `interrupted`, com `finished_at` igual ao `ts` do novo evento. Nenhuma linha fica `running` para sempre.

```sql
CREATE TABLE workspace (                   -- uma única linha
  id            TEXT PRIMARY KEY,
  state         TEXT NOT NULL,
  profile       TEXT,
  branch        TEXT NOT NULL,             -- milestone/<nome>
  base_sha      TEXT NOT NULL,             -- ponta da main na criação
  head_sha      TEXT,                      -- ponta atual da branch da milestone
  plan_version  INTEGER,                   -- versão do plano aprovado
  flow_cursor   TEXT,                      -- JSON: posição nos flows upstream/closing
  budget_usd_micros INTEGER,
  cost_usd_micros   INTEGER NOT NULL DEFAULT 0, -- soma dos session.finished do workspace
  updated_at    TEXT NOT NULL
);

CREATE TABLE nodes (
  id            TEXT PRIMARY KEY,          -- nd_<ulid>
  issue_ref     TEXT NOT NULL UNIQUE,      -- '42' (GitHub) ou iid (GitLab)
  title         TEXT NOT NULL,
  state         TEXT NOT NULL,             -- ver state-machine.md
  prev_state    TEXT,                      -- só enquanto o nó está 'blocked'; limpo ao sair
  blocked_reason TEXT,
  areas         TEXT NOT NULL,             -- JSON array de nomes de área
  branch        TEXT,                      -- issue/42-api-agenda
  worktree_path TEXT,
  attempt       INTEGER NOT NULL DEFAULT 0,
  round         INTEGER NOT NULL DEFAULT 0,
  step_index    INTEGER,                   -- etapa atual do flow de nó
  overrides     TEXT,                      -- JSON: modelo/harness por label ou milestone
  budget        TEXT NOT NULL,             -- JSON: {attempts, minutes, usd_micros}
  cost_usd_micros INTEGER NOT NULL DEFAULT 0, -- soma dos session.finished do nó
  critical      INTEGER NOT NULL DEFAULT 0, -- no caminho crítico (recalculado)
  merged_sha    TEXT,
  flow          TEXT,                      -- JSON: progresso no flow de nó (evento node.flow)
  updated_at    TEXT NOT NULL
);

CREATE TABLE edges (
  from_node     TEXT NOT NULL REFERENCES nodes(id),
  to_node       TEXT NOT NULL REFERENCES nodes(id),
  kind          TEXT NOT NULL,             -- 'blocks' | 'contract' | 'conflict'
  source        TEXT NOT NULL,             -- 'tracker' | 'plan' | 'area' | 'diff'
  PRIMARY KEY (from_node, to_node, kind)
);

CREATE TABLE subtasks (
  node_id       TEXT NOT NULL REFERENCES nodes(id),
  idx           INTEGER NOT NULL,
  title         TEXT NOT NULL,
  state         TEXT NOT NULL,             -- 'todo' | 'doing' | 'done'
  PRIMARY KEY (node_id, idx)
);

-- Projeção de session.started (cria ou assume a linha) e session.finished
-- (estado, resultado, custo somado), events.md.
CREATE TABLE sessions (
  id            TEXT PRIMARY KEY,          -- ss_<ulid>
  node_id       TEXT REFERENCES nodes(id), -- nulo em sessões de upstream/closing
  role          TEXT NOT NULL,
  harness       TEXT NOT NULL,             -- 'claude-code' | 'opencode'
  model         TEXT NOT NULL,
  harness_session_ref TEXT,                -- id da sessão no harness, para Resume
  attempt       INTEGER,
  round         INTEGER,
  state         TEXT NOT NULL,             -- 'running' | 'finished' | 'failed' | 'cancelled'
  result        TEXT,                      -- JSON validado (results.md)
  cost_usd_micros INTEGER NOT NULL DEFAULT 0,
  started_at    TEXT NOT NULL,
  finished_at   TEXT
);

CREATE TABLE gate_runs (
  id            TEXT PRIMARY KEY,
  node_id       TEXT REFERENCES nodes(id), -- nulo para gates da milestone (E2E)
  gate          TEXT NOT NULL,             -- nome em kluzo.yaml
  sha           TEXT NOT NULL,
  state         TEXT NOT NULL,             -- 'running' | 'passed' | 'failed' | 'error' | 'interrupted'
  log_ref       TEXT,                      -- arquivo em logs/ ou URL do CI
  started_at    TEXT NOT NULL,
  finished_at   TEXT
);

CREATE TABLE decisions (
  id            TEXT PRIMARY KEY,
  node_id       TEXT REFERENCES nodes(id),
  session_id    TEXT REFERENCES sessions(id),
  author        TEXT NOT NULL,             -- 'agent' | 'human'
  title         TEXT NOT NULL,
  rationale     TEXT NOT NULL,
  ts            TEXT NOT NULL
);

CREATE TABLE questions (
  id            TEXT PRIMARY KEY,
  node_id       TEXT REFERENCES nodes(id),
  session_id    TEXT NOT NULL REFERENCES sessions(id),
  question      TEXT NOT NULL,
  context       TEXT NOT NULL,             -- JSON: motivo, trechos, opções sugeridas
  blocking      INTEGER NOT NULL,
  answer        TEXT,
  answered_by   TEXT,
  asked_at      TEXT NOT NULL,
  answered_at   TEXT
);

CREATE TABLE proposals (
  id            TEXT PRIMARY KEY,
  origin_node   TEXT REFERENCES nodes(id),
  session_id    TEXT REFERENCES sessions(id),
  kind          TEXT NOT NULL,             -- 'new_node' | 'split' | 'edge' | 'change'
  payload       TEXT NOT NULL,             -- JSON
  blocking      INTEGER NOT NULL,
  state         TEXT NOT NULL,             -- 'pending' | 'approved' | 'rejected'
  decided_by    TEXT,
  created_at    TEXT NOT NULL,
  decided_at    TEXT
);
```

## Arquivos do workspace

```
~/.local/share/kluzo/
├── cache/<project>/              # caches de download verificados por hash, compartilhados (ADR 0025)
└── workspaces/<project>-<milestone>/
    ├── state.db
    ├── home/                     # HOME sintético das sessões (ADR 0024)
    ├── cache/                    # caches de build, só deste workspace
    ├── worktrees/<issue>/        # um worktree por nó ativo; removido quando o nó é integrado ou cancelado
    └── logs/
        ├── sessions/<session>.jsonl   # stream bruto do harness
        └── gates/<gate-run>.log
```
