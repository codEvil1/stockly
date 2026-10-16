# 0004 — Idempotência no POST e atualização condicional

**Status:** Aceito

## Contexto
- Clientes repetem requisições após timeouts de rede. Sem proteção, um retry cria pedidos duplicados.
- Duas requisições simultâneas podem tentar mudar o mesmo pedido (ex.: concluir e cancelar ao mesmo tempo).

## Decisão
**Criação:** o cliente envia o header `Idempotency-Key`. A chave é guardada em `orders.idempotency_key` com índice `UNIQUE`. Repetição devolve o pedido original (`200` + `Idempotent-Replay: true`). A checagem prévia cobre o caso comum; a constraint `UNIQUE` resolve a corrida entre duas requisições simultâneas, e a perdedora devolve o pedido da vencedora.

**Transições:** `UPDATE orders SET status = ? WHERE id = ? AND status = ?`. Se nenhuma linha for afetada, outra requisição já mudou o pedido, e a API responde `409 Conflict`. É concorrência otimista, sem locks longos.

## Consequências
- (+) Retries seguros e sem estado extra em memória; funciona com várias réplicas.
- (−) A chave não está atrelada a um hash do corpo: reutilizar a mesma chave com um corpo diferente devolve o primeiro pedido em vez de erro. Validar o hash do corpo é um próximo passo.
- (−) Chaves ficam gravadas por tempo indeterminado (sem expiração).
