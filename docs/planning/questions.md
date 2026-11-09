# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 7b · Supervisor e execução do flow do nó

### Q47 · Onde fica o progresso do nó no flow

- **Contexto:** a retomada depois de um crash precisa da etapa atual, do registro de findings (com ids estáveis entre rodadas e tentativas), da sessão do implementer a retomar, do custo gasto e dos commits da rodada. Nenhum evento de events.md carrega isso, e data-model.md não tem onde guardar os findings.
- **Hoje:** cada decisão do núcleo grava um evento novo, `node.flow` (`{step, run}`), com o `core.Run` inteiro; o store o projeta em `nodes.step_index` e numa coluna nova, `nodes.flow` (migration 0002). Marcado como provisório em events.md e data-model.md.
- **Pergunta:** aceitar o evento e a coluna (e então tirar o "provisório"), ou preferir eventos menores (por exemplo, o resultado validado em `session.finished` e uma tabela `findings`) com o estado reconstruído por dobra?

### Q48 · Sessões do Kluzo, contador do harness e tabela `sessions`

- **Contexto:** prompt.md e ADR 0024 pedem "sessão nova com o próximo `Counter`"; data-model.md tem a tabela `sessions`, mas nenhum evento traz papel, modelo, referência do harness e resultado.
- **Hoje:** cada efeito `session.start` é uma sessão do Kluzo (`ss_<ulid do efeito>`), e cada execução do efeito é uma sessão do harness com `Counter` = número da execução − 1. A execução é contada antes de rodar (`BeginEffect`), então uma execução interrompida por crash não repete o número. Os eventos da sessão vão para o journal com o `session_id`; a tabela `sessions` continua vazia, e o manifesto do prompt não é gravado no `session.started` (o evento vem do adaptador, que não conhece o manifesto) nem em `logs/sessions/<id>.prompt.md`.
- **Também:** o custo de uma sessão entra no `Run` (e no orçamento do nó) na transação da decisão. Se o Kluzo morre entre o fim da sessão e a decisão, a sessão roda de novo e o custo da interrompida fica só nos eventos `cost` do journal. O custo do nó também não é projetado em `nodes.cost_usd_micros` nem no workspace, então o orçamento da milestone ainda não o vê.
- **Pergunta:** qual evento alimenta `sessions` (estender `session.started`/`session.finished` com papel, modelo, referência e manifesto?), e quem grava o texto do prompt? O custo do nó deve ser somado dos eventos `cost` (o que cobre o crash) e projetado nas colunas de custo?

### Q49 · Sessões e gates no laço

- **Contexto:** overview.md diz que sessões e gates rodam em goroutines próprias e devolvem o resultado como entrada.
- **Hoje:** com `max_parallel: 1` na v0.1, o laço roda a sessão e os gates dentro do executor do outbox, sem goroutine: enquanto uma sessão roda, labels e comentários de outros nós esperam. Com o workspace fora de `running`, o laço não inicia sessão nem gate, mas labels, comentários e notificações continuam saindo.
- **Pergunta:** nenhuma para a v0.1; registrado para quem ligar o paralelismo.

### Q50 · Etapa que falha sem a triagem

- **Contexto:** `CauseGuard`, `CauseMaxTurns`, `CauseRefusal` e `CauseNoResult` "falham a etapa", e quem decide depois é a triagem, que ainda não existe; idem para `status: failed` do implementer, as repetições esgotadas de `CauseRateLimit`/`CauseRuntime` e o `worktree.setup` que continua falhando.
- **Hoje:** todos levam a `blocked` (`triage`), com notificação e o motivo na segunda linha do comentário. `CauseAuth` leva o nó a `blocked` (`paused`) e o workspace a `paused`; `CauseBudget` e o orçamento do nó esgotado antes de uma sessão levam a `blocked` (`budget`). A conferência pós-sessão que encontra adulteração leva a `failed`. Ainda não há como sair de `blocked`: o desbloqueio (pela Inbox, pela retomada do workspace ou pela triagem) fica para quem fizer esses caminhos, e então a etapa atual roda de novo.
- **Pergunta:** confirmar até a triagem existir; e decidir se retomar o workspace desbloqueia os nós `paused` sozinho.

### Q51 · Tentativa nova: worktree e fatos da tentativa anterior

- **Contexto:** uma tentativa nova (gate completo vermelho) é "sessão nova com o contexto do erro" e `Previous attempt (facts)` "gerado do journal".
- **Hoje:** a tentativa nova apaga a branch do nó e recria o worktree da ponta da milestone (o trabalho da tentativa anterior fica só no journal). Os fatos são os gates que falharam e o número de findings abertos. A lista `Verify` do prompt traz todos os gates com `run` do `kluzo.yaml` da ponta da milestone. Um gate com estado `error` conta como falha; um erro do port (o gate não rodou) repete o efeito.
- **Pergunta:** a tentativa nova deve partir do trabalho anterior (rebase na ponta) em vez do zero? Quais fatos o journal deve dar?
