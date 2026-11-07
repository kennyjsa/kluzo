# 0009. Rebase seguido de merge commit na branch da milestone

- Status: aceito
- Data: 2026-11-07

## Contexto

A fila de merge integra um nó por vez na branch da milestone (ADR 0003). As opções eram fast-forward após o rebase (histórico linear, sem marca de nó), squash (um commit por nó, perde o histórico do agente) ou merge commit.

## Decisão

Cada nó é integrado assim:
1. Rebase da branch do nó sobre a ponta da milestone.
2. Freshness gate.
3. `git merge --no-ff`, com a mensagem `Merge #<issue>: <título>` e trailers `Kluzo-Node`, `Kluzo-Workspace` e `Kluzo-Attempt`.

O merge da milestone na `main` é decisão humana. O padrão sugerido no PR é merge commit.

## Consequências

- Cada nó vira um bloco identificável no histórico e pode ser revertido isoladamente (`git revert -m 1`).
- Os commits do agente dentro do nó ficam preservados para auditoria.
- O histórico da milestone não é linear, mas cada trecho entre merges é linear graças ao rebase.
