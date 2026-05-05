# Frontend Integration Guide — EDN Core API

Guia para integrar o site (`developer.funcionario.online` / `funcionario.online`) com o backend EDN Core.

## Ambientes

| Ambiente | Base URL da API | Frontend |
| --- | --- | --- |
| Desenvolvimento | `https://edn-core-dev-cuwktlrora-uc.a.run.app` | `https://developer.funcionario.online` ou `localhost:*` |
| Produção | _(a definir após aprovação)_ | `https://funcionario.online` |

Configure a base URL via variável de ambiente no projeto frontend:

```bash
# .env.local (Next.js, Vite, etc.) — nunca commitar valores reais
NEXT_PUBLIC_API_URL=https://edn-core-dev-cuwktlrora-uc.a.run.app
# ou Vite:
VITE_API_URL=https://edn-core-dev-cuwktlrora-uc.a.run.app
```

---

## Fluxo completo de checkout

```text
Frontend                           EDN Core (Cloud Run)              InfinitePay
   |                                       |                               |
  |-- GET /v1/plans?organization_slug=... |
  |   &tenant_slug=...&channel=web ------>|                               |
   |<-- [ { slug, name, price_cents } ] ---|                               |
   |                                       |                               |
   |-- POST /v1/checkout/sessions -------->|                               |
  |   { organization_slug, tenant_slug,   |-- POST /v1/checkout -------->|
  |     channel, plan_slug,               |                               |
  |     billing_cycle,                    |                               |
   |     customer: { name, email,          |<-- { checkout_url, invoice } -|
   |     phone, document } }               |                               |
   |<-- 201 { order_nsu,                  |                               |
   |     checkout_intent_key,             |                               |
   |     checkout_url } ------------------|                               |
   | (persist order_nsu + intent_key)     |                               |
   |                                       |                               |
   |-- window.location = checkout_url ---->|   (usuário paga na InfinitePay)
   |                                       |                               |
   |<-- redirect para /checkout/return?order_nsu=...  (InfinitePay redireciona)
   |                                       |                               |
   |-- POST /v1/checkout/sessions/track -->|                               |
   |   { checkout_intent_key }            |                               |
   |<-- 200 { status, resumable, ... } ----|                               |
   |                                       |                               |
   |-- GET /v1/orders/{order_nsu}/status ->|                               |
   |<-- { status: "paid", ... } -----------|                               |
```

---

## Passo 1 — Listar planos

```http
GET /v1/plans?organization_slug=edn-core&tenant_slug=fun-onl&channel=web
```

Para o runtime multi-tenant, envie sempre `organization_slug` e `tenant_slug` juntos. O backend resolve os IDs reais da organização e do tenant no MongoDB e lê o catálogo publicado em `versioned_plans`.

### Catálogo dev atual

Escopo de desenvolvimento: `organization_slug=edn-core`, `tenant_slug=fun-onl`.

| Plano (`plan_slug`) | `billing_cycle` | `price_cents` |
| --- | --- | --- |
| `administrative` | `monthly` | 14 900 |
| `administrative` | `annual` | 143 040 |
| `medical` | `monthly` | 24 900 |
| `medical` | `annual` | 239 040 |
| `dental` | `monthly` | 9 900 |
| `dental` | `annual` | 95 040 |

O campo `id` retornado na listagem é o `plan_uuid` do MongoDB — use-o apenas como chave React/lista. **Não envie `id` no POST `/v1/checkout/sessions`**: o checkout recebe `plan_slug` + `billing_cycle` e o backend resolve o plano publicado.

Resposta:

```json
{
  "plans": [
    {
      "id": "<uuid-retornado-pela-api>",
      "slug": "administrative",
      "name": "Plano Administrativo Mensal",
      "billing_cycle": "monthly",
      "price_cents": 14900,
      "currency": "BRL",
      "active": true,
      "max_installments": 1
    },
    {
      "id": "<uuid-retornado-pela-api>",
      "slug": "administrative",
      "name": "Plano Administrativo Anual",
      "billing_cycle": "annual",
      "price_cents": 143040,
      "currency": "BRL",
      "active": true,
      "max_installments": 12
    }
  ]
}
```

O campo `id` é apenas para exibição (e.g. React key). **Não envie `id` no POST /checkout/sessions** — o checkout recebe `plan_slug` + `billing_cycle` e o backend resolve o plano pelo catálogo publicado.

Use `price_cents / 100` para exibir o preço. **Nunca envie o preço no request de checkout** — o backend sempre usa o valor do plano.

---

## Passo 2 — Criar sessão de checkout (create-or-resume)

```http
POST /v1/checkout/sessions
Content-Type: application/json
Idempotency-Key: checkout-2026-04-25-user-123
```

> **Browser / CORS:** o endpoint público aceita o header `Idempotency-Key` via preflight. Envie sempre essa chave em requisições originadas no browser.

### Criar novo checkout

Body (sem `checkout_intent_key`):

