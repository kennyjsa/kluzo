# Dúvidas de implementação

Pontos em que um contrato está ambíguo ou não decide, encontrados durante a implementação. Em cada um, o código segue uma **escolha provisória** (descrita abaixo) até a sessão de produto/arquitetura decidir. Quando uma dúvida for resolvida, atualize o contrato (com ADR, se mudar arquitetura), ajuste o código e remova a entrada daqui.

Formato: contexto, o que o código faz hoje, e a pergunta.

## Item 2 · Configuração `.kluzo/`

### Q10 · `kluzo roles check` e a regra do `phase: in_review`

- **Contexto:** [config.md](../contracts/config.md) lista a regra "papel usado em etapa `in_review` não tem `edit` nem `bash`" entre as validações que valem também em `kluzo roles check`, mas diz que ela é checada ao carregar os flows.
- **Hoje:** `roles check` valida só `kluzo.yaml` e os papéis (`LoadRoles`), sem olhar os flows; por isso ele **não** acusa um reviewer com `edit`/`bash`. `Load` (usado por `run`/`doctor`) acusa.
- **Pergunta:** `roles check` deve carregar também os flows (e então reportar erros de flow), ou carregar só para aplicar essa regra?
