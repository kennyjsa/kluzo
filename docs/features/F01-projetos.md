# F01 · Projetos

- **Grupo:** A. Navegação e organização
- **Status:** planejada

- **O quê:** a lista dos repositórios que o Kluzo gerencia.
- **Comportamento:**
  - Para adicionar um projeto, você informa o repositório (GitLab/GitHub) e o caminho local.
  - O Kluzo detecta o `.kluzo/` e valida a configuração.
  - Cada projeto mostra: a saúde da config, os workspaces ativos e a última atividade.
- **Telas:** lista de projetos (cards); página do projeto.
- **Aceite:** um projeto com config inválida aparece com o erro apontando o arquivo e a linha.
- **Versão:** v0.3 (na v0.1, um projeto via CLI).
