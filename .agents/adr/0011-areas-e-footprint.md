# 0011. Áreas declaradas no plano e conferidas pelo diff

- Status: aceito
- Data: 2026-11-07

## Contexto

As arestas de conflito dependem de saber o que cada nó vai tocar (o footprint). Declarar à mão é impreciso; inferir só do código chega tarde, depois que o conflito já aconteceu.

## Decisão

- **Área** é um nome curto (`agenda`, `auth`) definido em `kluzo.yaml`, com uma lista de globs de caminho.
- O planner atribui as áreas de cada nó, que viram labels `area/<nome>` e entram no plano. Nós com área em comum recebem aresta de conflito e são serializados.
- Antes de entrar na fila de merge, o Kluzo compara o diff do nó com os globs das áreas declaradas. Se o diff tocou uma área não declarada, ele:
  1. registra o desvio no journal;
  2. adiciona a área ao nó;
  3. se existe um nó em execução naquela área, o nó atual espera na fila até o outro ser integrado.
- Arquivos que não casam com nenhuma área caem na área implícita `shared`, que não gera conflito.

## Consequências

- O conflito é evitado na maior parte dos casos pelo plano, e corrigido nos casos restantes antes do merge.
- O projeto precisa manter as áreas em `kluzo.yaml`. `kluzo init` sugere áreas a partir dos diretórios de primeiro nível.
