# Plano da milestone

O plano é o grafo aprovado de uma milestone. Ele é gerado pelo planner (v0.3) ou escrito à mão (v0.1), e na aprovação vai como commit para `.kluzo/plans/<milestone>.yaml`, na branch da milestone. A cada mudança aprovada durante a execução (proposta aceita, nó dividido), `version` sobe e o arquivo ganha um novo commit.

Quem aprova é um dos `tracker.operators`: o `kluzo approve --by <login>` exige um login da lista, e sem `--by` usa o único operador quando a lista tem um só; identidade vazia ou fora da lista é recusada. `approved_by` e `approved_at` são preenchidos pelo Kluzo no conteúdo que vai para o commit, com os mesmos valores do evento `plan.approved`; no plano escrito à mão eles ficam ausentes. A conferência do plano ignora esses dois campos.

```yaml
version: 1                      # versão do formato
plan_version: 3                 # versão deste plano (sobe a cada mudança aprovada)
milestone: v2.0                 # título da milestone no tracker
approved_by: kennyjsa
approved_at: 2026-11-05T14:02:11.000Z
profile: default

budget:
  usd: 38

nodes:
  - issue: 41
    title: Schema da agenda
    areas: [agenda]
  - issue: 43
    title: Contrato OpenAPI da agenda
    areas: [api-contract]
    publishes_contract: openapi/agenda.yaml   # libera arestas 'contract'
  - issue: 42
    title: API de agendamentos
    areas: [agenda]
    blocks_on: [41]             # arestas 'blocks' (também lidas do tracker)
    acceptance:                 # numerados AC1, AC2… nesta ordem (ADR 0023)
      - Não permite dois agendamentos no mesmo slot
      - Remarcar para um slot ocupado falha com conflito
    subtasks:
      - Modelo e repositório
      - Checagem de conflito de horário
      - Endpoints de criação e listagem
      - Cancelamento e remarcação
      - Testes de contrato com o #43
    budget: { usd: 6 }
    overrides: { implementer: { model: sonnet, effort: high } }
  - issue: 44
    title: Tela de agenda
    areas: [web-agenda]
    contract_on: [43]           # arestas 'contract'
  - issue: 46
    title: Permissões na agenda
    areas: [agenda-acl]
    contract_on: [44]
  - issue: 45
    title: Lembrete por WhatsApp
    areas: [agenda]
    blocks_on: [42]
  - issue: 47
    title: E2E da agenda
    areas: [e2e]
    blocks_on: [45, 46]

new_issues: []                  # issues criadas na aprovação, já com número
```

Regras:
- Todo nó tem ao menos um critério em `acceptance`. Os ids `AC1..n` seguem a ordem da lista e ficam estáveis enquanto o `plan_version` não mudar. Na v0.1, o plano escrito à mão copia os critérios da issue; a partir da v0.3, o planner os extrai.
- Toda issue **aberta** da milestone no tracker deve estar no plano, ou ser marcada como fora com `excluded: true`. Isso impede que uma issue seja esquecida. Uma issue fechada fica fora do plano sem precisar ser listada; um nó do plano que aponta para uma issue fechada é erro.
- Dependência de uma issue fechada conta como satisfeita, e a aresta é ignorada. Dependência de uma issue aberta marcada `excluded` é erro: ela nunca seria integrada.
- Toda área de um nó existe em `areas:` do `kluzo.yaml`, exceto `shared` (ADR 0011).
- `overrides` aceita, por papel, as chaves `harness`, `model`, `advisor` e `effort`, com os mesmos valores e validações do frontmatter do papel. O papel precisa existir em `roles/`.
- Issues pai com sub-issues na mesma milestone (por exemplo, funcionalidade e histórias) entram no plano com `excluded: true`; só as folhas viram nós. O agrupamento nativo por sub-issue chega com a visão Estrutura (F30, v0.3).
- Arestas `contract` valem desde a v0.1. Com o scheduler sequencial, elas só mudam a ordem de despacho; o ganho de paralelismo vem na v0.2.
- As arestas `blocks` vêm da união do plano com o tracker. Se houver divergência, a validação acusa, e a aprovação sincroniza o tracker com o plano.
- Uma aresta `contract` só libera o nó dependente quando o nó de origem publicou o contrato. Ou seja: o caminho em `publishes_contract` existe na branch da milestone, ou o nó de origem está `integrated`.
- As arestas `conflict` não são escritas no plano. Elas são derivadas das áreas (ADR 0011).
- O grafo de `blocks` + `contract` não pode ter ciclos.
- O caminho crítico é calculado pela soma das estimativas de tempo, ou pela contagem de nós quando não há estimativa. Ele é recalculado a cada transição.
