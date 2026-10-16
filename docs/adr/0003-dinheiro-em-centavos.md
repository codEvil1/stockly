# 0003 — Valores monetários em centavos (`int64`)

**Status:** Aceito

## Contexto
`float64` não representa decimais exatos (0,1 + 0,2 ≠ 0,3), o que é inaceitável para dinheiro.

## Decisão
Preços e totais são inteiros em centavos (`price_cents`, `total_cents`, `BIGINT` no banco). O domínio impõe limites (quantidade ≤ 1.000.000, preço ≤ 10.000.000.000 centavos, ≤ 200 itens) de modo que o total máximo (≈ 2·10¹⁸) caiba em `int64` sem overflow.

## Consequências
- (+) Aritmética exata e simples; sem dependência de biblioteca decimal.
- (−) Assume uma única moeda com 2 casas decimais. Suportar várias moedas exigiria um tipo `Money` com código da moeda. Se algum limite subir, o risco de overflow em `Order.Total()` deve ser reavaliado.
