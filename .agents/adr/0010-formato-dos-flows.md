# 0010. Flows sequenciais com laços de devolução, sem paralelismo interno

- Status: aceito
- Data: 2026-11-07

## Contexto

Os flows (`upstream`, `node`, `closing`) precisam expressar etapas, laços (o reviewer devolve ao implementer, o critic devolve ao planner) e portões. Uma linguagem de workflow completa (condições arbitrárias, paralelismo, sub-flows) aumentaria muito a superfície do núcleo.

## Decisão

- Um flow é uma **lista ordenada de etapas**, executadas uma por vez.
- Os tipos de etapa são: `role`, `gate`, `approval`, `merge_queue` e `loop`. A gramática está em [contracts/config.md](../../docs/contracts/config.md#flows).
- O único desvio permitido é a **devolução**: uma etapa `role` com `on_reject: <id>` volta para uma etapa anterior, com um limite de rodadas (`max_rounds`). Estourou o limite, o nó vai para a triagem.
- Não há paralelismo **dentro** de um flow. O paralelismo acontece **entre nós**, e quem controla é o scheduler.
- Não há condições arbitrárias. Variações por projeto se fazem editando o flow ou com perfis.

## Consequências

- Um flow é simples de validar, de desenhar na web e de retomar depois de um crash: o estado é o índice da etapa mais o contador de rodadas.
- Casos mais ricos (por exemplo, QA condicional por área) ficam para um ADR futuro, se aparecerem na prática.
