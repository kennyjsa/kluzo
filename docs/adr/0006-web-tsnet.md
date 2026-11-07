# 0006. Web mobile-first acessada via tsnet

- Status: aceito
- Data: 2026-11-07

## Contexto

A operação deve funcionar do celular, de qualquer lugar, sem expor portas públicas e sem implementar autenticação própria.

## Decisão

A web (Vue + Vue Flow, responsiva e mobile-first, com visões de grafo, kanban e estrutura) é a interface principal de operação. O binário entra na tailnet via tsnet, com HTTPS da tailnet, e autoriza por identidade via WhoIs.

## Consequências

- Sem superfície pública e sem sistema de login.
- O acesso depende do Tailscale. Um modo sem tsnet (listen local) fica disponível para desenvolvimento.
