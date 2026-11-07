# F30 · Visão Estrutura

- **Grupo:** E. Visualização do progresso
- **Status:** planejada

- **O quê:** a milestone como hierarquia indentada com linha do tempo, no estilo do MS Project.
- **Comportamento:**
  - A milestone é a linha-resumo; os nós ficam um nível abaixo e as subtasks um nível abaixo de cada nó.
  - Cada nó pode ser expandido ou recolhido; um controle expande ou recolhe todos.
  - Abaixo do nome, a coluna de predecessores (`41b` = bloqueio pelo #41, `43c` = contrato com o #43), o papel ativo, a tentativa e o custo.
  - Barras de tempo: preenchido = realizado, contorno = previsto, tracejado = ainda não começou. Uma linha marca "agora".
  - A escala se ajusta à duração da milestone (horas ou dias). Em telas estreitas, a área das barras rola na horizontal; a coluna de nomes fica fixa.
- **Telas:** Workspace › Progresso › Estrutura ([protótipo](../prototype/screens/Estrutura.dc.html)).
- **Aceite:** a Estrutura, o Grafo e o Kanban mostram sempre o mesmo estado; os predecessores batem com as arestas do grafo.
- **Versão:** v0.3.
