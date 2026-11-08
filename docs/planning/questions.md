# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 6b · Montagem do prompt e MCP mínimo

### Q28 · Título do nó e critérios de aceite no segmento 3

- **Contexto:** `prompt.md` mostra `Node: #<issue> <title>` e a seção `Acceptance criteria` fora de envelope, mas o mesmo documento exige que nenhum texto externo apareça fora de um envelope. O título e o texto dos critérios vêm da issue.
- **Hoje:** a linha `Node:` leva só `#<issue>` e a branch; o título entra no envelope de `Issue`, e a lista `AC1..n` inteira vai num envelope (`source=plan kind=acceptance`), com os ids dentro dele. Pelo mesmo critério, resumos de dependências (`Neighbors`) e `Decisions so far` também vão envelopados.
- **Pergunta:** o título e os critérios podem sair do envelope por virem do plano aprovado por um humano, ou o envelope fica?

### Q29 · Formato dos schemas de resultado

- **Contexto:** `results.md` mostra só exemplos; o formato formal não está no contrato.
- **Hoje:** `additionalProperties: false` nos objetos, `decisions` e `gaps` opcionais, `response.note` obrigatória (também em `fixed`), `check` obrigatório em `blocking` e `major`, `reason` obrigatório em `failed`, `params.subtasks_as_nodes` e `params.question` obrigatórios em `split` e `escalate`. Rejeitar campo desconhecido pode devolver ao agente por um campo extra inofensivo.
- **Pergunta:** os schemas devem ser estritos assim, ou tolerar campos desconhecidos?

### Q30 · Chamada de ferramenta declarada e ainda não servida

- **Contexto:** `mcp.md` define `forbidden` (não declarada no papel) e `unavailable` (índice de código em construção ou adaptador fora do ar), mas na v0.1 só `kluzo_result` existe, e um papel pode listar outras em `mcp:`.
- **Hoje:** uma ferramenta conhecida, declarada no papel e ainda não servida responde `unavailable`; qualquer outra responde `forbidden`. `tools/list` mostra só `kluzo_result`.
- **Pergunta:** `unavailable` serve para "ainda não implementada", ou isso merece outro código?

### Q31 · Validação e correção na mesma chamada

- **Contexto:** `mcp.md` permite uma correção após `accepted: false`, e o item 7 acrescenta ao `kluzo_result` a validação de cobertura (`criteria`, `responses`, `previous`), no `core`.
- **Hoje:** o `internal/mcp` só valida o schema. A segunda chamada inválida encerra a etapa (`Done`) e as seguintes recebem `session_closed`. Quando o item 7 somar as regras do `core`, elas devem passar pelo mesmo contador de correções.
- **Pergunta:** confirmar que a regra de "uma correção" vale para o erro de schema e o de cobertura somados.

