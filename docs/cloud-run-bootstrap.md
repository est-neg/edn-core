# Cloud Run Bootstrap Analysis

## Problem Framing

O projeto já tinha bootstrap básico de aplicação e conexão com MongoDB, mas ainda faltavam artefatos de operação para dois ambientes, separação entre segredos e configuração, e um fluxo explícito para materializar `edn-core-db-dev` e `edn-core-db-prd` no Atlas.

## Deployment Topology

### Cloud Run services

- desenvolvimento: `edn-core-dev`
- produção: `edn-core-prd`

### MongoDB databases

- desenvolvimento: `edn-core-db-dev`
- produção: `edn-core-db-prd`

### Why the databases were not visible yet

MongoDB Atlas não cria o banco no painel só porque a URI existe. O banco e a coleção aparecem quando a aplicação executa uma operação material de bootstrap, como `CreateCollection`, `CreateIndexes` ou a primeira escrita de documento.

## Bootstrap Strategy

### Runtime bootstrap

No startup a aplicação agora faz, nesta ordem:

1. carrega `VIL_*`
2. falha rápido se `VIL_MONGODB_URI`, `VIL_MONGODB_DATABASE` ou `VIL_LEADS_AUTH_TOKEN` estiverem ausentes
3. conecta ao Atlas e executa `Ping`
4. cria explicitamente a coleção `leads` se ela ainda não existir
5. cria os índices necessários de forma idempotente
6. publica readiness apenas se o `Ping` do Mongo continuar saudável

Isso é suficiente para materializar `edn-core-db-dev.leads` e `edn-core-db-prd.leads` assim que cada serviço subir pela primeira vez.

### Why the Docker image changed

O runtime anterior usava `scratch`. Para MongoDB Atlas, a conexão padrão exige TLS e certificados raiz do sistema. Uma imagem `scratch` pura pode quebrar essa conexão porque não traz CA bundle. O runtime foi trocado para `distroless` com certificados disponíveis.

## Environment Model

Os arquivos [../.env.development](../.env.development) e [../.env.production](../.env.production) existem como arquivos de input para `gcloud run deploy --env-vars-file`, não como repositório de segredos.

Entram nesses arquivos apenas valores não sensíveis:

- timeouts HTTP
- nível de log
- nome do banco
- nome da coleção
- limites do endpoint de leads

Segredos ficam fora do Git e são injetados por Secret Manager:

- `VIL_MONGODB_URI`
- `VIL_LEADS_AUTH_TOKEN`

## Cloud Build Design

### Development

O arquivo [cloudbuild.development.yaml](../cloudbuild.development.yaml) publica imagem em Artifact Registry e faz deploy do serviço `edn-core-dev` com:

- `--env-vars-file .env.development`
- `--set-secrets` para URI Mongo e token do webhook de leads
- escalonamento mais conservador para desenvolvimento

### Production

O arquivo [cloudbuild.yaml](../cloudbuild.yaml) faz deploy do serviço `edn-core-prd` com:

- `--env-vars-file .env.production`
- `--set-secrets` para URI Mongo e token do webhook de leads
- `min-instances=1` para reduzir cold start
- limites de concorrência mais altos

## Secret Handling

Mesmo com a string de conexão fornecida para análise, ela não deve ser gravada em `.env.production`, `cloudbuild.yaml`, Dockerfile ou qualquer arquivo versionado.

Segredos recomendados no GCP:

- `edn-core-dev-mongodb-uri`
- `edn-core-prd-mongodb-uri`
- `edn-core-dev-leads-auth-token`
- `edn-core-prd-leads-auth-token`

## First-Time Provisioning Commands

### Create runtime service accounts

```bash
gcloud iam service-accounts create edn-core-dev \
   --display-name="edn-core dev runtime"

gcloud iam service-accounts create edn-core-prd \
   --display-name="edn-core production runtime"
```

### Create Secret Manager entries

Use placeholders or terminal input. Não grave a URI literal em arquivo versionado.

```bash
gcloud secrets create edn-core-dev-mongodb-uri --replication-policy=automatic
gcloud secrets create edn-core-prd-mongodb-uri --replication-policy=automatic
gcloud secrets create edn-core-dev-leads-auth-token --replication-policy=automatic
gcloud secrets create edn-core-prd-leads-auth-token --replication-policy=automatic
```

```bash
printf '%s' '<mongodb-uri-dev>' | gcloud secrets versions add edn-core-dev-mongodb-uri --data-file=-
printf '%s' '<mongodb-uri-prd>' | gcloud secrets versions add edn-core-prd-mongodb-uri --data-file=-
printf '%s' '<leads-auth-token-dev>' | gcloud secrets versions add edn-core-dev-leads-auth-token --data-file=-
printf '%s' '<leads-auth-token-prd>' | gcloud secrets versions add edn-core-prd-leads-auth-token --data-file=-
```

### Grant runtime access to secrets

```bash
gcloud secrets add-iam-policy-binding edn-core-dev-mongodb-uri \
   --member="serviceAccount:edn-core-dev@${PROJECT_ID}.iam.gserviceaccount.com" \
   --role="roles/secretmanager.secretAccessor"

gcloud secrets add-iam-policy-binding edn-core-prd-mongodb-uri \
   --member="serviceAccount:edn-core-prd@${PROJECT_ID}.iam.gserviceaccount.com" \
   --role="roles/secretmanager.secretAccessor"
```

Repita a mesma política para os segredos de `leads-auth-token`.

### Submit the pipelines

```bash
gcloud builds submit --config cloudbuild.development.yaml .
gcloud builds submit --config cloudbuild.yaml .
```

### About the supplied MongoDB URI

Para o primeiro bootstrap, dev e prd podem apontar para o mesmo cluster Atlas e se diferenciar pelo nome do banco (`edn-core-db-dev` e `edn-core-db-prd`). Mesmo assim, o ideal operacional é usar credenciais separadas por ambiente ou rotacionar a credencial compartilhada antes do go-live.

## Agent Contributions Requested

### Security reviewer

Contribuição solicitada:

- validar a estratégia de Secret Manager e IAM
- revisar exposição pública do endpoint de leads
- confirmar segregação real entre dev e prd
- revisar necessidade de rate limiting ou Cloud Armor

### QA / TDD

Contribuição solicitada:

- validar startup sem segredos e com segredos válidos
- validar criação de coleção e índices no Atlas
- validar smoke check de `livez`, `readyz` e `POST /api/leads`
- validar deduplicação e health checks após deploy

## Operational Checks After First Deploy

1. confirmar que `edn-core-dev` sobe e `GET /readyz` retorna `200`
2. confirmar no Atlas que `edn-core-db-dev` apareceu com a coleção `leads`
3. repetir o fluxo para `edn-core-prd`
4. confirmar no Atlas a presença dos índices:
   - `idx_id_unique`
   - `idx_dedup_key_unique`
   - `idx_dedup`
   - `idx_status_received`
   - `idx_notification_status_received`

## Residual Risks

- a URI fornecida externamente deve ser tratada como potencialmente exposta e idealmente rotacionada antes do go-live
- a deduplicação ainda usa proteção por janela e chave de bucket; para produção de maior volume, vale um segundo ciclo de hardening para idempotência estrita
- ainda não existe rate limit distribuído no nível da aplicação
