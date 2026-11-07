# Stack

Cada peça da stack, com o motivo da escolha. As versões exatas ficam fixadas em `go.mod` e `web/package.json`. Este documento registra a escolha, não o número da versão. Trocar uma peça exige um ADR. Os critérios para aceitar uma dependência estão no ADR 0017.

## Backend (Go)

| Peça | Escolha | Motivo |
|---|---|---|
| Linguagem | Go (versão estável suportada; mínimo em `go.mod`) | ADR 0005. Binário único, concorrência simples, ecossistema das referências (koryph, belay, Pinard) |
| CLI | `github.com/spf13/cobra` | Padrão de fato para CLIs em Go, com completions e ajuda |
| Logs | `log/slog` (stdlib) | Estruturado, sem dependência |
| SQLite | `modernc.org/sqlite` | Sem cgo, então o cross-compile é simples (ADR 0014) |
| Migrations | Embutidas (`embed`) e aplicadas por um runner próprio em `store` | São poucas e só avançam; uma ferramenta externa não compensa |
| YAML | `github.com/goccy/go-yaml` | Mantido, e reporta linha e coluna nos erros (exigência de `config`) |
| JSON Schema | `github.com/santhosh-tekuri/jsonschema/v6` | Draft 2020-12, mensagens de erro com caminho |
| IDs | `github.com/oklog/ulid/v2` | Ordenáveis por tempo, curtos |
| MCP | `github.com/modelcontextprotocol/go-sdk` | SDK oficial |
| GitHub | `github.com/google/go-github` + autenticação de GitHub App (`github.com/bradleyfalzon/ghinstallation`) | Cliente mais completo; o App dá identidade própria ao bot |
| GitLab | `gitlab.com/gitlab-org/api/client-go` | Cliente oficial |
| Tailnet | `tailscale.com/tsnet` | ADR 0006 |
| Git | Binário `git` (2.38+) via `platform/exec` | Fidelidade total ao git do usuário; bibliotecas Go de git não cobrem worktree e rebase bem |
| Comparação em testes | `github.com/google/go-cmp` | Diffs legíveis; sem framework de asserção |
| Lint | `golangci-lint` (v2), configurado em `.golangci.yml` | Agrega vet, staticcheck, errcheck, revive, dupl (código duplicado) e unused (código morto) |

## Frontend (`web/`)

ADR 0016.

| Peça | Escolha | Motivo |
|---|---|---|
| Framework | Vue 3 (Composition API, `<script setup>`) | Mesma família do minvault |
| Linguagem | TypeScript em modo estrito | Os tipos da API são gerados dos contratos |
| Build | Vite | Rápido; o build estático é embutido no binário |
| Roteador | Vue Router | Padrão do ecossistema |
| Estado | Pinia | Oficial; simples para snapshot + eventos |
| Estilo | Tailwind CSS v4, com tokens em `src/design/` | Mobile-first por construção (`md:`, `lg:`); tokens únicos para as cores de estado |
| Grafo | Vue Flow + `elkjs` (layout) | Nós como componentes Vue; ELK faz o layout hierárquico com arestas ortogonais |
| i18n | vue-i18n | `pt-BR` padrão, `en` desde o início (ADR 0008) |
| Testes | Vitest + Vue Test Utils; Playwright para e2e nas larguras 390, 768 e 1280 | Rápidos e com bom suporte a viewport |
| Lint e formatação | ESLint (flat config) + Prettier | Padrão do ecossistema |

## Ferramentas de desenvolvimento

| Peça | Uso |
|---|---|
| `make` | Ponto único de comandos ([development.md](../development.md)) |
| GitHub Actions | CI: `make check`, build multiplataforma e release |
| GoReleaser | Binários de release (Linux e macOS, amd64 e arm64) e checksums |

## Fora da stack, por enquanto

| Peça | Por quê não |
|---|---|
| NATS JetStream | A v1 roda num processo só; distribuir não está no roadmap |
| Temporal | O journal e o outbox resolvem a durabilidade sem um serviço externo (ADR 0013) |
| ORM | O SQL é pequeno e explícito; um ORM esconderia as transações do journal |
| WebSocket | O SSE basta, porque o tempo real só vai do servidor para o cliente (ADR 0015) |
| Framework de DI | A injeção manual basta (ver [patterns.md](patterns.md)) |
