# Kluzo — Features

Catálogo de features para orientar o protótipo e o roadmap. Cada feature tem:
- **O quê:** o que ela faz.
- **Comportamento:** as regras e o fluxo.
- **Telas:** onde aparece na web (mobile-first).
- **Aceite:** os critérios para considerar a feature pronta.
- **Versão:** em qual versão do roadmap ela entra.

Referência de conceitos: [SPEC.md](SPEC.md).

## Mapa de navegação (mobile-first)

```
Inbox ◄──────────────────────────── (atalho global: o que precisa de você)
Projetos
 └── Projeto
      ├── Workspaces ──► Workspace
      │                   ├── Visão geral
      │                   ├── Progresso: [Grafo | Kanban]
      │                   ├── Plano
      │                   ├── Times (por flow, em execução)
      │                   ├── Fila de merge
      │                   ├── Decisões
      │                   └── Custos
      │                        └── Nó ──► Execução ao vivo
      ├── Times (por flow, configurados)
      └── Configuração
```

O layout é **responsivo e mobile-first**:
- **Celular:** a navegação principal fica numa barra inferior com quatro destinos (**Inbox · Projetos · Ativos · Mais**). Dentro de um workspace, as seções são abas roláveis no topo.
- **Tablet:** a barra vira um trilho lateral. Listas e detalhes ficam lado a lado.
- **Desktop:** barra lateral fixa. As visões de progresso ocupam a área principal, e o detalhe do nó abre num painel à direita sem sair da tela. O kanban mostra todas as colunas ao mesmo tempo.

Critério de aceite comum a todas as telas: funcionar em 390, 768 e 1280 px de largura, sem rolagem horizontal da página (só dentro de áreas próprias, como a linha do tempo da Estrutura).

---

## A. Navegação e organização

### F01 · Projetos
- **O quê:** a lista dos repositórios que o Kluzo gerencia.
- **Comportamento:**
  - Para adicionar um projeto, você informa o repositório (GitLab/GitHub) e o caminho local.
  - O Kluzo detecta o `.kluzo/` e valida a configuração.
  - Cada projeto mostra: a saúde da config, os workspaces ativos e a última atividade.
- **Telas:** lista de projetos (cards); página do projeto.
- **Aceite:** um projeto com config inválida aparece com o erro apontando o arquivo e a linha.
- **Versão:** v0.3 (na v0.1, um projeto via CLI).

### F02 · Workspaces
- **O quê:** uma sessão de trabalho sobre uma milestone.
- **Comportamento:**
  - Para criar, você escolhe a milestone do tracker. O Kluzo cria ou adota a branch `milestone/<nome>`.
  - Ciclo de vida: `refining → awaiting_approval → approved → running → closing → ready_to_merge → merged → archived`.
  - Ações: pausar, retomar, cancelar e arquivar.
  - Vários workspaces por projeto (esteira).
- **Telas:** lista de workspaces do projeto; "Ativos" global; visão geral do workspace.
- **Aceite:** um workspace pausado e retomado depois de reiniciar a máquina continua do último passo concluído.
- **Versão:** v0.1.

### F03 · Inbox
- **O quê:** a caixa única de tudo que precisa de você, em todos os projetos.
- **Comportamento:** reúne:
  - planos aguardando aprovação;
  - propostas de nó;
  - escalações (`kluzo_ask`);
  - entrevistas aguardando resposta;
  - milestones prontas para merge.

  Cada item resolve na hora ou leva à tela de contexto. Ordenação por bloqueio: o que trava mais trabalho vem primeiro.
- **Telas:** Inbox (tela inicial no celular).
- **Aceite:** todo item que bloqueia trabalho aparece na Inbox em até 5 segundos e gera notificação (F27).
- **Versão:** v0.3.

---

## B. Configuração

### F04 · Configuração `.kluzo/`
- **O quê:** `kluzo.yaml`, `roles/`, `flows/`, `schemas/` e `plans/`.
- **Comportamento:**
  - O comando `kluzo init` gera uma config inicial com os papéis e flows padrão.
  - A validação acontece ao carregar e antes de cada workspace:
    - papéis citados nos flows existem;
    - os pares harness + modelo são válidos;
    - os schemas são válidos;
    - as versões das CLIs estão disponíveis.
- **Telas:** Configuração do projeto (somente leitura na v1, com o diagnóstico de validação).
- **Aceite:** uma config inválida impede iniciar um workspace, com mensagem acionável.
- **Versão:** v0.1.

