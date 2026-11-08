# Desenvolvimento

Como compilar, testar e contribuir com o Kluzo. Vale para humanos e agentes.

## Requisitos

- Go (a versão fica fixada em `go.mod`).
- `git` 2.38 ou mais novo (worktrees e `merge --no-ff`).
- Node LTS, só para desenvolver a web (`web/`).
- `golangci-lint`.
- Para os testes de integração: `claude` e `opencode` instalados e autenticados, e um token de teste do GitHub.

## Comandos

| Comando | O que faz |
|---|---|
| `make build` | Compila a web (se existir) e o binário em `bin/kluzo` |
| `make test` | Testes unitários (`go test ./...`), sem rede e sem LLM |
| `make lint` | `golangci-lint run` e `go vet` |
| `make arch` | Teste de regras de dependência (`internal/archtest`) |
| `make test-integration` | Testes com GitHub real e harness real (tag `integration`); não roda no CI de PR |
| `make web-dev` | Vite em modo dev apontando para um Kluzo local em `--listen` |
| `make vuln` | `govulncheck ./...` (e `npm audit` na web); roda no CI |
| `make dist` | Binários para Linux e macOS (amd64 e arm64) em `dist/` |
| `make check` | `lint` + `arch` + `test`; o CI roda isso, mais `vuln` e `dist` |

## Testes

### Pirâmide

1. **Núcleo como tabela de casos** (`internal/core`). A maior parte dos testes fica aqui. Cada linha da tabela de transições de [state-machine.md](contracts/state-machine.md) tem pelo menos um caso válido, e há casos para as transições proibidas. Exemplo:
   ```go
   {name: "review rejeita dentro do limite",
    state: nodeIn(InReview, round(1)), input: reviewResult(RequestChanges),
    want: nodeIn(Running, round(2)), effects: []Effect{StartSession("implementer")}},
   ```
2. **Supervisor com fakes** (`internal/supervisor`). Uma milestone inteira roda contra `ports/fakes`, com runtimes roteirizados: o fake devolve resultados pré-definidos por papel e tentativa. Cobre crash e retomada: o processo é morto em pontos sorteados e o teste verifica que o resultado final é o mesmo.
3. **Golden files do journal.** Um cenário roteirizado gera a sequência de eventos, que é comparada com `testdata/*.golden.jsonl`. Para atualizar: `go test ./... -update`.
4. **Contrato dos adaptadores.** A mesma suíte (`ports/contracttest`) roda contra o fake e contra cada adaptador real. Isso garante que o fake se comporta como o GitHub e o GitLab, por exemplo na idempotência e na regra de uma label `orch/*` por issue.
5. **Normalização de eventos dos runtimes.** Streams reais gravados em `testdata/streams/` passam pelo adaptador, e a saída é comparada com golden files.
6. **Integração** (tag `integration`). Um repositório de teste no GitHub e uma milestone de 2 ou 3 nós triviais, executada de verdade. Roda manualmente e antes de cada release.

### Avaliações de prompt

Os testes golden garantem que o texto do prompt não mudou sem revisão, mas não dizem se ele funciona. Para isso existe `make eval` (ADR 0019), que não roda no CI de PR, mas sim antes de cada release e quando um prompt ou papel padrão muda:
- **Repositórios-fixture** em `testdata/eval/`, cada um com uma milestone pequena e um resultado esperado verificável:
  - issue simples;
  - issue com injeção de prompt no corpo;
  - lint "tentador de silenciar";
  - teste que falha por um motivo sutil;
  - diff grande para o reviewer.
- **O que se mede, por cenário e por adaptador/modelo:**
  - critério de aceite atendido;
  - nenhuma violação de caminho protegido;
  - instrução injetada não seguida;
  - tentativas, turnos e custo.
- **Resultados** em `testdata/eval/results/<kluzo-version>/`, comparados com a versão anterior. Regressão em segurança bloqueia o release.

### Regras

- Os testes unitários não usam rede, LLM nem relógio real (o relógio é injetado). Também não usam `time.Sleep`.
- Bug corrigido ganha um teste que falhava antes da correção.
- A cobertura do `internal/core` deve ficar acima de 90%. Nos outros pacotes não há meta numérica.

## Convenções

- **Idioma:** código, identificadores, logs, erros e commits em inglês. Documentação em português (ADR 0008).
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org/) (`feat(core): …`, `fix(github): …`, `docs: …`). O escopo é o pacote ou a área.
- **Branches:** seguem o mesmo fluxo que o Kluzo impõe aos projetos (ADR 0003):
  - `issue/<n>-<slug>`, a partir da branch da milestone em andamento (`milestone/v0.X`), integrada nela por merge commit (ADR 0009).
  - **Até o dogfooding (v0.1, item 11),** os itens da v0.1 ainda não são issues: a branch é `item/v0.1-<nn>-<slug>`.
  - A branch da milestone vai para a `main` por PR, com merge humano.
  - Mudanças só de documentação podem ir direto para a `main`.
- **Erros:** com contexto (`fmt.Errorf("approve plan %s: %w", id, err)`). Erros de domínio são tipados em `core` (`ErrInvalidTransition`, `ErrCycle`…).
- **Logs:** `log/slog` estruturado, com `workspace`, `node` e `session` como atributos quando houver.
- **Configuração:** nenhuma chave nova em `kluzo.yaml` sem atualizar [contracts/config.md](contracts/config.md) no mesmo PR.
- **Padrões:** os padrões de projeto e o que é proibido estão em [architecture/patterns.md](architecture/patterns.md).

## Definition of Done de um PR

- [ ] Os critérios de aceite do item da milestone estão atendidos, e cada um tem um teste ou uma verificação descrita no PR.
- [ ] `make check` verde.
- [ ] Nenhum código duplicado e nenhum código morto. É critério bloqueante, não observação.
- [ ] Toda transição nova ou alterada tem caso na tabela de testes do núcleo.
- [ ] Os contratos (`docs/contracts/`) estão atualizados se algum formato ou interface mudou.
- [ ] Decisão de arquitetura nova tem ADR.
- [ ] O status da feature foi atualizado em `docs/product/features/`, quando for o caso.
- [ ] Dependência nova segue o ADR 0017 e está em `docs/architecture/stack.md`.
- [ ] Mudança de tela: confere com o protótipo nas larguras 390, 768 e 1280 px.

## Estrutura do repositório

```
kluzo/
├── cmd/kluzo/
├── internal/          # ver architecture/packages.md
├── web/               # Vue (fonte) → web/dist (embutido)
├── testdata/
├── docs/
├── .kluzo/            # configuração usada para o Kluzo orquestrar a si mesmo (v0.1, item 11)
├── AGENTS.md
├── Makefile
└── go.mod
```
