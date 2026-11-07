# F09 · Revisão e aprovação do plano

- **Grupo:** C. Upstream
- **Status:** planejada

- **O quê:** o portão entre upstream e downstream.
- **Comportamento:**
  - Você vê o grafo proposto, as issues novas, as alterações e a estimativa de custo.
  - Pode aprovar, recusar ou comentar **item a item**, e depois aprovar o plano inteiro.
  - Na aprovação, o Kluzo:
    1. cria as issues e os links no tracker;
    2. faz commit de `.kluzo/plans/<milestone>.yaml`;
    3. libera o downstream.
  - Nada é criado no tracker antes da aprovação.
- **Telas:** Workspace › Plano › Revisão; item na Inbox.
- **Aceite:** recusar um item devolve ele ao planner com o seu comentário; o plano só é aprovável sem itens pendentes.
- **Versão:** v0.3 (na v0.1, o plano é escrito à mão e aprovado via CLI).
