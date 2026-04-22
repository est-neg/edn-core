# Lead Submission Implementation Plan

## Problem Framing

O backend precisa aceitar leads enviados pelo frontend via webhook HTTP, validar o payload em uma trust boundary pública, persistir o registro com durabilidade, bloquear duplicatas de curto prazo e preparar o caminho para notificação sem acoplar regras de negócio ao transporte HTTP.

Esta entrega toca contrato HTTP, configuração, persistência MongoDB, observabilidade e side effects. Pela regra do repositório, isso exige uma nota de arquitetura antes da implementação.

## Architecture Decisions

### 1. Public route stays exactly at `POST /api/leads`

- O frontend já está integrado com esse path.
- A base atual usa middleware global no router raiz em `internal/platform/httpserver`, então a rota pode ficar fora de `/api/v1` sem perder observabilidade, `RequestID`, `RealIP` e `Recoverer`.
- Não criar alias em `/api/v1/leads` no primeiro slice. Duas rotas para o mesmo contrato só aumentam superfície e duplicam telemetria.

### 2. Backend owns authentication settings

- O backend deve carregar o nome do header e o token esperado via configuração `VIL_`.
- O padrão continua sendo `Authorization`, mas o backend não pode depender apenas do que o frontend diz usar.
- A comparação do token deve ser feita em constant time.
- O valor bruto do token nunca pode ir para logs, erros ou arquivos versionados.

### 3. MongoDB is the persistence target for the first slice

- O usuário explicitou MongoDB como datastore para esta feature.
- Para este fluxo inicial, o lead é um documento simples, sem relacionamentos fortes com outras entidades do core.
- O banco autoritativo deste módulo será MongoDB, com revisão futura se o ciclo de vida do lead evoluir para workflow relacional mais rígido.

### 4. Deduplication uses server receive time, not client time

- `submittedAt` deve ser persistido como metadado do chamador.
- A janela de duplicidade de 5 minutos deve usar o tempo autoritativo do backend (`received_at`).
- O critério mínimo é `whatsapp + profile` dentro da janela configurável.

### 5. Notification is asynchronous from the request contract

- O request só deve depender de autenticação, validação, deduplicação e persistência.
- Notificação deve ser tratada como side effect desacoplado. O lead não pode se perder se o downstream de notificação falhar.
- No primeiro slice, o módulo deve persistir o lead com um estado interno de notificação pendente e expor uma porta para um adaptador de notificação.
- Quando não houver canal configurado, o estado de notificação deve ser explícito como `disabled`, não `pending`.
- O primeiro adaptador concreto recomendado é webhook interno configurável, por ser o canal menos acoplado ao core atual.

## Proposed Module Boundaries

### `internal/platform/config`

Responsável por carregar configuração nova, sem lógica de domínio.

Adicionar:

- `MongoConfig`
- `LeadsConfig`

Campos propostos:

- `mongodb.uri`
- `mongodb.database`
- `mongodb.connect_timeout_sec`
- `mongodb.collection_leads`
- `leads.auth_header`
- `leads.auth_token`
- `leads.max_body_bytes`
- `leads.dedup_window_sec`
- `leads.notification_webhook_url`
- `leads.notification_timeout_sec`

### `internal/platform/mongodb`

Responsável só pela infraestrutura compartilhada de MongoDB:

- abrir conexão
- aplicar timeout de conexão
- fazer `Ping`
- expor `*mongo.Client` para módulos consumidores
- criar índices idempotentes no startup

Não colocar regra de negócio aqui.

### `internal/leads`

Novo módulo de negócio para captura de leads.

Responsabilidades:

- DTO HTTP de entrada e saída
- validação defensiva e normalização
- caso de uso `SubmitLead`
- interface de repositório
- interface de notificação
- mapeamento de erros de domínio para HTTP

Arquivos sugeridos:

- `internal/leads/types.go`
- `internal/leads/handler.go`
- `internal/leads/service.go`
- `internal/leads/repository.go`
- `internal/leads/mongodb_repository.go`
- `internal/leads/notifier.go`

## HTTP Contract

### Request handling rules

- Path: `POST /api/leads`
- Content-Type obrigatório: `application/json`
- Limite de body: `16 KiB`
- JSON estrito com rejeição de campos desconhecidos
- `source` deve ser exatamente `lumina-ia-site`
- `submittedAt` deve ser RFC3339 válido
- `lead.name` e `lead.businessName`: trim, mínimo 2 caracteres
- `lead.whatsapp`: trim, somente dígitos, mínimo 8 caracteres
- `lead.email`: opcional, mas válido quando presente
- `lead.profile`: enum `administrative | medical | dental`
- `lead.message`: opcional, máximo 500 caracteres
- `lead.consent`: obrigatório e precisa ser `true`

### Response mapping

- `200 {}`: lead aceito e persistido
- `400 {"error":"invalid_payload","details":"..."}`
- `401 {"error":"unauthorized"}`
- `409 {"error":"duplicate_lead"}`
- `500 {"error":"internal_error"}`

### Logging and observability

Logar apenas:

- `request_id`
- `path`
- `method`
- `status`
- `lead_id` quando houver persistência
- `source`
- `profile`
- motivo de rejeição categorizado

Não logar:

- token bruto
- header bruto de autenticação
- payload completo
- `message`
- `email`
- `whatsapp`

## MongoDB Design

### Database names

