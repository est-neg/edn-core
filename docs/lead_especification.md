# API Spec — Lead Submission Endpoint

**Versão:** 1.0  
**Produto:** Funcionario.online  
**Responsável:** Backend  
**Origem:** `lib/lead/submit.ts` + `lib/lead/schema.ts` + `app/actions/lead.ts`

---

## Visão Geral

O frontend envia leads captados pelo formulário de contato via webhook HTTP. O backend deve expor um endpoint que receba, valide, persista e notifique de novos leads.

O frontend já está instrumentado com um adaptador (`WebhookLeadAdapter`) que envia `POST` com `Content-Type: application/json` assim que o servidor Next.js valida o formulário com Zod. O backend não precisa validar novamente, mas **deve** validar como boa prática de defesa.

---

## Endpoint

```http
POST /api/leads
```

### Autenticação

Bearer token via header customizável. O frontend envia:

```http
Authorization: <LEAD_WEBHOOK_AUTH_TOKEN>
```

O nome do header é configurável pela variável de ambiente `LEAD_WEBHOOK_AUTH_HEADER` no frontend (padrão sugerido: `Authorization`). O backend deve rejeitar requisições sem token válido com `401`.

> As variáveis de ambiente do lado frontend são:
>
> ```env
> LEAD_WEBHOOK_URL=https://<backend>/api/leads
> LEAD_WEBHOOK_AUTH_HEADER=Authorization
> LEAD_WEBHOOK_AUTH_TOKEN=Bearer <token-secreto>
> ```

---

## Request

### Headers

| Header        | Valor obrigatório                         |
| ------------- | ----------------------------------------- |
| Content-Type  | `application/json`                        |
| Authorization | `Bearer <token>` (ou header configurado)  |

### Body

```jsonc
{
  "source": "lumina-ia-site",          // string, fixo — identifica a origem
  "submittedAt": "2026-04-11T14:30:00.000Z", // ISO 8601 UTC — timestamp gerado pelo frontend
  "lead": {
    "name": "João Silva",              // string, min 2 chars, obrigatório
    "businessName": "Clínica Saúde+", // string, min 2 chars, obrigatório
    "whatsapp": "11999990000",         // string, min 8 chars, obrigatório — somente dígitos após trim
    "email": "joao@clinica.com",       // string | undefined — e-mail válido ou ausente
    "profile": "medical",             // enum: "administrative" | "medical" | "dental"
    "message": "Quero saber mais...",  // string | undefined — max 500 chars ou ausente
    "consent": true                   // boolean, sempre true — usuário autorizou contato
  }
}
```

### Campos do objeto `lead`

- `name`: `string`, obrigatório, mínimo 2 caracteres após trim.
- `businessName`: `string`, obrigatório, mínimo 2 caracteres após trim.
- `whatsapp`: `string`, obrigatório, mínimo 8 caracteres após trim.
- `email`: `string`, opcional, e-mail válido; omitido se vazio.
- `profile`: `string` (enum), obrigatório, `"administrative"`, `"medical"` ou `"dental"`.
- `message`: `string`, opcional, máximo 500 caracteres; omitido se vazio.
- `consent`: `boolean`, obrigatório, sempre `true`; o frontend bloqueia envio se `false`.

### Campos do envelope

| Campo         | Tipo     | Obrigatório | Notas                                    |
| ------------- | -------- | ----------- | ---------------------------------------- |
| `source`      | `string` | Sim         | Valor fixo `"lumina-ia-site"`            |
| `submittedAt` | `string` | Sim         | ISO 8601 UTC gerado no momento do envio  |

---

## Response

### 200 OK — Lead recebido e processado

```json
{}
```

Corpo vazio ou JSON mínimo. O frontend não consome o body de sucesso.

### 400 Bad Request — Payload inválido

```json
{
  "error": "invalid_payload",
  "details": "Campo 'profile' deve ser um dos valores: administrative, medical, dental."
}
```

### 401 Unauthorized — Token ausente ou inválido

```json
{
  "error": "unauthorized"
}
```

### 409 Conflict — Lead duplicado (opcional, recomendado)

