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

## Item 7a · Worktree do nó e regras de review

### Q42 · Contador de gate rápido: por tentativa

- **Contexto:** state-machine.md dá ao gate rápido um contador próprio limitado por `max_retries`, sem dizer quando ele zera.
- **Hoje:** `Node.FastGateFailures` zera a cada tentativa nova (como `Round`) e não zera entre rodadas. A falha que passa de `max_retries` leva a `blocked` (`triage`) por `core.FastGateFailed`; ao desbloquear sem tentativa nova, a próxima falha bloqueia de novo.
- **Pergunta:** confirmar o escopo por tentativa, ou zerar também a cada rodada de review?

### Q43 · Rodada final com critério não atendido

- **Contexto:** results.md diz que, na última rodada, `major` vira gap e `blocking` sempre bloqueia; não diz o que acontece com um critério `met: false` sem finding `blocking`.
- **Hoje:** pelo desempate ("critério sem evidência executável é `blocking`"), critério não atendido na última rodada leva a `blocked` (`triage`), como um `blocking` aberto.
- **Pergunta:** confirmar.

### Q44 · `auto`, `caused_by` e evidência do reviewer

- **Contexto:** o schema do `review-result` aceita `auto` e `caused_by` livres, e a conferência de `caminho:linha` está descrita na seção do `node-result`.
- **Hoje:** (1) `auto: true` vindo do reviewer é saída inválida ("auto is set by Kluzo only"); só o Kluzo cria findings automáticos. (2) Da rodada 2 em diante, um `caused_by` que não aponta para uma linha alterada na rodada conta como ausente (o finding fora dos trechos alterados vira `minor`, exceto `security`). (3) A evidência dos critérios do reviewer passa pela mesma conferência de arquivo e linha que a do implementer. (4) O rebaixamento para `minor` fora dos trechos alterados vale também para `duplication` e `dead_code`, que results.md chama de `blocking`; só `security` escapa. (5) Um `major` vira gap só quando o nó é aprovado na última rodada; se a última rodada vai para `triage` (por um `blocking` ou critério não atendido), os `major` continuam abertos, para uma nova tentativa os receber.
- **Pergunta:** confirmar as cinco leituras; se (1) valer, tirar `auto` do schema do `review-result`.

### Q45 · Ambiente do `worktree.setup` e nome da branch do nó

- **Contexto:** config.md diz "env como nos gates (env_allow, caches)", mas cada gate tem o seu `env_allow`; e nenhum contrato fixa o nome da branch do nó.
- **Hoje:** `gitops.Prepare` recebe o ambiente pronto (montado com `sessionenv`, com caches e `COMPOSE_PROJECT_NAME`), e `AddWorktree` recebe o nome da branch; as duas escolhas ficam para o supervisor (item 7b).
- **Pergunta:** qual `env_allow` vale para o setup (o de `runtimes.<harness>`, a união dos gates, ou uma chave própria em `worktree:`)? E qual o nome da branch do nó (sugestão: `kluzo/<milestone>/<issue>`)?

### Q46 · Escritas do próprio Kluzo durante uma sessão

- **Contexto:** a conferência pós-sessão (ADR 0024, item 4) compara todos os refs do repositório com o snapshot de antes da sessão.
- **Hoje:** só os refs passados como permitidos (a branch do nó) podem mudar; qualquer outra mudança é restaurada e falha o nó. Com `max_parallel: 1` na v0.1 não há escrita concorrente, mas com nós em paralelo (ou um merge na milestone durante uma sessão) o supervisor terá de passar esses refs ou serializar as escritas.
- **Também:** a configuração do repositório continua sendo lida pelo git do Kluzo, e chaves como `filter.*.smudge`, `diff.external` e `core.sshCommand` executam comandos. A proteção depende de uma ordem: `Check` restaura a configuração depois de cada sessão, antes de qualquer outro git do Kluzo, e o Kluzo nunca roda git com o worktree do nó como diretório enquanto uma sessão pode escrever nele. O `Snapshot` guarda o diretório git do checkout principal e recusa um worktree; o `Check` não o resolve de novo. Sem configuração global, o filtro do Git LFS some: worktrees de repositórios com LFS recebem os ponteiros. E `AddWorktree` reaproveita a branch do nó se ela existir (para o outbox repetir sem dano), então uma tentativa nova precisa apagar a branch antes.
- **Pergunta:** nenhuma para a v0.1; registrado para o item 7b e para o que liga o paralelismo. O LFS fica como limite conhecido até alguém pedir.
