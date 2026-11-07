# 0016. Stack do frontend

- Status: aceito
- Data: 2026-11-07

## Contexto

A web é a interface principal de operação (ADR 0006): responsiva, mobile-first, com um grafo interativo, tempo real e i18n. Ela é embutida no binário Go, sem servidor Node em produção.

## Decisão

- **Vue 3** (Composition API) + **TypeScript** estrito + **Vite**.
- **Pinia** para estado (snapshot + aplicação de eventos SSE) e **Vue Router**.
- **Tailwind CSS v4** com tokens próprios (cores de estado, tipografia) em `src/design/`.
- **Vue Flow** para o grafo, com **elkjs** para o layout hierárquico.
- **vue-i18n**, com `pt-BR` padrão e `en`.
- **Vitest** para unidade e **Playwright** para e2e em 390, 768 e 1280 px.
- O build estático (`web/dist`) é embutido via `embed`.

## Consequências

- Mesma família de tecnologia do minvault (Go + Vue).
- Tailwind torna o mobile-first o caminho natural (estilo base = celular).
- Tipos da API gerados a partir dos contratos evitam divergência entre front e back.
- O pipeline de build precisa de Node, mas só no desenvolvimento e no CI.
