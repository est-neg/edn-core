# InfinitePay Webhook Relay Implementation Plan

## Problem Framing

O source of truth para este plano e `docs/infinitepay-webhook-relay-architecture-specification.md`.

Hoje o webhook publico de InfinitePay ainda entra em runtimes que tambem carregam a autoridade financeira do core em `cmd/app/main.go` e `cmd/payments-api/main.go`.
Esse desenho mistura ingress publico, credenciais sensiveis, acesso a MongoDB e Redis, verificacao com `payment_check` e mutacao financeira no mesmo runtime exposto.

O objetivo desta entrega nao e escrever outra nota arquitetural.
O objetivo e decompor a especificacao aprovada em Stories executaveis, em ordem fixa, para que um `golang-developer` implemente o relay publico, o hop autenticado para o core, o hardening obrigatorio de reconciliacao e o cutover sem perder integridade financeira.

## Primary Target

O alvo principal e colocar `edn-webhook-dev` como unico ingress publico do provider e manter `edn-core-dev` como unica autoridade financeira em uma superficie autenticada.

Direcao obrigatoria:

- `edn-webhook-dev` recebe o webhook, valida apenas concerns de edge, preserva o raw body byte-for-byte e encaminha para o core
- `edn-core-dev` expoe `POST /internal/payments/providers/infinitepay/webhook-reconcile` apenas em superficie autenticada
- o fluxo canonico de reconciliacao continua centrado em `internal/payments/webhook.go`, mas so pode ser reutilizado depois do hardening obrigatorio de identidade canonica da transacao
- `payment_check`, deduplicacao, locks, persistencia, outbox e mutacao de `payments` e `orders` continuam exclusivamente no core
- a politica de transicao permanece a da especificacao: falhas temporarias de relay, core, auth interna, lock ou provider continuam provider-facing `400 Bad Request` ate validacao em ambiente inferior; falhas deterministicas continuam `422 Unprocessable Entity`

## Non-Negotiable Invariants

- `docs/infinitepay-webhook-relay-architecture-specification.md` e a referencia normativa para codigo, deploy, QA e cutover
- o relay e um adapter fino e stateless; ele nao vira uma segunda implementacao do dominio de pagamentos
- o provider payload nunca e fonte de verdade financeira; o core continua verificando estado no provider via `payment_check`
- o `transaction_id` retornado pelo provider em `payment_check` e a identidade canonica da transacao para reconciliacao
- o `transaction_id` do webhook e apenas hint de lookup e consistencia; nunca e verdade canonica
- se o webhook trouxer `transaction_id` e ele divergir do `transaction_id` verificado no provider, o core deve retornar `422 Unprocessable Entity` e nao pode mutar `payment` nem `order`
- se ja existir `transaction_nsu` canonico persistido para a mesma order ou invoice, o ID verificado no provider precisa coincidir; divergencia tambem retorna `422 Unprocessable Entity` sem mutacao
- se ainda nao existir identidade canonica persistida, o ID verificado no provider passa a ser o valor canonico a persistir antes de qualquer mutacao downstream
- `order not found`, `amount mismatch` e `transaction identity mismatch` sao integrity gates duros: retornam `422`, nao mutam estado e nunca podem virar `pending_review` neste path
- `lock acquisition timeout` ou `concurrent reconcile conflict` e falha temporaria: o core responde `5xx` e o relay expone provider-facing `400 Bad Request` sob a politica de transicao aprovada
- o relay deve preservar o raw body byte-for-byte, sem parse semantico, reserializacao JSON, normalizacao de whitespace ou reordenacao de campos
- o relay nao pode vazar payload bruto, secret path, tokens, `order_nsu`, `transaction_id`, `transaction_nsu` ou `invoice_slug` em logs, traces, metric labels ou respostas
- a rota interna nao pode compartilhar superficie Cloud Run com trafego publico nao autenticado
- a primeira release do relay nao introduz novas collections, nao muda TTL e nao transfere ownership de `webhook_events`, `outbox_events`, MongoDB ou Redis para o edge publico

