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
- **Gates:** o resultado vem do comando ou do CI, nunca do agente.

### Isolamento do agente

- Cada sessão roda **no worktree do nó**, com o diretório de trabalho fixado nele.
- As ferramentas do harness são limitadas pelo campo `tools` do papel. Por exemplo, o reviewer não tem `edit`.
- **Sem credencial de push:** a sessão do agente não recebe o token do bot. Commit e push são feitos pelo Kluzo, depois de a sessão terminar.
- **Ambiente mínimo:** a sessão recebe só as variáveis do projeto declaradas em `kluzo.yaml` (`runtimes.<name>.env_allow`), nunca o ambiente completo do Kluzo.
- O kill do grupo de processos acontece em timeout ou cancelamento.
- **Limite conhecido:** sem sandbox de sistema operacional, o agente roda com o usuário do Kluzo e consegue ler arquivos fora do worktree. A recomendação é rodar o Kluzo com um usuário dedicado, ou num container ou VM. Um sandbox nativo (container por sessão) é candidato a uma versão futura.

### Injeção de prompt

- O conteúdo vindo do tracker (issues, comentários) chega ao agente dentro de um bloco marcado como **dados não confiáveis**, com instrução explícita de que não é instrução.
- **Nenhuma ação privilegiada depende do texto do agente:** aprovar, fazer merge, criar issue e mudar orçamento são operações do supervisor ou do humano.
- `kluzo_propose_node` e `kluzo_ask` só **pedem**. A decisão é sempre humana.

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
