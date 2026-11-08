# F32 · Memória do projeto

- **Grupo:** G. Plataforma
- **Status:** planejada

- **O quê:** o projeto aprende com as milestones sem memória livre escrita por agentes (ADR 0022).
- **Comportamento:**
  - A memória da milestone é o journal e o log de decisões; a do projeto são arquivos versionados (`AGENTS.md`, skills, contratos, ADRs).
  - Padrões repetidos viram propostas de lição, redigidas pelo papel `curator` no fechamento e aprovadas por você.
  - `kluzo_decisions` consulta decisões de milestones anteriores.
- **Aceite:**
  - Nenhum agente grava memória: só o Kluzo, e só no journal ou numa proposta aprovada.
  - Uma lição aprovada vira commit na branch da milestone e chega à `main` pelo PR da milestone.
- **Versão:** v0.4.