## Relay No-Go Rules

Para este slice, existem proibicoes explicitas no relay:

- sem MongoDB
- sem Redis
- sem `payment_check`
- sem financial decisioning
- sem interpretacao semantica de JSON de pagamento
- sem raw payload logging
- sem pass-through de body ou headers de resposta vindos do core
- sem expor o secret path publico no core interno

## V1 Controls Locked From The Spec

Os controles abaixo nao sao opcionais no v1:

- secret path com um unico segmento opaco e pelo menos 128 bits de entropia
- rotacao planejada com janela de overlap de no maximo 15 minutos
- runbook de revogacao imediata e cutover acelerado para suspeita de vazamento do secret path
- `maxScale` do relay travado em 2
- throttling orientado por source normalizado com no maximo 8 slots in-flight por source por instancia
- throttling orientado por source normalizado com no maximo 120 requests por minuto por source por instancia
- no maximo 32 forwards in-flight por instancia, sem unbounded buffering
- timeout de 3 segundos para o forward relay -> core
- evidencias de IAM deny pelo Cloud Audit Logs ou log de auditoria equivalente quando a negacao acontecer antes da aplicacao
- alertas minimos para burst suspeito, throttle, provider-facing `400` temporario, backpressure sustentada e tentativa negada na rota interna

## Delivery Strategy

O rollout deve ser pequeno, reversivel e com ownership claro por boundary.
Nao pode existir periodo de target state em que duas URLs publicas sejam tratadas como contrato ativo de provider.

Regras de entrega:

- a ordem das Stories abaixo e fixa
- se o core atual ainda compartilha runtime publico nao autenticado, o split de deploy e pre-condicao de cutover
- o relay entra primeiro como runtime separado, mas reconciliacao so fica pronta depois do hardening de Story 4
- a politica de retry provider-facing `400` para falha temporaria continua provisoria ate validacao em ambiente inferior
- a aposentadoria dos mounts legacy so acontece depois da prova de readiness e do cutover validado

## Implementation Order

- Story 0: travar arquitetura, contrato de cutover e pre-condicoes
- Story 1: criar o skeleton do novo servico publico `edn-webhook-dev`
- Story 2: implementar validacao de edge, preservacao de raw body, stripping de headers e response mapping seguro
- Story 3: adicionar a rota interna autenticada de reconciliacao no core
- Story 4: endurecer e refatorar o core para identidade canonica de transacao e integrity gates
- Story 5: fechar IAM, service account, segredos, `run.invoker` e split de deploy quando necessario
- Story 6: validar retry e response mapping em ambiente inferior com a politica de transicao aprovada
- Story 7: executar a matriz obrigatoria de testes e readiness
- Story 8: aposentar o webhook publico legacy no core depois do cutover

## Story 0: Architecture Lock And Cutover Preconditions

### Story 0 Objective

Travar o contrato de implementacao e as pre-condicoes de cutover antes de abrir mudancas de codigo e deploy.

### Story 0 User Story

Como owner da mudanca, quero travar topology, trust boundaries e gates de cutover, para que a implementacao nao reabra decisoes ja aprovadas e nao avance com dependencias ocultas.

### Story 0 Implementation Tasks

- registrar `docs/infinitepay-webhook-relay-architecture-specification.md` como source of truth do slice
- confirmar se o runtime atual de `edn-core-dev` ainda serve trafego publico nao autenticado na mesma superficie onde a rota interna entraria
- se a resposta acima for sim, abrir a tarefa de split de deploy como pre-condicao de cutover e nao como follow-up
- travar a matriz de respostas publicas: `404`, `405`, `413`, `415`, `200`, `422` e `400` temporario conforme a especificacao aprovada
- travar a matriz de respostas internas: `200`, `422`, `503` e `5xx` temporario conforme a especificacao aprovada
- definir checklist de cutover, rollback, negative invocation test e evidencias operacionais esperadas
- definir ownership de segredos por boundary: secret path no relay, token real do provider no core, token interno opcional apenas como defense in depth