### F05 · Times por flow
- **O quê:** a visualização do time derivado de cada flow.
- **Comportamento:**
  - Para cada flow (`upstream`, `node`, `closing`), lista os papéis em ordem, com harness, modelo, advisor, orçamento e skills.
  - Mostra se o mesmo papel está em vários flows.
  - Na versão "em execução" (F20), mostra o que cada papel está fazendo agora.
- **Telas:** Times do projeto (configurado); Times do workspace (em execução).
- **Aceite:** o time mostrado é sempre derivado dos flows, sem cadastro separado.
- **Versão:** v0.3.

### F06 · Perfis
- **O quê:** conjuntos de overrides para trocar o elenco inteiro (por exemplo "econômico" e "premium").
- **Comportamento:** um workspace escolhe um perfil na criação. A precedência é global → papel → perfil → milestone → label do nó.
- **Telas:** seletor de perfil na criação do workspace.
- **Aceite:** trocar o perfil altera os modelos sem editar nenhum papel.
- **Versão:** v0.4.

---

## C. Upstream

### F07 · Entrevistas
- **O quê:** um chat com os agentes `business`, `po` e `requirements`.
- **Comportamento:**
  - Cada etapa conduz perguntas até atingir os critérios de saída dela.
  - As respostas viram artefatos: refino nas issues e critérios de aceite.
  - Você pode pausar e retomar a entrevista. Perguntas pendentes vão para a Inbox.
- **Telas:** Workspace › Plano › Entrevista (chat mobile, com resposta por texto).
- **Aceite:** toda issue sai da entrevista com critérios de aceite, área e papel.
- **Versão:** v0.3.

### F08 · Planejamento e crítica
- **O quê:** o laço `planner ⇄ critic` que monta e revisa o grafo.
- **Comportamento:**
  - O planner gera os nós, as arestas e as áreas.
  - O critic aplica o checklist de lacunas e devolve findings. Isso repete até não haver lacuna relevante, com limite de rodadas.
  - Cada rodada fica registrada.
- **Telas:** Workspace › Plano (rodadas, findings por rodada).
- **Aceite:** a proposta final inclui as lacunas encontradas e como cada uma foi tratada.
- **Versão:** v0.3.

### F09 · Revisão e aprovação do plano
- **O quê:** o portão entre upstream e downstream.
- **Comportamento:**
  - Você vê o grafo proposto, as issues novas, as alterações e a estimativa de custo.
  - Pode aprovar, recusar ou comentar **item a item**, e depois aprovar o plano inteiro.
  - Na aprovação, o Kluzo:
    1. cria as issues e os links no tracker;
    2. faz commit de `.kluzo/plans/<milestone>.yaml`;
    3. libera o downstream.
  - Nada é criado no tracker antes da aprovação.
- **Telas:** Workspace › Plano › Revisão; item na Inbox.
- **Aceite:** recusar um item devolve ele ao planner com o seu comentário; o plano só é aprovável sem itens pendentes.
- **Versão:** v0.3 (na v0.1, o plano é escrito à mão e aprovado via CLI).

---

## D. Downstream

### F10 · Grafo e scheduler
- **O quê:** o agendamento determinístico dos nós.
- **Comportamento:**
  - Arestas de bloqueio, contrato e conflito.
  - Um nó fica pronto quando os bloqueios estão integrados.
  - A prioridade segue o caminho crítico.
  - O paralelismo máximo é configurável.
  - O grafo é validado antes de iniciar.
- **Telas:** Progresso › Grafo (F17).
- **Aceite:** dois nós com a mesma área nunca rodam ao mesmo tempo; nenhum nó inicia sem os bloqueios integrados.
- **Versão:** v0.1 (só bloqueio e execução sequencial); v0.2 (paralelismo, contrato e conflito).

### F11 · Execução de nó
- **O quê:** a pipeline `node.yaml` aplicada a um nó.
- **Comportamento:**
  - Worktree isolado, nascido da ponta da milestone.
  - Os papéis rodam em sequência pelo runtime de cada um.
  - O reviewer pode devolver o nó ao implementer com findings.
  - O progresso das subtasks é acompanhado.
- **Telas:** Nó › Execução ao vivo (F19).
- **Aceite:** um nó interrompido retoma do último papel concluído, sem refazer o que já passou.
- **Versão:** v0.1.

### F12 · Gates
- **O quê:** as verificações determinísticas: testes, lint, SAST e E2E.
- **Comportamento:**
  - São definidos em `kluzo.yaml`. Rodam localmente ou via pipeline do CI.
  - O resultado vem do CI ou do comando, nunca do agente.
  - Gate falho volta para o implementer com o log.
