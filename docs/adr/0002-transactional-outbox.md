# 0002 — Transactional outbox para eventos de domínio

**Status:** Aceito

## Contexto
Mudanças de estado do pedido precisam gerar eventos (`OrderCreated`, `OrderCompleted`, `OrderCanceled`) para outros sistemas. Gravar no banco e publicar no broker são duas operações sem transação em comum: publicar após o commit pode perder o evento, e publicar antes pode anunciar algo que nunca foi gravado.

## Decisão
- O evento é gravado na tabela `outbox` **na mesma transação** da mudança de estado.
- Um *relay* (`app.Relay`) faz polling, lê o lote pendente com `SELECT ... FOR UPDATE SKIP LOCKED`, publica via `app.Publisher` e marca `published_at`.
- Hoje o publisher só registra em log (`infra/eventlog`). Trocar por Kafka/SQS exige apenas implementar `app.Publisher`.

## Consequências
- (+) Nunca se perde nem se inventa um evento: o fato e o evento são atômicos.
- (+) `SKIP LOCKED` permite várias réplicas do relay sem processar o mesmo lote ao mesmo tempo.
- (−) Entrega **at-least-once**: se o commit falhar depois de publicar, o evento é reenviado. Consumidores devem deduplicar por `Event.ID`.
- (−) Polling adiciona latência (padrão 2 s) e a transação fica aberta durante a publicação; por isso os lotes são pequenos.
- (−) A tabela cresce. Falta uma rotina de limpeza de eventos antigos já publicados (ver roadmap).