### Story 0 Acceptance Criteria

- existe um backlog aprovado com Stories 0 a 8 em ordem fixa
- existe resposta explicita para a pergunta "deployment split e obrigatorio neste ambiente?"
- a equipe concorda que a reconciliacao nao sera declarada pronta antes da Story 4
- a matriz de cutover registra as evidencias obrigatorias de IAM deny, retry policy e secret rotation/revocation

### Story 0 Blocking Dependencies

- aprovacao previa de `docs/infinitepay-webhook-relay-architecture-specification.md`
- inventario real do runtime atual de `edn-core-dev`

## Story 1: New Public Service `edn-webhook-dev` Skeleton With Minimal Surface

### Story 1 Objective

Criar o novo runtime publico do relay com a menor superficie possivel e sem acoplamento a datastore ou regras financeiras.

### Story 1 User Story

Como owner do ingress publico, quero um servico `edn-webhook-dev` minimo e separado, para que o provider tenha um endpoint publico alcancavel sem expor a autoridade financeira do core.

### Story 1 Implementation Tasks

- adicionar um novo entrypoint em `cmd/` para o relay publico, preferencialmente `cmd/webhook-relay/main.go`, deployado como servico `edn-webhook-dev`
- criar um pacote pequeno como `internal/webhookrelay` para `RelayHandler`, `ForwardClient`, `ResponseMapper` e logging seguro
- limitar a configuracao do runtime a secret path, core target URL ou audience, timeout, body max bytes e correlacao necessaria
- expor apenas `POST /v1/webhooks/infinitepay/{secretPath}` e endpoints operacionais padrao ja exigidos pelo runtime, se existirem
- manter o runtime sem dependencias de MongoDB, Redis, `internal/payments` persistente ou token real do provider
- preparar logging estruturado com correlation ID, classe de status, tamanho de request e latencia de forward, sem campos sensiveis

### Story 1 Acceptance Criteria

- o novo binario sobe isolado do core financeiro
- o relay compila e roda sem segredo de MongoDB, Redis ou token real de InfinitePay
- a superficie publica do novo servico fica limitada ao webhook e endpoints operacionais minimos
- nao existe parse semantico de payload nem mutacao de negocio nesta Story

### Story 1 Blocking Dependencies

- Story 0 concluida

## Story 2: Public Edge Validation, Raw-Body Preservation, Header Stripping, And Safe Response Mapping

### Story 2 Objective

Implementar o comportamento de edge aprovado, preservando o payload bruto e limitando o relay a validacao, throttling, forward e mapeamento seguro de resposta.

### Story 2 User Story

Como responsavel pelo edge publico, quero validar apenas o necessario, encaminhar bytes opacos e responder com um contrato seguro e sanitizado, para que o provider receba respostas deterministicas sem exposicao de detalhes internos.

### Story 2 Implementation Tasks

- validar method `POST` apenas
- validar o path exato do secret path configurado sem vazar o valor esperado em erro
- aceitar apenas `Content-Type: application/json` sem parametros ou com um unico `charset=utf-8` case-insensitive
- impor body max bytes configurado e ler o corpo uma unica vez como `[]byte`
- encaminhar o raw body byte-for-byte para o core, sem `json.Unmarshal`, `json.Marshal` ou qualquer interpretacao semantica do payload
- suportar rotacao planejada de secret path com overlap limitado quando a configuracao carregar path atual e proximo path valido
- aplicar source-aware throttling por source normalizado com no maximo 8 slots in-flight por source por instancia e no maximo 120 requests por minuto por source por instancia
- aplicar backpressure com no maximo 32 forwards in-flight por instancia e sem fila ilimitada
- usar timeout de 3 segundos no hop relay -> core
- remover `Authorization`, `X-EDN-*`, `Forwarded`, `X-Forwarded-*` e todos os hop-by-hop headers antes do forward
- reemitir apenas a allow-list interna: `Content-Type`, `X-EDN-Relay-Request-ID`, `X-EDN-Relay-Received-At` e header de token interno opcional quando habilitado
- sintetizar a resposta provider-facing exclusivamente da allow-list aprovada, sem propagar body ou headers vindos do core
- garantir mapping fixo: `404`, `405`, `415`, `413`, `400` temporario, `200` sucesso/duplicata e `422` deterministico
- bloquear logs, traces, labels e erros que exponham secret path, payload bruto, identificadores sensiveis ou tokens

