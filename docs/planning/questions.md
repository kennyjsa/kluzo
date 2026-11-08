# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 6e · Ajustes do runtime

### Q40 · `--max-budget-usd` na retomada

- **Contexto:** `total_cost_usd` acumula entre retomadas, e o CLI confere `--max-budget-usd` depois da chamada. Se ele compara o limite com o total acumulado, passar "o que resta ao nó" numa retomada desconta as sessões anteriores duas vezes, e a sessão para cedo com `error_max_budget_usd`.
- **Hoje:** a retomada recebe `MaxBudgetUSDMicros` (o que resta ao nó), como o `Start`. O custo da sessão já é a diferença para o `PriorUsage`.
- **Pergunta:** verificar localmente se o limite vale para o total acumulado. Se valer, o adaptador passa `MaxBudgetUSDMicros + PriorUsage.CostUSD` na retomada (a flag é volátil e não entra na impressão digital).

### Q41 · Canário casado pelo nome da ferramenta

- **Contexto:** os eventos de hook do stream não trazem o `tool_use_id` (só `hook_name: "PreToolUse:<ferramenta>"`), e os de um subagente chegam antes do `tool_use`. O canário passou a casar cada `tool_result` com um `hook_started` da mesma ferramenta.
- **Hoje:** um hook de outra ferramenta não cobre a chamada, e a falta de hook aborta a sessão. Sobras (hook sem `tool_result`) entram em `Unknown` como `system/hook_started-unmatched`. Limite: uma sobra pode cobrir uma chamada posterior da mesma ferramenta sem hook, e um hook `PreToolUse` de outra origem também conta.
- **Pergunta:** reforçar com o `tool_use_id` que o corpo do hook traz (o servidor MCP guarda os ids recebidos e o supervisor confere cada `tool_result`), ou aceitar o limite na v0.1?
