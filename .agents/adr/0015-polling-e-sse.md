# 0015. Polling para o tracker e SSE para a web

- Status: aceito
- Data: 2026-11-07

## Contexto

O Kluzo costuma rodar num notebook ou numa VPS atrás de NAT, sem endpoint público. A web precisa de atualização em tempo real, mas o fluxo é quase todo do servidor para o cliente; as ações do usuário são pontuais.

## Decisão

- **Tracker:** polling com cursores, no intervalo de `tracker.poll_interval` (30 s por padrão). Webhooks podem entrar depois como otimização, alimentando a mesma fila de entradas.
- **Web:** **SSE** para eventos (`/api/…/events`, com retomada por `Last-Event-ID` = `seq` do journal) e REST para leituras e ações.

## Consequências

- Funciona atrás de NAT e sem expor nada à internet.
- Até um ciclo de polling de latência para mudanças feitas no tracker por humanos; aceitável.
- O SSE é simples, atravessa proxies HTTP e reconecta sozinho. Não há WebSocket para manter.
