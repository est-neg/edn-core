# Lead Protection Strategy

## Current Status

O endpoint `POST /api/leads` já possui alguns controles importantes:

- autenticação por token secreto entre canais server-side e o core
- validação server-side de payload
- limite de body
- JSON estrito
- deduplicação de curto prazo
- segredos fora do repositório

Isso é suficiente para evitar chamadas triviais sem credencial, mas não é suficiente para expor o fluxo publicamente com segurança forte.

## What Is Missing Today

Os controles abaixo ainda não estão ativos no runtime atual:

- rate limit distribuído
- prova de humanidade no formulário
- segregação explícita por consumer/site
- proteção forte contra replay além do bearer token

## Final Constraints For This Project

Decisão atual do projeto:

- Turnstile fica apenas no frontend
- não vamos usar Load Balancer nem Cloud Armor neste ciclo
- a proteção do backend precisa caber em Cloud Run + aplicação Go

## Recommended Architecture

### 1. Browser layer

Usar Cloudflare Turnstile nos formulários públicos:

- `https://developer.funcionario.online`
- `https://funcionario.online`
- `https://www.funcionario.online`

Motivo:

- menor fricção que reCAPTCHA tradicional
- boa proteção contra automação básica
- fácil restrição por hostname

Regra importante:

- o Turnstile ficará apenas no frontend neste projeto
- ele ajuda a reduzir bot no formulário, mas não é um controle confiável do core por si só

### 2. Site backend layer

O envio ao core deve continuar sendo server-to-server.

Fluxo recomendado:

1. o navegador envia o formulário ao site
2. o frontend do site resolve o Turnstile
3. o site chama `POST /api/leads`

Controles recomendados nesse hop:

- segredo exclusivo por ambiente
- identificação do consumer, por exemplo `developer-site` e `main-site`
- evolução futura para assinatura HMAC com timestamp e body hash

## 3. Cloud Run and application layer

Como o endpoint seguirá em Cloud Run sem proteção de edge, a contenção precisa existir no próprio serviço.

Controles implementáveis agora:

- rate limit em memória por instância
- contenção de concorrência por instância
- limites de escala e concorrência mais conservadores no Cloud Run
- manutenção de token secreto, validação, limite de body e deduplicação

## Turnstile vs application hardening

### Cloudflare Turnstile

Resolve:

- bots no formulário público

Não resolve sozinho:

- abuso direto ao endpoint do core
- rate limit da API
- replay com token vazado

### Captcha tradicional

Só vale como segunda opção se vocês não quiserem Turnstile. Para este caso, Turnstile é a escolha preferencial.

## Recommended Strategy For This Project

Estratégia recomendada em camadas:

1. Cloudflare Turnstile nos três sites
2. segredo separado por ambiente e, idealmente, por consumer
3. limiter na própria aplicação do endpoint de leads
4. contenção de concorrência na própria aplicação
5. Cloud Run com concorrência e escala mais conservadoras
6. métricas e alertas para `401`, `409`, `429` e falhas de verificação

## Rate Limit Guidance

Dentro das restrições finais, o caminho mais pragmático é sim aplicar rate limit em memória no Go.

Isso não vira perímetro distribuído, mas reduz dano e custo em cada instância.

Limitações inevitáveis:

- o limite é por instância do Cloud Run
- o limite global cresce quando o serviço escala horizontalmente
- o IP observado pelo core pode representar proxy ou backend intermediário

Por isso esse limiter deve ser tratado como contenção local, não como defesa completa.

## Consumer Model

Como apenas canais próprios vão consumir o core, a recomendação é separar consumidores logicamente:

- `developer-site`
- `main-site`

Benefícios:

- rotação seletiva de credenciais
- investigação de abuso por origem
- telemetria mais clara

## Anti-Replay Evolution

Hoje o bearer token já ajuda, mas não é a proteção final.

Próxima evolução recomendada:

- `X-Consumer-ID`
- `X-Lead-Timestamp`
- `X-Lead-Signature`

Com assinatura HMAC do corpo e janela curta de validade.

Isso reduz replay e tampering se um canal for comprometido.

## Delivery Priority

### Phase 1

- habilitar Turnstile nos sites
- separar segredos dev e prod de forma explícita
- ativar rate limit na aplicação
- ativar contenção de concorrência na aplicação

### Phase 2

- introduzir identificação formal do consumer
- reduzir replay com timestamp e assinatura HMAC
- criar alertas operacionais e dashboards

## Validation Requested From Other Agents

### Security reviewer

- revisar limites por instância e impacto do autoscaling
- revisar segregação de segredos por ambiente e consumer

### QA / TDD

- validar `429` na aplicação
- validar ausência de persistência em casos bloqueados
- validar métricas e logs sanitizados
