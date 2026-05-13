# Meta WhatsApp Webhook Phase 1 Implementation Plan

## Problem Framing

Hoje o repo nao tem um slice dedicado para receber webhook do Meta WhatsApp com trust boundaries explicitos.
Se esse fluxo entrar direto em um runtime publico com Mongo e regras de negocio, o primeiro release ja mistura verificacao publica, intake autenticado, persistencia duravel e hardening de seguranca no mesmo boundary.

Este slice precisa fazer so o necessario:

- completar a verificacao publica do webhook do Meta via `GET`
- receber notificacoes via `POST` e encaminhar somente o payload bruto para intake interno autenticado
- processar apenas `entry[].changes[].value.messages`
- persistir recibos duraveis em MongoDB
- deduplicar mensagens repetidas por ate 7 dias

Fica fora da fase 1:

- envio outbound para WhatsApp
- templates, automacao, chatbot ou handoff humano
- processamento de `statuses`, `contacts` ou outros campos fora de `messages`
- publicacao em outbox, Pub/Sub ou side effects de negocio

## Primary Target

O alvo principal e introduzir um adapter publico fino e uma superficie interna autenticada, sem expor MongoDB ou regras de intake no edge publico.

Direcao obrigatoria:

- criar `cmd/meta-whatsapp-webhook/main.go` para `GET /v1/webhooks/meta/whatsapp` e `POST /v1/webhooks/meta/whatsapp`
- criar `cmd/meta-whatsapp-api/main.go` para `POST /internal/whatsapp/meta/messages/intake`
- centralizar parse, dedupe e persistencia em `internal/whatsapp`
- adicionar bootstrap Mongo especifico em `internal/platform/mongodb/whatsapp.go`
- adicionar configuracao explicita em `internal/platform/config/config.go` para tokens, segredo, limites de body e collections
- manter `cmd/app/main.go` e `cmd/payments-api/main.go` fora do primeiro slice para nao reabrir boundaries existentes

## Non-Negotiable Invariants

- o runtime publico e stateless: sem MongoDB, sem Redis e sem chamadas outbound ao Graph API
- `GET /v1/webhooks/meta/whatsapp` existe apenas para o challenge do Meta e nao escreve em datastore
- `POST /v1/webhooks/meta/whatsapp` valida `Content-Type`, body maximo, assinatura `X-Hub-Signature-256` e encaminha o raw body byte-for-byte
- a rota interna `POST /internal/whatsapp/meta/messages/intake` e autenticada por service-to-service auth; token interno opcional e apenas defense in depth
- a fase 1 interpreta apenas `entry[].changes[].value.messages`; eventos sem `messages` sao aceitos e ignorados
- a verdade duravel de receipt e dedupe fica em MongoDB; memoria de processo nao pode decidir replay safety
- a chave canonica de dedupe por mensagem e `phone_number_id + ":" + message.id`
- a janela de dedupe e de 7 dias; receipts duraveis nao dependem de TTL para continuarem auditaveis
- nenhum payload bruto, app secret, verify token, auth header ou identificador sensivel completo pode aparecer em logs
- a fase 1 nao envia resposta automatica, nao cria outbox e nao aciona workflow downstream

## Phase 1 Data Shape

Collections novas:

- `whatsapp_message_receipts`: recibo duravel por mensagem aceita, com payload bruto e campos minimos indexaveis
- `whatsapp_message_dedupe`: reserva autoritativa da chave de dedupe por 7 dias

Campos minimos em `whatsapp_message_receipts`:

- `receipt_id`
- `provider` com valor fixo `meta_whatsapp`
- `message_id`
- `phone_number_id`
- `from`
- `message_type`
- `received_at`
- `payload_hash`
- `dedupe_key`
- `ingress_request_id`

Nota: `raw_payload` e `raw_message` foram removidos da persistencia duravel do phase 1.
Raw content nao pode ser armazenado sem uma politica de retencao explicita.

Campos minimos em `whatsapp_message_dedupe`:

- `dedupe_key`
- `message_id`
- `phone_number_id`
- `expires_at`
- `created_at`

Indexes obrigatorios:

- `whatsapp_message_receipts.receipt_id` unico
- `whatsapp_message_receipts.message_id` nao unico
- `whatsapp_message_receipts.phone_number_id + received_at`
- `whatsapp_message_receipts.received_at`
- `whatsapp_message_dedupe.dedupe_key` unico
- `whatsapp_message_dedupe.expires_at` TTL com `expireAfterSeconds=0`

Regra de rollout de dados:

- o TTL de 7 dias vale apenas para `whatsapp_message_dedupe`
- `whatsapp_message_receipts` permanece sem cleanup destrutivo na fase 1

## Implementation Order

