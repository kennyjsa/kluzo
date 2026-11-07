# F10 · Grafo e scheduler

- **Grupo:** D. Downstream
- **Status:** planejada

- **O quê:** o agendamento determinístico dos nós.
- **Comportamento:**
  - Arestas de bloqueio, contrato e conflito.
  - Um nó fica pronto quando os bloqueios estão integrados.
  - A prioridade segue o caminho crítico.
  - O paralelismo máximo é configurável.
  - O grafo é validado antes de iniciar.
- **Telas:** Progresso › Grafo (F17).
- **Aceite:** dois nós com a mesma área nunca rodam ao mesmo tempo; nenhum nó inicia sem os bloqueios integrados.
- **Versão:** v0.1 (só bloqueio e execução sequencial); v0.2 (paralelismo, contrato e conflito).
