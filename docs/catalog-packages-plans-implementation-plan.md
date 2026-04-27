# Catalog, Packages, Plans, Organizations, and Tenants Implementation Plan

## Goal

Implementar uma estrutura MongoDB-only para organizacoes, tenants, catalogo, pacotes, planos, pedidos, pagamentos e assinaturas, preservando:

- isolamento por tenant
- rastreabilidade entre ordem, plano, pacote, produtos e servicos
- idempotencia forte em checkout e webhook
- seguranca adequada para Cloud Run
- evolucao backward-compatible por fases pequenas

## Non-Negotiable Invariants

- MongoDB Atlas e a unica truth duravel.
- Redis fica restrito a cache, locks efemeros e rate limiting.
- Toda colecao autoritativa carrega `organization_id` e `tenant_id` quando aplicavel.
- Toda unicidade quente deve ser prefixada por `tenant_id` quando fizer sentido de negocio.
- Checkout requer `Idempotency-Key`.
- Webhook nunca promove estado sem verificacao server-side no provider.
- `orders` e `subscriptions` armazenam `refs` e `snapshots`.

## Delivery Strategy

O rollout deve acontecer por fatias pequenas, com testes executaveis, sem cleanup destrutivo no primeiro release.

## Phase 0: Architecture Lock And Infra Preconditions

Objetivo:

- consolidar a nota arquitetural MongoDB-only
- assumir Atlas com suporte a transacoes
- definir boundaries para Cloud Run

Entregas:

- nota arquitetural aprovada
- configuracao de Atlas tier compativel com sessao Mongo
- confirmacao de Secret Manager para segredos de ambiente

Validacao:

- bootstrap atual continua funcional
- `readyz` so sobe apos `Ping` e bootstrap

## Phase 1: Mongo Bootstrap And Index Contracts

Objetivo:

- materializar colecoes e indices idempotentes para o novo dominio

Colecoes a bootstrapar:

- `organizations`
- `tenants`
- `catalog_items`
- `packages`
- `plans`
- `orders`
- `subscriptions`
- `payments`
- `webhook_events`
- `idempotency_keys`
- `outbox_events`

Implementacao:

- ampliar `internal/platform/mongodb`
- adicionar nomes de colecao no `config`
- garantir indices tenant-scoped e unicos
- `ensureVersionedPlansIndexes` remove automaticamente o indice legado
  `idx_versioned_plans_tenant_slug_version_unique` antes de criar os novos indices;
  nenhum passo manual e necessario em staging ou producao

Testes obrigatorios:

- bootstrap em banco vazio
- bootstrap idempotente em banco ja inicializado
- contrato de indices por colecao

## Phase 2: Organizations And Tenants Foundation

Objetivo:

- criar a base de ownership e isolamento

Implementacao:

- novo modulo `internal/organizations`
- novo modulo `internal/tenants`
- tipos, repositorios Mongo e servicos minimos
- validacoes de vinculo `organization -> tenant`

Escopo minimo:

- criar organizacao
- criar tenant pertencente a uma organizacao
- listar tenants por organizacao
- resolver tenant ativo por `organization + tenant slug`

Testes obrigatorios:

- mesmo slug de tenant em organizacoes diferentes
- rejeicao de tenant em organizacao errada
- rejeicao de organizacao ou tenant inativos

## Phase 3: Catalog, Packages, And Plans

Objetivo:

- introduzir a estrutura comercial versionada

Implementacao:

- novo modulo `internal/catalog`
- novo modulo `internal/packages`
- novo modulo `internal/plans`
- suporte a `product` e `service`
- versao imutavel para pacote e plano quando houver referencia em ordem

Regras:

- mudar composicao gera nova versao de pacote
- mudar preco, ciclo ou canal gera nova versao de plano
- desativacao pode ser in-place apenas para parar novas vendas

Testes obrigatorios:

- slug unico por tenant
- mesma combinacao de slug/versao nao duplica
- plano ativo mais recente por tenant/canal/ciclo
- pacote ou plano referenciado nao pode ser mutado

## Phase 4: Assertive Checkout Idempotency And Order Lineage

Objetivo:

- endurecer a criacao de checkout antes de expandir o contrato publico

Implementacao:

- estender `internal/checkout/types.go`
- adicionar `organization_id`, `tenant_id`, `plan_ref`, `package_ref`, `item_refs`, `commercial_snapshot`
- criar `IdempotencyRepository`
- exigir header `Idempotency-Key`
- persistir `request_hash`
- transacao curta para reservar idempotencia, criar pedido e outbox

