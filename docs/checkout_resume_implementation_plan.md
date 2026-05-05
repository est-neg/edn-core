# Checkout Resume Implementation Plan

## Problem Framing

O fluxo atual do payments API cria checkout de forma sincronica contra InfinitePay em `POST /v1/checkout/sessions`.
O worker atual so entra depois do pagamento ou da reconciliacao posterior, entao ele nao resolve abandono, troca de dispositivo ou retomada pre-pagamento.

Hoje o comportamento cobre apenas retry tecnico por `Idempotency-Key` e `request_hash`.
Isso evita duplicacao da mesma chamada, mas nao entrega resume de negocio cross-device porque depende da mesma tentativa tecnica e nao de um handle duravel de checkout pendente.

## Primary Target

O alvo principal e `create-or-resume` no proprio `POST /v1/checkout/sessions`.

Direcao obrigatoria:

- `Idempotency-Key` continua apenas para retry tecnico da mesma tentativa HTTP
- o backend passa a emitir e persistir `checkout_intent_key`, ou equivalente opaco, como handle canonico de resume
- resume nunca e autorizado apenas por PII ou por tupla de negocio como tenant, plano, e-mail, telefone ou valor
- `expires_at` passa a ser autoritativo do backend, persistido e validado pelo relogio do backend
- nao existe sucesso publico se a ordem foi criada sem `provider_checkout_url`; o backend deve reparar, recriar com seguranca ou retornar erro explicito de recuperacao

## Non-Negotiable Invariants

- o backend e dono do lifecycle de `checkout_intent_key`, `expires_at`, `status` e `provider_checkout_url`
- `Idempotency-Key` nao substitui resume de negocio e nao vira identificador duravel de checkout
- o payload canonico de sucesso deve ser o mesmo para create e resume, com diferenca apenas no status HTTP
- `expires_at` e sempre calculado e persistido server-side; o cliente nunca define a verdade de expiracao
- resume exige handle opaco emitido pelo backend e verificacao de elegibilidade server-side
- PII e tupla de negocio podem ajudar diagnostico interno, mas nao autorizam resume sozinhas
- ordem criada sem `provider_checkout_url` nao pode virar sucesso publico; o resultado deve ser reparo, recreate seguro ou erro estavel de recuperacao
- o worker continua pos-pagamento; resume pre-pagamento pertence ao caminho sincronico da API e a persistencia duravel

## Minimum API Semantics

- `201 Created`: checkout novo criado, persistido e com `provider_checkout_url` pronto para uso
- `200 OK`: checkout existente, ativo e resumivel retornado com o mesmo payload canonico de sucesso
- `409 Conflict`: checkout encontrado, mas nao resumivel, com `error_code` estavel como `checkout_non_resumable` ou `checkout_expired`
- `503 Service Unavailable`: `error_code` estavel para `checkout_recovery_required` ou `provider_state_ambiguous`; o cliente nao recebe falso sucesso
- `429 Too Many Requests`: create ou resume bloqueado por abuso, replay ou rate limit

Payload canonico minimo de sucesso:

- `checkout_intent_key`
- `provider_checkout_url`
- `expires_at`
- `status`
- identificador estavel do pedido ou sessao backend quando ja existir

## Delivery Strategy

O rollout deve ser aditivo, observavel e reversivel por fases pequenas.
Persistencia, contrato, semantica HTTP e tratamento de falha precisam avancar juntos para evitar falso resume ou falso sucesso.

## Phase 0: Contract Lock

Objetivo:

- travar a semantica canonica de `POST /v1/checkout/sessions`
- definir o nome final de `checkout_intent_key` e os `error_code` estaveis
- alinhar o payload de sucesso unico para create e resume

Entregas:

- nota de contrato aprovada para `201`, `200`, `409`, `503` e `429`
- matriz de estados resumiveis e nao resumiveis
- decisao explicita de que `Idempotency-Key` fica restrito a retry tecnico

## Phase 1: Observability Baseline

Objetivo:

- medir o baseline antes de mudar a semantica publica

Implementacao:

- metricas para `checkout_created`, `checkout_resumed`, `checkout_idempotent_retry`, `checkout_non_resumable`, `checkout_recovery_required` e `provider_state_ambiguous`
- tracing da chamada sincronica ao adapter InfinitePay
- logs estruturados com `request_id`, `tenant_id`, `checkout_intent_key` mascarado, status final e motivo categorizado

Validacao:

- dashboard basico para taxa de create versus resume
- alerta para ordens sem `provider_checkout_url`

## Phase 2: Persistence And Index Changes

Objetivo:

- introduzir o estado duravel necessario para resume canonico

Implementacao:

- persistir `checkout_intent_key`, `expires_at`, `status`, `provider_checkout_url`, `provider_reference`, `last_provider_sync_at` e flags de recuperacao no datastore duravel ja dono do checkout
- manter `request_hash` e `Idempotency-Key` apenas para retry tecnico e conflito de payload
- criar indice unico para `checkout_intent_key`
- criar indice de apoio para `status + expires_at`
- criar indice operacional para localizar intents em recuperacao sem cleanup destrutivo no primeiro release

Regras:

- `expires_at` precisa ser backend-authoritative desde a primeira persistencia
- nao depender de PII ou de tupla de negocio como chave canonica de localizacao

## Phase 3: Additive Response Rollout

Objetivo:

- expor os campos de resume sem quebrar clientes existentes

Implementacao:

- adicionar `checkout_intent_key` e `expires_at` ao payload de sucesso atual
- manter os campos antigos enquanto clientes migram
- introduzir `error_code` estavel nas respostas `409` e `503`

Validacao:

- clientes atuais ignoram os campos novos sem regressao
- telemetria confirma leitura dos campos novos por clientes piloto

## Phase 4: Create-Or-Resume Behavior

Objetivo:

- tornar `POST /v1/checkout/sessions` a superficie canonica de create e resume

Implementacao:

- sem handle opaco valido, a API cria um checkout novo e chama InfinitePay de forma sincronica
- com `checkout_intent_key` valido, ativo, nao expirado e com `provider_checkout_url` presente, a API retorna resume com `200`
- com `checkout_intent_key` valido, mas em estado nao resumivel, a API retorna `409` com `error_code` estavel
- retry tecnico com mesma `Idempotency-Key` e mesmo `request_hash` continua devolvendo o mesmo resultado tecnico da tentativa original

Guardrails:

- resume cross-device depende do handle opaco emitido pelo backend, nao de inferencia por dados do comprador
- o contrato deve continuar backward-compatible para clientes que ainda so sabem criar

## Phase 5: Recovery Semantics

Objetivo:

- impedir falso sucesso quando o estado backend ou provider estiver incompleto ou ambiguo

Implementacao:

- se a ordem existir sem `provider_checkout_url`, tentar repair ou recreate seguro antes de responder sucesso
- se nao houver certeza de qual URL ou sessao do provider esta valida, responder `503` com `error_code=provider_state_ambiguous`
- quando o backend souber que o checkout precisa de reparo antes de novo uso, responder `503` com `error_code=checkout_recovery_required`
- persistir o resultado da tentativa de recuperacao para evitar loops cegos de retry

Regra publica:

- e proibido responder sucesso enquanto `provider_checkout_url` nao estiver confirmado e persistido

## Phase 6: Security And Rate Limit Hardening

Objetivo:

- fechar abuso, enumeracao e replay antes de readiness geral

Implementacao:

- `checkout_intent_key` com alta entropia e formato opaco
- nenhum log com PII sensivel, handle bruto completo ou URL sensivel do provider
- rate limit por IP, tenant, fingerprint ou combinacao equivalente no edge e no backend quando aplicavel
- controles para replay abusivo de resume e tentativas repetidas de handles invalidos
- auditoria de expiracao, invalidacao e transicao de estado

## Phase 7: Docs, OpenAPI, And Client Rollout

Objetivo:

- alinhar contrato documentado, clientes e operacao

Implementacao:

- atualizar `api/openapi.yaml` com os novos campos, status codes e `error_code`
- documentar o lifecycle de `checkout_intent_key` e `expires_at`
- orientar clientes a persistirem o handle opaco como referencia de resume
- publicar runbook para `checkout_recovery_required` e `provider_state_ambiguous`

Plan B:

- se a mudanca de semantica de `POST /v1/checkout/sessions` nao for aceita de imediato, criar endpoint dedicado de resume com o mesmo payload canonico de sucesso
- esse endpoint alternativo nao muda os invariantes: `Idempotency-Key` continua tecnico, `checkout_intent_key` continua opaco e `provider_checkout_url` continua obrigatorio para sucesso publico

## Rollout Strategy

- liberar primeiro os campos aditivos e a telemetria
- habilitar create-or-resume por feature flag, tenant allow-list ou rollout percentual
- promover para default apenas depois de queda comprovada de abandonos e ausencia de falso sucesso
- rollback deve desligar a semantica nova sem remover persistencia, indices ou `error_code` ja publicados

## Security Requirements From Review

- nao autorizar resume com base apenas em e-mail, telefone, CPF, nome ou combinacao de atributos de negocio
- tratar `checkout_intent_key` como segredo de capacidade: alto entropia, opaco, sem derivacao reversivel de PII
- `expires_at`, status e elegibilidade de resume sao sempre server-side authoritative
- bloquear enumeracao e abuso com rate limit, observabilidade e respostas estaveis
- nunca responder sucesso publico para ordem sem `provider_checkout_url` confirmado e persistido
- mascarar ou omitir PII, handle bruto completo, tokens e payloads sensiveis em logs, erros e eventos
- manter segredos e credenciais do provider fora do codigo e fora de logs

## QA Requirements From Review

- comecar por TDD das semanticas `201`, `200`, `409`, `503` e `429`
- validar que retry tecnico por `Idempotency-Key` continua funcionando sem virar resume de negocio
- testar resume cross-device com `checkout_intent_key` valido e sem depender de PII ou tupla de negocio
- testar expiracao pelo relogio do backend, inclusive fronteira de `expires_at`
- testar concorrencia entre create, retry tecnico, resume e recovery
- testar explicitamente o caso de ordem criada sem `provider_checkout_url` para garantir ausencia de falso sucesso
- validar `error_code` estavel, observabilidade nova e rate limit sob abuso
- nao aprovar por inspecao quando houver comportamento executavel a validar

## Requested Agent Handoff

### `golang-developer`

Implementar em fases pequenas nesta ordem:

1. travar contrato, `error_code` e estados resumiveis
2. adicionar observabilidade de baseline e alertas
3. persistir `checkout_intent_key`, `expires_at` e indices necessarios
4. liberar payload aditivo de sucesso e erros estaveis
5. ativar `create-or-resume` em `POST /v1/checkout/sessions`
6. implementar recovery semantics e bloquear falso sucesso sem `provider_checkout_url`
7. finalizar hardening, OpenAPI e rollout controlado de clientes

### `security-reviewer`

Validar trust boundaries, entropia do handle opaco, regras de resume, redacao de logs e rate limits antes de readiness.

### `qa-tdd`

Executar a matriz de regressao e cenarios de falha apos cada fase relevante e validar o plano completo antes de readiness.
