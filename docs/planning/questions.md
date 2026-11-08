# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 4 · Adaptador GitHub

### Q14 · "Faz merge": o port `SCM` só observa

- **Contexto:** o aceite do item 4 e o enunciado dizem que o adaptador "faz merge", mas [interfaces.md](../contracts/interfaces.md) dá ao `SCM` só `IsMerged` ("só verifica se aconteceu; nunca executa"), e o ADR 0003 reserva o merge na `main` ao humano. O merge de cada nó na branch da milestone é local (`gitops`, `merge --no-ff`).
- **Hoje:** `SCM` é exatamente o de interfaces.md. O adaptador cria a branch, abre o PR (idempotente), consulta o PR e os checks e **observa** o merge. Não há operação de merge.
- **Pergunta:** o aceite deve passar a dizer "observa o merge"? Ou o `SCM` ganha uma operação de merge (para quê, já que o da milestone é local e o da `main` é humano)?

### Q15 · Tipos e semântica que interfaces.md não define

- **Contexto:** interfaces.md usa `Milestone`, `NewIssue`, `CommentRef`, `Comment`, `Change`, `NewPR`, `PR` e `EdgeKind` sem defini-los, e não diz o formato das referências nem dos cursores.
- **Hoje (em `internal/ports`):**
  - Referências (milestone, issue, PR) são os números do GitHub em decimal (`"42"`).
  - `Dependencies(issueRef)` devolve só as dependências **de entrada** (`ToRef == issueRef`), nativas primeiro, depois as da descrição.
  - O cursor de `CommentsSince` é o id do último comentário visto. O de `Changes` é o id do último evento. Cursor vazio em `Changes` devolve nenhuma mudança e um cursor posicionado no presente (o polling começa "agora").
  - `Change` cobre só `labeled` e `unlabeled`, com o autor (`Actor`), para o núcleo reverter `orch/*` de outros.
  - `ports.EdgeKind` repete os valores de `core.EdgeKind` (`blocks`, `contract`), porque `ports` não pode importar `core`.
  - Erros tipados: `ErrNotFound`, `ErrInvalid`, `ErrPermanent` e `IsPermanent`, para o executor do outbox decidir entre repetir e falhar (política em data-model.md).
- **Pergunta:** confirmar esses tipos e semânticas em interfaces.md? E `core.EdgeKind` deve virar alias de `ports.EdgeKind` (ou ir para `contracts`) para não haver dois tipos?

### Q16 · De onde vêm as credenciais

- **Contexto:** config.md define `auth.app_id_env` e `auth.key_file_env` para o `github_app`, mas nenhum campo para `auth.kind: token`, e não diz como achar o `installation_id` do App.
- **Hoje:** o adaptador recebe os valores já lidos (`Auth{Kind, Token, AppID, PrivateKey}`); quem lê o ambiente é a montagem em `app` (item 10). O `installation_id` é descoberto pela API (`GET /repos/{o}/{r}/installation`) com o JWT do App. Não há `token_env` em `kluzo.yaml`.
- **Pergunta:** qual é o nome da variável do token (campo `tracker.auth.token_env`, ou um nome fixo como `KLUZO_GH_TOKEN`)? Precisa entrar em config.md.

### Q17 · Sintaxe de "Depende de:" e "Contrato:" na descrição

- **Contexto:** interfaces.md e integrations.md citam as linhas `Depende de:` e `Contrato:` da descrição, mas não dão a sintaxe.
- **Hoje:** uma linha começando por `Depende de:` ou `Contrato:` (sem diferenciar maiúsculas) com referências `#n`, em qualquer quantidade por linha. `SetDependencies` grava as arestas de bloqueio como links nativos e reescreve **uma** linha `Contrato: #a, #b` (ou a remove). As linhas `Depende de:` nunca são escritas. Se a mesma aresta de bloqueio aparece nos dois lugares, vale a nativa.
- **Pergunta:** essa sintaxe serve? O Kluzo pode reescrever a linha `Contrato:` que um humano escreveu?

### Q18 · Formato do comentário por transição

- **Contexto:** state-machine.md diz que cada transição gera "um comentário curto: o estado novo, o motivo e um link", mas não define o texto. O aceite pede o formato "de integrations.md", que também não o traz.
- **Hoje:** o port recebe o corpo já montado; o adaptador só acrescenta, no fim, o marcador oculto `<!-- kluzo:idem=KEY -->` (e o remove ao ler). A chave de idempotência é a do efeito do outbox.
- **Pergunta:** qual é o template do comentário, e quem o monta (o `core`, ao emitir o efeito `tracker.comment`)?

### Q19 · Polling e ETag

- **Contexto:** o item pede "polling no intervalo configurado" e integrations.md pede polling com cursores e respeito ao rate limit. ETag não é pedido.
- **Hoje:** o adaptador só expõe leituras por cursor (`Changes`, `CommentsSince`); o laço a cada `poll_interval` é do supervisor (itens 5 e 7), que ainda não existe. O rate limit é tratado no transporte HTTP: espera até o reset quando a cota acaba, respeita `Retry-After`, tenta de novo até 3 vezes e devolve o erro se a espera passar de 15 min. Não há requisições condicionais (ETag).
- **Pergunta:** o laço de polling fica mesmo no supervisor? Vale usar ETag (`If-None-Match`) para baratear o polling de `Changes`?

### Q20 · Pendências do F23 fora do item 4

- **Contexto:** F23 cita o grafo em Mermaid na milestone, e security.md manda reverter labels `orch/*` de outros usuários.
- **Hoje:** não há operação para editar a descrição da milestone (o grafo) nem para saber o login do bot (o `Actor` das `Change` precisa ser comparado a ele). O adaptador não decide nada disso.
- **Pergunta:** quais métodos entram no `Tracker` (por exemplo `SetMilestoneDescription` e `Whoami`), e em que item?

### Q21 · `platform/redact` mínimo e versão do go-github

- **Contexto:** o item exige "nada de segredo em log, erro ou journal", e `platform/redact` ainda não existia.
- **Hoje:** `internal/platform/redact` mascara os valores de segredo conhecidos e os formatos comuns (tokens `gh*_`, `github_pat_`, JWT, chave PEM, `Bearer …`), e embrulha erros sem perder `errors.Is`. O adaptador passa todo erro por ele. O journal e o stream das sessões ainda não o usam (itens 6 e 7).
- **Go-github:** o `go.mod` está em `go 1.24`; as versões novas de `go-github` (v84 em diante) e `ghinstallation` (v2.18 em diante) exigem Go 1.25 ou 1.26. Fixei `go-github` v75.0.0 e `ghinstallation` v2.17.0, as mais novas que cabem no Go atual.
- **Pergunta:** subir o Go do projeto (e então as duas bibliotecas) ou ficar nestas versões até um motivo concreto?
