# 0004. Ciclos upstream e downstream separados por portão humano

- Status: aceito
- Data: 2026-11-07

## Contexto

A autonomia do downstream depende da qualidade do refino. Lacunas escondidas são a principal causa de falha da execução autônoma.

## Decisão

O upstream (business, po, requirements, architect, planner ⇄ critic) produz um plano que só libera o downstream após aprovação humana. Nada é criado no tracker antes da aprovação. O downstream nunca replaneja: lacunas e falhas voltam ao upstream como proposta.

## Consequências

- O tempo humano se concentra no upstream.
- Uma Definition of Ready rigorosa vira pré-requisito.
- Os workspaces podem rodar em esteira (N no downstream, N+1 no upstream).