### Story 2 Acceptance Criteria

- requests invalidos de path, method, media type ou tamanho sao rejeitados localmente sem chamar o core
- o forward preserva exatamente os bytes recebidos do provider
- nenhum header proibido chega ao core
- nenhuma resposta do core e repassada diretamente ao provider
- throttle, backpressure e timeout obedecem os limites v1 aprovados
- o relay continua sem JSON semantic parsing e sem qualquer decisao financeira

### Story 2 Blocking Dependencies

- Story 1 concluida

## Story 3: Internal Core Route `POST /internal/payments/providers/infinitepay/webhook-reconcile` On An Authenticated-Only Surface

### Story 3 Objective

Criar a superficie interna de reconciliacao no core, protegida por autenticacao de servico para servico e fora de qualquer superficie publica nao autenticada.

### Story 3 User Story

Como maintainer do core de pagamentos, quero uma rota interna autenticada e separada do edge publico, para que a reconciliacao financeira continue centralizada no core com authn e authz explicitos.

### Story 3 Implementation Tasks

- adicionar `POST /internal/payments/providers/infinitepay/webhook-reconcile`
- montar essa rota apenas em superficie Cloud Run autenticada e sem trafego publico nao autenticado compartilhado
- se o deploy atual nao permitir isso, executar o split de deploy antes de qualquer cutover de provider
- excluir essa rota da documentacao publica e do surface OpenAPI publico
- impor bounded request-body size e leitura segura do raw body antes de delegar ao fluxo core
- exigir Cloud Run service-to-service authentication como controle primario
- validar token interno opcional apenas como controle secundario, nunca como substituto de IAM
- delegar para o fluxo canonico de reconciliacao sem copiar regra financeira para o relay

### Story 3 Acceptance Criteria

- a rota interna nao e invocavel por caller externo nao autenticado no ambiente implantado
- o core aceita apenas o raw payload encaminhado pelo relay e nao usa o secret path publico
- a superficie interna responde `200`, `422`, `503` ou `5xx` conforme a classificacao aprovada
- a rota interna nao aparece na documentacao publica

### Story 3 Blocking Dependencies

- Story 0 concluida
- split de deploy concluido quando o runtime atual misturar superficie publica e interna

## Story 4: Core Hardening And Refactor For Canonical Transaction Identity And Integrity Gates

### Story 4 Objective

Endurecer o fluxo de reconciliacao do core para que a identidade canonica da transacao e os integrity gates aprovados sejam verdade antes de declarar o path pronto.

### Story 4 User Story

Como owner do dominio de pagamentos, quero que a reconciliacao use identidade canonica verificada no provider e gates de integridade explicitos, para que webhook retries ou payloads inconsistentes nao mutem estado financeiro incorretamente.

### Story 4 Implementation Tasks

- reutilizar `internal/payments/webhook.go` onde for possivel, mas extrair metodo reutilizavel se o shape atual impedir validacao explicita e testavel
- tratar o `transaction_id` retornado por `payment_check` como identidade canonica da transacao
- tratar `transaction_id` do webhook apenas como hint de lookup e consistency check
- quando o webhook trouxer `transaction_id` e ele divergir do ID verificado no provider, retornar `422 Unprocessable Entity` e nao mutar `payment` nem `order`
- quando ja existir `transaction_nsu` canonico persistido para a mesma order ou invoice e ele divergir do ID verificado, retornar `422 Unprocessable Entity` e nao mutar `payment` nem `order`
- quando ainda nao existir identidade canonica persistida, gravar o ID verificado no provider como valor canonico antes de qualquer mutacao downstream
- manter `order not found` como falha deterministica `422` sem mutacao
- manter `amount mismatch` como hard integrity gate `422` sem mutacao
- remover ou sobrescrever qualquer legado neste path que ainda tente traduzir `amount mismatch` ou `transaction mismatch` para `pending_review`
- preservar ownership do core sobre dedupe por identidade de transacao e event hash, fallback MongoDB quando Redis indisponivel, locks e outbox
- classificar `lock acquisition timeout` e `concurrent reconcile conflict` como falha temporaria `5xx` sem duplicate side effect

