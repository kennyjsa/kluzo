# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 2 · Configuração `.kluzo/`

### Q10 · `kluzo roles check` e a regra do `phase: in_review`

- **Contexto:** [config.md](../contracts/config.md) lista a regra "papel usado em etapa `in_review` não tem `edit` nem `bash`" entre as validações que valem também em `kluzo roles check`, mas diz que ela é checada ao carregar os flows.
- **Hoje:** `roles check` valida só `kluzo.yaml` e os papéis (`LoadRoles`), sem olhar os flows; por isso ele **não** acusa um reviewer com `edit`/`bash`. `Load` (usado por `run`/`doctor`) acusa.
- **Pergunta:** `roles check` deve carregar também os flows (e então reportar erros de flow), ou carregar só para aplicar essa regra?

## Item 3 · Estado e journal em SQLite

### Q11 · Eventos de criação do workspace e do nó

- **Contexto:** o journal é a fonte da verdade e as projeções devem poder ser reconstruídas dele ([data-model.md](../contracts/data-model.md)), mas [events.md](../contracts/events.md) não tem evento que traga os dados iniciais do workspace (`id`, `branch`, `base_sha`, `profile`, orçamento) nem do nó (`issue_ref`, `title`, `areas`, `budget`). `workspace.transition` e `node.transition` só carregam `from`/`to`.
- **Hoje:** `internal/contracts/events.go` define `workspace.created` e `node.added`, com esses campos. O workspace nasce com estado `""` (`WorkspaceNone`) e a transição `create` o leva a `refining`; o nó nasce `planned`. `merge.done` atualiza `nodes.merged_sha` e `workspace.head_sha`, e `edge.added` preenche `edges`. Os demais `kind` só entram no journal, sem projeção, até que um item precise deles.
- **Pergunta:** esses dois `kind` entram em events.md? Ou os dados iniciais vêm em outro evento (por exemplo `plan.approved` para os nós)?

### Q12 · Política do outbox: tentativas, backoff e `failed`

- **Contexto:** data-model.md define `attempts`, `next_at`, `last_error` e os estados `pending | done | failed`, mas não diz quando um efeito vira `failed` nem como se calcula o backoff.
- **Hoje:** o store só oferece as operações (`CompleteEffect`, `RetryEffect` com `next_at`, `FailEffect`); `attempts` sobe a cada uma delas. Quem decide o número máximo de tentativas e o backoff é o executor (supervisor), ainda não implementado. Um efeito só sai de `pending` por essas operações.
- **Pergunta:** qual é a política (máximo de tentativas, curva de backoff) e quem a aplica?

### Q13 · `prev_state` de um nó que falha ou é cancelado estando `blocked`

- **Contexto:** `core.NextNode` mantém `PrevState` preenchido quando um nó `blocked` vai para `failed` ou `cancelled`; o estado de destino é final, então o valor não é usado.
- **Hoje:** a projeção só guarda `prev_state` enquanto o nó está `blocked` (limpa nas demais transições), para que a reconstrução a partir do journal dê o mesmo resultado sem acrescentar campo ao evento.
- **Pergunta:** vale alinhar o `core` (limpar `PrevState` ao sair de `blocked`)? Não muda nenhum comportamento observável.
