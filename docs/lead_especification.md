# Guia de Integração — Envio de Leads (Site Estaleiro → Backend)

**Versão:** 3.0
**Data:** 2026-05-26
**Escopo:** Integração server-to-server entre o site Next.js (Estaleiro) e o endpoint `POST /api/leads` do backend edn-core.

---

## Propósito

Este guia descreve o contrato **atual e implementado** do endpoint de leads.

O backend aceita dois shapes de payload:

- **Payload aninhado do site** (preferido) — `source`, `submittedAt`, e `lead` (objeto).
- **Payload flat legado** (depreciado, compatibilidade retroativa) — `name`, `email`, `phone`, `source` no topo.

Os dois shapes são **mutuamente exclusivos**. Combinar campos do topo (`name`, `email`, `phone`) com o objeto `lead` resulta em `400 invalid_payload`.

---

## Variáveis de Ambiente (Lado do Site)

Configure no `.env.local` para desenvolvimento e em secrets do ambiente de deploy para produção.
**Nunca use o prefixo `NEXT_PUBLIC_`** — o token nunca deve ser exposto ao browser.

### Desenvolvimento

```env
# .env.local
LEADS_API_URL=https://dev.edn-core.app/api/leads
LEADS_API_TOKEN=<token-de-dev>
```

> URL de referência para dev Cloud Run: `https://edn-site-dev-cuwktlrora-ew.a.run.app`

### Produção

```env
# Secret do ambiente de deploy (ex: Cloud Run, Vercel)
LEADS_API_URL=https://edn-core.app/api/leads
LEADS_API_TOKEN=<token-de-producao>
```

> O backend lê o token via configuração com prefixo `VIL_` — isso é interno ao backend.
> O site precisa apenas enviar o valor no header `Authorization`.
> O valor é comparado **literalmente** — não adicione prefixo `Bearer` a não ser que o token armazenado já o inclua.

---

## Endpoint

```http
POST /api/leads
```

- **Dev:** `https://dev.edn-core.app/api/leads`
- **Produção:** `https://edn-core.app/api/leads`

### Headers obrigatórios

- `Content-Type: application/json`
- `Authorization: <token>` — valor comparado por exact-match

---

## Shape preferido — Payload aninhado do site

Body máximo: **16 KiB**. Campos desconhecidos são **rejeitados**.

### Campos do topo

- `source` (string, obrigatório) — Trimado, máx 80 chars.
- `submittedAt` (string, obrigatório) — Timestamp RFC3339, ex: `2026-05-26T10:11:12Z`.
- `lead` (object, obrigatório) — Ver campos abaixo.

### Campos do objeto `lead`

- `name` (string, obrigatório) — Mínimo 2 chars após trim.
- `email` (string, obrigatório) — E-mail válido.
- `consent` (boolean, obrigatório) — Deve ser `true`.
- `whatsapp` (string, opcional) — Normalizado para dígitos; mínimo 8 dígitos.
- `businessName` (string, opcional) — Mínimo 2 chars se presente.
- `profile` (string, opcional) — Um de: `administrative`, `medical`, `dental`.
- `message` (string, opcional) — Máx 500 chars após trim.

### Exemplo de body (site)

```json
{
  "source": "estaleiro-site",
  "submittedAt": "2026-05-26T10:11:12Z",
  "lead": {
    "name": "Joao Silva",
    "businessName": "Clinica X",
    "whatsapp": "11999990000",
    "email": "joao@example.com",
    "profile": "medical",
    "message": "Oi",
    "consent": true
  }
}
```

---

## Shape legado — Payload flat (depreciado)

Aceito para compatibilidade retroativa durante a transição. Prefira o payload aninhado para novas integrações.

- `name` (string, obrigatório) — Mínimo 2 chars.
- `email` (string, obrigatório) — E-mail válido.
- `phone` (string, opcional) — Normalizado para dígitos pelo backend; mínimo 8 dígitos se presente. Duplicata gera 409.
- `source` (string, opcional) — Identifica a origem do lead. Trimado, máx 80 chars.

