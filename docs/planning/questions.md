# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 7d · Ajustes de 7b

### Q52 · Semântica de `budgets.milestone.alert_at`

- **Contexto:** config.md traz `milestone: { usd: 60, alert_at: 1.1 }`, sem dizer como `alert_at` se aplica; events.md tem `budget.alert` com `scope: milestone`.
- **Hoje:** `alert_at` multiplica o orçamento (`usd`, ou o `budget_usd_micros` do próprio workspace): o `budget.alert` sai uma vez, com notificação, quando o custo do workspace passa de `usd × alert_at` (US$ 66 no padrão). `alert_at` 0 vale 1. O alerta não pausa nada.
- **Pergunta:** `alert_at` é esse multiplicador (alerta depois de estourar 10 %) ou uma fração (alerta antes de chegar ao limite)? Passar do orçamento da milestone deve pausar o workspace?

## Item 8 · Gates e garantias

### Q53 · Gate `source: tracker` e o commit do nó

- **Contexto:** um gate `source: tracker` lê os checks do CI para o commit do nó (`ports.CI`), mas na v0.1 o Kluzo não publica a branch do nó; o CI só vê commits enviados ao tracker.
- **Hoje:** o gate consulta o CI pelo SHA do último commit do implementer, até nenhum check obrigatório estar pendente ou o timeout passar (então `error`). Sem push, ele termina em `error` no timeout. Os testes usam o fake de CI.
- **Pergunta:** quem envia a branch do nó ao tracker antes de um gate `source: tracker` (o executor do gate, como efeito do outbox, ou só a fila de merge)?

### Q54 · Proposta de mudança: registro e decisão

- **Contexto:** a conferência pós-rodada leva o nó a `blocked` (`proposal`) e grava `proposal.created` (`kind: change`, `state: pending`), com os motivos no comentário e na notificação. A tabela `proposals` de data-model.md ainda não existe, e `proposal.created` não carrega os motivos.
- **Hoje:** não há projeção nem caminho de decisão; a saída de `blocked` é humana, pela CLI do item 10 (`kluzo node unblock`/`cancel`). Desbloqueado, o implementer roda de novo e a conferência acusa de novo a mesma mudança, se ela continuar no trabalho da tentativa.
- **Pergunta:** a proposta aprovada deve liberar a mudança (e como a conferência sabe disso: um registro da aprovação por arquivo e commit?), e os motivos entram no payload de `proposal.created` e na tabela `proposals`?

### Q55 · `gates[].report` e o `doctor` parcial

- **Contexto:** o item pede falhas estruturadas "do JUnit XML se o gate apontar um relatório", e config.md não tinha a chave.
- **Hoje:** `gates[].report` (opcional, só com `run`, caminho dentro do worktree, lido por `os.Root`) foi acrescentado a config.md e à validação. `kluzo doctor` existe e por ora só confere a configuração e as credenciais do host (saída 2 para configuração inválida, 3 para credenciais recusadas); o resto do doctor é do item 10. A tabela `gate_runs` também não é projetada: os gates ficam em `gate.started`/`gate.finished`.
- **Pergunta:** confirmar a chave `report` (ou preferir outro formato) e os códigos de saída do doctor.