Retornar `409` se o mesmo `whatsapp` + `profile` já submeteu nos últimos 5 minutos, para evitar duplicatas por duplo clique ou retry do usuário. O frontend trata qualquer resposta não-2xx como erro silencioso — não impacta a UX.

```json
{
  "error": "duplicate_lead"
}
```

### 500 Internal Server Error — Falha interna

```json
{
  "error": "internal_error"
}
```

---

## Comportamento do Frontend

- Envia **somente após validação Zod bem-sucedida** — o backend nunca receberá campos ausentes obrigatórios ou fora do enum.
- `email` e `message` podem estar **ausentes** no objeto `lead` (não enviados como `null`, e sim omitidos).
- `consent` é sempre `true` no payload — a validação do checkbox acontece no frontend e o formulário não envia se `false`.
- Em caso de erro HTTP ou timeout, o usuário vê a mensagem: _"Ocorreu um erro ao enviar. Tente novamente ou use o WhatsApp."_ O frontend **não** faz retry automático.
- `submittedAt` é o momento em que a Server Action Next.js despachou o webhook — pode ter até ~2 s de atraso em relação ao clique do usuário.

---

## Fluxo Completo

```text
Usuário → Formulário (Next.js)
  └─► Server Action: valida com Zod
      └─► POST /api/leads  ──────────────────────────► Backend
            Authorization: Bearer <token>                │
            Content-Type: application/json               │
                                                         ├─ Autenticar token
                                                         ├─ Validar payload (defesa)
                                                         ├─ Deduplicar (opcional)
                                                         ├─ Persistir lead (DB/CRM)
                                                         ├─ Notificar equipe (e-mail/Slack/WhatsApp)
                                                         └─ Retornar 200
```

---

## Recomendações de Implementação

### Persistência mínima

Salvar ao menos:

| Campo           | Tipo      | Notas                                           |
| --------------- | --------- | ----------------------------------------------- |
| `id`            | UUID      | Chave primária                                  |
| `source`        | string    | `"lumina-ia-site"`                              |
| `submitted_at`  | timestamp | Do payload (campo `submittedAt`)                |
| `received_at`   | timestamp | Gerado pelo backend no momento do registro      |
| `name`          | string    |                                                 |
| `business_name` | string    |                                                 |
| `whatsapp`      | string    |                                                 |
| `email`         | string?   | Nullable                                        |
| `profile`       | enum      | `administrative`, `medical`, `dental`           |
| `message`       | string?   | Nullable                                        |
| `consent`       | boolean   |                                                 |
| `status`        | enum      | `new`, `contacted`, `qualified`, `lost`         |

### Notificação

Disparar ao menos uma das seguintes ao receber um nuevo lead:

- E-mail para `comercial@funcionario.online`
- Mensagem WhatsApp via API (mesmo stack usada no produto)
- Webhook interno para CRM ou Slack

### Segurança

- Token deve ser armazenado como secret (não em código-fonte).
- Endpoint deve aceitar somente HTTPS em produção.
- Rate limit recomendado: 10 req/min por IP.
- Log de tentativas com token inválido para monitoramento de abuso.

---

## Variáveis de Ambiente (Lado Frontend)

```env
LEAD_WEBHOOK_URL=https://api.funcionario.online/api/leads
LEAD_WEBHOOK_AUTH_HEADER=Authorization
LEAD_WEBHOOK_AUTH_TOKEN=Bearer eyJ...
```

Configurar em `.env.local` (desenvolvimento) e em secrets do ambiente de deploy (produção).

---

## Exemplo Completo de Requisição

```http
POST /api/leads HTTP/1.1
Host: api.funcionario.online
Content-Type: application/json
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...

{
  "source": "lumina-ia-site",
  "submittedAt": "2026-04-11T17:45:12.334Z",
  "lead": {
    "name": "Carla Mendonça",
    "businessName": "Odonto Sorrir",
    "whatsapp": "11988887777",
    "email": "carla@odontosorrir.com.br",
    "profile": "dental",
    "message": "Tenho 3 dentistas e preciso de suporte no agendamento.",
    "consent": true
  }
}
```

```http
HTTP/1.1 200 OK
Content-Type: application/json

{}
```
