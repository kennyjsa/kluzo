# kluzo

> *Kluzo* é "eclusa" em esperanto.

Orquestrador que **resolve uma milestone inteira** com agentes de código (Claude Code, OpenCode). Você refina e aprova o plano no **upstream**; o **downstream** executa sozinho o grafo de issues, integra tudo na branch da milestone passando por gates e entrega o PR `milestone → main` para o seu merge.

```
issue/*  ──►  milestone/<nome>  ──►  main
          (Kluzo)            (você)
```

**Status:** em desenvolvimento (v0.1 · núcleo). Veja o [roadmap](docs/planning/roadmap.md).

## Documentação

- [Mapa da documentação](docs/README.md)
- [Visão](docs/product/vision.md) · [Conceitos](docs/product/concepts.md) · [Ciclo de vida](docs/product/lifecycle.md)
- [Roadmap](docs/planning/roadmap.md) · [Features](docs/product/features/)
- [Arquitetura](docs/architecture/overview.md) · [Contratos](docs/contracts/) · [Decisões (ADRs)](.agents/adr/)
- [Desenvolvimento](docs/development.md) · [Protótipo](docs/design/prototype/)
