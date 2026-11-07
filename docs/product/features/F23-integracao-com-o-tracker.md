# F23 · Integração com o tracker

- **Grupo:** G. Plataforma
- **Status:** planejada

- **O quê:** os adaptadores `Tracker`, `SCM` e `CI`.
- **Comportamento:**
  - Lê milestones, issues e links.
  - Espelha o estado em labels `orch/*` e num comentário por transição, e publica o grafo em Mermaid na milestone.
  - Labels têm dono. Começa por polling.
- **Aceite:** o estado no tracker corresponde ao do Kluzo em até um ciclo de polling.
- **Versão:** GitHub v0.1; GitLab v0.2.
