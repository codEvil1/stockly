# 0001 — Arquitetura hexagonal organizada por feature

**Status:** Aceito

## Contexto
O serviço precisa de regras de negócio testáveis sem banco nem HTTP, e de espaço para crescer com novas áreas (estoque, clientes) sem virar um único pacote gigante.

## Decisão
- Código organizado **por feature** (`internal/orders`), e cada feature dividida em três camadas:
  - `domain`: agregados, regras e a interface do repositório (a "porta"). Não importa HTTP nem SQL.
  - `app`: casos de uso, que orquestram o domínio. Depende só de `domain`.
  - `infra`: adaptadores (`httpapi`, `mysql`, `eventlog`) que implementam as portas.
- Dependências apontam sempre para dentro: `infra → app → domain`.
- `cmd/api/main.go` é o *composition root*: instancia adaptadores e os injeta. Código transversal (config, conexão com o banco, servidor HTTP) fica em `internal/platform`.
- Tudo sob `internal/`, para que o compilador impeça outros módulos de importar detalhes.

## Consequências
- (+) Regras de negócio e casos de uso testam-se com fakes em memória; o SQL é testado à parte com o MySQL real.
- (+) Trocar MySQL por outro banco, ou HTTP por gRPC, mexe só em `infra`.
- (−) Mais arquivos e mapeamentos (DTO ↔ domínio) do que um CRUD direto. Aceito: o custo é pequeno e o domínio tem invariantes reais (limites, transições de status).
- Quando surgir uma segunda feature, ela ganha o próprio diretório com as mesmas três camadas; features só conversam por interfaces ou eventos, nunca importando o `infra` uma da outra.