### Story 4 Acceptance Criteria

- a identidade canonica da transacao passa a vir do `payment_check` e nao do webhook payload
- divergencia entre `transaction_id` do webhook e ID verificado gera `422` sem mutacao
- divergencia entre `transaction_nsu` persistido e ID verificado gera `422` sem mutacao
- ausencia de identidade canonica persistida faz o ID verificado virar o novo valor canonico persistido
- `order not found`, `amount mismatch` e `transaction identity mismatch` nunca produzem `pending_review` neste path
- a reconciliacao nao e declarada pronta antes desta Story estar concluida e validada

### Story 4 Blocking Dependencies

- Story 3 concluida
- provider verification disponivel via `payment_check` no core

## Story 5: IAM, Service Account, `run.invoker`, Secret Separation, And Deployment Split If Required

### Story 5 Objective

Fechar a separacao operacional entre relay e core com least privilege, segredos por boundary e evidencia auditavel de negacoes.

### Story 5 User Story

Como owner de plataforma e seguranca, quero IAM e segredos separados por boundary, para que o relay publico nao herde privilegios ou credenciais da autoridade financeira.

### Story 5 Implementation Tasks

- criar ou confirmar uma service account dedicada para `edn-webhook-dev`
- conceder apenas `roles/run.invoker` dessa service account para o target interno do core
- configurar o relay para gerar identity token com a audience correta do core
- separar segredos no Secret Manager por boundary: secret path publico no relay, token real do provider no core e token interno opcional como controle secundario
- confirmar que o relay nao monta nem carrega segredos de MongoDB, Redis ou token real de InfinitePay
- aplicar `maxScale` 2 no deploy do relay
- concluir o split de deploy quando a rota interna ainda compartilha runtime com trafego publico nao autenticado
- garantir que request logging ou tracing gerenciado pela plataforma nao exponha o secret path; se isso nao puder ser provado, bloquear cutover
- documentar runbook de rotacao planejada com overlap maximo de 15 minutos
- documentar runbook de revogacao imediata e provider cutover para suspeita de vazamento
- garantir que negacoes de IAM antes do codigo fiquem evidenciadas em Cloud Audit Logs e que falhas de token interno apos IAM gerem audit log aplicacional sem body nem segredos

### Story 5 Acceptance Criteria

- o relay opera com privilegios minimos e apenas com `run.invoker` sobre o core interno
- existe prova de que o relay nao tem acesso a MongoDB, Redis nem token real do provider
- IAM deny no path interno fica auditavel por Cloud Audit Logs ou equivalente
- o split de deploy esta completo quando necessario
- os runbooks de rotacao e revogacao existem antes do cutover

### Story 5 Blocking Dependencies

- Stories 1 e 3 concluidas
- acesso a configuracao e IAM do ambiente alvo

## Story 6: Retry And Response Validation In Lower Environment Under The Approved Transition Policy

### Story 6 Objective

Validar o comportamento real de retry e timeout do provider sem quebrar a politica de transicao aprovada para falhas temporarias.

### Story 6 User Story

Como owner do rollout, quero provar em ambiente inferior como InfinitePay reage a `200`, `400`, `422`, `5xx` e timeouts, para que o cutover use uma politica de retry validada e nao uma suposicao perigosa.

### Story 6 Implementation Tasks

