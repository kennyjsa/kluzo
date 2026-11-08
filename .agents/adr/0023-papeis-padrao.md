# 0023. Papéis padrão do implementer e do reviewer, com veredito derivado pelo Kluzo

- Status: aceito
- Data: 2026-11-08
- Origem: investigação 2 ([docs/research/role-prompts.md](../../docs/research/role-prompts.md))

## Contexto

O `kluzo init` precisa gerar papéis padrão que funcionem em qualquer projeto. Os dois erros que mais custam num laço implementer ⇄ reviewer são:
- **aprovar demais:** evidência que ninguém confere, veredito que contradiz os próprios findings, injeção vinda da issue ou do relatório do implementer;
- **devolver demais:** findings de estilo, critério que muda a cada rodada, contestações sem regra, rodadas que acabam sem saída definida.

Os projetos lidos (polyphony, symphony, autonomous-dev-team, bernstein, orca, delega) resolvem parte disso no texto do prompt. A validação adversária mostrou que o texto sozinho não basta.

## Decisão

1. **Papéis curtos e genéricos**, em inglês, sem comandos nem convenções de projeto. Eles fixam o processo e a entrega e dizem explicitamente que o "como" é das regras do projeto (`AGENTS.md`, skills). Os textos normativos ficam em [docs/contracts/roles/](../../docs/contracts/roles/). Na v0.1 eles só citam `kluzo_result`.
2. **Critérios com id.** O Kluzo numera os critérios de aceite (`AC1..n`) a partir do plano. Implementer e reviewer cobrem exatamente esse conjunto, com evidência por critério.
3. **O veredito é derivado em código.** O reviewer relata critérios, findings e o estado dos findings anteriores. O Kluzo decide: `approve` só sem `blocking` ou `major` aberto e com todos os critérios atendidos. Na última rodada, um `major` vira `gap` registrado; um `blocking` leva o nó a `blocked` (`triage`).
4. **Findings rastreáveis.** Cada finding recebe do Kluzo um id estável por nó (`F1…`) e precisa de um `check` concreto. Da rodada 2 em diante:
   - o implementer responde a cada id com `fixed` ou `contested`;
   - o reviewer marca cada id anterior como `resolved` ou `open`;
   - um finding novo fora dos trechos alterados na rodada exige `caused_by`, senão é rebaixado para `minor` (exceto `security`).
   Os findings automáticos do ADR 0020 não são contestáveis.
5. **Verificação mecânica da evidência.** Na v0.1, `caminho:linha` precisa existir no worktree. Na v0.2, um teste citado precisa constar como executado e aprovado no relatório estruturado do gate `test`.
6. **Saída de outro papel é dado não confiável** e vai envelopada, como texto do tracker.
7. **Contadores separados.** Falhas de gate rápido têm `max_retries` próprio (padrão 3) e não consomem as rodadas de review.
8. **Verificação local com os comandos certos.** A seção `Verify` do segmento 3 traz os comandos dos gates congelados, para o implementer não adivinhar.
9. **Avaliação estatística.** Os cenários de papel no `make eval` rodam N vezes e são medidos por taxa de falso positivo e falso negativo.

## Consequências

- Muda `results.md` (o `review-result` perde `verdict` e ganha `previous`, `check`, `caused_by`, `auto`; o `node-result` ganha `criteria` e `responses`), `prompt.md`, `state-machine.md` e `config.md`.
- O núcleo ganha regras puras para derivar o veredito, atribuir ids e validar a cobertura de `criteria`, `responses` e `previous`, todas testáveis por tabela.
- Uma contestação aceita fica registrada e é contada no relatório da milestone, para o humano ver quantos findings foram resolvidos por argumento e não por código.
- Ficam para depois: a verificação de nome de teste no relatório do gate (v0.2) e a heurística de reabertura (finding novo com mesmo arquivo e categoria de um resolvido).
