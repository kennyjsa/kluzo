# 0022. Memória do projeto: só o Kluzo grava, e só o que o humano aprovou

- Status: aceito
- Data: 2026-11-07
- Origem: discussão sobre memória compartilhada entre agentes (mem0, Letta, claude-mem e semelhantes)

## Contexto

Os nós de uma milestone repetem erros que outros nós já cometeram, e as milestones seguintes não aprendem com as anteriores. Uma memória compartilhada, escrita livremente pelos agentes, resolveria isso, mas:
- **Envenenamento:** uma issue maliciosa ou um agente confuso grava uma "lição", e ela chega a todos os nós seguintes, furando a fronteira de confiança dos ADRs 0019 e 0020.
- **Desatualização:** a memória diz uma coisa e o código, depois do merge, diz outra. Ninguém revisa.
- **Perda de determinismo:** o mesmo nó se comporta diferente conforme o que ficou gravado, e a execução deixa de ser reproduzível pelo journal (ADR 0001).

## Decisão

1. **Duas memórias, ambas auditáveis:**
   - **Da milestone:** o journal e o log de decisões. Já existem; o `kluzo_context` e o `kluzo_changes` as entregam.
   - **Do projeto:** arquivos versionados no repositório: `AGENTS.md`, skills em `.kluzo/skills/`, contratos e ADRs. Chegam a qualquer harness e passam por review como o resto do código.
2. **Só o Kluzo grava.** Nenhum agente escreve memória diretamente:
   - No journal, o agente só gera entradas pelo MCP. O Kluzo grava o evento com autor, nó e sessão, e o texto do agente fica marcado como não confiável.
   - No repositório, `AGENTS.md` e `.kluzo/**` são caminhos protegidos. Um diff de nó que os altera vira proposta (ADR 0020).
   - Não existe ferramenta MCP de escrita livre de memória.
3. **Ciclo de lições, com aprovação humana:**
   1. O núcleo detecta padrões de forma determinística: o mesmo finding de review, a mesma falha de gate ou a mesma pergunta em vários nós.
   2. No fechamento da milestone, o papel `curator` recebe esses padrões, com as ocorrências como evidência, e redige uma mudança em `AGENTS.md` ou numa skill. Ele não grava nada: entrega um resultado estruturado.
   3. O Kluzo transforma a redação numa proposta do tipo `lesson`. Você aprova, edita ou descarta.
   4. Aprovada, o Kluzo faz o commit na branch da milestone pelo outbox, e a lição vai para a `main` junto com o PR da milestone, sob o seu merge.
4. **Consulta:** `kluzo_decisions` busca decisões de milestones anteriores do mesmo projeto, por texto e por área, sem efeito colateral.

## Consequências

- A memória do projeto é texto revisado e versionado. Desfazer uma lição é reverter um commit.
- O agente nunca lê algo que um humano não tenha aprovado ou que o Kluzo não tenha registrado como fato, com origem.
- O aprendizado é mais lento que numa memória livre: uma lição só vale a partir da milestone seguinte. Esse atrito é aceito.
- O padrão de `protected_paths` passa a incluir `AGENTS.md`.
