# F04 · Configuração `.kluzo/`

- **Grupo:** B. Configuração
- **Status:** em andamento

- **O quê:** `kluzo.yaml`, `roles/`, `flows/`, `schemas/` e `plans/`.
- **Comportamento:**
  - O comando `kluzo init` gera uma config inicial com os papéis e flows padrão.
  - A validação acontece ao carregar e antes de cada workspace:
    - papéis citados nos flows existem;
    - os pares harness + modelo são válidos;
    - os schemas são válidos;
    - as versões das CLIs estão disponíveis.
- **Telas:** Configuração do projeto (somente leitura na v1, com o diagnóstico de validação).
- **Aceite:** uma config inválida impede iniciar um workspace, com mensagem acionável.
- **Versão:** v0.1.
