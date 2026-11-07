# Design

O sistema visual da web do Kluzo. A referência navegável está no [protótipo](prototype/). Os tokens daqui são a fonte de `web/src/design/` na implementação.

## Princípios

1. **Mobile-first e responsivo:** desenha-se primeiro para 390 px e depois expande-se para 768 e 1280 px ([interfaces](../product/interfaces.md#layout-responsivo-mobile-first)).
2. **Estado legível de relance:** cor, símbolo e texto juntos. A cor nunca é a única pista.
3. **A interface reflete o Kluzo, não o controla diretamente:** visões somente leitura (kanban sem arrastar). As ações são explícitas e passam pelos comandos.
4. **Densidade técnica com calma:** tema escuro, fundo neutro, uma cor de ação.

## Tokens

### Cores de base

| Token | Valor | Uso |
|---|---|---|
| `bg` | `#0E1014` | Fundo da página |
| `surface` | `#161A21` | Cards e painéis |
| `surface-2` | `#1D222B` | Botões secundários, trilhas |
| `border` | `#2A313D` | Bordas |
| `text` | `#E7EAF0` | Texto principal |
| `text-muted` | `#A3ACBA` | Texto secundário (contraste AA sobre `bg` e `surface`) |
| `action` | `#5EEAD4` | Botões primários, links, caminho crítico |
| `action-ink` | `#062F2A` | Texto sobre `action` |

### Cores de estado do nó

| Estado | Símbolo | Borda / destaque | Fundo do card | Texto da tarja |
|---|---|---|---|---|
| `planned` (aguarda) | ◌ | `#3A4250` tracejada | `#12151B` | `#A3ACBA` |
| `ready` (pronto) | ○ | `#CBD5E1` | `#1D222B` | `#E7EAF0` |
| `running` (executando) | ● | `#60A5FA` | `#12203A` | `#93C5FD` |
| `in_review` / `gating` | ↻ | `#A78BFA` | `#1E1833` | `#C4B5FD` |
| `queued` / `integrating` (fila) | ⇢ | `#FCD34D` | `#2A2410` | `#FCD34D` |
| `integrated` (integrado) | ✓ | `#1F6F5A` | `#0F2A21` | `#6EE7B7` |
| `blocked` (bloqueado) | ▲ | `#FB923C` | `#2A1C10` | `#FDBA74` |
| `failed` (falhou) | ✕ | `#F87171` | `#2A1414` | `#FCA5A5` |

As cores de estado diferem também em claridade, para continuarem distinguíveis com daltonismo. O par crítico, bloqueado × executando, é laranja × azul.

### Tipografia

| Token | Valor |
|---|---|
| `font-ui` | Inter (400, 500, 600, 700) |
| `font-mono` | JetBrains Mono (400, 500): ids, branches, hashes, streams |
| Escala | 11 · 12 · 13 · 14 · 15 · 17 · 22 · 28 px |

### Forma e espaço

- Raio: 9–12 px em controles e cards, 14–16 px em painéis.
- Espaçamento em múltiplos de 4 px.
- Alvos de toque de no mínimo 44 px.

## Componentes-chave

- **Card de nó** em quatro faixas, sempre na mesma ordem:
  1. número da issue e tarja de estado;
  2. título;
  3. papel, harness e tentativa (âmbar a partir da segunda);
  4. barra de progresso das subtasks.

  Os estados e os níveis de zoom estão em [`NoCard.dc.html`](prototype/screens/NoCard.dc.html).
- **Arestas do grafo:**
  - Bloqueio: linha contínua.
  - Contrato: tracejada.
  - Conflito: pontilhada laranja.
  - Caminho crítico: em `action`.
- **Zoom semântico do grafo:** geral (só cor), médio (número e estado), detalhe (card completo).
- **Navegação:**
  - Celular: barra inferior **Inbox · Projetos · Ativos · Mais**.
  - Workspace: abas.
  - Desktop: barra lateral e painel de detalhe à direita.
