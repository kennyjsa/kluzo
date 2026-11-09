# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 7d · Ajustes de 7b

### Q52 · Semântica de `budgets.milestone.alert_at`

- **Contexto:** config.md traz `milestone: { usd: 60, alert_at: 1.1 }`, sem dizer como `alert_at` se aplica; events.md tem `budget.alert` com `scope: milestone`.
- **Hoje:** `alert_at` multiplica o orçamento (`usd`, ou o `budget_usd_micros` do próprio workspace): o `budget.alert` sai uma vez, com notificação, quando o custo do workspace passa de `usd × alert_at` (US$ 66 no padrão). `alert_at` 0 vale 1. O alerta não pausa nada.
- **Pergunta:** `alert_at` é esse multiplicador (alerta depois de estourar 10 %) ou uma fração (alerta antes de chegar ao limite)? Passar do orçamento da milestone deve pausar o workspace?
