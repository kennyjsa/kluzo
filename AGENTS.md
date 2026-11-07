# AGENTS.md

Contexto para agentes que trabalham **no código do Kluzo**.

## O projeto

O Kluzo é um orquestrador de milestones para agentes de código. Leia `docs/SPEC.md` antes de qualquer mudança, e `docs/adr/` para as decisões já tomadas.

## Regras inegociáveis

- **O núcleo é determinístico.** Scheduler, máquina de estados, fila de merge e gates não chamam LLM (ADR 0001).
- **Agentes não conversam entre si.** Toda comunicação passa pelo MCP do Kluzo (ADR 0007).
- **Nenhum agente chega à `main`.** O merge na `main` é humano (ADR 0003).
- Mudança de arquitetura exige um ADR novo em `docs/adr/` (formato MADR).

## Stack

- Go (binário único), SQLite (`modernc.org/sqlite`), Vue + Vue Flow embutidos via `embed`, `tsnet`.
- A web é **responsiva e mobile-first**: desenhe para 390 px primeiro e expanda para 768 e 1280 px.

## Convenções

- Documentação em português.
- Código duplicado e código morto bloqueiam o merge.
- Toda transição de estado tem teste.
