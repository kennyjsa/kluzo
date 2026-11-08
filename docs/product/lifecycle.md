# Ciclo de vida de uma milestone

```
upstream → aprovação → downstream (uma pipeline por nó) → fechamento → merge humano
              ▲__________ lacunas e falhas voltam como proposta __________|
```

Os estados e as transições exatas estão em [contracts/state-machine.md](../contracts/state-machine.md). Este documento descreve o comportamento.

## 1. Upstream: refinar

```
business → po → requirements → architect → planner ⇄ critic → [aprovação humana]
```

- `business`, `po` e `requirements` conduzem **entrevistas** com o humano, pelo chat da web.
- O `architect` registra ADRs e publica contratos (por exemplo, uma spec OpenAPI).
- O `planner` monta o grafo: nós, arestas, áreas, subtasks e estimativas.
- O `critic`, numa sessão separada, revisa o grafo procurando lacunas. O checklist cobre:
  - dados e migrations;
  - infra e configuração;
  - contratos entre front e back;
  - E2E;
  - observabilidade;
  - segurança;
  - rollout e rollback;
  - documentação.
- O laço planner ⇄ critic repete até o crítico não encontrar lacuna bloqueante, com limite de rodadas.

**Definition of Ready:**
- toda issue tem critérios de aceite, área, papel e dependências;
- o grafo não tem ciclos;
- o crítico não encontrou lacuna bloqueante.

## 2. Aprovação do plano

O humano revisa o plano **item a item** (nós novos, alterações, dependências) e aprova. Só então o Kluzo:
1. cria no tracker as issues e os links aprovados;
2. faz commit do plano em `.kluzo/plans/<milestone>.yaml`, na branch da milestone;
3. libera o downstream.

**Nada é criado no tracker antes da aprovação.**

## 3. Downstream: desenvolver

```
implementer → gates rápidos → reviewer → gates completos → fila de merge → integrado
     ▲___________|________|  (devolução com findings ou log)
```

- O **scheduler** despacha os nós prontos, com o paralelismo que o grafo permitir. Ele é determinístico e não chama LLM.
  - Um nó fica **pronto** quando as dependências de bloqueio estão **integradas**, e não apenas concluídas.
  - Nós com área em comum nunca rodam juntos.
  - O caminho crítico tem prioridade: com vagas limitadas, roda primeiro o que destrava mais trabalho.
- Cada nó roda num **worktree** que nasce da ponta da branch da milestone.
- Os **gates rápidos** (build, lint) rodam antes do review, para não gastar o reviewer com código que nem compila.
- O `reviewer` só lê: não edita nem executa código (ADR 0020). Ele pode devolver o nó ao `implementer`, com limite de rodadas.
- Os **gates** (testes, lint) rodam fora do agente. O resultado vem do comando ou do CI.
- Antes da fila de merge, o diff é conferido contra as áreas declaradas (ADR 0011) e contra os caminhos protegidos. Mexer em configuração de gate, lint, CI ou `.kluzo/`, ou apagar testes, vira proposta para o humano (ADR 0020).
- O downstream **nunca replaneja**. Lacunas viram **propostas**, que voltam para o humano:
  - Proposta que não bloqueia: o nó segue.
  - Proposta que bloqueia: só aquele nó espera.
- Em ambiguidade pequena, o agente **decide e registra** a decisão. Ele só **escala** (pergunta ao humano) o que muda escopo, contrato ou segurança.

### Falhas e orçamento

Cada nó tem orçamento de tentativas, tempo e custo. Quando um nó falha:
1. **Tabela de regras primeiro:**
   - teste instável → retry;
   - lint → volta ao implementer;
   - terceira falha → fallback de modelo ou de harness.
2. **Papel `triage`** só para o caso ambíguo. Ele responde `retry`, `switch_model`, `switch_harness`, `split` ou `escalate`.
3. **Orçamento estourado:** o nó fica bloqueado e entra na Inbox. O resto do grafo segue.

## 4. Integração na branch da milestone

1. **Início:** a branch `milestone/<nome>` é criada a partir da `main`, ou uma branch existente é adotada.
2. **Fila de merge:** um nó por vez. Rebase na ponta da milestone, depois o **freshness gate** (se a ponta mudou desde a review, os gates rodam de novo), depois `merge --no-ff` (ADR 0009). Em seguida, os dependentes são destravados.
3. **Sincronia com a `main`:** um hotfix que entra na `main` é trazido para a milestone entre duas integrações, nunca no meio de uma, e os gates rodam em seguida.
4. **Proteção recomendada:**
   - Em `milestone/*`, só o bot do Kluzo faz merge.
   - Na `main`, só humanos.

## 5. Fechamento

```
gate E2E → reporter → PR milestone → main → [merge humano]
```

O `reporter` redige o relatório a partir dos números do journal:
- nós entregues;
- decisões;
- lacunas que viraram issues;
- custo contra a estimativa;
- plano × entrega.

O relatório vai na descrição do PR.

**Definition of Done:** todos os nós integrados, todos os gates verdes, E2E verde e relatório publicado.

## 6. Esteira

Enquanto a milestone N está no downstream, a N+1 pode estar no upstream com o humano. Cada uma é um workspace próprio.
