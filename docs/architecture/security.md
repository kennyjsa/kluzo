# Modelo de segurança

O Kluzo executa agentes de código com permissão para editar arquivos e rodar comandos, e guarda credenciais do tracker. Este documento define o que é protegido, contra o quê, e como. ADR 0018.

## O que proteger

1. **A `main` e o histórico do repositório.**
2. **As credenciais:** o GitHub App ou o token do GitLab, as chaves dos provedores de LLM e os segredos do projeto usados nos gates.
3. **A máquina onde o Kluzo roda:** o notebook ou a VPS do usuário.
4. **O controle do Kluzo:** quem aprova planos, responde escalações e opera os workspaces.

## Ameaças consideradas

| Ameaça | Exemplo |
|---|---|
| Agente faz algo destrutivo por engano | `rm -rf` fora do worktree, `git push --force` |
| Injeção de prompt via conteúdo do repositório ou da issue | Um texto numa issue manda o agente exfiltrar segredos ou aprovar o próprio trabalho |
| Agente tenta pular os portões | Declara gate verde, faz merge sozinho, aplica `plan/approved` |
| Vazamento de segredo em log ou comentário | O token aparece no stream e vai para o journal ou para o tracker |
| Acesso indevido à web | Alguém na rede executa ações |

## Controles

### Portões que agentes não alcançam

- **Merge na `main`:** a credencial do bot não tem permissão de escrita na `main`. A proteção de branch fica do lado do tracker, e o Kluzo só abre o PR.
- **Merge na branch da milestone:** só a fila de merge, executada pelo supervisor, faz. Nenhum papel tem uma ferramenta para isso.
- **Aprovações:**
  - `plan/approved` e os comandos `/kluzo` só valem quando vêm de usuários listados em `tracker.operators`.
  - Labels de dono errado são ignoradas e revertidas.
- **Gates:**
  - O resultado vem do comando ou do CI, nunca do agente.
  - A definição dos gates e os arquivos de configuração que eles usam são lidos da ponta da branch da milestone no despacho do nó, nunca do worktree (ADR 0020).
- **Caminhos protegidos:** um diff que altera `protected_paths` (`.kluzo/**`, configuração de lint e de CI), apaga ou desativa testes vira proposta para o humano, e o nó não entra na fila. Uma supressão de lint nova vira finding `blocking` (ADR 0020).

### Isolamento do agente

- Cada sessão roda **no worktree do nó**, com o diretório de trabalho fixado nele.
- As ferramentas do harness são limitadas pelo campo `tools` do papel. O `reviewer` é **somente leitura**: sem `edit` e sem `bash`. Ele recebe o resultado dos gates como dado, e assim nunca executa código não confiável.
- **Sem credencial de push:** a sessão do agente não recebe o token do bot. Commit e push são feitos pelo Kluzo, depois de a sessão terminar.
- **Ambiente mínimo:** a sessão recebe só as variáveis do projeto declaradas em `kluzo.yaml` (`runtimes.<name>.env_allow`), nunca o ambiente completo do Kluzo.
- O kill do grupo de processos acontece em timeout ou cancelamento.
- **Sessão do Claude Code (ADR 0024):** modo `--restricted`, `HOME` sintético por workspace, sem agente SSH, configuração em arquivos `0600` fora do worktree, hook `PreToolUse` para o Kluzo com canário, e git do Kluzo sem hooks nem configuração global. Refs e `.git/config` são conferidos depois de cada sessão.
- **Limite conhecido:** além do controle de credenciais acima, sem sandbox de sistema operacional, o agente roda com o usuário do Kluzo e consegue ler arquivos fora do worktree. A recomendação é rodar o Kluzo com um usuário dedicado, ou num container ou VM. Um sandbox nativo (container por sessão) é candidato a uma versão futura.

### Injeção de prompt

- **Envelope único:** tudo o que vem de fora do Kluzo chega ao agente num envelope de dado não confiável, delimitado por nonce e saneado, tanto no prompt quanto nas respostas MCP ([contracts/prompt.md](../contracts/prompt.md#envelope-de-dado-não-confiável)). Isso inclui issues, comentários, findings, logs, diff e arquivos citados. O saneamento remove caracteres de controle, bidi, zero-width e o bloco Unicode Tags.
- **O próprio diff é não confiável para o reviewer:** ele pode carregar texto plantado via issue ("mudança pré-aprovada"). Por isso o reviewer só lê, e as garantias críticas são aplicadas em código, não pelo julgamento do reviewer.
- **Nenhuma ação privilegiada depende do texto do agente:** aprovar, fazer merge, criar issue e mudar orçamento são operações do supervisor ou do humano.
- `kluzo_propose_node` e `kluzo_ask` só **pedem**. A decisão é sempre humana.

### Gates e credenciais do host

- **Os gates rodam código escrito pelo agente**, por isso não recebem segredos por padrão. Só recebem as variáveis em `gates[].env_allow`. A recomendação é rodar os gates em container com egress restrito.
- **Credenciais do host:** se `gh` autenticado, credential helper do git ou chaves em `~/.ssh` estiverem visíveis para o usuário que roda os agentes, o Kluzo recusa iniciar sessões, a menos que `security.allow_host_credentials: true` esteja explícito (ADR 0020).

### Segredos

- As credenciais do Kluzo vêm de variáveis de ambiente ou de arquivo com permissão `0600`. Nunca do `.kluzo/`, que é versionado.
- O `platform/redact` remove valores de segredos conhecidos e padrões de token de tudo o que vai para o journal, os logs, a web e o tracker.
- O stream bruto das sessões (`logs/sessions/`) também passa pelo redact antes de ser gravado.

### Acesso à web e ao MCP

- **Web:** só pela tailnet (`tsnet`), com identidade por `WhoIs` e os papéis `admin`, `operador` e `leitor`. O modo `--listen` local é só para desenvolvimento e escuta em `127.0.0.1`.
- **MCP:** escuta em `127.0.0.1`, com um token por sessão, revogado quando a sessão termina.

## Responsabilidades do usuário

- Configurar a proteção da `main` no tracker, com merge apenas por humanos.
- Dar à credencial do bot o mínimo necessário: issues, branches `milestone/*` e `issue/*`, PRs, checks.
- Rodar o Kluzo com um usuário dedicado, ou isolado em container ou VM, quando o repositório for sensível.
