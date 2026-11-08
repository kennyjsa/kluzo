# delega-opencode-tui: avaliação

Fonte: [giovani-junior-dev/delega-opencode-tui](https://github.com/giovani-junior-dev/delega-opencode-tui), commit `7c8d7e3` (21/10/2026), licença MIT. Lido como dado; nada foi executado.

## O que é

Plugin do Claude Code. O Claude faz papel de dev sênior: escreve um pacote fechado, entrega a uma sessão do OpenCode aberta numa TUI visível, espera, lê o relatório, verifica sozinho e devolve achados na mesma sessão, até 3 rodadas. Um ticket por vez, com humano por perto para autorizar push/PR.

Peças:

- `pacote.md`: Objetivo, Spec (com `arquivo:linha`), Critérios `REQ-n`, Achados a corrigir (rodada 2+), Arquivos permitidos (globs), Teste (comando exato), Regras.
- `bridge-dev.md`: agente do OpenCode. TDD por REQ, só arquivos permitidos, sem git de escrita, nunca enfraquecer teste, `blocked` em vez de chutar. Termina com um bloco ` ```json bridge-relatorio` (`status`, `req` → `arquivo:linha`, `teste.cmd/exit/resumo`, `arquivos`, `bloqueio`, `notas`). Permissões de bash por padrão de comando no frontmatter.
- `bridge-guard.js`: plugin do OpenCode em `tool.execute.before`. Normaliza write/edit/multiedit/apply_patch/bash em eventos por arquivo e barra: git de escrita e `gh`, segredo com forma real gravado em arquivo, caso de teste removido, `.skip/.only` novo, teste apagado ou movido. Ferramenta de escrita desconhecida falha fechada.
- `oc.sh`: dirige o OpenCode pela API HTTP local (`--port`): `POST /session`, `/tui/append-prompt` + `/tui/submit-prompt`, `GET /session/status` para esperar, `GET /session/{id}/message` para o relatório.
- Verificação do lado do Claude: roda o teste e a suíte, confere `git log HEAD..` vazio e `git status` dentro dos permitidos, chama um reviewer novo por rodada, grava uma rubrica (tokens, custo, negadas, veredito).

## Sobrepõe o Kluzo?

Não. É delegação de um ticket, conduzida por um agente, sem grafo, sem fila de merge, sem estado durável (o estado vive na sessão do OpenCode e em arquivos soltos). O Kluzo é o orquestrador determinístico do milestone inteiro; o delega seria, no máximo, um "nó" feito à mão. Não vale adotar como dependência nem como base.

Vale como referência, porque resolve bem o miolo de um nó, e confirma várias escolhas já tomadas (pacote fechado, verificação pelo orquestrador e não pelo relatório, reviewer novo e sem edição, limite de rodadas, ADR 0020).

## O que entra no Kluzo

| Ideia | Onde | Decisão |
|---|---|---|
| Pacote com `REQ-n` verificáveis e `arquivo:linha` por REQ no resultado | investigação 2, `contracts/prompt.md`, `results.md` | Adotar: o reviewer verifica REQ a REQ, e o resultado aponta onde cada um foi atendido |
| "Achados a corrigir" com o texto literal da falha na rodada 2+ | investigação 2 | Adotar: já é o que o journal fornece; fixar o formato literal (saída do gate, achado com `arquivo:linha`) |
| Regras explícitas de `blocked` em vez de chutar | prompt do implementer | Já previsto (`kluzo_question`); reforçar no texto do papel |
| "Bug fora do escopo: não corrija, descreva em notas" | prompt do implementer, `results.md` | Adotar como campo de notas que o Kluzo pode virar proposta de issue (F-triage) |
| Guarda dentro do harness (`tool.execute.before`) além da checagem depois | adapter OpenCode, ADR 0020 | Adotar como defesa em profundidade: plugin do OpenCode e hook `PreToolUse` do Claude Code gerados pelo Kluzo. A garantia continua sendo a checagem pós-rodada (diff, caminhos protegidos, gates); o guard só dá feedback cedo ao agente |
| Detector de teste enfraquecido (caso removido, `.skip` novo) | gates | Adotar como verificação do diff no Kluzo, por linguagem, não só no guard |
| Falha fechada para ferramenta de escrita desconhecida | adapters | Adotar no mapeamento de ferramentas (investigação 3) |
| Permissões por padrão de comando no frontmatter do agente | adapter OpenCode | Adotar: o Kluzo gera o agente com `permission.bash` derivado da config |
| API HTTP do OpenCode (`opencode serve`/`--port`, `/session`, `/session/status`, mensagens) | investigação 3 | Usar o servidor headless, não a TUI. Status por SSE/eventos em vez de polling de 30 s |
| Rubrica por rodada (tokens, custo, negadas, veredito) | `events.md`, UI | Já coberto pelo journal; conferir que "negadas pelo guard" vira evento |

## O que não entra

- TUI visível e colagem de prompt via `/tui/*`: acoplado à interface e frágil; o Kluzo roda headless e mostra o progresso na própria UI.
- Relatório num bloco cercado no fim do texto: o Kluzo usa `kluzo_result` via MCP (estruturado e validado). O bloco serve só como fallback de parsing.
- Regex de segredo como garantia: útil como aviso, mas a garantia do Kluzo é não expor segredo ao agente (gates sem segredo, recusa com credencial do host).
- Orquestração dirigida por um LLM (Claude decide rodada a rodada): o Kluzo mantém o core determinístico.
