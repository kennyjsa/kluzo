# 0007. MCP embutido como canal agente → Kluzo

- Status: aceito
- Data: 2026-11-07

## Contexto

Os agentes precisam consultar contexto, registrar decisões, propor lacunas, escalar perguntas e entregar resultados, de forma auditável e igual em todos os harnesses.

## Decisão

O binário expõe um servidor MCP injetado em cada sessão, identificado por nó e papel, com as ferramentas kluzo_context, kluzo_decision, kluzo_propose_node, kluzo_ask e kluzo_result. Os agentes não conversam entre si; a comunicação é em estrela, através do Kluzo.

## Consequências

- Funciona no Claude Code e no OpenCode sem adaptação.
- Permissões por papel garantidas no servidor.
- Toda interação fica no journal.
