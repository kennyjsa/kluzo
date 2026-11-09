# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.


## Item 9 · Branch da milestone e merge

### Q56 · Onde fica o PR `milestone → main`

- **Contexto:** o fechamento abre o PR e, depois, `IsMerged` observa o merge humano; para isso o Kluzo precisa guardar a referência do PR. events.md não tem evento para o PR aberto, e data-model.md só tem `workspace.flow_cursor` ("posição nos flows upstream/closing").
- **Hoje:** o efeito `scm.open_pr` grava o evento `pr.opened` `{ref, url}`, projetado em `workspace.flow_cursor` como `{"pr": {...}}`, na mesma transação de `closing → ready_to_merge`. O supervisor consulta `IsMerged` a cada `tracker.poll_interval` (30 s por padrão) e leva o workspace a `merged`.
- **Pergunta:** `pr.opened` entra em events.md (e `flow_cursor` em data-model.md) assim, ou o PR deve ficar noutro lugar?

### Q57 · Label `kluzo.node` e falha do daemon Docker na limpeza

- **Contexto:** ADR 0025 diz que imagens dos gates "levam as labels" `kluzo.workspace` e `kluzo.node`, sem dizer o valor de `kluzo.node`; e só prevê pular a limpeza de Docker quando não há Docker na máquina.
- **Hoje:** a limpeza do nó remove as imagens com `kluzo.workspace=<ws>` e `kluzo.node=<nd_…>` (o id do nó, não a issue). Sem CLI `docker`, a limpeza de Docker é pulada com aviso; com a CLI e o daemon parado (Docker Desktop fechado), o efeito falha e é repetido pela política do outbox, até `effect.failed` com notificação.
- **Pergunta:** `kluzo.node` é o id do nó? Daemon parado deve ser tratado como "sem Docker" (pular com aviso)?

### Q58 · Branch da milestone divergente e despacho

- **Contexto:** a criação do workspace cria ou adota `milestone/<nome>` (`git.milestone`). Se a branch local e a remota divergiram, uma pessoa precisa reconciliar.
- **Hoje:** `git.milestone` falha na hora (`effect.failed`, notificação), mas o despacho não espera por ele: o nó roda sobre a branch local e para em `integrating`, quando o rebase encontra a mesma divergência. Nada chega ao remoto.
- **Pergunta:** o scheduler deve deixar de despachar enquanto `git.milestone` não terminou?

### Q59 · Branch remota do nó depois da integração

- **Contexto:** a branch do nó vai para o remoto antes de um gate `source: tracker`. ADR 0025 só manda remover a branch local do nó integrado ou cancelado.
- **Hoje:** a branch remota `issue/<n>-…` fica no remoto depois da integração.
- **Pergunta:** a limpeza do nó também apaga a branch remota (push `:refs/heads/<branch>`)?
