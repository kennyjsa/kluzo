# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 6 · Runtime Claude Code

### Q32 · Custo no meio do stream

- **Contexto:** o ADR 0024 (item 8) manda contar turnos e custo no stream e chamar `Cancel` ao estourar. O custo por `modelUsage` só aparece na mensagem `result`; as mensagens `assistant` trazem só tokens (`usage`), e o Kluzo não tem tabela de preços.
- **Hoje:** os turnos são contados por mensagem `assistant` (ids distintos) e cancelam a sessão ao passar de `max_turns`. O custo é somado do `modelUsage` no `result`, e quem estoura o orçamento durante a sessão é o `--max-budget-usd` (que recebe o restante do nó). O `Result.CostUSD` vale para o orçamento do nó, e o `error_max_budget_usd` vira `CauseBudget`.
- **Pergunta:** aceitar o `--max-budget-usd` como a barreira de custo no meio da sessão, ou o Kluzo deve manter uma tabela de preços por modelo para estimar e cancelar sozinho?

### Q33 · Prompt do sistema na linha de comando

- **Contexto:** o ADR 0024 passa os segmentos 1, 2 e 2b em `--append-system-prompt`. O Linux limita um argumento a 128 KiB, e papel mais regras do projeto mais skills podem passar disso.
- **Hoje:** acima de 120 KiB, `Start` falha com `ErrPromptTooLarge`, sem iniciar nada. A nota de pesquisa lista "prompt grande pelo stdin" como verificação local.
- **Pergunta:** se o CLI não aceitar o prompt por arquivo ou stdin, qual é a saída: cortar as skills, ou mandar o excedente na primeira mensagem?

### Q34 · Chaves de `kluzo.yaml` que chegam antes dos consumidores

- **Contexto:** `config.md` ganhou `worktree`, `secret_paths`, `caches` e `retention`; os itens 7, 9 e 10 os consomem.
- **Hoje:** o loader aceita e valida a forma (globs, caminhos limpos, ecossistemas conhecidos, `90d` e `20GB`). `secret_paths` e `caches` já têm uso (hook, ambiente da sessão); `worktree` e `retention` só são lidos, sem consumidor ainda.
- **Pergunta:** nenhuma; registrado para o revisor saber que os campos existem antes do código que os usa.

### Q35 · `HarnessRef` é opaco e carrega a impressão digital

- **Contexto:** `Resume(ctx, harnessRef, spec)` precisa saber se segmentos, cwd, `HOME` e argv são os mesmos, e a interface não passa os hashes da sessão anterior.
- **Hoje:** o `HarnessRef` do claude-code é `claude-code/<uuid>/<impressão digital>`, e a impressão digital cobre os hashes dos segmentos, o cwd, o `HOME` e o argv sem `--session-id`, `--resume`, `--max-turns` e `--max-budget-usd` (limites que mudam com o saldo do nó). O supervisor guarda a referência como string opaca.
- **Pergunta:** confirmar que os limites ficam fora da comparação, ou eles também invalidam a retomada.

### Q36 · Campos extras no contrato do runtime

- **Contexto:** `interfaces.md` não tinha o token do MCP, o contador de sessões, as versões dos segmentos, os comandos `Verify` nem o `kluzo_result` aceito; o `SessionSpec` ganhou esses campos, e o `OutputSchema` saiu (o servidor MCP valida a saída). O `Result` ganhou `Cause`, `Turns`, `Unknown` e `Version`. O `session.started` ganhou `argv` (a linha de comando congelada, com o prompt como hash), que o supervisor copia para o manifesto.
- **Pergunta:** aprovar os campos, ou prefere outro desenho para o argv no manifesto (hoje o prompt.md não prevê o campo).