```json
{
  "organization_slug": "edn-core",
  "tenant_slug": "fun-onl",
  "channel": "web",
  "plan_slug": "administrative",
  "billing_cycle": "monthly",
  "customer": {
    "name": "João da Silva",
    "email": "joao@example.com",
    "phone": "+5511987654321",
    "document": "529.982.247-25"
  }
}
```

| Campo | Formato | Obrigatório |
| --- | --- | --- |
| `organization_slug` | string slug pública | sim para catálogo multi-tenant |
| `tenant_slug` | string slug pública | sim para catálogo multi-tenant |
| `channel` | `"web"` ou `"mobile"` | não, backend assume `"web"` |
| `plan_slug` | string, apenas letras minúsculas, números e `-_` | sim |
| `billing_cycle` | `"monthly"` ou `"annual"` | sim |
| `checkout_intent_key` | handle opaco de uma resposta 201 anterior | não — omitir para criar novo |
| `customer.name` | string não vazia | sim |
| `customer.email` | e-mail válido | sim |
| `customer.phone` | E.164 (`+5511...`) ou 10/11 dígitos BR | sim |
| `customer.document` | CPF — com ou sem pontuação (`000.000.000-00` ou `00000000000`) | sim |

Resposta `201` (novo checkout criado):

```json
{
  "order_nsu": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "checkout_intent_key": "Xy7K2qP9mN3vR5wL",
  "status": "checkout_created",
  "checkout_url": "https://checkout.infinitepay.io/...",
  "expires_at": "2026-04-24T15:30:00Z"
}
```

**Ação do frontend:** redirecionar o usuário para `checkout_url`. **Persistir `order_nsu` E `checkout_intent_key`** no `sessionStorage` — ambos são necessários para retomada e rastreamento.

```js
const { order_nsu, checkout_intent_key, checkout_url } = await response.json()
sessionStorage.setItem('order_nsu', order_nsu)
sessionStorage.setItem('checkout_intent_key', checkout_intent_key)
window.location.href = checkout_url
```

### Retomar checkout existente (create-or-resume)

Se o usuário atualizou a página, saiu e voltou, ou um retry de rede ocorreu após a criação:

```json
{
  "organization_slug": "edn-core",
  "tenant_slug": "fun-onl",
  "channel": "web",
  "plan_slug": "administrative",
  "billing_cycle": "monthly",
  "checkout_intent_key": "Xy7K2qP9mN3vR5wL"
}
```

> O campo `customer` é opcional no caminho de retomada — o backend usa os dados do pedido original.

Resposta `200` (checkout retomado, sem criar novo pedido):

```json
{
  "order_nsu": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "checkout_intent_key": "Xy7K2qP9mN3vR5wL",
  "status": "checkout_created",
  "checkout_url": "https://checkout.infinitepay.io/...",
  "expires_at": "2026-04-24T15:30:00Z"
}
```

**Idempotencia:** reutilize a mesma `Idempotency-Key` quando for repetir exatamente o mesmo checkout por retry de rede. Se o payload mudar, gere uma nova chave.

**Falha parcial tolerada:** se a InfinitePay já tiver criado o checkout mas o backend tiver sofrido falha parcial na persistência local, repetir o mesmo request com a mesma `Idempotency-Key` retorna a mesma sessão de checkout em vez de criar um segundo checkout.

---

## Passo 3 — Rastrear progresso (opcional)

Se o usuário retornar antes do webhook processar, use o endpoint de rastreamento público para verificar o progresso sem precisar de `order_nsu`:

```http
POST /v1/checkout/sessions/track
Content-Type: application/json
```

```json
{
  "checkout_intent_key": "Xy7K2qP9mN3vR5wL"
}
```

Resposta `200`:

```json
{
  "order_nsu": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "checkout_intent_key": "Xy7K2qP9mN3vR5wL",
  "status": "checkout_created",
  "plan_slug": "administrative",
  "expires_at": "2026-04-24T15:30:00Z",
  "resumable": true,
  "checkout_url": "https://checkout.infinitepay.io/..."
}
```

Use `resumable: true` + `checkout_url` para oferecer botão de retomada. Use `status: "paid"` + `receipt_url` para exibir tela de sucesso. Nenhum campo de PII do cliente é retornado.

---

## Passo 4 — Página de retorno `/checkout/return`

Após o pagamento, a InfinitePay redireciona o usuário para:

```text
https://developer.funcionario.online/checkout/return?order_nsu=<order_nsu>
```

Leia o `order_nsu` da query string e faça polling no status:

```js
// Exemplo em JavaScript puro
async function waitForPayment(orderNsu, maxAttempts = 20, intervalMs = 3000) {
  const url = `${API_URL}/v1/orders/${orderNsu}/status`
  for (let i = 0; i < maxAttempts; i++) {
    const res = await fetch(url)
    if (!res.ok) throw new Error(`status check failed: ${res.status}`)
    const data = await res.json()

    if (data.status === 'paid') return data          // sucesso
    if (data.status === 'failed' || data.status === 'expired') throw new Error(data.status)

    await new Promise(r => setTimeout(r, intervalMs))
  }
  throw new Error('timeout waiting for payment confirmation')
}

// Na página de retorno:
const params = new URLSearchParams(window.location.search)
const orderNsu = params.get('order_nsu') ?? sessionStorage.getItem('order_nsu')

waitForPayment(orderNsu)
  .then(data => {
    // data.subscription_status === 'active'
    // data.receipt_url — link para o comprovante
    showSuccessScreen(data)
  })
  .catch(err => showErrorScreen(err.message))
```

### Possíveis valores de `status` no polling

| `status` | Significado | Ação no frontend |
| --- | --- | --- |
| `checkout_created` | aguardando pagamento | continuar polling |
| `pending` | processamento em andamento | continuar polling |
| `paid` | aprovado | exibir tela de sucesso |
| `failed` | recusado | exibir tela de erro |
| `expired` | sessão expirada | oferecer nova tentativa |

---

## Desenvolvimento local → Cloud Run dev

O frontend pode rodar em `localhost` e chamar diretamente o Cloud Run dev. O CORS já está configurado para aceitar `localhost:3000`, `localhost:5173` e `localhost:8000`.

O worker de pagamentos e ativação roda em um serviço Cloud Run interno separado; o frontend continua falando apenas com o serviço HTTP público.

```bash
# .env.local (desenvolvimento local do frontend)
VITE_API_URL=https://edn-core-dev-cuwktlrora-uc.a.run.app
```

Não é necessário rodar o backend localmente para desenvolver o frontend.

---

## Tratamento de erros

| HTTP | Causa | O que fazer |
| --- | --- | --- |
| `400` | Body JSON malformado (parse falhou) | Verificar o corpo da requisição e retentar |
| `422` | Campo inválido (phone, email, CPF) ou campo obrigatório ausente | Mostrar erro de validação inline |
| `404` | Organização, tenant ou plano não encontrados | Recarregar catálogo e conferir o escopo atual |
| `409` `checkout_idempotency_conflict` | Idempotency-Key já foi usada com payload diferente | Gerar nova `Idempotency-Key` |
| `409` `checkout_in_progress` | Outro request está processando o mesmo checkout | Aguardar e usar `checkout_intent_key` para retomar |
| `409` `checkout_non_resumable` | `checkout_intent_key` existe mas está em estado terminal | Iniciar novo checkout sem `checkout_intent_key` |
| `409` `checkout_expired` | `checkout_intent_key` expirado | Iniciar novo checkout sem `checkout_intent_key` |
| `429` | Rate limit excedido | Aguardar `Retry-After` segundos e tentar novamente |
| `500` | Erro interno inesperado (banco de dados indisponível etc.) | Mostrar mensagem genérica; logar no frontend para diagnóstico |
| `502` | InfinitePay indisponível — o checkout não pôde ser criado | Mostrar mensagem genérica e tentar novamente após alguns segundos |
| `503` `checkout_recovery_required` | Provider URL capturada mas pedido não foi persistido; retry seguro com a mesma Idempotency-Key. Também retornado pelo `/track` se o recovery é realizado com sucesso mas o re-read do pedido falha | Aguardar brevemente e retentar com a mesma Idempotency-Key |
| `503` `provider_state_ambiguous` | Estado do provider incerto após falha parcial; não é seguro recriar o checkout | Usar `checkout_intent_key` para rastrear/retomar depois |

> **400 vs 422:** `400` é retornado quando o corpo JSON não pode ser parseado. `422` é retornado quando o JSON é válido mas um campo tem valor inválido ou está ausente (e.g. CPF inválido, email malformado, campo obrigatório vazio).
> **500 vs 502 vs 503:** `500` indica erro interno do backend. `502` indica que a InfinitePay estava indisponível no momento da criação do checkout. `503` indica que o estado do checkout ficou ambíguo (`provider_state_ambiguous`) ou que o recovery foi realizado mas o estado atualizado não pôde ser lido (`checkout_recovery_required`) — retentar com a mesma `Idempotency-Key` é seguro para `checkout_recovery_required`.

---

## Sequência de testes manuais

1. `GET /v1/plans?organization_slug=edn-core&tenant_slug=fun-onl&channel=web` — confirmar que planos retornam com preços corretos
2. `POST /v1/checkout/sessions` com `organization_slug=edn-core`, `tenant_slug=fun-onl`, `plan_slug=administrative`, `billing_cycle=monthly` e CPF válido → receber `checkout_url`, `order_nsu`, e **`checkout_intent_key`** — persistir ambos
3. `POST /v1/checkout/sessions/track` com `checkout_intent_key` recebido → confirmar `status`, `resumable`, e `checkout_url`
4. Abrir `checkout_url` no browser → completar pagamento de teste (InfinitePay sandbox)
5. `GET /v1/orders/{order_nsu}/status` → confirmar `status: "paid"` e `subscription_status: "active"`
