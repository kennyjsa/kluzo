# F31 · Mapa de código

- **Grupo:** G. Plataforma
- **Status:** planejada

- **O quê:** contexto de código e da milestone para o agente, servido pelo MCP do Kluzo, sem depender de uma ferramenta de mapa específica (ADR 0021).
- **Comportamento:**
  - `kluzo_changes` mostra o que já foi integrado na milestone desde o despacho do nó e quem trabalha nas mesmas áreas.
  - `kluzo_code_map`, `kluzo_code_find` e `kluzo_code_refs` consultam um índice determinístico através do port `CodeMapper`; o adaptador é escolhido em `code_map.adapter`.
  - Sem adaptador, as ferramentas de código não aparecem e o agente usa as do harness.
- **Aceite:**
  - Trocar o adaptador não muda papéis, prompts nem contratos.
  - O índice reflete a ponta da milestone depois de cada integração.
  - O A/B no `make eval` mostra tokens, chamadas de ferramenta e taxa de aprovação com e sem índice.
- **Versão:** `kluzo_changes` na v0.2; port `CodeMapper` e primeiro adaptador na v0.3.
