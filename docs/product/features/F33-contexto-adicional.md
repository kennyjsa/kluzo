# F33 · Contexto adicional e vários repositórios

- **Grupo:** G. Plataforma
- **Status:** planejada (partes 1 e 2); candidata (parte 3)

- **O quê:** dar ao agente mais do que o repositório do nó, sem perder o controle sobre o que ele pode alterar.
- **Comportamento:**
  1. **Referências do repo:** `references:` no `kluzo.yaml` (geral ou por papel) lista caminhos do próprio repositório que entram no `Focus` da tarefa como leitura recomendada. O agente lê sob demanda; nada é colado no prompt.
  2. **Contexto externo somente leitura:** `context:` lista outros repositórios (num commit fixo) ou pastas locais. O Kluzo materializa uma cópia congelada no workspace e a entrega como diretório adicional da sessão; o hook bloqueia escrita nela e a conferência do diff a ignora.
  3. **Milestone com vários repositórios** (candidata): cada nó declara o repo; uma branch `milestone/<nome>` por repo tocado; gates e áreas por repo; arestas entre repos; um PR por repo no fechamento, cada um com merge humano. No GitLab, milestones de grupo atravessam projetos; no GitHub, exige convenção. Precisa de ADR próprio.
- **Aceite (1 e 2):** o agente lê as referências e o contexto externo; uma escrita no contexto externo é bloqueada e registrada.
- **Versão:** 1 e 2 na v0.2; 3 depois da v0.4, salvo necessidade de um projeto real antes.
