# 0008. Código em inglês, documentação em português

- Status: aceito
- Data: 2026-11-07

## Contexto

A documentação do Kluzo está em português. Sem uma regra explícita, cada agente escolhe uma língua para identificadores, mensagens e nomes de arquivo, e o código vira uma mistura difícil de buscar e de manter.

## Decisão

- **Em inglês:** código, identificadores, nomes de pacote, chaves de configuração (`kluzo.yaml`, frontmatter, flows, planos), nomes de estados, labels (`orch/*`), eventos, ferramentas MCP, mensagens de log e de erro, commits.
- **Em português:** documentação em `docs/`, ADRs, `AGENTS.md`, textos da interface web (via i18n, com `pt-BR` como idioma padrão) e comentários que expliquem regra de negócio.
- Os termos de domínio têm um nome canônico em inglês, definido no [glossário dos contratos](../contracts/README.md#glossário).

## Consequências

- Código, configuração e API ficam prontos para um público internacional sem renomear nada.
- A documentação precisa citar os nomes em inglês (`running`, `blocked`) quando fala de estados e chaves.
- A web nasce com i18n, mesmo tendo um único idioma no início.