- implementar provider-facing `400 Bad Request` para falhas temporarias de relay, core, auth interna apos admissao, lock conflict e falha temporaria de `payment_check` durante a transicao
- manter provider-facing `422 Unprocessable Entity` para malformed payload, payload inutilizavel, `order not found`, `amount mismatch` e `transaction identity mismatch`
- validar em ambiente inferior o comportamento do provider para `2xx`, `400`, `422`, `4xx`, `5xx`, timeout, connection reset e resposta atrasada perto do limite de timeout
- provar que o relay nunca devolve falso `200 OK` quando o core nao reconciliou o evento
- medir se o timeout de 3 segundos e compativel com o hop relay -> core nas condicoes normais
- validar shed local por source-aware throttling ou backpressure antes de qualquer chamada ao core
- registrar o resultado observado e, se necessario, atualizar a politica final de producao antes da promocao

### Story 6 Acceptance Criteria

- existe evidencia de ambiente inferior para o comportamento real de retry e timeout do provider
- a politica final de producao fica registrada antes do cutover
- as falhas temporarias continuam a induzir retry sem gerar falso sucesso
- os erros deterministas continuam `422` e nao sao recategorizados como falha temporaria

### Story 6 Blocking Dependencies

- Stories 2, 3, 4 e 5 concluidas
- ambiente inferior com relay, core interno e provider validation funcional

## Story 7: Mandatory Test Matrix And Cutover Readiness

### Story 7 Objective

Fechar a matriz obrigatoria de testes, readiness e evidencias operacionais antes de liberar trafego real.

### Story 7 User Story

Como owner de QA e release, quero uma matriz obrigatoria de testes e readiness executavel, para que o cutover ocorra com prova de comportamento e nao por inspecao manual.

### Story 7 Implementation Tasks

- escrever testes failing-first para preservacao byte-for-byte do raw body
- testar rejeicoes locais `404`, `405`, `415` e `413` sem forward ao core
- testar stripping de `Authorization`, `X-EDN-*`, `Forwarded`, `X-Forwarded-*` e hop-by-hop headers
- testar que o relay so devolve a allow-list publica de headers e bodies minimos aprovados, sem body ou header upstream pass-through
- testar `200 OK` para sucesso autenticado e duplicata conhecida
- testar `422` interno e provider-facing para malformed payload, `order not found`, `amount mismatch` e `transaction identity mismatch`
- testar `5xx` interno e provider-facing `400` para `lock acquisition timeout` ou `concurrent reconcile conflict`
- testar core indisponivel ou falha temporaria de `payment_check` com provider-facing `400` e ausencia de falso `200`
- testar caller externo falhando ao invocar a rota interna e coletar a evidencia correspondente em Cloud Audit Logs ou equivalente
- testar fallback de dedupe quando Redis estiver indisponivel e garantir duplicate-safe success
- testar preservacao de exactly-once para efeitos downstream e outbox sob duplicate deliveries e retries
- validar que logs e tracing gerenciados pela plataforma nao expõem o secret path antes do cutover
- validar operacionalmente os runbooks de overlap de rotacao e revogacao imediata
- configurar e validar os alertas minimos: 20 rejeicoes invalidas em 5 minutos, 10 throttles em 1 minuto, 5 provider-facing `400` temporarios em 5 minutos, backpressure sustentada por 1 minuto e invocacao negada da rota interna
- executar checklist formal de cutover e rollback

### Story 7 Acceptance Criteria

- a matriz obrigatoria de testes passa com evidencias reproduziveis
- readiness de seguranca, QA e operacao fica documentada
- o ambiente implantado prova que a rota interna nao e externamente invocavel
- o cutover nao e aprovado enquanto qualquer item obrigatorio desta Story permanecer aberto

### Story 7 Blocking Dependencies

- Stories 1 a 6 concluidas

## Story 8: Legacy Public Webhook Retirement In `cmd/app/main.go` And `cmd/payments-api/main.go` After Cutover

### Story 8 Objective

Remover o contrato publico legacy do core depois que o relay estiver validado em producao e o cutover estiver estavel.

### Story 8 User Story

