# F08 · Planejamento e crítica

- **Grupo:** C. Upstream
- **Status:** planejada

- **O quê:** o laço `planner ⇄ critic` que monta e revisa o grafo.
- **Comportamento:**
  - O planner gera os nós, as arestas e as áreas.
  - O critic aplica o checklist de lacunas e devolve findings. Isso repete até não haver lacuna relevante, com limite de rodadas.
  - Cada rodada fica registrada.
- **Telas:** Workspace › Plano (rodadas, findings por rodada).
- **Aceite:** a proposta final inclui as lacunas encontradas e como cada uma foi tratada.
- **Versão:** v0.3.
