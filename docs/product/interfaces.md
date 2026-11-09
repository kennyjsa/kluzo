# Interfaces

O Kluzo tem quatro superfícies. Todas usam a mesma camada de comandos (`internal/app`), então qualquer ação feita numa delas tem o mesmo efeito nas outras.

| Superfície | Papel | Versão |
|---|---|---|
| **CLI** `kluzo` | Base de tudo; automação e scripts | v0.1 |
| **Tracker** (GitHub, GitLab) | Visibilidade e aprovação pelo app do tracker: labels, comentários, grafo em Mermaid | v0.1 |
| **Web** | Interface principal de operação, responsiva e mobile-first | v0.3 |
| **Notificações** (ntfy) | Avisar quando algo precisa do humano | v0.3 |

## Web

A web é servida pelo próprio binário e acessada pela tailnet do usuário (ADR 0006): HTTPS da tailnet e identidade pelo Tailscale, sem porta pública e sem login próprio. Os detalhes de segurança estão em [architecture/security.md](../architecture/security.md).

### Layout responsivo, mobile-first

O layout é desenhado primeiro para o celular e se expande em três faixas.

| Faixa | Largura | Navegação | Conteúdo |
|---|---|---|---|
| Celular | < 768 px | barra inferior: **Inbox · Projetos · Ativos · Mais** | uma coluna; o detalhe abre em tela própria ou num painel inferior |
| Tablet | 768–1199 px | trilho lateral | lista e detalhe lado a lado |
| Desktop | ≥ 1200 px | barra lateral fixa | a visão de progresso ocupa a área principal; o detalhe do nó abre num painel à direita; o kanban mostra todas as colunas |

**Critério comum a todas as telas:** funcionar em 390, 768 e 1280 px de largura, sem rolagem horizontal da página. A rolagem horizontal só é permitida dentro de áreas próprias, como a linha do tempo da Estrutura.

### Mapa de navegação

```
Inbox ◄──────── atalho global: o que precisa do humano
Ativos ◄─────── workspaces em andamento de todos os projetos
Projetos
 └── Projeto
      ├── Workspaces ──► Workspace          (novo workspace a partir de uma milestone)
      │                   ├── Visão geral
      │                   ├── Progresso: [Grafo | Kanban | Estrutura]
      │                   ├── Plano
      │                   ├── Times (por flow, em execução)
      │                   ├── Fila de merge
      │                   ├── Decisões
      │                   ├── Custos
      │                   └── Nó ──► Execução ao vivo
      ├── Times (por flow, configurados)
      └── Configuração
```

Dentro de um workspace, as seções são abas: roláveis no topo no celular, fixas no desktop.

### Três visões de progresso

As três mostram o mesmo estado, atualizado em tempo real (SSE):
- **Grafo** ([F17](features/F17-visao-grafo.md)): dependências, caminho crítico e zoom semântico.
- **Kanban** ([F18](features/F18-visao-kanban.md)): colunas por estado; somente leitura, porque quem muda o estado é o Kluzo.
- **Estrutura** ([F30](features/F30-visao-estrutura.md)): hierarquia milestone → nós → subtasks, com predecessores e linha do tempo, no estilo do MS Project.

A referência visual está no [protótipo](../design/prototype/), e o sistema visual (cores de estado, card de nó, tipografia) em [design/](../design/).

## CLI

```
kluzo init                       gera .kluzo/ com papéis e flows padrão
kluzo doctor                     verifica CLIs, tokens e configuração
kluzo plan <milestone>           cria o workspace a partir de um plano
kluzo approve <milestone>        aprova o plano (ou uma proposta)
kluzo run <milestone>            executa (ou retoma) o downstream
kluzo status [<milestone>]       estado da milestone e dos nós
kluzo pause|resume <milestone>   pausa ou retoma
kluzo answer <question-id>       responde uma escalação
kluzo serve                      sobe a web e o supervisor (v0.3)
```

`<milestone>` é sempre o **título** da milestone no tracker, o mesmo do campo `milestone` do plano e do nome do workspace. O `kluzo plan` lê `.kluzo/plans/<milestone>.yaml` e acha a milestone no tracker pelo título; título ausente ou repetido é erro de configuração (saída 2), e `--ref <número>` desfaz a ambiguidade. Sem o registro global (v0.2), `<milestone>` é obrigatório também no `status`.

O detalhamento de cada comando está em [F29](features/F29-cli.md).

## Tracker

- **Uma label `orch/*` por issue**, indicando o estado do nó ([contracts/state-machine.md](../contracts/state-machine.md#labels-no-tracker)).
- **Um comentário curto por transição**, com o estado novo e o motivo.
- **O grafo em Mermaid** num comentário da milestone, atualizado a cada transição.
- **Aprovação pelo tracker:** a label `plan/approved` aplicada pelo humano equivale ao `kluzo approve`.

## Notificações

Só quando a ação humana é necessária:
- plano para aprovar;
- proposta de nó;
- escalação;
- entrevista aguardando resposta;
- milestone pronta para merge;
- orçamento estourado.

Rajadas são agrupadas, e cada notificação traz o link direto para a tela correspondente.
