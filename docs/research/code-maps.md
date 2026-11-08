# Mapas de código para agentes

Pesquisa de 07/11/2026. Os números são dos próprios autores e servem de ordem de grandeza, não de prova.

## Pergunta

Ferramentas que mapeiam o código prometem economizar tokens porque o harness acha as coisas mais rápido. Isso vale para o Kluzo, e como entraria?

## Dois tipos

| Tipo | Exemplos | Como funciona | Problema para o Kluzo |
|---|---|---|---|
| Mapa escrito por LLM | Cartographer (plugin do Claude Code) | Subagentes leem o repositório em paralelo e escrevem `docs/CODEBASE_MAP.md`, com resumo no `CLAUDE.md`; atualiza pelo histórico do git | Gerar custa caro; desatualiza sem aviso a cada merge; é texto de modelo injetado como fato |
| Índice determinístico por MCP | cartog, CodeGraph, codebase-memory-mcp, CodeMap (.NET), Bito AI Architect, XCE | Analisa o código (AST, LSP, grafo de símbolos e chamadas) e responde consultas: onde está, quem chama, mapa resumido com orçamento de tokens | Cada um tem a sua instalação por harness, e nenhum conhece a milestone |

## O que os números dizem

- O cartog anuncia cerca de 280 tokens por consulta e um `map --tokens N` ranqueado por centralidade, com suporte a Claude Code e OpenCode.
- O CodeGraph reporta de -38% a -64% de tokens conforme o repositório, mas custo parecido em alguns casos, porque as respostas são mais longas.
- O XCE reporta cerca de -20% num benchmark de 500 tarefas de correção; o Bito, -47% no SWE-Bench Pro.
- O ganho é maior em repositórios grandes e em perguntas estruturais ("quem chama isto?") e menor em tarefas de edição já bem focadas.

## Por que importa no Kluzo

Cada nó e cada rodada de review abrem uma sessão nova. A descoberta do código se repete dezenas de vezes por milestone, então qualquer economia se multiplica. O foco por nó (globs das áreas e contratos vizinhos, ADR 0019) já reduz parte disso, sem custo nenhum.

## Decisão

[ADR 0021](../../.agents/adr/0021-mcp-como-camada-de-contexto.md):
- o MCP do Kluzo é a camada de contexto;
- `kluzo_changes` traz o que só o Kluzo sabe (o que já foi integrado na milestone e quem mexe na mesma área);
- a navegação de código fica atrás do port `CodeMapper`, com adaptadores trocáveis e só índice determinístico;
- opcional, e padrão só depois de A/B no `make eval`.

Candidato ao primeiro adaptador: cartog (local, multilinguagem, licença a confirmar, já fala MCP e CLI). Antes de escolher, comparar com um adaptador de LSP (gopls para Go), que não acrescenta binário de terceiros.