Regra de conflito:

- mesma chave + mesmo hash retorna o mesmo pedido
- mesma chave + hash diferente retorna conflito

Testes obrigatorios:

- retries sequenciais retornam o mesmo `order_nsu`
- concorrencia com 20 ou mais requests gera um unico pedido
- mesma chave com payload divergente falha
- provider nao e chamado duas vezes para a mesma chave valida

## Phase 5: Payments, Webhooks, And Subscriptions

Objetivo:

- manter consistencia entre pagamento, pedido e assinatura

Implementacao:

- endurecer `webhook_events` com dedupe duravel
- usar verificacao server-side no provider antes de promover estado
- transacao curta para `webhook_events`, `payments`, `orders`, `subscriptions` e `outbox_events`
- ativar assinatura a partir do snapshot da ordem

Testes obrigatorios:

- mesmo webhook nao ativa duas vezes
- evento fora de ordem nao quebra o fluxo
- mismatch de valor ou moeda cai em `pending_review`
- assinatura nao depende de lookup no plano vivo

## Phase 6: Public And Administrative Surfaces

Objetivo:

- expor o que for necessario sem misturar trust boundaries

Superficies:

- publico: listagem de planos e criacao de checkout
- admin: organizacoes, tenants, catalogo, pacotes e planos
- webhook: endpoint dedicado do provider
- partner: APIs autenticadas de integracao, quando necessario

Implementacao:

- proteger admin e partner com autenticacao forte e autorizacao server-side
- manter publico limitado a leitura publicada e checkout
- ocultar dados sensiveis em logs e erros

## Phase 7: Cloud Run Rollout And Operational Hardening

Objetivo:

- colocar o slice em producao com rollback simples

Implementacao:

- revisar `cloudbuild.development.yaml` e `cloudbuild.yaml`
- isolar boundaries por servico ou por rota protegida
- manter segredos em Secret Manager
- configurar pools Mongo conservadores por instancia
- manter worker separado para outbox

Smoke checks obrigatorios:

- `livez` e `readyz`
- listagem publica de planos por tenant
- checkout idempotente
- webhook duplicado
- ausencia de PII e segredos em logs

Rollback:

- rollback por revisao do Cloud Run
- sem drop de colecao ou indice no mesmo release funcional

## Security Requirements From Review

- toda operacao administrativa exige authn forte e authz server-side
- nenhum indice unico de negocio deve ignorar `tenant_id` quando o dominio for tenant-scoped
- `tenant_id`, `organization_id`, `channel` e `amount_cents` nunca sao aceitos do cliente como verdade
- webhook precisa de dedupe duravel, replay protection e verificacao no provider
- logs nao podem conter e-mail, CPF, telefone, token, URI do Mongo ou payload bruto sensivel

## QA Requirements From Review

- comecar pelos testes `P0` de tenancy, checkout, payments e bootstrap
- usar replica set real para testes com transacao Mongo
- testar concorrencia e falha parcial, nao apenas happy path
- nao aprovar por inspecao quando houver comportamento executavel a validar

## Requested Agent Handoff

### `database-engineer`

Ja apoiou a modelagem MongoDB-only, as colecoes e os indices.

### `security-reviewer`

Ja sinalizou bloqueios importantes: boundaries publicos, isolamento tenant-scoped, PII em logs e idempotencia forte.

### `qa-tdd`

Ja definiu a ordem dos testes e o criterio de prontidao.

### `golang-developer`

Implementar nesta ordem:

1. bootstrap e indices Mongo novos
2. `organizations` e `tenants`
3. `catalog`, `packages` e `plans`
4. checkout com `Idempotency-Key`, snapshots e lineage
5. webhook, payments e subscriptions com transacao curta

Condicoes de implementacao:

- seguir TDD por fatia
- manter compatibilidade onde for viavel
- nao usar Redis como truth
- nao fazer chamadas externas dentro de transacao Mongo

## Definition Of Done

- arquitetura MongoDB-only documentada e consistente
- bootstrap e indices tenant-scoped implementados
- tenancy basica funcional
- estrutura comercial versionada funcional
- checkout idempotente com lineage e snapshot
- webhook e assinatura idempotentes e consistentes
- smoke checks de Cloud Run definidos e executaveis
