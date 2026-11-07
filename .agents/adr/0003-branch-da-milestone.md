# 0003. Integração na branch da milestone com fila de merge

- Status: aceito
- Data: 2026-11-07

## Contexto

O fluxo de trabalho adotado é issue → branch da milestone → main, com o merge na main sempre feito por um humano.

## Decisão

Cada nó nasce da ponta da branch da milestone e abre o MR contra ela. A integração é serial, por fila de merge: rebase, freshness gate e merge. Hotfixes da main entram na branch da milestone entre duas integrações. No fim, o Kluzo abre o MR milestone → main com o relatório. Em milestone/* só o bot faz merge; na main, só humanos.

## Consequências

- Nenhum agente chega à main.
- A integração serial limita o throughput, aceitável no volume de uma milestone.
- Os dependentes partem sempre de código real e integrado.
