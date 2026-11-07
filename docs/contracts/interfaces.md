# Interfaces Go

Os ports que o núcleo usa. O núcleo depende só destas interfaces, nunca de um adaptador concreto ([packages.md](../architecture/packages.md)). Os nomes e assinaturas abaixo são o contrato. Os detalhes de implementação ficam livres.

Todas as chamadas recebem `context.Context`, e todas as operações de escrita são **idempotentes**: repetir a chamada depois de uma falha não duplica efeito. Para isso, cada operação carrega uma chave de idempotência derivada do evento que a originou.

```go
package ports

// ---------- Tracker: issues, milestones, labels, comentários ----------

type Tracker interface {
	Milestone(ctx context.Context, ref string) (Milestone, error)
	MilestoneIssues(ctx context.Context, ref string) ([]Issue, error)
	Issue(ctx context.Context, ref string) (Issue, error)

	// Dependências nativas do tracker (GitHub blocked_by, GitLab blocks)
	// mais as linhas "Depende de:"/"Contrato:" da descrição.
	Dependencies(ctx context.Context, issueRef string) ([]Dependency, error)
	SetDependencies(ctx context.Context, issueRef string, deps []Dependency) error

	CreateIssue(ctx context.Context, in NewIssue, idemKey string) (Issue, error)

	// Substitui a label orch/* atual pela nova (no máximo uma orch/* por issue).
	SetStateLabel(ctx context.Context, issueRef string, label string) error
	AddLabels(ctx context.Context, issueRef string, labels []string) error

	Comment(ctx context.Context, issueRef string, body string, idemKey string) (CommentRef, error)
	// Comentários novos desde o cursor; usado para respostas a escalações e aprovações.
	CommentsSince(ctx context.Context, issueRef string, cursor string) ([]Comment, string, error)

	// Eventos de label/estado desde o cursor (polling).
	Changes(ctx context.Context, milestoneRef string, cursor string) ([]Change, string, error)
}

type Issue struct {
	Ref, Title, Body, State string // State: "open" | "closed"
	Labels                  []string
	MilestoneRef            string
	URL                     string
}

type Dependency struct {
	FromRef, ToRef string
	Kind           EdgeKind // EdgeBlocks | EdgeContract
	Source         string   // "native" | "description"
}

// ---------- SCM: branches, PRs/MRs, merge ----------

type SCM interface {
	EnsureBranch(ctx context.Context, name, fromRef string) (sha string, err error)
	BranchHead(ctx context.Context, name string) (sha string, err error)

	OpenPR(ctx context.Context, in NewPR, idemKey string) (PR, error)
	UpdatePR(ctx context.Context, ref string, body string) error
	PR(ctx context.Context, ref string) (PR, error)

	// Merge da branch da milestone na main: só verifica se aconteceu; nunca executa.
	IsMerged(ctx context.Context, prRef string) (bool, error)
}

// Operações locais de git (worktree, rebase, merge --no-ff na milestone) ficam
// no pacote internal/gitops, sobre o binário git: não dependem do tracker.

// ---------- CI: checks e pipelines ----------

type CI interface {
	// Estado dos checks exigidos para um commit.
	Checks(ctx context.Context, sha string, required []string) ([]CheckRun, error)
	LogURL(ctx context.Context, check CheckRun) (string, error)
}

type CheckRun struct {
	Name  string
	State string // "pending" | "passed" | "failed" | "error"
	URL   string
}

// ---------- Runtime: harnesses de agente ----------

type Runtime interface {
	Name() string // "claude-code" | "opencode"
	// Verifica binário, versão mínima e autenticação.
	Check(ctx context.Context) (RuntimeInfo, error)
	Start(ctx context.Context, spec SessionSpec) (Session, error)
	Resume(ctx context.Context, harnessRef string, spec SessionSpec) (Session, error)
}

type SessionSpec struct {
	SessionID   string            // ss_<ulid> do Kluzo
	Role        RoleConfig        // já com a precedência de overrides aplicada
	Prompt      string            // prompt do papel + contexto montado pelo núcleo
	Workdir     string            // worktree do nó, ou o repositório no upstream
	MCPEndpoint string            // servidor MCP do Kluzo para esta sessão
	Env         map[string]string // sem segredos do Kluzo; só os do projeto
	OutputSchema []byte           // JSON Schema da saída esperada
	Timeout     time.Duration
}

type Session interface {
	HarnessRef() string               // id da sessão no harness, para Resume
	Events() <-chan Event             // eventos normalizados (events.md); fecha ao terminar
	Wait() (Result, error)            // bloqueia até o fim; Result.Raw validado pelo núcleo
	Send(ctx context.Context, msg string) error // entrevistas e respostas a escalações
	Cancel() error                    // mata o grupo de processos
}

type Result struct {
	State   string          // "finished" | "failed" | "cancelled" | "timeout"
	Raw     json.RawMessage // saída estruturada, antes da validação
	CostUSD int64           // micro-dólares
}

// ---------- Notifier ----------

type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

type Notification struct {
	Title, Body, URL string
	Priority         string // "low" | "default" | "high" (bloqueia trabalho)
	Group            string // agrupa rajadas
}
```

## Implementações previstas

| Interface | v0.1 | v0.2 | v0.3 |
|---|---|---|---|
| `Tracker`, `SCM`, `CI` | `github` | `gitlab` | |
| `Runtime` | `claudecode` | `opencode` | |
| `Notifier` | `log` (no-op) | | `ntfy` |

Todas as interfaces têm um **fake em memória** em `internal/ports/fakes`, com o comportamento do contrato (idempotência, uma label `orch/*` por issue). Os testes do núcleo usam só os fakes ([development.md](../development.md#testes)).
