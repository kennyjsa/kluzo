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

	// Login com que o Kluzo age no tracker (bot ou usuário do token). Usado
	// para reverter labels orch/* aplicadas por outros (security.md).
	Whoami(ctx context.Context) (string, error)
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

// Semântica (decidida no item 4 da v0.1):
// - Referências (milestone, issue, PR) são os números do tracker em decimal ("42"),
//   opacos para o núcleo.
// - Dependencies devolve só as dependências de entrada (ToRef == issueRef),
//   nativas primeiro, depois as da descrição. A mesma aresta nos dois lugares vale
//   como nativa.
// - Sintaxe na descrição: uma linha que começa por "Depende de:" ou "Contrato:"
//   (sem diferenciar maiúsculas), com referências #n em qualquer quantidade.
//   SetDependencies grava os bloqueios como links nativos e reescreve uma única
//   linha "Contrato: #a, #b" (ou a remove). "Depende de:" só é lido. No GitLab
//   Free, sem link nativo, os bloqueios também vão para uma linha "Depende de:"
//   reescrita pelo Kluzo.
// - Cursores: o de CommentsSince é o id do último comentário visto; o de Changes,
//   o id do último evento. Cursor vazio em Changes não devolve mudanças e posiciona
//   no presente.
// - Change cobre labeled e unlabeled, com o autor (Actor).
// - EdgeKind é um só tipo, em internal/contracts, usado por core e ports.
// - Erros tipados: ErrNotFound, ErrInvalid, ErrPermanent e IsPermanent, para o
//   executor do outbox decidir entre repetir e falhar (data-model.md).
// - Uma escrita com idemKey grava a chave num marcador oculto no fim do corpo
//   (<!-- kluzo:idem=KEY -->), removido na leitura.

// ---------- SCM: branches e PRs/MRs ----------

type SCM interface {
	EnsureBranch(ctx context.Context, name, fromRef string) (sha string, err error)
	BranchHead(ctx context.Context, name string) (sha string, err error)

	OpenPR(ctx context.Context, in NewPR, idemKey string) (PR, error)
	UpdatePR(ctx context.Context, ref string, body string) error
	PR(ctx context.Context, ref string) (PR, error)

	// Só observa. O merge do nó na branch da milestone é local (gitops), e o merge
	// da milestone na main é humano (ADR 0003): nenhum port executa merge.
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