- **Telas:** Nó › Gates (status e log de cada gate).
- **Aceite:** um nó nunca entra na fila de merge com gate vermelho.
- **Versão:** v0.1.

### F13 · Fila de merge
- **O quê:** a integração serial na branch da milestone.
- **Comportamento:**
  - Para cada nó: rebase, freshness gate e merge, depois destrava os dependentes.
  - Hotfix da `main` é sincronizado entre duas integrações.
  - Conflito de rebase volta para o implementer.
- **Telas:** Workspace › Fila de merge (ordem, nó atual, histórico).
- **Aceite:** se a branch da milestone mudar depois da review, os gates rodam de novo antes do merge.
- **Versão:** v0.2 (na v0.1, merge direto após os gates, porque a execução é sequencial).

### F14 · Triagem, orçamento e fallback
- **O quê:** o que acontece quando um nó falha.
- **Comportamento:**
  - Primeiro a tabela de regras decide: retry, voltar ao implementer, fallback de modelo ou de harness.
  - Caso ambíguo vai para o papel `triage`.
  - Orçamento estourado deixa o nó `blocked` (e escala para você); o resto do grafo segue.
  - Kill do grupo de processos em timeout.
- **Telas:** Nó › Tentativas (cada tentativa com motivo e decisão).
- **Aceite:** um nó travado nunca bloqueia nós sem dependência dele.
- **Versão:** v0.2.

### F15 · Propostas de lacuna
- **O quê:** um nó novo descoberto durante a execução (`kluzo_propose_node`).
- **Comportamento:**
  - A proposta vai para a Inbox.
  - Se não bloqueia, o nó atual segue. Se bloqueia, o nó atual vai para `blocked`.
  - Ao ser aprovada, entra no grafo e no tracker.
- **Telas:** Inbox; Plano › Propostas.
- **Aceite:** nenhuma issue é criada sem a sua aprovação.
- **Versão:** v0.2.

### F16 · Escalações e decisões
- **O quê:** o diálogo assíncrono com os agentes.
- **Comportamento:**
  - `kluzo_ask` cria um card na Inbox. A resposta volta para a sessão do agente.
  - `kluzo_decision` registra a decisão, a justificativa e o nó, no log da milestone.
- **Telas:** Inbox; Workspace › Decisões (filtrável por nó e papel).
- **Aceite:** toda decisão registrada aparece no relatório final.
- **Versão:** v0.2.

---

## E. Visualização do progresso

### F17 · Visão Grafo
- **O quê:** o grafo da milestone ao vivo.
- **Comportamento:**
  - Nós coloridos por estado.
  - Arestas com traço por tipo: contínua para bloqueio, tracejada para contrato, pontilhada para conflito.
  - Caminho crítico destacado e layout automático.
  - No celular: pan/zoom por gesto e toque no nó abre um resumo em painel inferior (bottom sheet). Um filtro mostra só o caminho crítico ou só os bloqueados.
- **Telas:** Workspace › Progresso › Grafo.
- **Aceite:** uma mudança de estado aparece em até 2 segundos, sem recarregar a página.
- **Versão:** v0.3.

### F18 · Visão Kanban
- **O quê:** os nós em colunas por estado.
- **Comportamento:**
  - Colunas: Planejado · Pronto · Executando · Review/Gates · Fila · Integrado.
  - Bloqueados e escalados aparecem com destaque em qualquer coluna, e também num filtro.
  - No celular, uma coluna por vez, com abas e contador. Deslizar troca de coluna.
  - O card mostra: título, papel ativo, tentativa, progresso das subtasks e custo.
  - Os cards não são arrastáveis: o estado é do Kluzo, não seu.
- **Telas:** Workspace › Progresso › Kanban.
- **Aceite:** o kanban e o grafo mostram sempre o mesmo estado.
- **Versão:** v0.3.

### F19 · Execução ao vivo do nó
- **O quê:** o detalhe de um nó em execução.
- **Comportamento:**
  - A esteira de papéis do flow, com a etapa ativa.
  - Advisor e subagents.
  - O stream de ferramentas e eventos, o diff, as tentativas, os gates e o custo.
  - Ações: pausar, retry, trocar modelo e cancelar.
- **Telas:** Nó.
- **Aceite:** o stream mostra os eventos do runtime normalizados, iguais para Claude Code e OpenCode.
- **Versão:** v0.3.

### F20 · Times em execução
- **O quê:** a visão do workspace por papel: o que cada papel do time está fazendo agora.
- **Comportamento:**
  - Agrupa as sessões ativas por papel (por exemplo, implementer em 3 nós e reviewer em 1).
  - Mostra o modelo, o harness e o custo por papel.
