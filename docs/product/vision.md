# Visão

> *Kluzo* é "eclusa" em esperanto: a estrutura que leva um barco de um nível do rio a outro passando por comportas que só abrem quando a câmara está pronta. É o desenho do produto: o trabalho sai do upstream, atravessa portões e chega à `main`.

## Problema

Os agentes de código já escrevem boa parte do código. O gargalo virou tudo o que fica em volta:
- quebrar o trabalho e decidir a ordem;
- evitar que agentes em paralelo colidam;
- revisar;
- integrar;
- saber o que está acontecendo.

As ferramentas existentes orquestram **sessões de agente**: uma issue vira um agente, que vira um PR. Ninguém cuida do **conjunto**: as dependências entre as issues, as lacunas que ninguém previu, a integração na ordem certa e o momento em que a entrega inteira está pronta.

## Proposta

O Kluzo **resolve uma milestone inteira** de forma autônoma. Ele orquestra o **estado da milestone**, e os agentes são os trabalhadores.

- O humano atua no **upstream**: entrevistas de refino e aprovação do plano.
- O **downstream** executa sozinho o grafo de issues: implementação, revisão, gates e integração.
- O resultado é uma branch da milestone integrada e verificada, com um relatório, pronta para o merge humano na `main`.

```
issue/*  ──►  milestone/<nome>  ──►  main
       (Kluzo integra)      (humano faz o merge)
```

## Princípios

1. **A milestone é a unidade.** Ela tem estado próprio, plano aprovado e gate próprio.
2. **O código controla, o modelo julga.** O núcleo (agendamento, estados, fila de merge, gates) é determinístico. O LLM só entra em etapas delimitadas, com saída estruturada e validada.
3. **"Pronto" é decidido por gates, não pelo agente.** Testes, lint e E2E rodam fora do agente.
4. **O humano decide o que é irreversível ou ambíguo.** Plano, lacunas novas, escalações e o merge na `main` são decisões humanas.
5. **Tudo é auditável e retomável.** Cada passo vai para um journal, e um crash retoma do último passo concluído.
6. **Agentes não conversam entre si.** Toda comunicação passa pelo Kluzo.
7. **Independente de ferramenta.** Os harnesses (Claude Code, OpenCode) e os trackers (GitHub, GitLab) entram por adaptadores.

## Não-objetivos

- Substituir o tracker. O tracker continua sendo a fonte da verdade da **definição** do trabalho.
- Embutir um LLM próprio no orquestrador.
- Ser uma IDE ou um editor de código.
- Fazer o merge na `main`.

## Para quem

Desenvolvedores e times pequenos que já usam agentes de código e querem entregar milestones inteiras com o tempo humano concentrado no refino e na revisão final.

Documentos relacionados:
- [Conceitos](concepts.md)
- [Ciclo de vida](lifecycle.md)
- [Interfaces](interfaces.md)
- [Features](features/)
