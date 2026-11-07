# Modelo de dados

Cada workspace tem o seu próprio banco SQLite em `~/.local/share/kluzo/workspaces/<project>-<milestone>/state.db`. Um banco global, `~/.local/share/kluzo/kluzo.db`, guarda só o registro de projetos e workspaces.

Configuração do SQLite: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`. As migrations ficam em `internal/store/migrations/`, numeradas e sem volta (só para frente).

## Regras

- **O journal (`events`) é a fonte da verdade.** As tabelas de estado (`nodes`, `sessions`…) são projeções mantidas na mesma transação que grava o evento. Reconstruir as projeções a partir do journal deve dar o mesmo resultado; existe um teste para isso.
- Toda escrita de estado passa por `store.Tx`, que grava o evento e atualiza as projeções juntos.
- Nada é apagado. Cancelar ou arquivar é mudança de estado.

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
  cost_usd_micros   INTEGER NOT NULL DEFAULT 0,
  updated_at    TEXT NOT NULL
);

CREATE TABLE nodes (
  id            TEXT PRIMARY KEY,          -- nd_<ulid>
  issue_ref     TEXT NOT NULL UNIQUE,      -- '42' (GitHub) ou iid (GitLab)
  title         TEXT NOT NULL,
  state         TEXT NOT NULL,             -- ver state-machine.md
  prev_state    TEXT,                      -- para voltar de 'blocked'
  blocked_reason TEXT,
  areas         TEXT NOT NULL,             -- JSON array de nomes de área
  branch        TEXT,                      -- issue/42-api-agenda
  worktree_path TEXT,
  attempt       INTEGER NOT NULL DEFAULT 0,
  round         INTEGER NOT NULL DEFAULT 0,
  step_index    INTEGER,                   -- etapa atual do flow de nó
  overrides     TEXT,                      -- JSON: modelo/harness por label ou milestone
  budget        TEXT NOT NULL,             -- JSON: {attempts, minutes, usd_micros}
  cost_usd_micros INTEGER NOT NULL DEFAULT 0,
  critical      INTEGER NOT NULL DEFAULT 0, -- no caminho crítico (recalculado)
  merged_sha    TEXT,
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
  state         TEXT NOT NULL,             -- 'running' | 'passed' | 'failed' | 'error'
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
~/.local/share/kluzo/workspaces/<project>-<milestone>/
├── state.db
├── worktrees/<issue>/        # um worktree por nó ativo, removido após a integração
└── logs/
    ├── sessions/<session>.jsonl   # stream bruto do harness
    └── gates/<gate-run>.log
```
