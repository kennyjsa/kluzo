# 0018. Modelo de segurança dos agentes

- Status: aceito
- Data: 2026-11-07

## Contexto

Os agentes editam arquivos e executam comandos, leem conteúdo não confiável (issues, comentários) e trabalham perto de credenciais com escrita no repositório.

## Decisão

As decisões estão detalhadas em [security.md](../../docs/architecture/security.md). Em resumo:
- **Portões fora do alcance dos agentes:** merge, aprovação, criação de issue e mudança de orçamento são do supervisor ou do humano. Os gates são executados pelo Kluzo.
- **Sessão sem credencial de push:** commit e push são feitos pelo Kluzo, depois da sessão.
- **Ambiente mínimo:** só as variáveis permitidas em `runtimes.<name>.env_allow`.
- **Conteúdo do tracker** entra no prompt marcado como dados não confiáveis.
- **Remoção de segredos** em tudo o que é gravado ou publicado.
- **Web e MCP** só pela tailnet e pelo loopback, com identidade e token por sessão.
- **Sem sandbox de SO na v1:** recomenda-se rodar com usuário dedicado, em container ou VM.

## Consequências

- Uma injeção de prompt bem-sucedida, no pior caso, produz código ruim que ainda precisa passar pela review, pelos gates e pelo merge humano na `main`.
- O isolamento de sistema de arquivos depende do ambiente do usuário até existir um sandbox nativo.
