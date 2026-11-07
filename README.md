# kluzo

> *Kluzo* é "eclusa" em esperanto.

Orquestrador que **resolve uma milestone inteira** com agentes de código (Claude Code, OpenCode). Você refina e aprova o plano no **upstream**; o **downstream** executa sozinho o grafo de issues, integra tudo na branch da milestone passando por gates e entrega o MR `milestone → main` para o seu merge.

```
issue/*  ──►  milestone/<nome>  ──►  main
          (Kluzo)            (você)
```

Status: em especificação.

- [Especificação](docs/SPEC.md)
- [Features](docs/FEATURES.md)
- [Decisões (ADRs)](docs/adr/)
- [Referências](docs/references.md)
