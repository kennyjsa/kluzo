# 0001. Núcleo determinístico, sem LLM próprio

- Status: aceito
- Data: 2026-11-07

## Contexto

Orquestradores que usam um LLM para decidir o fluxo são não-determinísticos, gastam tokens no agendamento e são difíceis de auditar e retomar depois de um crash.

## Decisão

Scheduler, máquina de estados, fila de merge e gates são código Go determinístico. Todo julgamento (planejamento, lacunas, triagem ambígua, relatório) é um papel executado por um harness, com saída estruturada validada por schema. A triagem de falha consulta primeiro uma tabela de regras.

## Consequências

- Execuções são reproduzíveis e auditáveis pelo journal.
- Uma única conta e configuração para todos os LLMs (os dos harnesses).
- Qualquer decisão inteligente nova precisa virar um papel com schema, o que exige mais desenho antes de implementar.
