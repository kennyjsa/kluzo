# F22 · Fechamento da milestone

- **Grupo:** F. Fechamento
- **Status:** em andamento

- **O quê:** o gate E2E, o relatório e o MR para a `main`.
- **Comportamento:**
  - Com todos os nós integrados: roda o E2E, depois o `reporter` gera o relatório (entregas, decisões, lacunas, custo, plano × entrega).
  - O Kluzo abre o MR `milestone → main` e avisa você.
  - Depois do seu merge, arquiva o workspace.
- **Telas:** Workspace › Visão geral (estado de fechamento); item na Inbox.
- **Aceite:** o MR só abre com o E2E verde; o relatório vai na descrição do MR.
- **Versão:** v0.4 (na v0.1, só o MR sem relatório).
