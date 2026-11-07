# F14 · Triagem, orçamento e fallback

- **Grupo:** D. Downstream
- **Status:** planejada

- **O quê:** o que acontece quando um nó falha.
- **Comportamento:**
  - Primeiro a tabela de regras decide: retry, voltar ao implementer, fallback de modelo ou de harness.
  - Caso ambíguo vai para o papel `triage`.
  - Orçamento estourado deixa o nó `blocked` (e escala para você); o resto do grafo segue.
  - Kill do grupo de processos em timeout.
- **Telas:** Nó › Tentativas (cada tentativa com motivo e decisão).
- **Aceite:** um nó travado nunca bloqueia nós sem dependência dele.
- **Versão:** v0.2.
