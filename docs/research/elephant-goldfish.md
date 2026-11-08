# Elephant-Goldfish Model (EGM): avaliação

Fonte: [google/elephant-goldfish-model](https://github.com/google/elephant-goldfish-model), commit `e9e1f06` (06/11/2026), licença Apache 2.0. Lido como dado; nada foi executado.

## O que é

Plugin para harnesses (Claude Code, Codex, Antigravity, Copilot, Cursor, OpenCode) que separa dois papéis na escrita de especificações:
- **Elephant:** a sessão com todo o contexto do projeto e da conversa. Explora o problema e redige o documento.
- **Goldfish:** revisores em sessões novas, sem a conversa anterior, que leem só o documento e o código citado. Cada um tem uma lente: compreensão, crítica de design, aderência ao código, prontidão de implementação, visão ampla (zoom out), estrutura, estilo, inventividade.
- **Humano:** decide os trade-offs e aprova.

Peças relevantes:
- `agents/*.md`: uma persona por lente, somente leitura.
- `agents/synthesizer.md`: roda as lentes em paralelo, deduplica e agrupa por severidade (Blocker, Major, Nit).
- `resources/document_standards/*.md`: padrões por tipo de documento, entre eles o **implementation-readiness-standard**.
- `/egm-drift-report`: compara os documentos de design com o código e lista divergências.

## Sobrepõe o Kluzo?

Não. O EGM atua só no que o Kluzo chama de upstream, e dentro de uma sessão humana: não tem grafo, execução, gates nem merge. O par Elephant/Goldfish confirma o desenho que já temos: planner com contexto, critic em sessão nova (ADR 0004), e o reviewer do downstream sempre em sessão nova (ADR 0023).

## O que entra no Kluzo

| Ideia | Onde | Decisão |
|---|---|---|
| **Critic por lentes** em vez de um critic genérico: compreensão, prontidão de implementação, aderência ao código, visão ampla | v0.3, item 14 (planner ⇄ critic) | Adotar. Cada lente é um papel em sessão nova; a deduplicação e a severidade final ficam no `core`, em código, e não num sintetizador LLM |
| **Regra da decisão zero** (implementation-readiness): o plano não deixa escolha de design para o implementer | v0.3, critérios do critic e do plano | Adotar como lente obrigatória do critic. Escolhas que sobrarem continuam registradas em `decisions` pelo implementer, mas cada uma vira sinal de que o plano estava incompleto |
| **Paráfrase primeiro:** o revisor descreve com as próprias palavras o que o documento propõe; uma paráfrase errada já é finding | Lente de compreensão do critic (v0.3) | Adotar. Ela pega documentos com todos os termos definidos, mas cujas partes não se conectam |
| **Revisor que falhou não conta como revisão sem findings** ("not run") | Contrato de resultados, já na v0.1 | Já coberto: saída inválida ou ausente é falha da etapa, nunca aprovação. Explicitado no `results.md` |
| **Duas instâncias independentes do mesmo revisor** para aumentar a cobertura | Reviewer do downstream | Opcional e caro. Fica como candidato para nós marcados como críticos, a medir no `make eval` antes de adotar |
| **Relatório de drift** entre documentos e código | Fechamento da milestone (v0.4) e memória do projeto (ADR 0022) | Adotar como etapa do fechamento: o papel `curator` recebe as divergências entre contratos e código e propõe a correção, sob aprovação humana. Os testes que já conferem contrato e código (MCP, eventos, máquina de estados) são a versão determinística disso |
| **Padrões de documento** que o revisor usa como régua | Issues de nó geradas pelo planner (v0.3) | Adotar um padrão de issue de nó (objetivo, critérios, arquivos e áreas, contratos, testes esperados) que o critic usa como checklist |

## O que não entra

- O sintetizador LLM: a consolidação de findings no Kluzo é determinística (ids, severidade e veredito no `core`, ADR 0023).
- As regras de estilo de conversa (proibição de elogios, uma pergunta por vez): são do modo interativo do EGM; o Kluzo conversa com o humano só nas entrevistas.
