# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.


## Item 5 · Grafo a partir do plano e das issues

### Q22 · Arestas `contract` na v0.1

- **Contexto:** o aceite do item 5 fala em "só com arestas de bloqueio"; `plan.md` define `contract_on` e o `Contrato:` da descrição, e `core.Graph` já resolve arestas `contract`.
- **Hoje:** `core.GraphFromPlan` monta as arestas `blocks` e `contract`, e compara as duas com o tracker do mesmo jeito (a divergência de uma aresta `contract` também é acusada).
- **Pergunta:** na v0.1 `contract_on` deve ser recusado (com mensagem) ou o comportamento atual está certo?

### Q23 · Issue fechada e estado inicial do nó

- **Contexto:** `plan.md` exige que "toda issue da milestone" esteja no plano ou em `excluded: true`; `MilestoneIssues` devolve também as fechadas.
- **Hoje:** uma issue fechada precisa estar no plano ou ser marcada `excluded: true` como qualquer outra, e todo nó nasce `planned`.
- **Pergunta:** uma issue já fechada deve ser tratada como `excluded` implicitamente, ou virar nó já `integrated`?

### Q24 · Dependência de uma issue `excluded`

- **Hoje:** uma aresta (no plano ou no tracker) vinda de uma issue `excluded: true`, ou de uma issue da milestone que não é nó do plano, é recusada com explicação. Um link do tracker que tem a issue excluída como destino é ignorado.
- **Pergunta:** isso é o desejado, ou uma issue excluída que já está pronta pode bloquear nós?

### Q25 · Áreas do plano e `kluzo.yaml`

- **Contexto:** o ADR 0011 diz que as áreas são definidas em `kluzo.yaml`; `plan.md` não diz se uma área do plano ausente de `areas:` é erro.
- **Hoje:** só se exige que cada nó tenha ao menos uma área (`shared` vale). Nomes de área não são conferidos contra `kluzo.yaml`.
- **Pergunta:** área desconhecida é erro?

### Q26 · `overrides` do nó no plano

- **Contexto:** `plan.md` mostra `overrides: { implementer: { model: sonnet, advisor: opus } }`, mas nenhum contrato define os papéis e as chaves aceitas.
- **Hoje:** o arquivo é lido como `map[papel]map[chave]valor` sem conferência; a precedência é aplicada onde o papel efetivo é calculado.
- **Pergunta:** quais chaves valem (`harness`, `model`, `advisor`, `fallback`…) e o papel precisa existir em `roles/`?

### Q27 · Ordem dos ids no desempate do scheduler

- **Contexto:** as referências são opacas para o núcleo (`interfaces.md`) e `Graph.ids()` ordena como texto, então o desempate "menor id" escolhe `10` antes de `9`.
- **Hoje:** mantido como está.
- **Pergunta:** o desempate deve comparar números quando as duas referências forem numéricas (ordem de criação no tracker), ou a ordem textual basta?
