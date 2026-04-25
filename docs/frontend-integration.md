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
   |<-- { order_nsu, checkout_url } -------|                               |
   |                                       |                               |
   |-- window.location = checkout_url ---->|   (usuário paga na InfinitePay)
   |                                       |                               |
   |<-- redirect para /checkout/return?order_nsu=...  (InfinitePay redireciona)
   |                                       |                               |
   |-- GET /v1/orders/{order_nsu}/status ->|                               |
   |<-- { status: "paid", ... } -----------|                               |
```

---

## Passo 1 — Listar planos

```http
GET /v1/plans?organization_slug=acme&tenant_slug=public&channel=web
```

Para o runtime multi-tenant, envie sempre `organization_slug` e `tenant_slug` juntos. O backend resolve os IDs reais da organização e do tenant no MongoDB e lê o catálogo publicado em `versioned_plans`.

Resposta:

```json
{
  "plans": [
    {
      "slug": "pro",
      "name": "Plano Pro",
      "billing_cycle": "monthly",
      "price_cents": 9900,
      "currency": "BRL"
    }
  ]
}
```

Use `price_cents / 100` para exibir o preço. **Nunca envie o preço no request de checkout** — o backend sempre usa o valor do plano.

---

## Passo 2 — Criar sessão de checkout

```http
POST /v1/checkout/sessions
Content-Type: application/json
Idempotency-Key: checkout-2026-04-25-user-123
```

Body:

```json
{
  "organization_slug": "acme",
  "tenant_slug": "public",
  "channel": "web",
  "plan_slug": "pro",
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
| `channel` | `"web"`, `"mobile"`, `"partner"` ou `"all"` | não, backend assume `"web"` |
| `plan_slug` | string, apenas letras minúsculas, números e `-_` | sim |
| `billing_cycle` | `"monthly"` ou `"annual"` | sim |
| `customer.name` | string não vazia | sim |
| `customer.email` | e-mail válido | sim |
| `customer.phone` | E.164 (`+5511...`) ou 10/11 dígitos BR | sim |
| `customer.document` | CPF — com ou sem pontuação (`000.000.000-00` ou `00000000000`) | sim |

Resposta `201`:

```json
{
  "order_nsu": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "status": "checkout_created",
  "checkout_url": "https://checkout.infinitepay.io/...",
  "expires_at": "2026-04-24T15:30:00Z"
}
```

**Ação do frontend:** redirecionar o usuário para `checkout_url`. Persistir `order_nsu` em `sessionStorage` ou incluí-lo na `redirect_url`.

**Idempotencia:** reutilize a mesma `Idempotency-Key` quando for repetir exatamente o mesmo checkout por retry de rede ou refresh. Se o payload mudar, gere uma nova chave.

**Falha parcial tolerada:** se a InfinitePay já tiver criado o checkout mas o backend tiver sofrido falha parcial na persistência local, repetir o mesmo request com a mesma `Idempotency-Key` retorna a mesma sessão de checkout em vez de criar um segundo checkout.

```js
const { order_nsu, checkout_url } = await response.json()
sessionStorage.setItem('order_nsu', order_nsu)
window.location.href = checkout_url
```

---

## Passo 3 — Página de retorno `/checkout/return`

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
| `400` | Campo inválido (phone, email, CPF) | Mostrar erro de validação inline |
| `404` | Organização, tenant ou plano não encontrados | Recarregar catálogo e conferir o escopo atual |
| `409` | Chave de idempotência em conflito ou checkout ainda em andamento | Repetir somente com a mesma `Idempotency-Key`; se o payload mudou, gere nova chave |
| `422` | Escopo ou payload inválido | Corrigir os campos enviados |
| `429` | Rate limit excedido | Esperar e tentar novamente |
| `502` | InfinitePay indisponível | Mostrar mensagem genérica e tentar novamente |

---

## Sequência de testes manuais

1. `GET /v1/plans?organization_slug=<org>&tenant_slug=<tenant>&channel=web` — confirmar que planos retornam com preços corretos
2. `POST /v1/checkout/sessions` com `organization_slug`, `tenant_slug` e CPF válido → receber `checkout_url` e `order_nsu`
3. Abrir `checkout_url` no browser → completar pagamento de teste (InfinitePay sandbox)
4. `GET /v1/orders/{order_nsu}/status` → confirmar `status: "paid"` e `subscription_status: "active"`
