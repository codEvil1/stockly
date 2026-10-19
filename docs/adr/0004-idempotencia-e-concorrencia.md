# 0004 — Idempotência no POST e atualização condicional

**Status:** Aceito

## Contexto
- Clientes repetem requisições após timeouts de rede. Sem proteção, um retry cria pedidos duplicados.
- Duas requisições simultâneas podem tentar mudar o mesmo pedido (ex.: concluir e cancelar ao mesmo tempo).

## Decisão
**Criação:** o cliente envia o header `Idempotency-Key`. A chave é guardada em `orders.idempotency_key` com índice `UNIQUE`. Repetição com o mesmo `customer_id` e itens devolve o pedido original (`200` + `Idempotent-Replay: true`); reutilizar a mesma chave com parâmetros divergentes responde `409 Conflict`. A checagem prévia cobre o caso comum; a constraint `UNIQUE` resolve a corrida entre duas requisições simultâneas, e a perdedora devolve o pedido da vencedora.

**Transições:** `UPDATE orders SET status = ? WHERE id = ? AND status = ?`. Se nenhuma linha for afetada, outra requisição já mudou o pedido, e a API responde `409 Conflict`. É concorrência otimista, sem locks longos.

## Consequências
- (+) Retries seguros e sem estado extra em memória; funciona com várias réplicas.
- (+) Proteção contra reutilização acidental da mesma chave com outro cliente ou outros itens (`409 Conflict`).
- (−) Chaves ficam gravadas por tempo indeterminado (sem expiração).
