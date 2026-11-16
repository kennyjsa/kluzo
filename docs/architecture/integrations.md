# Integrações

Como o Kluzo fala com o tracker, o SCM, o CI e os harnesses. As interfaces estão em [contracts/interfaces.md](../contracts/interfaces.md).

## Tracker, SCM e CI

| Conceito | GitHub (v0.1) | GitLab (v0.2) |
|---|---|---|
| Milestone | Milestone | Milestone |
| Nó | Issue | Issue / work item |
| Subtask | Checklist na descrição ou sub-issue | Checklist ou task filha |
| Bloqueio | `blocked_by` nativo (API de issue dependencies) | link `blocks` (Premium) ou `Depende de: #n` na descrição (Free) |
| Contrato | `Contrato: #n` na descrição, ou o plano | igual |
| PR/MR | Pull request | Merge request |
| Gates no CI | Check runs / Actions | Pipelines |
| Identidade | GitHub App | Usuário bot com project access token |

### Permissões no GitHub

A credencial do bot (`tracker.auth`) precisa, no repositório do projeto:

| Permissão | Nível | Para quê |
|---|---|---|
| Metadata | leitura | obrigatória |
| Issues | leitura e escrita | issues, comentários, labels `orch/*`, milestones, dependências (`blocked_by`) |
| Contents | leitura e escrita | push da branch da milestone e das branches dos nós; apagar a branch do nó depois da integração |
| Pull requests | leitura e escrita | abrir e editar o PR `milestone → main` (o merge é humano) |
| Commit statuses | leitura | gates `source: tracker` com statuses |
| Checks | leitura | gates `source: tracker` com check runs |

**Checks** só existe para GitHub App: o token fine-grained não oferece essa permissão, e a leitura de check runs devolve 403. Com token, use um token clássico com o escopo `repo`, ou um projeto cujos gates não leiam check runs. O recomendado é o GitHub App (`tracker.auth.kind: github_app`).

As listas de issues e milestones (`MilestoneIssues`, a busca do marcador de idempotência do `CreateIssue`, `MilestonesByTitle`) usam a API GraphQL do GitHub, com as mesmas permissões. As listas da API REST (`GET /repos/{dono}/{repo}/issues` e `/milestones`, com ou sem filtro) ficam cerca de um segundo atrás das escritas: uma issue recém-criada não aparece nelas, e o `CreateIssue` repetido criava outra. A GraphQL e o `GET` de uma issue refletem a escrita na hora.

O GitHub vem antes do GitLab no roadmap; o motivo está no [roadmap](../planning/roadmap.md#por-que-o-github-vem-antes-do-gitlab).

### Sincronização

- **Polling** a cada `poll_interval` (30 s por padrão), usando cursores por milestone (ADR 0015). O laço é do supervisor; o adaptador só expõe leituras por cursor e trata o rate limit no transporte (espera até o reset, respeita `Retry-After`, até 3 repetições, desiste acima de 15 min). Requisições condicionais (ETag) entram na v0.2. Webhooks podem vir depois como otimização, sem mudar o modelo.
- **Leitura:** issues, dependências, labels e comentários novos (respostas a escalações e aprovações).
- **Escrita:** label `orch/*`, comentário por transição, criação de issues aprovadas, branches e o PR/MR `milestone → main`. O grafo em Mermaid na descrição da milestone entra na v0.2. O merge do nó na branch da milestone é local (`gitops`), não pelo tracker.
- **Comentário por transição:** montado por uma função pura do `core` ao emitir o efeito `tracker.comment`, em inglês e curto:

  ```
  **Kluzo** · `running` → `in_review` · implementer delivered (attempt 1, round 0)
  ```

  Com a web (v0.3), ganha um link para a tela do nó. Motivos de bloqueio e falha trazem uma segunda linha com o resumo, sem logs.
- Toda escrita é um **efeito idempotente** do outbox ([overview.md](overview.md#o-laço-do-supervisor)).
- **Limites de taxa:** o adaptador respeita os cabeçalhos de rate limit e espera quando necessário. O núcleo não sabe disso.

### Comandos pelo tracker

Para operar sem a web, alguns comandos funcionam por label ou comentário:

| Ação | Como |
|---|---|
| Aprovar o plano | label `plan/approved` na milestone ou na issue de plano, aplicada por um usuário autorizado |
| Responder escalação | comentário iniciado por `/kluzo answer` na issue |
| Aprovar ou recusar proposta | comentário `/kluzo approve <id>` ou `/kluzo reject <id>` |

Só os usuários listados em `kluzo.yaml` (`tracker.operators`) podem acionar esses comandos.

## Git local

O `gitops` executa o binário `git` (versão 2.38 ou mais nova):
- worktree por nó, nascido da ponta da branch da milestone;
- rebase, `merge --no-ff` com trailers (ADR 0009);
- diff por área (ADR 0011);
- push com as credenciais do bot.

O Kluzo nunca escreve na `main`.

## Harnesses

| Harness | Execução | Retomada | Saída estruturada | Recursos específicos |
|---|---|---|---|---|
| Claude Code | `claude -p --restricted` com stream-json de entrada e saída (ADR 0024) | `--resume <uuid>`, só com segmentos inalterados | `kluzo_result` (MCP) | `--tools`/`--allowedTools` por papel; advisor e subagents depois de verificados |
| OpenCode | `opencode run` ou `opencode serve` (HTTP) | sessão do servidor | `kluzo_result` (MCP) | multi-provedor |

- A versão mínima de cada CLI fica em `kluzo.yaml` e é verificada por `kluzo doctor` e no início de cada sessão.
- O adaptador traduz `tools` do papel para os nomes do harness e injeta o endpoint MCP do Kluzo.
- O stream bruto é guardado em `logs/sessions/`. O que vai para o journal e para a web são os eventos normalizados.

## Mapa de código

O port `CodeMapper` (ADR 0021) embrulha uma ferramenta externa de índice determinístico, chamada pelo `exec` do `platform`. O supervisor mantém um índice por workspace: constrói na ponta da milestone ao iniciar e atualiza depois de cada integração. O agente nunca fala com a ferramenta: as consultas chegam pelas ferramentas `kluzo_code_*` do MCP do Kluzo, e trocar de ferramenta é trocar `code_map.adapter`.

## Notificações

O `Notifier` começa com `log` (só registra) e ganha o `ntfy` na v0.3: um tópico privado, com prioridade alta quando o item bloqueia trabalho, e link direto para a tela.
