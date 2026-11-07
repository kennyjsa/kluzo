# Padrões de projeto

Os padrões adotados no Kluzo, onde cada um se aplica e o que fica proibido. Um padrão novo, ou o abandono de um destes, exige um ADR.

## 1. Ports & adapters (hexagonal)

- **Onde:** fronteira entre o domínio e o mundo externo (tracker, git, harnesses, notificações). ADR 0012.
- **Como:**
  - As interfaces ficam em `internal/ports`.
  - Os adaptadores implementam essas interfaces e não conhecem o domínio.
  - Cada port tem um fake em memória e uma suíte de contrato (`ports/contracttest`), que roda igual contra o fake e contra cada adaptador real.
- **Proibido:** o núcleo importar um adaptador; um adaptador tomar decisões de domínio. Exemplo: o adaptador do GitHub decide quando aplicar `orch/blocked` em vez de apenas aplicar o que recebeu.

## 2. Núcleo funcional, casca imperativa

- **Onde:** `internal/core` (núcleo) e `internal/supervisor` (casca). ADR 0012.
- **Como:** `core.Apply(state, input) → (state, []Effect, error)`. Não há I/O no núcleo. O relógio e os ids chegam dentro da entrada. A casca executa os efeitos.
- **Por quê:** toda transição vira um teste de tabela, sem rede e sem LLM. A retomada depois de um crash é trivial.
- **Proibido:** `time.Now()`, `rand`, `os`, `net` e `exec` dentro de `core`.

## 3. Máquina de estados explícita

- **Onde:** workspace e nó ([contracts/state-machine.md](../contracts/state-machine.md)).
- **Como:**
  - Os estados são tipos fechados, um `type NodeState string` com constantes.
  - As transições ficam numa tabela única no código, `transitions[from][input] → to`, espelhando o contrato.
  - Uma transição fora da tabela retorna `ErrInvalidTransition`.
  - Um teste confere que o código e a tabela do contrato não divergem.
- **Proibido:** mudar o estado com atribuição direta fora da máquina.

## 4. Journal como fonte da verdade (event sourcing leve)

- **Onde:** `internal/store`. ADR 0013.
- **Como:**
  - Cada transição grava eventos no journal.
  - As tabelas de estado são projeções atualizadas na mesma transação.
  - `store.Rebuild()` reconstrói as projeções a partir do journal, e há um teste garantindo que dá o mesmo resultado.
- **Diferença do event sourcing completo:** o estado corrente é lido das projeções, não reconstruído a cada leitura. Também não há snapshots nem versionamento de agregados.
- **Proibido:** apagar ou alterar eventos; escrever numa projeção sem gravar o evento.

## 5. Outbox de efeitos idempotentes

- **Onde:** fronteira entre `supervisor` e `ports`. ADR 0013.
- **Como:**
  - Os efeitos decididos pelo núcleo vão para a tabela `effects`, na mesma transação dos eventos.
  - Um executor processa os pendentes e marca cada um como concluído.
  - Cada efeito tem `idem_key`, que os adaptadores usam para não duplicar nada: o comentário carrega um marcador oculto, e o PR é procurado pela branch antes de ser criado.
- **Proibido:** chamar um port direto do caso de uso sem passar pelo outbox, quando a chamada muda o mundo externo.

## 6. Casos de uso como comandos

- **Onde:** `internal/app`.
- **Como:** um tipo por operação (`ApprovePlan`, `AnswerQuestion`…), com `Execute(ctx, input) (output, error)`. A CLI e a web chamam os mesmos comandos.
- **Proibido:** regra de negócio em handler HTTP ou em comando Cobra.

## 7. Injeção de dependência manual

- **Onde:** composição em `internal/app` (montagem) e `cmd/kluzo` (main).
- **Como:** construtores recebem as interfaces de que precisam (`NewSupervisor(store, tracker, runtimes, clock)`), e não há framework.
- **Proibido:** contêiner de DI, singletons, variáveis globais mutáveis, `init()` com efeito colateral.

## 8. Fakes em vez de mocks

- **Onde:** testes.
- **Como:**
  - Fakes em memória com o comportamento real do contrato (`ports/fakes`).
  - Runtimes roteirizados, que devolvem resultados por papel e tentativa.
  - Relógio controlável.
- **Proibido:** bibliotecas de mock que verificam a sequência de chamadas. Elas testam a implementação, não o comportamento.

## 9. Validação na borda, tipos fortes no núcleo

- **Onde:** `internal/config` (`.kluzo/`), `internal/contracts` (saídas dos papéis, MCP) e `internal/web` (requisições).
- **Como:** tudo o que entra é validado (JSON Schema, regras de `config`) e convertido em tipos Go. Dentro do núcleo, os dados já são válidos.
- **Proibido:** `map[string]any` circulando no domínio.

## 10. Erros como valores, com contexto

- **Como:**
  - Erros de domínio são variáveis sentinela ou tipos em `core` (`ErrInvalidTransition`, `ErrCycle`, `ErrBudgetExceeded`).
  - Ao subir de camada, o erro é embrulhado com `fmt.Errorf("…: %w", err)`.
  - O usuário vê uma mensagem acionável, que diz o que fazer.
- **Proibido:** `panic` fora de `main` e de invariantes impossíveis; engolir erro.

## 11. Concorrência por propriedade

- **Como:**
  - Um workspace pertence a uma goroutine, o laço do supervisor. As outras goroutines (sessões, gates) se comunicam com ela por canais.
  - Cancelamento via `context.Context` em toda operação que bloqueia.
- **Proibido:** estado compartilhado protegido por mutex entre workspaces; goroutine sem dono que não termina com o contexto.

## 12. Observabilidade estruturada

- **Como:**
  - `log/slog` com atributos fixos (`workspace`, `node`, `session`, `effect`).
  - O journal é a trilha de auditoria; o log é para depuração.
  - Os segredos são removidos antes de gravar (`platform/redact`).
- **Proibido:** log com segredo, log como única fonte de um fato de negócio.

## Frontend

| Padrão | Aplicação |
|---|---|
| Snapshot + eventos | A store carrega o snapshot (`GET`) e aplica os eventos do SSE em ordem de `seq`. Na reconexão, retoma por `Last-Event-ID`. |
| Componentes de apresentação × de feature | `components/` não conhece a API. Só `features/` liga dados a componentes. |
| Mobile-first no CSS | Os estilos base são os do celular; `md:` e `lg:` expandem. Nada é desenhado primeiro para o desktop. |
| Acessibilidade como requisito | Elementos nativos (`button`, `a`, `label`), alvos de toque de no mínimo 44 px, contraste AA. O estado nunca é indicado só por cor: a tarja tem texto e símbolo. |
