# 0012. Ports & adapters com núcleo funcional

- Status: aceito
- Data: 2026-11-07

## Contexto

O Kluzo integra trackers, git, harnesses de agente e notificações, e cada um tem mais de uma implementação prevista. O núcleo (estados, scheduler, fila de merge) precisa ser determinístico (ADR 0001), testável sem rede nem LLM, e retomável depois de um crash.

## Decisão

- Arquitetura **ports & adapters**: o domínio depende só de interfaces (`internal/ports`); cada integração é um adaptador.
- **Núcleo funcional, casca imperativa**: `core.Apply(state, input) → (state, effects)` é puro. O `supervisor` grava os eventos e executa os efeitos.
- As regras de dependência entre pacotes estão em [packages.md](../../docs/architecture/packages.md) e são verificadas no CI (`internal/archtest`).

## Consequências

- Toda transição vira um teste de tabela, sem fakes de rede.
- Trocar ou adicionar um tracker ou harness não toca o domínio.
- Há uma camada a mais de tipos de fronteira (`Input`, `Effect`), e mais disciplina para não vazar I/O no núcleo.
