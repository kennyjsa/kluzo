# Roadmap

A sequência de entregas do Kluzo. Este arquivo é o **único lugar** onde a ordem das entregas é definida:
- o detalhe de cada entrega fica em [`milestones/`](milestones/);
- o comportamento de cada feature fica em [`features/`](../product/features/).

| Entrega | Objetivo | Marco que destrava | Status |
|---|---|---|---|
| [v0.1 · Núcleo](milestones/v0.1.md) | Executar uma milestone do GitHub de ponta a ponta, em sequência, com Claude Code, pela CLI. | **O Kluzo passa a orquestrar o próprio desenvolvimento**: a v0.2 já é executada por ele. | planejada |
| [v0.2 · Paralelo e GitLab](milestones/v0.2.md) | Paralelismo seguro, fila de merge, triagem, MCP, OpenCode e GitLab. | Uso real nos projetos em GitLab (Agiclin, Payment.hub). | planejada |
| [v0.3 · Web e upstream](milestones/v0.3.md) | Web responsiva mobile-first, operada pela tailnet; upstream completo com entrevistas e aprovação do plano. | Operar tudo pelo celular; o humano atua só no upstream. | planejada |
| [v0.4 · Fechamento](milestones/v0.4.md) | Fechamento com relatório, perfis, esteira de workspaces e telas complementares. | Ciclo completo sem CLI: da demanda ao merge na `main`. | planejada |

## Features por entrega

Uma feature pode ser entregue em partes; a tabela mostra o que entra em cada versão.

| Feature | v0.1 | v0.2 | v0.3 | v0.4 |
|---|---|---|---|---|
| F01 Projetos | um projeto, via CLI | | web | |
| F02 Workspaces | um por vez, via CLI | | web | esteira (vários em paralelo) |
| F03 Inbox | | | ● | |
| F04 Configuração `.kluzo/` | ● | | | tela de config |
| F05 Times por flow | | | ● | |
| F06 Perfis | | | | ● |
| F07 Entrevistas | | | ● | |
| F08 Planejamento e crítica | | | ● | tela de rodadas |
| F09 Aprovação do plano | plano manual, aprovado via CLI | | ● | |
| F10 Grafo e scheduler | bloqueio, sequencial | paralelo, contrato, conflito | | |
| F11 Execução de nó | ● | | | |
| F12 Gates | ● | | | aba no nó |
| F13 Fila de merge | merge direto após gates | ● | | |
| F14 Triagem e orçamento | | ● | | aba no nó |
| F15 Propostas de lacuna | | via CLI e tracker | web | |
| F16 Escalações e decisões | | via CLI e tracker | web | |
| F17 Visão Grafo | | | ● | |
| F18 Visão Kanban | | | ● | |
| F19 Execução ao vivo | | | ● | diff |
| F20 Times em execução | | | ● | |
| F21 Custos e métricas | registro | | ● | |
| F22 Fechamento | PR milestone→main sem relatório | | | ● |
| F23 Tracker | GitHub | GitLab | | |
| F24 Runtimes | Claude Code | OpenCode | | |
| F25 MCP do Kluzo | só `kluzo_result` | completo | | |
| F26 Acesso e permissões | | | ● | tela de ajustes |
| F27 Notificações | | | ● | tela de ajustes |
| F28 Journal e retomada | ● | | | |
| F29 CLI | ● | | | `doctor` na web |
| F30 Visão Estrutura | | | ● | |
| F31 Mapa de código | | `kluzo_changes` | `CodeMapper` + 1 adaptador | |

## Por que o GitHub vem antes do GitLab

O repositório do Kluzo está no GitHub. Com o adaptador do GitHub na v0.1, o próprio Kluzo orquestra o seu desenvolvimento a partir da v0.2. É o teste mais honesto da ferramenta. A API de dependências do GitHub (`blocked_by`) é nativa e não exige plano pago; no GitLab Free, as dependências vão na descrição da issue.

## Como as milestones viram issues

Cada item em `milestones/vX.md` já está no formato de issue: título, features, área, dependências e critérios de aceite. A partir da v0.1, os itens da próxima entrega viram issues numa milestone do GitHub, e o Kluzo executa a milestone.