- Story 0: travar contrato, nomes de routes, config e dados
- Story 1: subir o runtime publico com `GET` de verificacao e `POST` de relay seguro
- Story 2: subir o runtime interno autenticado e o parser de `messages`
- Story 3: persistir receipt duravel e dedupe Mongo de 7 dias
- Story 4: wiring final, smoke checks, docs operacionais minimos e readiness

## Story 0: Contract Lock

Objetivo:

- congelar o primeiro contrato antes de abrir codigo em `cmd/`, `internal/` e `docs/`

Implementacao:

- criar `WhatsAppConfig` em `internal/platform/config/config.go`
- adicionar `CollectionWhatsAppMessageReceipts` e `CollectionWhatsAppMessageDedupe` em `MongoConfig`
- definir env vars minimas: verify token, app secret, internal token, target URL interna e body max bytes
- travar os paths publicos e internos acima
- decidir o response contract minimo: `GET` retorna challenge; `POST` publico retorna `200` para accepted, duplicate e ignored; retorna `503` para falha temporaria de intake

Aceite:

- nomes de rotas, collections e secrets estao fechados
- nao existe dependencia de OpenAPI publica para a fase 1
- `api/openapi.yaml` fica inalterado neste slice

## Story 1: Public Meta Edge

Objetivo:

- receber o webhook do Meta sem expor Mongo nem logica de persistencia ao publico

Implementacao:

- criar `cmd/meta-whatsapp-webhook/main.go`
- criar handler publico em `internal/whatsapp/public_handler.go`
- implementar `GET /v1/webhooks/meta/whatsapp` com validacao de `hub.mode`, `hub.verify_token` e retorno literal de `hub.challenge`
- implementar `POST /v1/webhooks/meta/whatsapp` com `POST` only, `application/json`, body limitado e validacao de `X-Hub-Signature-256`
- encaminhar o raw body para `POST /internal/whatsapp/meta/messages/intake`
- gerar apenas headers internos allow-listed para correlacao e auth

Aceite:

- o runtime publico compila sem dependencia de MongoDB ou Redis
- `GET` valido responde o challenge correto
- `POST` invalido por assinatura, media type ou tamanho falha no edge
- o edge nao faz `json.Unmarshal` dos campos de negocio de WhatsApp

## Story 2: Internal Authenticated Intake

Objetivo:

- processar o payload do Meta apenas no boundary autenticado

Implementacao:

- criar `cmd/meta-whatsapp-api/main.go`
- criar `internal/whatsapp/internal_handler.go`
- exigir Cloud Run IAM service-to-service auth como controle primario
- validar `X-EDN-Internal-Token` apenas como controle secundario quando configurado
- ler o raw body uma vez e delegar para `internal/whatsapp/service.go`
- aceitar payloads sem `messages` como `ignored` para nao transformar `statuses` em erro de provider
- extrair de forma minima `entry`, `changes`, `value.metadata.phone_number_id` e `value.messages`

Aceite:

- a rota interna nao fica montada em superficie publica
- payload valido com `messages` chega ao service
- payload valido sem `messages` retorna sucesso sem escrita
- nenhuma chamada outbound ao Graph API aparece nesta story

## Story 3: Durable Receipt Persistence And 7-Day Dedupe

Objetivo:

- gravar receipt duravel por mensagem e impedir replay util dentro da janela de 7 dias

Implementacao:

- criar `internal/whatsapp/types.go`, `errors.go`, `repository.go`, `mongodb_repository.go` e `service.go`
- para cada item de `value.messages`, montar a chave `phone_number_id + ":" + message.id`
- validar todos os pares `(phone_number_id, message.id)` antes de qualquer escrita; retornar `ErrInvalidPayload` se qualquer item for invalido
- reservar a chave em `whatsapp_message_dedupe` com `expires_at = now + 7 dias`
- em caso de duplicate key, tratar como `duplicate` e nao gravar segundo receipt
- em caso de primeira entrega, inserir um documento em `whatsapp_message_receipts`
- manter a operacao curta e atomica por mensagem; se for necessario usar sessao Mongo, isolar isso no repository

Aceite:

- a mesma mensagem recebida duas vezes em ate 7 dias gera um unico receipt duravel
- mensagens diferentes no mesmo payload geram receipts independentes
- falha temporaria no Mongo retorna erro retryable para o edge publico
- receipts sobrevivem a restart e nao dependem de cache para dedupe

## Story 4: Wiring, Bootstrap And Readiness

Objetivo:

- fechar o slice para deploy repetivel e QA executavel

Implementacao:

