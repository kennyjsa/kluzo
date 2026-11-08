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

// ---------- Files: leitura do worktree para a conferência de evidência ----------

// O núcleo confere a evidência `caminho:linha` dos resultados (ADR 0023, item 5)
// sem fazer I/O: lê o worktree do nó por este port (implementação: gitops.Files).
type Files interface {
	// Número de linhas do arquivo (quebras de linha, mais uma se a última não
	// tiver). ok é false para caminho não normalizado ("./a.go"), absoluto,
	// fora do worktree, inexistente, ilegível ou que não seja arquivo regular
	// (inclusive FIFO, sem bloquear); err é falha de leitura do worktree.
	Lines(path string) (n int, ok bool, err error)
}

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
	SessionID string // ss_<ulid> do Kluzo
	Counter   int    // numera as sessões do harness de um mesmo SessionID (0, 1…); entra no UUID
	Attempt, Round int
	Role      RoleConfig // já com a precedência de overrides aplicada
	System    string     // system prompt: segmentos 1, 2 e 2b (protocolo, papel, regras do projeto); vai por arquivo 0600, sem limite de tamanho
	Prompt    string     // segmento 3, a primeira mensagem do usuário
	Segments  SegmentHashes // hashes que a retomada compara (ADR 0024, item 9)
	Workdir   string     // worktree do nó, ou o repositório no upstream
	Verify    []string   // comandos dos gates que a ferramenta `test` pode rodar

	MCPEndpoint string // servidor MCP do Kluzo para esta sessão
	MCPToken    string // token da sessão
	MCPTools    []string
	Accepted    func() (json.RawMessage, bool) // o kluzo_result aceito pelo servidor MCP

	Env                map[string]string // sem segredos do Kluzo; só os do projeto (env_allow, caches)
	MaxBudgetUSDMicros int64             // o que resta ao nó
	PriorUsage         Usage             // Result.Cumulative da sessão anterior da mesma conversa do harness (Resume); zero no Start
	Timeout            time.Duration
}

// Usage é o que uma conversa do harness consumiu. O Claude Code soma custo e
// tokens entre retomadas; o Result conta só a sessão.
type Usage struct {
	InputTokens, OutputTokens int64
	CostUSD                   int64 // micro-dólares
}

type Session interface {
	HarnessRef() string               // id da sessão no harness, para Resume
	Events() <-chan Event             // eventos normalizados (events.md); fecha ao terminar; o chamador precisa continuar lendo
	Wait() (Result, error)            // bloqueia até o fim
	Send(ctx context.Context, msg string) error // entrevistas e respostas a escalações
	Cancel() error                    // mata o grupo de processos; pode ser chamado duas vezes
}

type Event struct {
	Kind    string          // events.md
	Payload json.RawMessage // o JSON do tipo de internal/contracts
}

type Result struct {
	State   string          // "finished" | "failed" | "cancelled" | "timeout"
	Cause   Cause           // se failed: max_turns, budget, auth, rate_limit, runtime, refusal, no_result, guard (ADR 0024, item 7)
	Detail  string
	Raw     json.RawMessage // o kluzo_result aceito, antes da validação de cobertura do núcleo
	CostUSD int64           // micro-dólares desta sessão: o total do harness menos SessionSpec.PriorUsage
	Cumulative Usage        // o total que o harness informou, para o PriorUsage da próxima retomada
	Turns   int
	Unknown map[string]int  // tipos de evento do stream que o adaptador não conhece
	Version string          // versão do harness vista no início
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

`Resume` falha com `ErrResumeMismatch` quando os segmentos, o diretório, o `HOME` ou a linha de comando mudaram; o chamador então abre uma sessão nova com o `Counter` seguinte.

## Implementações previstas

| Interface | v0.1 | v0.2 | v0.3 |
|---|---|---|---|
| `Tracker`, `SCM`, `CI` | `github` | `gitlab` | |
| `Runtime` | `claudecode` | `opencode` | |
| `Notifier` | `log` (no-op) | | `ntfy` |

Todas as interfaces têm um **fake em memória** em `internal/ports/fakes`, com o comportamento do contrato (idempotência, uma label `orch/*` por issue). Os testes do núcleo usam só os fakes ([development.md](../development.md#testes)).
