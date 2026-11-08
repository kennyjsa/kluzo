# Pilotos

Projetos reais usados para validar o Kluzo, além do dogfooding (item 11 da v0.1).

## Travel Agency Workspace (TAW)

Repositório `kennyjsa/travel-agency-workspace`: backoffice da ViaJosy, Go + Nuxt, single-tenant. Avaliado em 08/11/2026 na ponta de `milestone/E1.2`.

### Por que serve

- **Mesmo modelo de branches:** as fatias já viram milestones (`E1.1`, `E1.2`…) com branches `milestone/<fatia>` e merge humano na `main`.
- **Gates prontos:** `make lint`, `make test` e `make build`, mais CI no GitHub Actions (`ci.yml`, workflows do Chatwoot).
- **Regras do projeto no padrão que o Kluzo injeta:** `AGENTS.md`, `CLAUDE.md`, `DESIGN.md`, `.agents/adr`, `.agents/skills`.
- **Backlog com rastreio:** `docs/entregas/` é a fonte da verdade; funcionalidades (`F<n>.<m>`) são issues pai, histórias (`US-<n>.<m>.<k>`) são sub-issues com critérios.
- **Hoje orquestrado pelo Orca** (`orca.yaml`, skill `pipeline-issue`): rodar uma fatia com o Kluzo é a comparação direta.

### O que o TAW exigiu do Kluzo (incorporado aos contratos)

| Necessidade | Onde está no TAW | Decisão |
|---|---|---|
| Preparar cada worktree (dependências, pilha Docker isolada, `.env` de desenvolvimento) | `tools/agentes/preparar-worktree.sh`, `.worktreeinclude`, `make dev:env` | `worktree.setup` e `worktree.include` no `kluzo.yaml`, executados pelo Kluzo antes da sessão ([config.md](../contracts/config.md)) |
| Não deixar o agente ler `.env` | `permissions.deny` em `.claude/settings.json`, ignorado no modo `--restricted` (ADR 0024) | `secret_paths` no `kluzo.yaml`: o hook do Kluzo nega leitura por ferramenta de arquivo; leitura por shell é limite conhecido |
| Pilha Docker por worktree com nome de projeto próprio (`COMPOSE_PROJECT_NAME` gerado em `.env.dev`) | `Makefile`, US-1.0.14 | A limpeza do ADR 0025 identifica os recursos do compose pelo diretório de trabalho (`com.docker.compose.project.working_dir` dentro do workspace), não pelo nome do projeto |
| Issues pai (funcionalidades) com sub-issues (histórias) na mesma milestone | `docs/entregas/`, skill `backlog-sync` | v0.1: as issues pai entram no plano com `excluded: true` e só as histórias viram nós. Agrupamento nativo por sub-issue fica para a visão Estrutura (F30, v0.3) |

### Plano do piloto

1. Pré-requisito: v0.1 com os itens 1 a 10 aceitos e o teste de integração no sandbox verde.
2. Escolher a próxima fatia ainda não iniciada (3 a 6 histórias), escrever `.kluzo/` e o plano à mão a partir de `docs/entregas/`.
3. Rodar com o Kluzo no lugar do Orca nessa fatia; o refinamento continua no Notion e em `docs/entregas/`.
4. Medir contra as fatias feitas com o Orca: rodadas de review, intervenções humanas, custo, tempo até o PR da milestone, defeitos encontrados depois do merge.
5. Registrar o resultado aqui e as lições como propostas (ADR 0022).
