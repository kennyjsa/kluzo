# Referências

Levantamento de novembro de 2026. Nenhum projeto open-source encontrado orquestra uma **milestone inteira**: com a milestone como entidade com estado, um plano aprovado, um grafo ligado às issues reais do tracker, o ciclo upstream/downstream e o gate da milestone integrada. Cada referência abaixo resolve uma peça do problema.

| Projeto | Linguagem | O que aproveitar |
|---|---|---|
| [koryph](https://github.com/koryph/koryph) | Go | **Referência principal.** O *footprint scheduler* (tarefas declaram o que tocam, e só roda em paralelo o que não conflita); o fluxo review → rebase → gate → merge; o replan; o release train. |
| [belay](https://github.com/dhaam-ai/belay) | Go | O **executor durável de nó**: máquina de estados plan → approve → code → test → fix → review, journal por passo, `resume`, teto de custo, modelo por etapa no Claude Code, kill do grupo de processos. |
| [Pinard](https://github.com/Genentech/pinard) | Go | **Loops semi-determinísticos**: o código controla o fluxo e o modelo julga em passos limitados. Run journal, NATS JetStream para workers distribuídos e gatilho por issue do GitLab. |
| [OrchestratorInc AO](https://github.com/OrchestratorInc/agent-orchestrator) (ex-ComposioHQ) | TypeScript | As **reações**: falha de CI, comentário de review e conflito voltam ao worker dono da branch. O dashboard kanban e o app mobile. Arquitetura de plugins (runtime, agent, workspace, tracker, SCM, notifier). |
| [Bernstein](https://github.com/chernistry/bernstein) | Python | Scheduler determinístico sem LLM, plano YAML de várias etapas, janitor de gates, journal com replay. |
| [MAGOS](https://github.com/UlyssesLeoLee/MAGOS) | — | O **freshness gate** e a fila de merge serial numa branch de integração; lanes com arquivos mutuamente exclusivos. |
| [Detent](https://github.com/digitaldrywood/detent) | Go | Issues avançam em "paradas" só quando um gate libera; contrato em `detent.yaml`. |
| [OpenAI Symphony](https://github.com/openai/symphony) | Spec | Loop poll → dispatch → reconcile com retry e backoff; `WORKFLOW.md` versionado no repositório. |
| [Polyphony](https://github.com/penso/polyphony) | Rust | Implementação do Symphony com suporte ao GitLab via GraphQL. |
| [prd-taskmaster](https://github.com/anombyte93/prd-taskmaster) | Python | PRD → grafo ordenado por dependência; "pronto" exige evidência verificada. |
| [Gas Town](https://github.com/steveyegge/gastown) | Go | Estado do trabalho persistido em git (hooks e beads), sobrevivendo ao reinício dos agentes. |
| [CCPM](https://github.com/automazeio/ccpm) | Shell | PRD → épico → issues. Contraexemplo: dividir uma issue em fluxos paralelos gera caos de merge. |

## Ferramentas adjacentes (não concorrentes)

- [Rivet](https://github.com/rivet-dev/rivet): actors duráveis com SQLite. É uma alternativa de substrato para o estado, descartada por prender o projeto ao TypeScript.
- [anywh](https://github.com/anywh-sh/anywh): relay para controlar agentes pelo celular.
- [Maestri](https://www.themaestri.app/): canvas macOS de orquestração supervisionada.
- [Claude Code advisor](https://code.claude.com/docs/en/advisor): o par Sonnet principal + Opus advisor dentro de cada sessão de nó.
