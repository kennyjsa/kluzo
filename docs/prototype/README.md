# Protótipo mobile — v0

Protótipo navegável da interface web do Kluzo. Usa dados fictícios de uma milestone do Agiclin. Esta versão cobre a faixa **celular** (390×844), o ponto de partida do layout responsivo; as faixas tablet e desktop estão descritas na [SPEC §10](../SPEC.md).

- **Canvas interativo:** https://claude.ai/artifact/4YPfkXrqkA8ZSgtmUcXvgN (privado; o dono pode compartilhar)
- **Fontes:** `screens/*.dc.html`, uma tela por arquivo, e `canvas.json`, com a disposição das telas no canvas. São referência de layout, conteúdo e estados, não código de produção: a implementação será em Vue (ver [SPEC §10](../SPEC.md)).

## Telas

| Tela | Arquivo | Features |
|---|---|---|
| Inbox | `Main.dc.html` | F03 |
| Projetos | `Projetos.dc.html` | F01 |
| Projeto (workspaces, times) | `Projeto.dc.html` | F01, F02, F05 |
| Ativos | `Ativos.dc.html` | F02 |
| Novo workspace | `NovoWorkspace.dc.html` | F02, F04, F06 |
| Workspace · visão geral | `Workspace.dc.html` | F02, F21 |
| Progresso · Grafo | `Grafo.dc.html` | F17 |
| Progresso · Kanban | `Kanban.dc.html` | F18 |
| Progresso · Estrutura (estilo MS Project) | `Estrutura.dc.html` | F30 |
| Card de nó · estados e zoom | `NoCard.dc.html` | F17 |
| Nó · execução ao vivo | `No.dc.html` | F11, F19 |
| Times por flow | `Times.dc.html` | F05, F20 |
| Plano · revisão e aprovação | `Plano.dc.html` | F09 |
| Entrevista · PO | `Entrevista.dc.html` | F07 |
| Escalação | `Escalacao.dc.html` | F16 |
| Fila de merge | `Fila.dc.html` | F13 |
| Decisões | `Decisoes.dc.html` | F16 |
| Custos | `Custos.dc.html` | F21 |
| Fechamento | `Fechamento.dc.html` | F22 |

## Decisões de design

- **Tema escuro.** Teal (`#5EEAD4`) é a cor de ação. Os estados diferem também em claridade, não só em cor:

  | Estado | Cor |
  |---|---|
  | Executando | azul |
  | Review / gates | violeta |
  | Fila de merge | âmbar |
  | Integrado | verde |
  | Bloqueado | laranja |
  | Aguarda | cinza tracejado |

- **Card de nó em quatro faixas:** identidade e estado → título → papel, harness e tentativa → progresso.
- **Três visões de progresso** sobre o mesmo estado: Grafo (dependências), Kanban (estado) e Estrutura (hierarquia e tempo). O kanban é somente leitura, porque quem muda o estado é o Kluzo.
- **Zoom semântico no grafo:** geral (só cor), médio (número e estado), detalhe (card completo).
- **Barra inferior:** Inbox · Projetos · Ativos · Mais. Dentro de um workspace, abas no topo.
- **Tipografia:** Inter na interface; JetBrains Mono em ids, branches e streams.

## Pendente no protótipo

- Configuração do projeto (F04).
- Mais/Ajustes: notificações (F27), acesso pela tailnet (F26) e `doctor` (F29).
- Aba de gates e tentativas no nó (F12/F14).
- Diff do nó.
- Rodadas planner ⇄ critic (F08).
