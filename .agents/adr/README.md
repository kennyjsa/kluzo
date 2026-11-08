# Decisões de arquitetura (ADRs)

Registro das decisões de arquitetura do Kluzo, no formato MADR (contexto, decisão, consequências).

Regras:
- Uma decisão por arquivo, numerada em sequência: `NNNN-titulo-curto.md`.
- Status: `proposto` → `aceito` → (`substituído por NNNN` | `obsoleto`).
- ADR aceito não é reescrito. Para mudar uma decisão, crie um ADR novo que substitui o anterior, e atualize o status do antigo.
- Toda mudança de arquitetura, de stack ou de padrão de projeto exige um ADR no mesmo PR.

| # | Decisão | Status |
|---|---|---|
| 0001 | [Núcleo determinístico, sem LLM próprio](0001-nucleo-deterministico.md) | aceito |
| 0002 | [Grafo da milestone com três tipos de aresta](0002-grafo-tipado.md) | aceito |
| 0003 | [Integração na branch da milestone com fila de merge](0003-branch-da-milestone.md) | aceito |
| 0004 | [Ciclos upstream e downstream separados por portão humano](0004-upstream-downstream.md) | aceito |
| 0005 | [Go como linguagem do núcleo](0005-go.md) | aceito |
| 0006 | [Web mobile-first acessada via tsnet](0006-web-tsnet.md) | aceito |
| 0007 | [MCP embutido como canal agente → Kluzo](0007-mcp-canal.md) | aceito |
| 0008 | [Código em inglês, documentação em português](0008-idioma-do-codigo.md) | aceito |
| 0009 | [Rebase seguido de merge commit na branch da milestone](0009-estrategia-de-merge.md) | aceito |
| 0010 | [Flows sequenciais com laços de devolução, sem paralelismo interno](0010-formato-dos-flows.md) | aceito |
| 0011 | [Áreas declaradas no plano e conferidas pelo diff](0011-areas-e-footprint.md) | aceito |
| 0012 | [Ports & adapters com núcleo funcional](0012-ports-adapters-nucleo-funcional.md) | aceito |
| 0013 | [Journal como fonte da verdade e outbox de efeitos idempotentes](0013-journal-e-outbox.md) | aceito |
| 0014 | [Um banco SQLite por workspace](0014-sqlite-por-workspace.md) | aceito |
| 0015 | [Polling para o tracker e SSE para a web](0015-polling-e-sse.md) | aceito |
| 0016 | [Stack do frontend](0016-stack-do-frontend.md) | aceito |
| 0017 | [Critérios para aceitar dependências](0017-criterios-de-dependencias.md) | aceito |
| 0018 | [Modelo de segurança dos agentes](0018-modelo-de-seguranca.md) | aceito |
| 0019 | [Montagem do prompt em três segmentos, com fronteira de confiança única](0019-montagem-do-prompt.md) | aceito |
| 0020 | [Garantias aplicadas em código, não pedidas no prompt](0020-garantias-fora-do-prompt.md) | aceito |
| 0021 | [MCP do Kluzo como camada de contexto, com o mapa de código atrás de um port](0021-mcp-como-camada-de-contexto.md) | aceito |
