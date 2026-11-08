# 0025. Recursos do workspace: caches compartilhados por projeto e limpeza determinística

- Status: aceito
- Data: 2026-11-08
- Origem: pergunta do Kenny sobre limpeza de worktrees, builds, containers e dependências repetidas

## Contexto

Uma milestone cria muita coisa fora do código: um worktree por nó, artefatos de build, dependências baixadas (`node_modules`, módulos Go, ambientes Python), containers, volumes e imagens Docker dos gates ou do próprio agente, e o `HOME` sintético das sessões (ADR 0024). Até aqui, o contrato só previa remover o worktree depois da integração. Sem regra:
- o disco cresce a cada nó e a cada milestone;
- as mesmas dependências são baixadas de novo em cada worktree;
- containers e volumes ficam vivos depois que o nó termina;
- uma limpeza "por esperteza" pode apagar coisas do usuário que o Kluzo não criou.

## Decisão

1. **Só se limpa o que o Kluzo consegue provar que criou.**
   - Tudo que é arquivo fica sob o diretório do workspace.
   - Containers, redes e volumes: sessões e gates recebem `COMPOSE_PROJECT_NAME=kluzo-<workspace>-<nó>`, e containers iniciados pelo próprio Kluzo levam as labels `kluzo.workspace` e `kluzo.node`. A limpeza remove só recursos com esse projeto ou essas labels.
   - O que não tem marca (um `docker run` solto feito pelo agente) não é apagado: o `kluzo doctor` lista como suspeito e o humano decide.
2. **Caches de download compartilhados por projeto**, fora dos worktrees, em `~/.local/share/kluzo/cache/<projeto>/`, injetados por variável de ambiente nas sessões e nos gates. `kluzo.yaml` declara os ecossistemas em `caches:` (`go`, `node`, `python`, `rust`, `java`), e cada um vira as variáveis certas (`GOMODCACHE`, `npm_config_cache` e a store do pnpm, `UV_CACHE_DIR`/`PIP_CACHE_DIR`, `CARGO_HOME`, `GRADLE_USER_HOME`/repositório Maven).
   - Só caches de download verificados por hash (módulos Go com `go.sum`, store do npm/pnpm com integridade, wheels) são compartilhados entre nós. Caches de build (`GOCACHE`, `target/`, `.next/`) ficam por workspace, porque um agente pode escrever neles e eles não são verificados por conteúdo.
   - `node_modules` continua por worktree; com pnpm, os arquivos vêm da store por hard link e não ocupam espaço repetido.
3. **Limpeza por ciclo de vida**, executada como efeito do outbox (idempotente e retomável):

   | Momento | O que sai | O que fica |
   |---|---|---|
   | Nó `integrated` ou `cancelled` | worktree (`git worktree remove` + `prune`), branch local do nó, `docker compose down -v` do projeto do nó, temporários do nó | logs e journal |
   | Nó `failed` | nada | worktree para diagnóstico, até o fechamento ou `kluzo clean` |
   | Workspace `merged`, `archived` ou `cancelled` | todos os worktrees, `HOME` sintético, recursos Docker marcados do workspace, caches de build do workspace | `state.db` e logs (o journal não se apaga), caches de download do projeto |
   | Retenção | logs de sessão e de gate mais antigos que `retention.logs` (padrão 90 dias) são compactados, depois removidos | `state.db` |

4. **Imagens Docker não são removidas por padrão**, porque baixar de novo custa mais que o disco. `kluzo clean --images` remove só imagens construídas pelo Kluzo (com label) e as pendentes dessas builds.
5. **Visibilidade:**
   - `kluzo status` mostra o uso de disco do workspace e dos caches do projeto.
   - `kluzo doctor` acusa worktrees órfãos (sem nó ativo no journal), recursos Docker marcados sem dono e uso de disco acima de `retention.disk_warn` (padrão 20 GB).
   - `kluzo clean` faz a limpeza sob demanda, com `--dry-run` como padrão.

## Consequências

- O disco de uma milestone é proporcional aos nós ativos, não ao total de nós.
- A segunda milestone de um projeto já encontra as dependências baixadas.
- Coisas criadas pelo agente fora das marcas do Kluzo continuam sendo responsabilidade do humano, mas aparecem no `doctor`.
- Compartilhar cache entre nós abre um caminho de envenenamento; restringir o compartilhamento a caches verificados por hash reduz esse risco sem eliminá-lo. O isolamento completo depende de container por sessão (ADR 0018).
