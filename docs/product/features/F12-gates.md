# F12 · Gates

- **Grupo:** D. Downstream
- **Status:** planejada

- **O quê:** as verificações determinísticas: testes, lint, SAST e E2E.
- **Comportamento:**
  - São definidos em `kluzo.yaml`. Rodam localmente ou via pipeline do CI.
  - O resultado vem do CI ou do comando, nunca do agente.
  - Gate falho volta para o implementer com o log.
- **Telas:** Nó › Gates (status e log de cada gate).
- **Aceite:** um nó nunca entra na fila de merge com gate vermelho.
- **Versão:** v0.1.
