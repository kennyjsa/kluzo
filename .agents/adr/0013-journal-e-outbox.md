# 0013. Journal como fonte da verdade e outbox de efeitos idempotentes

- Status: aceito
- Data: 2026-11-07

## Contexto

O Kluzo executa milestones que duram horas ou dias, e o processo pode morrer a qualquer momento (notebook dormindo, deploy, crash). Ao voltar, ele não pode perder trabalho nem repetir efeitos externos (comentário duplicado, issue criada duas vezes, merge em dobro). Um motor de workflow externo (Temporal) resolveria, ao custo de mais um serviço para operar.

## Decisão

- Cada transição grava **eventos** num journal append-only (tabela `events`). As tabelas de estado são **projeções** atualizadas na mesma transação.
- Os efeitos decididos pelo núcleo vão para uma tabela **`effects` (outbox)** na mesma transação. Um executor processa os pendentes e os marca como concluídos.
- Todo efeito tem uma **chave de idempotência**. Os adaptadores a usam para detectar uma execução anterior: marcador oculto no comentário, busca do PR pela branch, `idemKey` na criação de issue.

## Consequências

- Matar o processo em qualquer ponto não perde nem duplica trabalho. Existe um teste com kill em pontos sorteados.
- O journal serve de auditoria e de fonte dos números do relatório da milestone.
- Os adaptadores precisam implementar a detecção de duplicidade, coberta pela suíte de contrato.
- Sem dependência de serviço externo.
