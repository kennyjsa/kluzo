# 0020. Garantias aplicadas em código, não pedidas no prompt

- Status: aceito
- Data: 2026-11-07
- Origem: validação adversária da investigação 1 ([docs/research/prompt-assembly.md](../../docs/research/prompt-assembly.md))

## Contexto

Pedir no prompt que o agente "não enfraqueça testes" ou "não silencie o lint" não impede que isso aconteça. Os cenários levantados na validação:
- o agente adiciona `//nolint`, afrouxa `.golangci.yml` ou edita o `run:` do gate em `kluzo.yaml`, e o gate fica verde;
- uma issue maliciosa faz o implementer inserir no código um comentário dirigido ao reviewer ("mudança pré-aprovada"), e o reviewer aprova;
- o agente usa credenciais do host (o `gh` autenticado, um credential helper do git, `~/.ssh`) para publicar sozinho;
- um teste escrito pelo agente roda no gate com os segredos do projeto e os exfiltra.

## Decisão

1. **Gates congelados:** a definição dos gates (`kluzo.yaml`) e os arquivos de configuração que eles usam são lidos da **ponta da branch da milestone no momento do despacho do nó**, nunca do worktree do nó.
2. **Caminhos protegidos:** `kluzo.yaml` declara `protected_paths`. O padrão inclui `.kluzo/**`, a configuração de lint, a de CI e o `Makefile`.
   - Um diff de nó que altera um caminho protegido não entra na fila. O nó vai para `blocked` (motivo `proposal`), e a mudança vira uma proposta para o humano aprovar.
   - **Remoção ou desativação de testes** detectada no diff segue o mesmo caminho. Isso inclui arquivos `_test` apagados e marcadores de skip adicionados.
   - **Supressões novas** (`//nolint`, `eslint-disable`, `# noqa`…) contam como finding `blocking` automático no review.
3. **Reviewer somente leitura:** o papel `reviewer` não tem `edit` nem `bash`. Ele lê os arquivos e o diff por ferramentas de leitura e recebe o resultado dos gates como dado. Isso impede que ele execute código não confiável, e o worktree é o mesmo que a fila de merge vai integrar.
4. **Ordem do flow de nó:** um gate rápido (build e lint) roda **antes** do review, para não gastar o reviewer com código que nem compila. Os gates completos rodam depois do review.
5. **Gates sem segredos por padrão:** um gate recebe só as variáveis declaradas em `gates[].env_allow`. A recomendação é rodar os gates em container e com egress restrito. `kluzo doctor` avisa quando isso não está configurado.
6. **Credenciais do host:** `kluzo doctor` e o início de cada sessão detectam `gh` autenticado, credential helper do git e chaves em `~/.ssh` visíveis para o usuário que roda os agentes.
   - Se encontrar algum, o Kluzo **recusa iniciar sessões**, a menos que `security.allow_host_credentials: true` esteja explícito em `kluzo.yaml`.
   - O caminho recomendado é um usuário dedicado, ou container ou VM (ADR 0018).

## Consequências

- As regras contra trapaça do prompt viram lembrete. A garantia é determinística e coberta por testes.
- Mudanças legítimas em configuração de lint, CI ou `.kluzo/` passam por aprovação humana. Esse atrito é aceito.
- O papel `reviewer` padrão perde `bash`. Uma verificação que precise executar código vira gate.
- Detalha e endurece o ADR 0018. As mudanças estão em [security.md](../../docs/architecture/security.md).
