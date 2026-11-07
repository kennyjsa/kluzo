# 0002. Grafo da milestone com três tipos de aresta

- Status: aceito
- Data: 2026-11-07

## Contexto

Os orquestradores existentes despacham uma issue por agente, sem relação entre as tarefas. Isso gera conflitos de merge e trabalho feito na ordem errada.

## Decisão

A milestone é modelada como um grafo de nós (issues) com arestas de **bloqueio** (precisa do código integrado), **contrato** (precisa só da interface) e **conflito** (mesma área, então serializa). Um nó só fica pronto quando os bloqueios estão integrados na branch da milestone. O scheduler prioriza o caminho crítico.

## Consequências

- Paralelismo seguro e previsível.
- O upstream precisa produzir dependências e áreas de qualidade, papel do planner e do critic.
- Arestas de contrato e conflito não existem nos trackers; ficam na descrição, nas labels area/* e no plano versionado.
