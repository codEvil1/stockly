# Architecture Decision Records

Registro curto das decisões de arquitetura que moldam o projeto: o contexto, o que foi decidido e o que se aceita como custo. Cada ADR é imutável; se a decisão mudar, cria-se um novo ADR que substitui o anterior.

| # | Decisão | Status |
|---|---------|--------|
| [0001](0001-hexagonal-por-feature.md) | Arquitetura hexagonal organizada por feature | Aceito |
| [0002](0002-transactional-outbox.md) | Transactional outbox para eventos de domínio | Aceito |
| [0003](0003-dinheiro-em-centavos.md) | Valores monetários em centavos (`int64`) | Aceito |
| [0004](0004-idempotencia-e-concorrencia.md) | Idempotência no POST e atualização condicional | Aceito |