### Exemplo de body (flat)

```json
{
  "name": "Carla Mendonça",
  "email": "carla@estaleiro.com.br",
  "phone": "11988887777",
  "source": "estaleiro-site"
}
```

---

## Exemplo Next.js — Payload aninhado (Server Action)

```typescript
// app/actions/submit-lead.ts
"use server";

interface SiteLeadPayload {
  name: string;
  email: string;
  whatsapp?: string;
  businessName?: string;
  profile?: "administrative" | "medical" | "dental";
  message?: string;
}

export async function submitLead(data: SiteLeadPayload): Promise<void> {
  const url = process.env.LEADS_API_URL;
  const token = process.env.LEADS_API_TOKEN;

  if (!url || !token) {
    throw new Error("Variáveis LEADS_API_URL e LEADS_API_TOKEN não configuradas.");
  }

  const lead: Record<string, unknown> = {
    name: data.name,
    email: data.email,
    consent: true,
  };
  if (data.whatsapp) lead.whatsapp = data.whatsapp;
  if (data.businessName) lead.businessName = data.businessName;
  if (data.profile) lead.profile = data.profile;
  if (data.message) lead.message = data.message;

  const body = {
    source: "estaleiro-site",
    submittedAt: new Date().toISOString(),
    lead,
  };

  const res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: token,
    },
    body: JSON.stringify(body),
  });

  if (res.status === 409) {
    // Lead duplicado — tratar silenciosamente ou logar
    return;
  }

  if (!res.ok) {
    const text = await res.text();
    throw new Error(`Erro ao enviar lead: ${res.status} ${text}`);
  }
}
```

---

## Exemplo cURL

```bash
# Payload aninhado (preferido)
curl -s -X POST https://dev.edn-core.app/api/leads \
  -H "Content-Type: application/json" \
  -H "Authorization: <token-de-dev>" \
  -d '{
    "source": "estaleiro-site",
    "submittedAt": "2026-05-26T10:11:12Z",
    "lead": {
      "name": "João Silva",
      "email": "joao@estaleiro.com.br",
      "whatsapp": "11999990000",
      "consent": true
    }
  }'

# Resposta esperada em sucesso:
# HTTP 200
# {}
```

---

## Respostas do Backend

- `200` — Lead recebido e persistido com sucesso. Body: `{}`
- `400 invalid_payload` — Campos obrigatórios ausentes, tipo inválido, campo desconhecido, payload misto, ou `consent` falso.
- `401 unauthorized` — Token ausente ou não corresponde ao valor configurado.
- `409 duplicate_lead` — `email` já existe, ou `whatsapp`/`phone` já existe quando enviado.
- `429 rate_limited` — Rate limit excedido.
- `500 internal_error` — Falha interna no backend.

---

## Deduplicação

- `email` é sempre único — qualquer `email` repetido retorna `409`.
- `whatsapp`/`phone` é opcionalmente deduplicado — se presente e já cadastrado, retorna `409`.
- `submittedAt` é armazenado separadamente e **não** é usado para deduplicação.

---

## Checklist de Integração

- [ ] `LEADS_API_URL` configurada no ambiente de deploy (sem `NEXT_PUBLIC_`).
- [ ] `LEADS_API_TOKEN` configurada como secret no ambiente de deploy (sem `NEXT_PUBLIC_`).
- [ ] Envio feito de Server Action ou Route Handler — nunca direto do browser.
- [ ] Usar o payload aninhado (`lead` + `submittedAt`) para novas integrações.
- [ ] `consent: true` incluído no objeto `lead`.
- [ ] `Content-Type: application/json` presente em todas as requisições.
- [ ] Status `409` tratado como sucesso silencioso (lead duplicado já existe).
- [ ] Erros `4xx`/`5xx` logados no servidor, sem expor detalhes ao usuário final.
