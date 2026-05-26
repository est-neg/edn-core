# Catalog, Packages, Plans, Organizations, and Tenants Architecture

## Problem Framing

O fluxo atual vende um `Plan` achatado em MongoDB e cria `orders` referenciando apenas `plan_id` e `plan_slug`. Isso atende um checkout simples, mas nao atende bem os requisitos novos:

- vender produtos e servicos atomicos reutilizaveis
- montar pacotes versionados a partir desses itens
- vender planos comerciais que reutilizam os pacotes
- relacionar pedidos com plano, pacote, produtos e servicos sem perder historico
- suportar organizacoes com varios tenants isolados
- manter idempotencia forte em checkout, webhook e ativacao de assinatura
- operar em Cloud Run com MongoDB Atlas como unica persistencia duravel

Esta nota substitui a direcao anterior baseada em MariaDB. Para este escopo, MongoDB passa a ser a unica fonte de verdade duravel, com Redis restrito a cache, rate limiting e locks efemeros.

## Assumptions And Constraints

- MongoDB Atlas e a truth para catalogo, pacotes, planos, pedidos, pagamentos, assinaturas e outbox.
- Redis nao guarda estado de negocio duravel.
- Cloud Run continua sendo a plataforma de execucao.
- Checkout e webhook precisam de idempotencia assertiva baseada em indices unicos no MongoDB, nao apenas em cache.
- Caminhos transacionais usam leituras do primary e `writeConcern: majority`.
- Multi-document transactions exigem Atlas em replica set com suporte a sessao; tier minimo recomendado: M10.

## Architecture Decisions

### 1. Organization e tenant viram dimensoes de ownership explicitas

`Organization` e a raiz administrativa, comercial e de billing. Uma organizacao pode possuir varios `tenants`. O tenant e a unidade de isolamento operacional para catalogo, planos, pedidos e assinaturas.

Toda colecao autoritativa de negocio deve carregar `organization_id` e `tenant_id`, exceto a propria colecao `organizations`.

### 2. MongoDB-only com referencias leves e snapshots fortes

O sistema precisa de duas coisas ao mesmo tempo:

- navegacao e analytics por relacionamento
- consistencia historica sem depender de joins em dados vivos

Por isso, os documentos de negocio usam duas camadas:

- `refs` leves para apontar para organizacao, tenant, plano, pacote e itens
- `snapshots` embutidos em `orders` e `subscriptions` para congelar a oferta comprada

### 3. Orders carregam lineage comercial completo

Relacionar ordens com planos, produtos e servicos e obrigatorio. Cada `order` deve persistir:

- `plan_ref` com `plan_uuid` e `plan_version`
- `package_ref` com `package_uuid` e `package_version`
- `item_refs[]` com `item_uuid`, `item_type`, `item_slug` e `quantity`
- `commercial_snapshot` com nome, preco, ciclo, moeda, pacote e itens resolvidos

Isso preserva rastreabilidade e evita depender de dados atuais do catalogo para auditar compras antigas.

### 4. Idempotencia forte vira um aggregate proprio

Nao basta um campo `idempotency_key` solto no pedido. Para checkout publico e preciso registrar um documento proprio em `idempotency_keys` com:

- `operation`
- `organization_id`
- `tenant_id`
- `idempotency_key`
- `request_hash`
- `resource_type`
- `resource_id`
- `status`
- `expires_at`

Regras:

- mesma chave + mesmo hash retorna o mesmo recurso
- mesma chave + hash diferente retorna conflito
- nenhuma chamada externa ao provider ocorre antes da reserva autoritativa da chave

### 5. Webhook dedupe precisa de autoridade duravel

Redis pode acelerar, mas a autoridade final fica em MongoDB com indices unicos em `provider + event_hash` e `provider + transaction_nsu`. O status vindo do payload nunca e tratado como verdade sem verificacao server-side no provider.

### 6. Cloud Run deve refletir trust boundaries

O backend pode continuar no mesmo repositorio, mas os boundaries nao devem ficar misturados sem controle:

- checkout publico
- webhook publico do provider
- administracao de catalogo e tenancy
- APIs de parceiros

Admin e partner nao devem depender do mesmo boundary publico liberado por `--allow-unauthenticated`.

## Proposed Module Boundaries

### `internal/organizations`

Responsavel por:

- cadastro e ativacao de organizacoes
- configuracao comercial global
- ownership de tenants

### `internal/tenants`

Responsavel por:

- cadastro de tenant
- vinculo com organizacao
- canais habilitados
- politicas por tenant

### `internal/catalog`

Responsavel por itens atomicos:

- produto
- servico
- metadados funcionais
- ativacao e desativacao

### `internal/packages`

Responsavel por composicao versionada de itens de catalogo.

### `internal/plans`

Responsavel pela oferta comercial vendavel e versionada.

### `internal/checkout`

Responsavel por:

- resolver tenant e plano publicados
- aplicar idempotencia forte
- criar `order`
- persistir lineage e snapshot
- coordenar chamada ao provider

### `internal/payments`

Responsavel por:

- verificar pagamentos no provider
- persistir transacoes
- processar webhooks com dedupe forte
- transicionar pedidos

### `internal/subscriptions`

Responsavel por ativacao, renovacao, cancelamento e expiracao com base no snapshot comprado.

## Authoritative MongoDB Data Model

### `organizations`

Documento raiz de ownership:

```text
org_uuid
slug
name
billing_email
active
created_at
updated_at
```

Indices:

- unique `org_uuid`
- unique `slug`

### `tenants`

Tenant pertencente a uma organizacao:

```text
tenant_uuid
organization_id
slug
name
channels[]
active
created_at
updated_at
```

Indices:

- unique `tenant_uuid`
- unique compound `organization_id + slug`
- compound `organization_id + active`

### `catalog_items`

Itens atomicos de catalogo:

```text
item_uuid
organization_id
tenant_id
type                # product | service
slug
name
description
delivery_mode       # digital | physical | api_grant
active
metadata
created_at
updated_at
```

Indices:

- unique `item_uuid`
- unique compound `tenant_id + slug`
- compound `tenant_id + type + active`

### `packages`

Pacote versionado com snapshot leve dos itens:

```text
package_uuid
organization_id
tenant_id
slug
version
name
status              # draft | published | archived
items[]
  item_uuid
  item_type
  item_slug
  item_name
  quantity
  delivery_mode
created_at
updated_at
```

Indices:

- unique `package_uuid`
- unique compound `tenant_id + slug + version`
- compound `tenant_id + slug + status`

### `plans`

Plano versionado apontando para um pacote publicado:

```text
plan_uuid
organization_id
tenant_id
slug
version
package_ref
  package_uuid
  version
package_snapshot
name
billing_cycle       # monthly | annual | one_time
price_cents
currency
max_installments
channel             # web | mobile | partner | all
active
valid_from
valid_until
created_at
updated_at
```

Indices:

- unique `plan_uuid`
- unique compound `tenant_id + slug + billing_cycle + version`
- compound `tenant_id + channel + active + billing_cycle`
- partial compound `tenant_id + slug + billing_cycle` where `active = true`

> **Nota de migração:** `BootstrapTenancyStorage`/`ensureVersionedPlansIndexes` remove automaticamente o índice legado `idx_versioned_plans_tenant_slug_version_unique` antes de criar os novos índices. Não há etapa manual em staging ou produção.

### `orders`

Pedido autoritativo, com lineage e snapshot comercial completos:

```text
order_uuid
order_nsu
organization_id
tenant_id
idempotency_ref
  operation
  key
plan_ref
  plan_uuid
  version
package_ref
  package_uuid
  version
item_refs[]
  item_uuid
  item_type
  item_slug
  quantity
commercial_snapshot
  plan
  package
  items[]
amount_cents
currency
customer
status
channel
provider
provider_checkout_url
invoice_slug
receipt_url
created_at
updated_at
```

Indices:

- unique `order_uuid`
- unique `order_nsu`
- compound `tenant_id + status + created_at`
- compound `tenant_id + plan_ref.plan_uuid + status`
- compound `tenant_id + customer.email + status`

### `subscriptions`

Assinatura derivada de um pedido pago:

```text
subscription_uuid
organization_id
tenant_id
order_ref
  order_uuid
  order_nsu
plan_ref
  plan_uuid
  version
package_ref
  package_uuid
  version
item_refs[]
plan_snapshot
customer_email
customer_document
status
starts_at
ends_at
canceled_at
created_at
updated_at
```

Indices:

- unique `subscription_uuid`
- unique `order_ref.order_uuid`
- compound `tenant_id + customer_email + status`
- compound `tenant_id + status + ends_at`

### `payments`

Transacao financeira persistida:

```text
payment_uuid
organization_id
tenant_id
order_nsu
transaction_nsu
invoice_slug
amount_cents
paid_amount_cents
installments
capture_method
receipt_url
status
provider
raw_payload
created_at
updated_at
```

Indices:

- unique `payment_uuid`
- unique `transaction_nsu`
- compound `tenant_id + order_nsu`

### `webhook_events`

Recibo bruto de webhook com dedupe duravel:

```text
event_uuid
organization_id
tenant_id
provider
event_hash
transaction_nsu
order_nsu
status
raw_payload
received_at
processed_at
```

Indices:

- unique compound `provider + event_hash`
- unique compound `provider + transaction_nsu`
- TTL em `received_at` para limpeza operacional controlada

### `idempotency_keys`

Registro autoritativo de idempotencia:

```text
idempotency_uuid
operation
organization_id
tenant_id
idempotency_key
request_hash
resource_type
resource_id
status              # started | completed | failed
response_code
expires_at
created_at
updated_at
```

Indices:

- unique compound `tenant_id + operation + idempotency_key`
- compound `tenant_id + operation + created_at`
- TTL em `expires_at` apenas para operacoes efemeras de checkout

### `outbox_events`

Eventos confiaveis para publicacao assincrona:

```text
event_uuid
organization_id
tenant_id
event_type
aggregate_type
aggregate_id
payload
published
created_at
published_at
```

Indices:

