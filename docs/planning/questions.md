# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 2 · Configuração `.kluzo/`

### Q1 · O que é um par harness + modelo válido?

- **Contexto:** [config.md](../contracts/config.md) diz que `model` é "alias (`opus`, `sonnet`, `haiku`) ou id completo" e que o par harness + modelo deve ser válido, mas não há catálogo de modelos nem regra de formato para o `opencode`.
- **Hoje:** `claude-code` aceita os aliases `opus`, `sonnet`, `haiku` ou qualquer id começando com `claude-`; `opencode` exige `provedor/modelo` (por exemplo `openrouter/qwen3-coder`). A validação é por formato, não por catálogo (`internal/config/model.go`).
- **Pergunta:** o formato basta, ou o binário deve embutir um catálogo de modelos conhecidos (e como mantê-lo atualizado)? Os ids completos do Claude seguem sempre o prefixo `claude-`?

### Q2 · Vocabulário de `tools` e o "mapeamento do adaptador"

- **Contexto:** config.md exige que "toda ferramenta em `tools` exista no mapeamento do adaptador do harness", mas os adaptadores só chegam no item 6 e `config` não pode importar `runtime/*` (regras de dependência). Os únicos nomes citados na documentação são `read`, `edit`, `bash` e `test`.
- **Hoje:** `config` valida contra a lista fixa `read, edit, bash, test`, igual para os dois harnesses (`internal/config/role.go`, `knownTools`).
- **Pergunta:** o vocabulário canônico de ferramentas é este (sem `grep`, `glob`, `write`, web…)? Ele vive em `contracts` (para config e adaptadores compartilharem) ou cada adaptador publica o seu e o `app` o injeta na validação?

### Q3 · Chaves obrigatórias de `kluzo.yaml`

- **Contexto:** config.md mostra um exemplo, mas só a tabela dos papéis marca obrigatoriedade.
- **Hoje:** são obrigatórias `version`, `project.name`, `tracker.kind` e `tracker.repo`. Todo o resto é opcional. Se um papel não define harness ou modelo e `defaults` não os fornece, o erro aponta o papel.
- **Pergunta:** `defaults.harness` e `defaults.model` devem ser obrigatórios? `tracker.auth` e `branches` também?

### Q4 · `on_fail` nas etapas `gate`

- **Contexto:** a tabela de tipos de etapa lista, para `gate`, só `gate` e `scope`, mas a prosa diz "falha devolve ao `on_fail` (padrão: a primeira etapa `role`)".
- **Hoje:** `on_fail` é aceito em etapas `gate` e deve apontar para uma etapa anterior do flow (mesma regra do `on_reject`).
- **Pergunta:** `on_fail` entra na tabela de chaves? Pode apontar para qualquer etapa anterior ou só para uma etapa `role`?

### Q5 · Flows ausentes e arquivos de flow desconhecidos

- **Contexto:** config.md define `upstream.yaml`, `node.yaml` e `closing.yaml`, mas não diz se todos são obrigatórios (a v0.1 só executa o `node`).
- **Hoje:** nenhum flow é obrigatório no carregamento; quem precisar de um (o `plan`/`run`, no item 10) deve recusar a sua ausência. Um arquivo `flows/<outro>.yaml` é erro (chave desconhecida é erro, não aviso).
- **Pergunta:** `node.yaml` deve ser obrigatório já na v0.1? E `upstream`/`closing`, a partir de qual versão?

### Q6 · Chaves de `prompt_limits`

- **Contexto:** config.md diz que `prompt_limits` "sobrescreve os limites de prompt.md", mas não nomeia as chaves. A tabela de limites de [prompt.md](../contracts/prompt.md#limites) e o manifesto (`findings`, `log_head`, `log_tail`) dão os nomes só em parte.
- **Hoje:** aceita `findings`, `test_failures`, `failure_message_chars`, `log_head`, `log_tail`, `log_chars`, `changed_files` e `diff_lines`, todos inteiros; negativo é erro e zero significa "padrão".
- **Pergunta:** quais são os nomes oficiais? Eles devem constar em config.md.

### Q7 · Onde mora a lista de schemas de saída embutidos

- **Contexto:** `output` de um papel deve nomear um schema embutido ou um de `.kluzo/schemas/`. Os schemas embutidos (`internal/contracts/schemas/*.json`) ainda não existem.
- **Hoje:** `contracts.OutputSchemas()` devolve os oito nomes de [results.md](../contracts/results.md) (`node-result`, `review-result`, `triage-result`, `plan-result`, `critique-result`, `refinement-result`, `architecture-result`, `report-result`); `contracts.ProtocolVersion` vale 1. Ambos foram criados neste item só para o `config` ter onde consultar.
- **Pergunta:** quando os schemas entrarem, a lista deve ser derivada do `embed`. Sem ação até lá, só confirmar que o pacote `contracts` é o lugar certo para as duas coisas.

### Q8 · Papéis e gates padrão do `kluzo init`

- **Contexto:** o texto dos papéis padrão ainda não foi decidido (investigação 2).
- **Hoje:** `init` gera dez papéis (`business`, `po`, `requirements`, `architect`, `planner`, `critic`, `implementer`, `reviewer`, `triage`, `reporter`), com corpo-placeholder e comentário `TODO(investigation 2)` no frontmatter, mais os três flows do contrato. Escolhas minhas, a confirmar:
  - `mcp: [kluzo_result]` em todos, porque a v0.1 só expõe essa ferramenta (ADR 0019); o exemplo do contrato lista as cinco.
  - Não há papel `qa` (listado em concepts.md), porque results.md não tem schema de saída para ele.
  - `architect` e `planner` usam `model: opus`; os demais herdam `defaults` (`claude-code`, `sonnet`).
  - Os gates `build`, `test` e `e2e` têm comandos que **falham de propósito** (`exit 1`, com mensagem TODO), para que um gate nunca configurado não passe por acidente.
  - `tracker.repo: OWNER/REPO` é um placeholder que passa na validação de formato.
- **Pergunta:** confirmar essas escolhas ou substituí-las pelas da investigação 2.

### Q9 · Força do `advisor` quando o modelo é um id completo

- **Contexto:** o `advisor` "não pode ser mais fraco que `model`", mas só os aliases têm ordem óbvia.
- **Hoje:** a família é deduzida do nome (`haiku` < `sonnet` < `opus`, também dentro de ids `claude-…`). Se uma das famílias não for reconhecida, a comparação é pulada.
- **Pergunta:** isso basta, ou ids desconhecidos devem ser recusados como `advisor`?