- desenvolvimento: `edn-core-db-dev`
- produção: `edn-core-db-prd`

Esses nomes devem entrar por configuração, nunca hardcoded fora de defaults locais de documentação.

### Creation semantics

MongoDB Atlas cria banco e coleção na primeira escrita real. Portanto:

- startup deve garantir conectividade e índices
- o banco materializa na primeira inserção em `leads`
- o usuário do cluster precisa de `readWrite` no banco configurado

### Collection

- coleção principal: `leads`

Documento sugerido:

```text
_id                  ObjectID
id                   string (UUID)
dedup_key            string
source               string
submitted_at         datetime
received_at          datetime
name                 string
business_name        string
whatsapp             string
email                string?
profile              string
message              string?
consent              bool
status               string
notification_status  string
notification_error   string?
```

Valores padrão:

- `status = "new"`
- `notification_status = "pending"` quando houver webhook configurado
- `notification_status = "disabled"` quando não houver canal configurado

### Indexes

Criar no startup, de forma idempotente:

1. `id` único
2. `dedup_key` único para fechar a corrida mais perigosa entre requisições concorrentes
3. `(whatsapp, profile, received_at desc)` para deduplicação por janela de leitura
4. `(status, received_at desc)` para listagem operacional futura
5. `(notification_status, received_at asc)` para processamento assíncrono futuro

### TTL and retention

- Não usar TTL na coleção `leads`.
- Lead é dado de negócio e precisa permanecer auditável.
- Qualquer limpeza futura deve ser entrega separada e operacionalmente explícita.

## Application Workflow

1. Request entra em `POST /api/leads`.
2. Handler valida autenticação pelo header configurado.
3. Handler limita body, decodifica JSON estrito e valida o envelope.
4. Caso de uso normaliza os campos e monta a entidade de domínio.
5. Repositório consulta duplicidade por `whatsapp + profile + received_at >= now - dedup_window`.
6. Se duplicado, retornar `409` sem persistir.
7. Se não duplicado, persistir documento com UUID, `status=new` e `notification_status=pending`.
8. Retornar `200 {}`.
9. Registrar tentativa de notificação fora do contrato síncrono do request.

## Notification Strategy

### First implementation slice

- Definir uma interface `Notifier` no módulo `internal/leads`.
- Implementar um adaptador concreto de webhook interno com timeout curto e URL configurável.
- Se a URL de notificação não estiver configurada, o sistema deve manter `notification_status=pending` e emitir log estruturado de backlog.
- O request de captura não deve falhar por indisponibilidade transitória do canal de notificação depois que o lead já foi persistido.

### Why this tradeoff

- Evita perda de lead por falha em e-mail, Slack ou WhatsApp.
- Mantém a API simples para o frontend.
- Prepara um worker futuro sem refazer o modelo.

## Security Constraints

Obrigatórios no plano do implementador:

- validação server-side completa, independentemente do Zod do frontend
- comparação de token em constant time
- segredo apenas via ambiente ou secret manager
- rejeição de payload com campos desconhecidos
- rejeição de `consent != true`
- uso de `received_at` como tempo autoritativo
- rate limit como follow-up de hardening antes de produção; se houver tempo no slice, aplicar middleware de limite por IP confiável no edge ou em memória como mitigação temporária
- readiness deve evoluir para refletir estado do MongoDB quando a dependência passar a ser obrigatória

## Test Plan For The Golang Developer

### Order

1. teste de rota `POST /api/leads`
2. testes de configuração nova
3. testes do handler com fake do caso de uso
4. testes do caso de uso com fake de repositório e clock controlável
5. testes de integração do adaptador MongoDB

### Mandatory scenarios

- `401` sem token
- `401` com token inválido
- `400` para JSON inválido
- `400` para `source` inválido
- `400` para `submittedAt` inválido
- `400` para violações de campos do objeto `lead`
- `200` com payload válido e opcionais omitidos
- `409` para duplicata na janela configurada
- `500` para falha de persistência sem vazar detalhes internos

## Delivery Sequence

### Slice 1

- ampliar config
- criar bootstrap MongoDB
- criar módulo `internal/leads`
- expor `POST /api/leads`
- persistir em MongoDB
- deduplicar por 5 minutos
- testes unitários e de integração principais

### Slice 2

- adaptador de notificação por webhook interno
- atualização de `notification_status`
- observabilidade adicional para backlog de notificações

### Slice 3

- rate limit distribuído
- worker de reprocessamento de notificações pendentes
- endpoint/listagem operacional de leads se o produto exigir CRM mínimo

## Handoff To `golang-developer`

Implementar o endpoint seguindo esta ordem:

1. expandir `internal/platform/config` e `configs/config.example.yaml`
2. adicionar bootstrap MongoDB em `internal/platform`
3. registrar a rota `POST /api/leads` no router raiz com o middleware existente
4. implementar o módulo `internal/leads` com handler, serviço, repositório e erros tipados
5. criar índices idempotentes no startup
6. cobrir comportamento com TDD, incluindo deduplicação e persistência real no adaptador

## Operational Notes

- Dev deve usar `edn-core-db-dev`.
- Produção deve usar `edn-core-db-prd`.
- Como MongoDB cria o banco na primeira escrita, a equipe de deploy precisa validar permissões do usuário Atlas antes do primeiro envio real de lead.
- Não versionar a connection string nem qualquer token real de autenticação.