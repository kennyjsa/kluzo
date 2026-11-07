# F13 · Fila de merge

- **Grupo:** D. Downstream
- **Status:** planejada

- **O quê:** a integração serial na branch da milestone.
- **Comportamento:**
  - Para cada nó: rebase, freshness gate e merge, depois destrava os dependentes.
  - Hotfix da `main` é sincronizado entre duas integrações.
  - Conflito de rebase volta para o implementer.
- **Telas:** Workspace › Fila de merge (ordem, nó atual, histórico).
- **Aceite:** se a branch da milestone mudar depois da review, os gates rodam de novo antes do merge.
- **Versão:** v0.2 (na v0.1, merge direto após os gates, porque a execução é sequencial).
