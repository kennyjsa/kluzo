# 0017. Critérios para aceitar dependências

- Status: aceito
- Data: 2026-11-07

## Contexto

Cada dependência é superfície de ataque, custo de manutenção e risco de abandono. O Kluzo é distribuído como binário único e roda com credenciais sensíveis.

## Decisão

Uma dependência nova só entra se:
1. **resolver algo que não caberia em ~200 linhas** de código próprio com qualidade;
2. tiver **licença permissiva** (MIT, BSD, Apache-2.0, ISC, MPL-2.0); GPL e AGPL não entram;
3. estiver **mantida**: commits ou release nos últimos 12 meses, issues respondidas;
4. **no backend, não exigir cgo**;
5. tiver **árvore de dependências pequena**, verificada com `go mod graph` ou `npm ls`.

A inclusão é registrada em [stack.md](../../docs/architecture/stack.md), com o motivo, no mesmo PR. Trocar uma peça já listada exige um ADR.

## Consequências

- Menos dependências, menos surpresa em atualização.
- Algumas conveniências são escritas no próprio código (migrations, runner de efeitos).
- O CI roda `govulncheck` e `npm audit`; vulnerabilidade alta bloqueia o merge.