- **Telas:** Workspace › Times.
- **Aceite:** toda sessão ativa aparece em exatamente um papel.
- **Versão:** v0.3.

### F21 · Custos e métricas
- **O quê:** quanto custa e quanto demora.
- **Comportamento:**
  - Custo por nó, por papel e por modelo, e o total da milestone contra a estimativa do plano.
  - Tempo por nó, tentativas médias e taxa de retrabalho do reviewer.
- **Telas:** Workspace › Custos; resumo na visão geral.
- **Aceite:** o custo da milestone bate com a soma dos custos das sessões.
- **Versão:** v0.3.

---

## F. Fechamento

### F22 · Fechamento da milestone
- **O quê:** o gate E2E, o relatório e o MR para a `main`.
- **Comportamento:**
  - Com todos os nós integrados: roda o E2E, depois o `reporter` gera o relatório (entregas, decisões, lacunas, custo, plano × entrega).
  - O Kluzo abre o MR `milestone → main` e avisa você.
  - Depois do seu merge, arquiva o workspace.
- **Telas:** Workspace › Visão geral (estado de fechamento); item na Inbox.
- **Aceite:** o MR só abre com o E2E verde; o relatório vai na descrição do MR.
- **Versão:** v0.4 (na v0.1, só o MR sem relatório).

---

## G. Plataforma

### F23 · Integração com o tracker
- **O quê:** os adaptadores `Tracker`, `SCM` e `CI`.
- **Comportamento:**
  - Lê milestones, issues e links.
  - Espelha o estado em labels `orch/*` e num comentário por transição, e publica o grafo em Mermaid na milestone.
  - Labels têm dono. Começa por polling.
- **Aceite:** o estado no tracker corresponde ao do Kluzo em até um ciclo de polling.
- **Versão:** GitLab v0.1; GitHub v0.4.

### F24 · Runtimes
- **O quê:** os adaptadores de harness.
- **Comportamento:** contrato `Start`, `Resume`, `Events`, `Result` e `Cancel`; eventos normalizados; versões fixadas.
- **Aceite:** o mesmo papel roda nos dois harnesses só trocando o frontmatter.
- **Versão:** Claude Code v0.1; OpenCode v0.2.

### F25 · MCP do Kluzo
- **O quê:** o canal agente → Kluzo.
- **Comportamento:** ferramentas `kluzo_context`, `kluzo_decision`, `kluzo_propose_node`, `kluzo_ask` e `kluzo_result`, com permissões por papel.
- **Aceite:** um papel sem permissão recebe erro ao chamar uma ferramenta fora do escopo dele.
- **Versão:** v0.2 (na v0.1, o resultado vem pela saída estruturada do harness).

### F26 · Acesso e permissões
- **O quê:** `tsnet` com autorização por usuário da tailnet.
- **Comportamento:**
  - Papéis de acesso: `admin` (tudo), `operador` (operar e responder) e `leitor`.
  - Modo `--listen` local para desenvolvimento.
- **Aceite:** sem tailnet, a web não é alcançável de fora da máquina.
- **Versão:** v0.3.

### F27 · Notificações
- **O quê:** um aviso quando algo precisa de você.
- **Comportamento:** usa ntfy ou Telegram para cada item novo da Inbox, com link direto. Agrupa notificações em rajada.
- **Aceite:** nenhuma notificação é enviada para algo que não exige ação.
- **Versão:** v0.3.

### F28 · Journal e retomada
- **O quê:** a durabilidade.
- **Comportamento:** SQLite com WAL; uma entrada por transição; `kluzo resume` e retomada automática quando o binário sobe.
- **Aceite:** matar o processo em qualquer ponto e subir de novo não perde nem duplica trabalho.
- **Versão:** v0.1.

### F29 · CLI
- **O quê:** `kluzo init | plan | approve | run | status | pause | resume | doctor`.
- **Comportamento:** a mesma camada de comandos usada pela web. `doctor` checa as CLIs, tokens, tracker e config.
- **Versão:** v0.1.

---

## Resumo por versão

| Versão | Features |
|---|---|
| **v0.1** | F02, F04, F10 (parcial), F11, F12, F23 (GitLab), F24 (Claude Code), F28, F29 |
| **v0.2** | F10 (completo), F13, F14, F15, F16, F24 (OpenCode), F25 |
| **v0.3** | F01, F03, F05, F07, F08, F09, F17, F18, F19, F20, F21, F26, F27 |
| **v0.4** | F06, F22, F23 (GitHub) |
