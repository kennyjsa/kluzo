# 0021. MCP do Kluzo como camada de contexto, com o mapa de código atrás de um port

- Status: aceito
- Data: 2026-11-07
- Origem: [docs/research/code-maps.md](../../docs/research/code-maps.md)

## Contexto

Cada nó, e cada rodada de review, abre uma sessão nova do harness. Numa milestone de 20 issues, os agentes redescobrem o mesmo código dezenas de vezes com grep e leitura de arquivos inteiros. Ferramentas de mapa de código (cartog, CodeGraph, codebase-memory-mcp e outras) reduzem essa descoberta, mas:
- evoluem e são abandonadas rápido, e cada uma tem o seu formato e a sua instalação por harness;
- não sabem nada da milestone: o que outro nó acabou de integrar, quem mexe na mesma área agora, que contrato o nó consome.

Os mapas escritos por LLM (como o Cartographer, que gera um `CODEBASE_MAP.md`) custam caro para gerar, desatualizam sem aviso e injetam texto de modelo como se fosse fato.

## Decisão

1. **O MCP do Kluzo é a única camada de contexto do agente.** Além das ferramentas de protocolo (`kluzo_result`, `kluzo_ask`…), ele serve dois grupos de leitura:
   - **Contexto da milestone**, que só o Kluzo conhece: `kluzo_context` (já existente) e `kluzo_changes` (o que foi integrado na branch da milestone desde o despacho do nó, e quem trabalha nas mesmas áreas agora).
   - **Navegação de código**: `kluzo_code_map`, `kluzo_code_find` e `kluzo_code_refs`.
2. **A navegação passa por um port `CodeMapper`.** O Kluzo não tem parser nem indexador próprios. Cada adaptador embrulha uma ferramenta existente (cartog, LSP, ctags…). Trocar de ferramenta é trocar o adaptador em `kluzo.yaml`, sem mudar papéis, prompts nem contratos.
3. **Só índice determinístico.** Um adaptador só é aceito se o mapa vier de análise do código (AST, LSP, grafo de símbolos), nunca de texto gerado por LLM.
4. **O Kluzo controla quando o índice é atualizado.** Há um índice por workspace, construído sobre a ponta da branch da milestone e atualizado depois de cada integração. As consultas de um nó também enxergam as mudanças do próprio worktree, quando o adaptador suportar atualização incremental.
5. **Opcional e medido:**
   - Sem adaptador configurado (`code_map.adapter: none`, o padrão), as ferramentas `kluzo_code_*` não são listadas, e o agente usa as ferramentas do próprio harness.
   - Um adaptador só vira padrão de projeto depois de um A/B no `make eval`: tokens, chamadas de ferramenta, tempo e taxa de aprovação no review, com e sem índice.
6. **A fronteira de confiança continua a mesma.** Trechos de código e nomes vindos do índice são dado não confiável e vão no envelope do ADR 0019. O papel declara em `mcp:` quais ferramentas recebe; as de código não têm efeito colateral.

## Consequências

- O agente aprende um só conjunto de ferramentas, igual no Claude Code e no OpenCode, e o Kluzo não precisa instalar MCPs de terceiros em cada harness.
- Cada consulta vira evento `mcp.call` no journal, o que dá a métrica do A/B e da tela de custos.
- Um adaptador novo precisa passar na suíte `ports/contracttest` do `CodeMapper`.
- O Kluzo passa a depender de um binário externo quando um adaptador está ligado; `kluzo doctor` verifica a versão. A ferramenta escolhida segue o ADR 0017.
- Cada ferramenta listada ocupa contexto em toda sessão, por isso o conjunto fica pequeno e os papéis escolhem o que recebem.