- unique `event_uuid`
- compound `published + created_at`
- TTL em `published_at` apos publicacao, se necessario

## Relationship And Snapshot Strategy

Para pedidos e assinaturas, usar ao mesmo tempo:

- referencias estruturadas para analytics e navegacao
- snapshots embutidos para historico e cobranca

Regra pratica:

- `orders` e `subscriptions` nunca devem depender de lookup no `catalog_items`, `packages` ou `plans` para saber o que foi comprado
- `orders` e `subscriptions` sempre devem expor `plan_ref`, `package_ref` e `item_refs[]`
- mudanca em preco ou composicao gera nova versao; nao mutacao in-place

## Transaction Boundaries

### Checkout reservation transaction

Usar uma sessao Mongo curta para:

1. reservar `idempotency_keys`
2. validar conflito de `request_hash`
3. criar `order` com lineage e snapshot
4. criar `outbox_events` de `order.created`

A chamada HTTP ao provider de pagamento deve ocorrer fora da transacao.

### Checkout completion transaction

Depois da resposta do provider:

1. atualizar `orders.provider_checkout_url` e `invoice_slug`
2. marcar `idempotency_keys` como `completed`

### Webhook payment transaction

Usar uma sessao Mongo curta para:

1. inserir `webhook_events` com chave unica
2. persistir ou atualizar `payments`
3. atualizar `orders.status`
4. criar ou atualizar `subscriptions`
5. inserir `outbox_events`

Nenhuma chamada externa ao provider deve ocorrer dentro da transacao.

## API And Workflow Outline

### Administrative surfaces

APIs administrativas sugeridas:

- `POST /internal/organizations`
- `POST /internal/organizations/{orgId}/tenants`
- `POST /internal/catalog/items`
- `POST /internal/packages`
- `POST /internal/plans`
- `POST /internal/plans/{planId}/versions`

Essas rotas devem ficar atras de autenticacao forte e autorizacao server-side por papel e escopo.

### Public purchase flow

1. resolver `organization` e `tenant` no servidor
2. listar planos ativos do tenant
3. receber `Idempotency-Key` no checkout
4. reservar chave idempotente e criar pedido com snapshot
5. chamar provider
6. persistir URL de checkout
7. processar webhook com verificacao no provider
8. ativar assinatura a partir do snapshot comprado

## Security And Trust Boundaries

- rotas administrativas nao devem ficar expostas no mesmo boundary publico de checkout
- `organization_id`, `tenant_id`, `channel`, `amount_cents` e `provider` nunca sao aceitos como verdade do cliente
- toda consulta quente e todo indice unico devem ser tenant-scoped
- logs nao devem expor CPF, telefone, e-mail bruto, tokens, paths secretos ou payload raw de webhook
- checkout publico precisa de `Idempotency-Key`; webhook precisa de replay protection e verificacao server-side

## Cloud Run And MongoDB Atlas Operational Notes

- Atlas deve usar replica set com suporte a transacoes
- usar `writeConcern: majority` e `readPreference: primary` em checkout, webhook e status de pedido
- limitar pool por instancia do Cloud Run para evitar explosao de conexoes no Atlas
- manter um worker separado para `outbox_events`
- readiness deve depender de `Ping` no Mongo e bootstrap idempotente de colecoes e indices
- segredos continuam em Secret Manager; nao gravar URI do Mongo ou tokens em arquivos versionados

Configuracao recomendada de pool por instancia:

```text
minPoolSize=1
maxPoolSize=5
maxConnecting=2
serverSelectionTimeoutMS=5000
connectTimeoutMS=10000
socketTimeoutMS=45000
```

## Risks And Common Mistakes

- usar apenas Redis para dedupe e perder idempotencia sob retry ou failover
- criar indice unico global sem `tenant_id` e causar colisao cross-tenant
- mutar plano ou pacote referenciado e quebrar historico
- aceitar `tenant_id` vindo do cliente sem resolucao server-side
- chamar provider dentro de transacao Mongo longa
- manter admin APIs atras de `--allow-unauthenticated`
- gravar PII ou segredo em log

## Recommended Handoff Order

1. formalizar a excecao MongoDB-only e os invariantes de idempotencia
2. criar bootstrap de colecoes e indices tenant-scoped
3. introduzir `organizations` e `tenants`
4. introduzir `catalog`, `packages` e `plans` versionados
5. atualizar checkout para `Idempotency-Key`, lineage e snapshots
6. endurecer webhook, pagamentos e ativacao de assinatura com transacao curta
7. separar boundaries de Cloud Run para admin, partner e publico

## Impact On Current Code

O desenho atual em [internal/checkout/types.go](internal/checkout/types.go), [internal/checkout/mongodb_repository.go](internal/checkout/mongodb_repository.go), [internal/payments/service.go](internal/payments/service.go) e [internal/platform/mongodb/mongodb.go](internal/platform/mongodb/mongodb.go) ja mostra uma base MongoDB-first, mas ainda sem tenancy, sem lineage comercial completo e sem idempotencia forte de checkout. A implementacao precisa evoluir essa base sem depender de MariaDB.