- adicionar bootstrap em `internal/platform/mongodb/whatsapp.go`
- chamar o bootstrap no entrypoint interno
- revisar `configs/config.example.yaml` e `configs/local-development.example` para novas keys
- adicionar smoke notes em `docs/` para verificacao manual de `GET` e `POST`
- manter o contracto de docs neste arquivo; so abrir runbook separado se a operacao exigir

Aceite:

- as collections e indexes sobem de forma idempotente
- secrets e URLs internas estao configurados sem hardcode
- existem smoke checks objetivos para dev e homologacao

## Proposed Test File Structure

- `internal/whatsapp/public_handler_test.go`
- `internal/whatsapp/internal_handler_test.go`
- `internal/whatsapp/service_test.go`
- `internal/whatsapp/mongodb_repository_test.go`
- `internal/platform/mongodb/whatsapp_test.go`

Escopo por arquivo:

- `public_handler_test.go`: `GET` challenge, token invalido, `POST` sem assinatura, assinatura invalida, content-type, body limit e forward do raw body
- `internal_handler_test.go`: auth interna, leitura unica do body, ignored sem `messages`, status mapping
- `service_test.go`: parse de `messages`, iteracao por multiplas mensagens, chave canonica de dedupe, duplicate em 7 dias, erro retryable de repositorio
- `mongodb_repository_test.go`: insert de receipt, duplicate key em dedupe, leitura por janela
- `whatsapp_test.go`: bootstrap idempotente e presenca de indexes obrigatorios

## TDD Execution Order

1. Escrever os testes de `GET /v1/webhooks/meta/whatsapp` para challenge valido e token invalido.
2. Escrever os testes do `POST` publico para assinatura, media type, body maximo e raw forward.
3. Escrever os testes da rota interna para auth, ignored sem `messages` e erro retryable.
4. Escrever os testes do service para extracao de mensagens, dedupe key e split por mensagem.
5. Escrever os testes do repository Mongo para duplicate key e persistencia duravel.
6. Implementar o bootstrap Mongo e fechar os testes de indexes.
7. Rodar primeiro os testes de `internal/whatsapp`, depois os de bootstrap Mongo, e so entao os smokes de wiring em `cmd/`.

## Acceptance Matrix

| Scenario | Public Route | Internal Route | Mongo Outcome |
| --- | --- | --- | --- |
| GET com `hub.mode=subscribe`, token valido e challenge | `200` com challenge literal | n/a | sem escrita |
| GET com token invalido | `403` | n/a | sem escrita |
| POST com assinatura ausente ou malformada | `400` | nao chama | sem escrita |
| POST com HMAC invalido | `403` | nao chama | sem escrita |
| POST com content-type invalido | `415` | nao chama | sem escrita |
| POST valido com 1 mensagem nova | `200 {"status":"ok"}` | `200 {"status":"ok"}` | 1 dedupe key + 1 receipt |
| POST valido com mensagem repetida em ate 7 dias | `200 {"status":"ok"}` | `200 {"status":"ok"}` | sem novo receipt |
| POST valido sem `messages` e so com outros campos | `200 {"status":"ok"}` | `200 {"status":"ignored"}` | sem escrita |
| POST valido com payload invalido (phone_number_id ou message.id vazio) | `400 {"error":"bad_request"}` | `400 {"error":"bad_request"}` | sem escrita |
| POST valido com falha temporaria de Mongo | `503 {"error":"service_unavailable"}` | `503 {"error":"service_unavailable"}` | escrita incompleta, sem falso sucesso |
| POST com erro interno nao esperado (401, 403, 404, 5xx) | `503 {"error":"service_unavailable"}` | varia | sem falso sucesso |

## Release Readiness Checklist

- [ ] `cmd/meta-whatsapp-webhook` sobe sem MongoDB, Redis ou token outbound
- [ ] `cmd/meta-whatsapp-api` sobe apenas em superficie autenticada
- [ ] `GET /v1/webhooks/meta/whatsapp` passa no handshake real do Meta
- [ ] `POST /v1/webhooks/meta/whatsapp` valida assinatura antes de qualquer forward
- [ ] payload com duas mensagens gera dois receipts independentes
- [ ] retry da mesma `message.id` em ate 7 dias nao duplica receipt
- [ ] payload sem `messages` retorna `200` e nao escreve nada
- [ ] indexes de `whatsapp_message_receipts` e `whatsapp_message_dedupe` existem no Mongo
- [ ] logs nao expõem verify token, app secret, auth header ou raw payload
- [ ] nenhum teste da fase 1 depende de outbound automation

## Required Handoff After Phase 1

- `golang-developer`: implementar na ordem das stories, sem pular TDD de handler e service
- `qa-tdd`: executar a matriz acima com foco em duplicate, ignored e falha temporaria
- `security-reviewer`: validar assinatura do Meta, auth interna e redacao de logs antes de go-live
