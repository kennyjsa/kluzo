# Documentação do Kluzo

A documentação é organizada em camadas, da mais estável (por que o produto existe) à mais específica (formatos exatos). **Cada informação mora em um lugar só.** Os outros documentos apontam para ela, em vez de repeti-la.

## Mapa

| Camada | Pergunta que responde | Onde |
|---|---|---|
| **Produto** | O que é, para quem, como se comporta | [product/](product/) |
| **Planejamento** | O que entra em cada entrega, e em que ordem | [planning/](planning/) |
| **Design** | Como a interface se parece | [design/](design/) |
| **Arquitetura** | Como é construído e por quê | [architecture/](architecture/) |
| **Decisões** | Por que cada escolha técnica foi feita | [.agents/adr/](../.agents/adr/) |
| **Contratos** | Formatos e interfaces exatos que o código implementa | [contracts/](contracts/) |
| **Desenvolvimento** | Como compilar, testar e contribuir | [development.md](development.md) |
| **Pesquisa** | O que existe no mercado, investigações e validações adversárias | [research/](research/) |

### Produto
- [vision.md](product/vision.md): problema, proposta, princípios e não-objetivos.
- [concepts.md](product/concepts.md): glossário, hierarquia, grafo, papéis, labels e fontes da verdade.
- [lifecycle.md](product/lifecycle.md): upstream, aprovação, downstream, integração e fechamento.
- [interfaces.md](product/interfaces.md): web responsiva, CLI, tracker e notificações.
- [features/](product/features/): uma feature por arquivo (F01–F30), com critérios de aceite e status.

### Planejamento
- [roadmap.md](planning/roadmap.md): sequência de entregas e features por versão (fonte única da ordem).
- [milestones/](planning/milestones/): itens de cada entrega, já no formato de issue.
- [questions.md](planning/questions.md): dúvidas de implementação em aberto, com a escolha provisória do código.

### Design
- [README.md](design/README.md): princípios, tokens, cores de estado e componentes-chave.
- [prototype/](design/prototype/): protótipo navegável (19 telas).

### Arquitetura
- [overview.md](architecture/overview.md): componentes, laço do supervisor, estado e robustez.
- [packages.md](architecture/packages.md): layout do repositório e regras de dependência.
- [patterns.md](architecture/patterns.md): padrões de projeto adotados e o que é proibido.
- [stack.md](architecture/stack.md): cada peça da stack, com o motivo.
- [integrations.md](architecture/integrations.md): tracker, SCM, CI, git e harnesses.
- [security.md](architecture/security.md): modelo de segurança.

### Contratos
- [README.md](contracts/README.md): índice e regras gerais (versões, ids, dinheiro).
- `state-machine`, `data-model`, `config`, `plan`, `results`, `events`, `interfaces`, `mcp`, `prompt`.

## Ordem de leitura

- **Para entender o produto:** vision → concepts → lifecycle → interfaces.
- **Para implementar um item:** o item em `planning/milestones/` → as features citadas → os contratos envolvidos → `architecture/packages.md` → `development.md`.
- **Para decidir algo novo:** os ADRs existentes → `architecture/` → um ADR novo.

## Regras de manutenção

- **Precedência em caso de divergência:** contratos > ADRs > arquitetura > produto. Quem diverge é corrigido no mesmo PR.
- **Mudou um formato, interface ou estado?** Atualize `contracts/` no mesmo PR do código.
- **Mudou a arquitetura, a stack ou um padrão?** ADR novo em `.agents/adr/`.
- **Feature entregue?** Atualize o status dela em `product/features/` e no índice.
- **Documentação em português; nomes técnicos (estados, chaves, eventos) em inglês** (ADR 0008).
