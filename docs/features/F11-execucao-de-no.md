# F11 · Execução de nó

- **Grupo:** D. Downstream
- **Status:** planejada

- **O quê:** a pipeline `node.yaml` aplicada a um nó.
- **Comportamento:**
  - Worktree isolado, nascido da ponta da milestone.
  - Os papéis rodam em sequência pelo runtime de cada um.
  - O reviewer pode devolver o nó ao implementer com findings.
  - O progresso das subtasks é acompanhado.
- **Telas:** Nó › Execução ao vivo (F19).
- **Aceite:** um nó interrompido retoma do último papel concluído, sem refazer o que já passou.
- **Versão:** v0.1.
