# AGENTS.md

Contexto para agentes que trabalham **no código do Kluzo**.

## O projeto

O Kluzo é um orquestrador que resolve uma milestone inteira com agentes de código: o humano refina e aprova o plano (upstream), e o Kluzo executa o grafo de issues, integra na branch da milestone e entrega o PR para o merge humano na `main`.

O mapa completo da documentação está em [docs/README.md](docs/README.md).

## Antes de implementar um item

1. Leia o item em `docs/planning/milestones/vX.md` e os critérios de aceite dele.
2. Leia as features citadas em `docs/product/features/`.
3. Leia os contratos envolvidos em `docs/contracts/`. Eles valem sobre qualquer outro documento.
4. Confira onde o código vai em `docs/architecture/packages.md` e os padrões em `docs/architecture/patterns.md`.
5. Siga `docs/development.md` (comandos, testes, Definition of Done).

## Regras inegociáveis

- **O núcleo é determinístico:** `internal/core` não chama LLM e não faz I/O; relógio e ids chegam como dados (ADRs 0001, 0012).
- **Todo efeito externo passa pelo outbox** e é idempotente (ADR 0013).
- **Agentes não conversam entre si:** toda comunicação passa pelo MCP do Kluzo (ADR 0007).
- **Nenhum agente chega à `main`:** o merge na `main` é humano (ADR 0003).
- **As regras de dependência entre pacotes** são verificadas por `internal/archtest`. Não as contorne.
- **Mudança de arquitetura, stack ou padrão** exige ADR novo em `.agents/adr/`.
- **Dependência nova** segue o ADR 0017 e é registrada em `docs/architecture/stack.md`.

## Convenções

- Código, identificadores, logs, erros e commits em **inglês**. Documentação em **português** (ADR 0008).
- Conventional Commits (`feat(core): …`).
- Código duplicado e código morto **bloqueiam** o merge.
- Toda transição de estado tem caso na tabela de testes de `internal/core`.
- Testes unitários sem rede, sem LLM e sem relógio real; use os fakes de `internal/ports/fakes`.
- A web é responsiva e mobile-first: desenhe para 390 px e expanda para 768 e 1280 px.
