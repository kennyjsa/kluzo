# F15 · Propostas de lacuna

- **Grupo:** D. Downstream
- **Status:** planejada

- **O quê:** um nó novo descoberto durante a execução (`kluzo_propose_node`).
- **Comportamento:**
  - A proposta vai para a Inbox.
  - Se não bloqueia, o nó atual segue. Se bloqueia, o nó atual vai para `blocked`.
  - Ao ser aprovada, entra no grafo e no tracker.
- **Telas:** Inbox; Plano › Propostas.
- **Aceite:** nenhuma issue é criada sem a sua aprovação.
- **Versão:** v0.2.