Como maintainer da plataforma, quero aposentar os mounts publicos legacy do core, para que o target topology tenha um unico ingress publico e a autoridade financeira permaneca apenas na superficie interna autenticada.

### Story 8 Implementation Tasks

- executar o cutover do provider para a URL do relay e validar trafego real no novo caminho
- confirmar que o core interno esta reconciliando com estabilidade e observabilidade adequada
- remover ou desabilitar os mounts publicos legacy de webhook em `cmd/app/main.go` e `cmd/payments-api/main.go`
- confirmar que nenhuma rota target-state do core continua exigindo ou expondo o secret path publico
- rodar smoke checks de pos-cutover e manter rollback por revisao ou configuracao, nao por dupla autoridade publica permanente

### Story 8 Acceptance Criteria

- `edn-webhook-dev` passa a ser o unico ingress publico para InfinitePay
- os mounts publicos legacy do core ficam removidos ou explicitamente desabilitados depois da validacao de cutover
- nao existe dependencia operacional do target state nos mounts antigos

### Story 8 Blocking Dependencies

- Story 7 concluida
- cutover validado com trafego real ou validacao equivalente aprovada para o ambiente alvo

## Security Requirements From Review

- tratar todo payload de provider como input nao confiavel e validar trust boundary no relay e no core
- manter authn primario por Cloud Run service-to-service authentication e authz por `run.invoker` minimo
- nunca logar payload bruto, secret path, tokens, `order_nsu`, `transaction_id`, `transaction_nsu` ou `invoice_slug`
- descartar headers de forwarding e autenticacao vindos da internet antes do hop interno
- bloquear cutover se a plataforma nao garantir que request logging ou tracing nao exponha o secret path
- manter runbooks obrigatorios para rotacao planejada, revogacao imediata e resposta a comprometimento
- usar Cloud Audit Logs ou equivalente como evidencia de negacao de IAM antes da aplicacao
- impedir que a rota interna seja montada em superficie com trafego publico nao autenticado

## QA Requirements From Review

- comecar por testes failing-first para contrato do relay e para os integrity gates do core
- validar explicitamente os cenarios `200`, `400`, `422`, `503` e `5xx` aprovados neste plano
- testar a regra canonica de identidade da transacao, incluindo hint de `transaction_id`, mismatch com `payment_check` e mismatch com `transaction_nsu` persistido
- testar concorrencia, lock conflict, retries e duplicate deliveries; nao aprovar apenas por happy path
- testar negativamente a invocacao externa da rota interna e registrar a evidencia de auditoria correspondente
- validar throttling orientado por source, backpressure, timeout de 3 segundos e ausencia de falso sucesso
- validar que o relay nao usa MongoDB, Redis, `payment_check` nem interpretacao semantica do payload
- nao aprovar por inspecao quando existir comportamento executavel a validar

## Requested Agent Handoff

### `golang-developer`

Implementar obrigatoriamente na ordem das Stories 0 a 8.

Condicoes de implementacao:

- nao pular a Story 4; reconciliacao nao pode ser declarada pronta so porque o relay encaminha trafego
- manter todo financial decisioning, `payment_check`, dedupe, locks, persistencia e mutacao no core
- manter o relay fino, stateless e sem MongoDB, Redis ou parse semantico do payload
- concluir split de deploy antes de cutover se a rota interna ainda compartilhar superficie publica nao autenticada
- tratar a politica provider-facing `400` para falha temporaria como provisoria ate a validacao de Story 6

### `security-reviewer`

Validar trust boundaries, header stripping, IAM, `run.invoker`, service accounts, segregacao de segredos, redacao de logs, evidencias em Cloud Audit Logs e bloqueio de cutover quando houver risco de vazamento do secret path por logging ou tracing de plataforma.

### `qa-tdd`

Executar a matriz obrigatoria de testes Story por Story, exigir evidencias para retry policy em ambiente inferior, validar os integrity gates do core antes do cutover e nao liberar readiness enquanto qualquer item obrigatorio de Story 7 permanecer aberto.
