# Features

O que o Kluzo faz, uma feature por arquivo. A ordem de entrega fica no [roadmap](../../planning/roadmap.md). Cada feature tem:
- **O quê:** o que ela faz.
- **Comportamento:** as regras e o fluxo.
- **Telas:** onde aparece na web.
- **Aceite:** os critérios para considerar a feature pronta.
- **Versão:** em qual versão do roadmap ela entra (pode ser em partes).
- **Status:** `planejada`, `em andamento` ou `entregue`.

Referências: [conceitos](../concepts.md), [ciclo de vida](../lifecycle.md), [interfaces](../interfaces.md) e o [protótipo](../../design/prototype/).

## Critério comum

Toda feature com tela precisa funcionar em 390, 768 e 1280 px de largura, sem rolagem horizontal da página. O layout e o mapa de navegação estão em [interfaces.md](../interfaces.md).

## Catálogo

| ID | Feature | Grupo | Versão | Status |
|---|---|---|---|---|
| F01 | [Projetos](F01-projetos.md) | Navegação e organização | v0.3 (na v0.1, um projeto via CLI) | planejada |
| F02 | [Workspaces](F02-workspaces.md) | Navegação e organização | v0.1 | planejada |
| F03 | [Inbox](F03-inbox.md) | Navegação e organização | v0.3 | planejada |
| F04 | [Configuração `.kluzo/`](F04-configuracao-kluzo.md) | Configuração | v0.1 | planejada |
| F05 | [Times por flow](F05-times-por-flow.md) | Configuração | v0.3 | planejada |
| F06 | [Perfis](F06-perfis.md) | Configuração | v0.4 | planejada |
| F07 | [Entrevistas](F07-entrevistas.md) | Upstream | v0.3 | planejada |
| F08 | [Planejamento e crítica](F08-planejamento-e-critica.md) | Upstream | v0.3 | planejada |
| F09 | [Revisão e aprovação do plano](F09-revisao-e-aprovacao-do-plano.md) | Upstream | v0.3 (na v0.1, o plano é escrito à mão e aprovado via CLI) | planejada |
| F10 | [Grafo e scheduler](F10-grafo-e-scheduler.md) | Downstream | v0.1 (só bloqueio e execução sequencial); v0.2 (paralelismo, contrato e conflito) | planejada |
| F11 | [Execução de nó](F11-execucao-de-no.md) | Downstream | v0.1 | planejada |
| F12 | [Gates](F12-gates.md) | Downstream | v0.1 | planejada |
| F13 | [Fila de merge](F13-fila-de-merge.md) | Downstream | v0.2 (na v0.1, merge direto após os gates, porque a execução é sequencial) | planejada |
| F14 | [Triagem, orçamento e fallback](F14-triagem-orcamento-e-fallback.md) | Downstream | v0.2 | planejada |
| F15 | [Propostas de lacuna](F15-propostas-de-lacuna.md) | Downstream | v0.2 | planejada |
| F16 | [Escalações e decisões](F16-escalacoes-e-decisoes.md) | Downstream | v0.2 | planejada |
| F17 | [Visão Grafo](F17-visao-grafo.md) | Visualização do progresso | v0.3 | planejada |
| F18 | [Visão Kanban](F18-visao-kanban.md) | Visualização do progresso | v0.3 | planejada |
| F19 | [Execução ao vivo do nó](F19-execucao-ao-vivo-do-no.md) | Visualização do progresso | v0.3 | planejada |
| F20 | [Times em execução](F20-times-em-execucao.md) | Visualização do progresso | v0.3 | planejada |
| F21 | [Custos e métricas](F21-custos-e-metricas.md) | Visualização do progresso | v0.3 | planejada |
| F22 | [Fechamento da milestone](F22-fechamento-da-milestone.md) | Fechamento | v0.4 (na v0.1, só o MR sem relatório) | planejada |
| F23 | [Integração com o tracker](F23-integracao-com-o-tracker.md) | Plataforma | GitHub v0.1; GitLab v0.2 | planejada |
| F24 | [Runtimes](F24-runtimes.md) | Plataforma | Claude Code v0.1; OpenCode v0.2 | planejada |
| F25 | [MCP do Kluzo](F25-mcp-do-kluzo.md) | Plataforma | v0.2 (na v0.1, o resultado vem pela saída estruturada do harness) | planejada |
| F26 | [Acesso e permissões](F26-acesso-e-permissoes.md) | Plataforma | v0.3 | planejada |
| F27 | [Notificações](F27-notificacoes.md) | Plataforma | v0.3 | planejada |
| F28 | [Journal e retomada](F28-journal-e-retomada.md) | Plataforma | v0.1 | planejada |
| F29 | [CLI](F29-cli.md) | Plataforma | v0.1 | planejada |
| F30 | [Visão Estrutura](F30-visao-estrutura.md) | Visualização do progresso | v0.3 | planejada |
