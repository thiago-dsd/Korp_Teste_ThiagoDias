# Detalhamento técnico

Documento exigido pelo desafio. As seções **1 a 7** respondem item a item o que a especificação
pede, com o motivo de cada escolha — não só o que foi usado, mas por que valia a pena. As seções
**8 e 9** cobrem duas decisões que o desafio não perguntou e que sustentam metade do
comportamento do sistema: a mensageria e o provedor de IA.

Autor: Thiago Dias · Repositório: <https://github.com/thiago-dsd/Korp_Teste_ThiagoDias>

---

## 1. Ciclos de vida do Angular utilizados

O Angular 22 permite resolver com _signals_ e injeção boa parte do que antes exigia um ciclo de
vida. Por isso os que aparecem no código são poucos e cada um tem uma razão concreta.

| Ciclo de vida | Onde | Por quê |
|---|---|---|
| `ngOnInit` | telas de produtos, notas, detalhe da nota, dashboard, formulários, painel de histórico | Ponto em que os _streams_ de busca e recarga são montados. Não fica no construtor porque montar assinaturas ali dificulta o teste e roda antes de os `input()` estarem disponíveis — o painel de histórico provou isso na prática: lendo `product()` no construtor, o Angular acusa `NG0950`, porque um `input.required` ainda não tem valor nesse momento. |
| `ngOnDestroy` | `ClickOutsideDirective`, `MenuService`, `ModalComponent` | Os três guardam recursos que o Angular não recolhe sozinho: um ouvinte de evento no `document`, uma assinatura de longa duração e, no diálogo, a rolagem travada do `body` e o foco que precisa voltar para onde estava. |
| `ngAfterViewInit` | `ClickOutsideDirective`, `NavbarSubmenuComponent`, `ModalComponent` | Precisam do elemento já renderizado para medir, ouvir ou receber foco. |

**O que substituiu os demais**, e é a parte que merece atenção:

- **`takeUntilDestroyed(destroyRef)`** (8 arquivos) elimina o par
  `ngOnDestroy` + `Subject` + `takeUntil` que normalmente aparece em toda tela. A assinatura morre
  junto com o componente sem que exista um método para isso.
- **`effect()`** carrega o produto no formulário quando o `input()` muda — o papel que antes era do
  `ngOnChanges`, mas reagindo ao valor e não ao ciclo de renderização.
- **`computed()`** (8 arquivos) deriva estado (`hasMore`, `selectionFull`, `pendingAdjustments`,
  `isAdmin`) em vez de recalcular em `ngDoCheck` ou no template.
- **`input()` / `output()`** substituem `@Input`/`@Output`; `output()` aparece em
  `product-form` (`save`, `cancel`) e `bulk-result` (`dismiss`).
- **`inject()`** (13 arquivos) no lugar de injeção por construtor.

A aplicação roda **_zoneless_** (`provideZonelessChangeDetection()` em `main.ts`): sem Zone.js, a
detecção de mudanças é disparada pelos próprios signals, o que torna `OnPush` desnecessário na
maioria dos componentes — ele aparece só onde há um motivo explícito, como no `app-bulk-result`.

Isso tem uma consequência prática que apareceu nos testes: `fakeAsync` deixa de funcionar sem
Zone.js, e por isso o intervalo de _polling_ da impressão é injetável (`PRINT_POLL_INTERVAL`) em
vez de manipulado por relógio falso.

---

## 2. Uso de RxJS

Sim, e de forma deliberada: **signals para estado, RxJS para eventos ao longo do tempo**. Onde só
existe estado, não há Observable; onde existe uma sequência de acontecimentos, RxJS resolve com
menos código do que qualquer alternativa.

Operadores em uso: `debounceTime`, `distinctUntilChanged`, `switchMap`, `startWith`, `catchError`,
`tap`, `map`, `of`, `throwError`, `finalize`, `shareReplay`, `takeWhile`, `timer`, `forkJoin`,
`firstValueFrom`, `Subject`, `Subscription`, `toSignal`, `takeUntilDestroyed`.

Os três usos que justificam a biblioteca:

**Busca no catálogo** (`products.component.ts`) — `debounceTime(300)` espera a pessoa parar de
digitar, `distinctUntilChanged()` descarta o termo repetido e **`switchMap` cancela a resposta de
uma busca que já foi substituída por outra mais nova**. Sem esse último, uma resposta lenta
sobrescreveria na tela o resultado de uma busca posterior — o clássico problema de corrida de
digitação.

**Acompanhar a impressão** (`invoice.service.ts`) — a impressão é assíncrona, então
`timer(intervalo, intervalo)` + `switchMap(() => this.get(id))` + `takeWhile(status === 'PRINTING',
true)` consulta a nota até ela sair de PRINTING e **completa entregando a nota final** como última
emissão. O `true` no `takeWhile` é o detalhe que faz o resultado final chegar ao assinante.

**Filtros vindos da URL** (`products.component.ts`, `invoices.component.ts`) — a query string é a
fonte da verdade do que está na tela. `route.queryParamMap` → `map` para um estado →
`distinctUntilChanged` → `switchMap(() => this.fetch())`. Os controles não recarregam a lista: eles
navegam, e a navegação volta por esse mesmo _stream_. O `distinctUntilChanged` é o que impede o
laço, já que escrever na URL reemite o parâmetro.

**Painéis independentes do dashboard** (`home.component.ts`) — `forkJoin` de quatro listagens, cada
uma com o seu próprio `catchError` que degrada para página vazia. O `catchError` fica **dentro** do
`forkJoin`, não fora: fora dele, um serviço lento derrubaria os quatro painéis e a tela ficaria em
branco em vez de mostrar o que deu para ler.

**Renovação de sessão** (`auth.service.ts` + `auth.interceptor.ts`) — quando o token expira com
várias requisições em voo, todas precisam esperar **uma única** renovação. O `AuthService` mantém a
renovação em curso com `shareReplay(1)` e o interceptor apenas encadeia a requisição original com
`switchMap` depois dela. Sem isso seriam N renovações concorrentes — e, com detecção de reuso de
refresh token no servidor, isso derrubaria a sessão inteira.

---

## 3. Outras bibliotecas e suas finalidades

### Frontend

| Biblioteca | Finalidade |
|---|---|
| **Angular 22** | Framework da SPA: componentes _standalone_, roteamento com _lazy loading_, formulários reativos tipados, `HttpClient` com interceptors. |
| **Tailwind CSS 4** | Estilos utilitários direto no template, com `@tailwindcss/forms` (campos), `@tailwindcss/typography`, `@tailwindcss/aspect-ratio` e `tailwind-scrollbar`. |
| **angular-svg-icon** | Ícones SVG inline (Heroicons), permitindo colorir por CSS. |
| **ngx-sonner** | Notificações _toast_ de confirmação e erro. |
| **Vitest** (via `@angular/build:unit-test`) + **jsdom** | Testes unitários — 149 no total. |
| **Playwright** | Testes end-to-end (escritos; ver "limitações conhecidas"). |
| **Prettier** | Formatação, verificada no CI. |

`apexcharts` e `ng-apexcharts` constam no `package.json`: vieram com o template autorizado e
**não são usados por nenhuma tela** deste sistema. Estão registrados aqui por honestidade em vez de
listados como se fossem parte da solução.

### Backend

| Biblioteca | Finalidade |
|---|---|
| **jackc/pgx v5** | Driver PostgreSQL e _pool_ de conexões. Escolhido por expor recursos que o `database/sql` esconde e que este projeto usa: `FOR UPDATE SKIP LOCKED`, `COPY`/`unnest` com arrays, savepoints e códigos de erro do Postgres (violação de unicidade tratada por código, não por texto). |
| **golang-jwt/jwt v5** | Assinatura e verificação RS256 dos tokens de acesso. |
| **rabbitmq/amqp091-go** | Cliente AMQP: _publisher confirms_, _dead letter exchange_, reconexão. |
| **google/uuid** | Identificadores. |
| **golang.org/x/crypto** | `argon2id` para hash de senha. |

São **cinco dependências diretas**. Tudo o mais — HTTP, JSON, roteamento, testes, logging
estruturado (`log/slog`), concorrência — vem da biblioteca padrão.

### Bibliotecas de componentes visuais

**Nenhuma.** Não há Angular Material, PrimeNG ou similar. A interface é construída com Tailwind e
componentes próprios (`app-modal`, `app-bulk-result`, `app-invoice-status`, `app-stock-level`,
`app-product-form`, `app-product-import`, `app-product-movements`).

O que uma biblioteca costuma dar de graça está resolvido em dois lugares e não espalhado pelos
_templates_: `app-modal` concentra o que faz um diálogo ser um diálogo (Escape, foco inicial, foco
devolvido ao fechar, rolagem travada atrás), e um punhado de _utilities_ `action-*` em `styles.css`
concentra cor e estado de cada papel de ação — sólida, destrutiva, contornada, alternador ligado e
desligado, ação só-texto —, deixando no elemento apenas tamanho e forma. Antes disso a mesma
sequência de classes estava repetida em catorze _templates_, e três telas equivalentes tinham três
comportamentos de `hover` diferentes.

O motivo é específico deste sistema: as telas são poucas e o comportamento delas é o que importa —
seleção que sobrevive à paginação, painel que mostra sucesso parcial, formulário que preserva o que
foi digitado quando o servidor recusa. Uma biblioteca de componentes traria estilos prontos para
telas que não temos e não resolveria nenhum desses comportamentos, que são os que exigiram
trabalho de verdade.

---

## 4. Gerenciamento de dependências no Go

**Go Modules**, que é o mecanismo oficial desde o Go 1.11.

- `go.mod` declara o módulo (`github.com/thiagodias/korp-invoices`), a versão da linguagem
  (1.26.4) e as versões exatas de cada dependência.
- `go.sum` guarda o _hash_ criptográfico de cada módulo. Uma dependência que mude de conteúdo sob a
  mesma versão faz o build **falhar**, o que é a defesa contra um pacote adulterado no meio do
  caminho.
- `go mod tidy` mantém o arquivo alinhado ao que o código realmente importa; dependências diretas e
  indiretas ficam separadas em blocos.
- **Um único módulo** para os três serviços. Eles compartilham `internal/platform` e
  `internal/contracts` — os contratos de evento existem justamente para que os dois lados compilem
  contra a mesma definição, e módulos separados transformariam uma mudança de payload em uma
  publicação de versão.
- `internal/` impede que qualquer código fora do módulo importe esses pacotes: é o próprio
  compilador garantindo a fronteira.
- Não há `vendor/`: o `go.sum` já garante reprodutibilidade, e o cache de módulos é preservado
  entre execuções no CI e na camada de build do Dockerfile.

---

## 5. Frameworks utilizados no Go

**Nenhum framework web.** O roteamento é o `net/http` da biblioteca padrão, usando o suporte a
método + caminho introduzido no Go 1.22:

```go
mux.Handle("POST /products", guard(admin(write(http.HandlerFunc(a.createProduct)))))
mux.Handle("GET  /products/{id}", guard(read(http.HandlerFunc(a.getProduct))))
```

Foi uma decisão, não uma omissão. Um framework como Gin ou Echo entregaria roteamento com
parâmetros, _middlewares_ encadeados e _binding_ de JSON — e a biblioteca padrão passou a fazer as
três coisas. O que este projeto realmente precisava, nenhum deles daria pronto: outbox
transacional, idempotência com replay, cerca entre tentativas de impressão, limitação por
categoria de operação. Adicionar um framework significaria uma dependência a mais no caminho de
toda requisição para resolver a parte fácil do problema.

Os _middlewares_ são funções `func(http.Handler) http.Handler` compostas por `httpx.Chain`:
request id → log → recover → CORS → timeout → métricas → limite público → idempotência, e, por
rota, autenticação → papel → limite da categoria.

Também não há ORM. O SQL é escrito à mão porque as consultas que importam aqui não são as que um
ORM gera bem: `UPDATE ... WHERE balance >= $2` que recusa saldo negativo no próprio banco,
`FOR UPDATE SKIP LOCKED` no outbox, `INSERT ... ON CONFLICT DO NOTHING` para deduplicação,
paginação por _keyset_, `unnest` para inserir os itens de uma nota em uma ida ao banco.

Migrações são versionadas e aplicadas pelo próprio serviço no _start_, com _advisory lock_ (só uma
instância migra) e uma transação por migração.

---

## 6. Tratamento de erros e exceções no backend

Go não tem exceções: erros são valores e viajam explicitamente. O projeto padroniza isso em um
vocabulário próprio, `internal/platform/apperr`.

### Um erro carrega o que é preciso para agir

```go
type Error struct {
    Kind    Kind              // invalid, not_found, conflict, unauthorized,
                              // forbidden, too_many_requests, unavailable, internal
    Code    string            // "insufficient_balance" — estável, para o cliente
    Message string            // escrito para o operador, não para o log
    Details map[string]string // campo → o que há de errado
    Cause   error             // o erro original, para o log
}
```

A tradução para HTTP acontece **em um lugar só** (`httpx.StatusFor`), a partir do `Kind`. Nenhum
handler escreve `http.StatusConflict`.

### Decisões que valem explicar

- **`Kind` separado de `Code`.** O `Kind` diz o que o transporte deve fazer; o `Code` diz o que
  aconteceu. `forbidden` e `unauthorized` são _kinds_ distintos de propósito: entrar de novo
  resolve um e não resolve o outro.
- **Sentinelas comparáveis.** `apperr.Error.Is` compara `Kind` + `Code`, então
  `errors.Is(err, stock.ErrDuplicatedCode)` continua funcionando depois de `WithCause`/`WithDetails`.
  Isso foi corrigido por causa de um bug real: a comparação falhava silenciosamente após enriquecer
  o erro.
- **Validação acumulada.** Um corpo inválido responde com **todos** os campos ofensores de uma vez
  em `Details`, não com o primeiro que falhou.
- **A causa nunca vaza.** `Cause` vai para o log; o cliente recebe `Message` e `Code`. Um erro
  interno responde "An unexpected error occurred." com o `request_id` para correlação.
- **Pânico não derruba o processo.** O middleware `Recover` converte em 500 e registra.
- **Erro permanente × transitório.** No consumo de mensagens, `resilience.Permanent(err)` marca o
  que não adianta repetir (payload malformado) e interrompe o retry na hora; o resto é repetido com
  _backoff_ e, esgotado, vai para a _dead letter queue_ — que é inspecionável e reprocessável por
  endpoint interno.
- **Rejeição de negócio não é falha.** No débito de estoque, saldo insuficiente **é uma resposta**:
  vira o evento `stock.rejected` e a nota reabre com o motivo. Só erro de infraestrutura é repetido.
- **Erros embrulhados com `%w`** preservam a cadeia; o contexto é adicionado na borda de cada
  camada (`fmt.Errorf("insert invoice item: %w", err)`).

### Do lado do cliente

O `apiErrorInterceptor` transforma toda falha em `ApiError` com `code`, `message`, `details` e
`status`, e as telas mostram a `message` — que já foi escrita para quem está lendo. Serviço fora do
ar vira `service_unreachable` em vez de um erro de rede cru.

---

## 7. LINQ / C#

Não se aplica: o backend é em **Go**.

---

## 8. Mensageria: por que existe, e por que RabbitMQ

### O problema que ela resolve

Imprimir uma nota toca **dois serviços com dois bancos**: o faturamento fecha a nota, o estoque
debita os saldos. Não existe transação distribuída entre eles, então uma chamada HTTP síncrona
deixaria duas perguntas sem resposta boa: o que acontece se o estoque estiver fora do ar no
momento do clique, e o que acontece se ele debitar mas a resposta se perder no caminho.

A escolha aqui foi tornar o pedido **durável antes de ser entregue**.

### Padrão outbox: estado e mensagem, ou nenhum dos dois

`internal/billing/print_store.go` grava a mudança de status **e** a mensagem
`invoice.print_requested` na **mesma transação**. Ou as duas coisas acontecem, ou nenhuma. Não
existe o estado "a nota foi fechada mas o estoque nunca ficou sabendo", nem o contrário.

Um _relay_ (`messaging/relay.go`) roda dentro do serviço, lê o que está na `outbox_messages` e
publica no broker. Se o broker estiver fora, a mensagem continua na tabela e sai quando ele voltar
— e o relay avisa no log quando a fila para de escoar, em vez de reclamar uma vez por mensagem.

**É isso que o vídeo demonstra** ao derrubar o estoque no meio de uma impressão: a nota fica em
`PRINTING`, o pedido espera na outbox, e quando o serviço volta ela fecha sozinha.

### Entrega ao menos uma vez, efeito exatamente uma vez

Um broker que garante entrega acaba entregando de novo em caso de dúvida. Quem consome grava em
`processed_messages`, **na mesma transação do trabalho**, que já tratou aquela mensagem
(`messaging/inbox.go`). A redundância é reconhecida e descartada: entrega ao menos uma vez vira
efeito exatamente uma vez, que é o que importa para um débito de saldo.

### Por que RabbitMQ e não Kafka

O trabalho aqui é **distribuir tarefas**, não guardar um histórico de eventos para reprocessar
depois. O que este sistema precisa é confirmação por mensagem, redelivery, e um lugar para onde
mandar o que não pôde ser processado — que é exatamente o forte do RabbitMQ:

| Recurso usado | Onde |
|---|---|
| _Publisher confirms_ | o relay só apaga da outbox depois que o broker confirmou |
| Filas duráveis | sobrevivem ao restart do broker |
| _Dead letter exchange_ (`invoices.dlx`) | mensagem que falhou 3 tentativas com _backoff_ vai para a `.dlq`, em vez de sumir |
| Endpoint de _replay_ | `POST /internal/dead-letters/replay` devolve as mensagens mortas à fila, depois de corrigida a causa |
| `Qos`/_prefetch_ | limita quanto um consumidor puxa de uma vez |

O Kafka é forte no que este sistema **não** faz: retenção longa, releitura por _offset_, vazão
alta particionada. Em troca, custa mais para operar. Aqui ele seria complexidade sem uso.

---

## 9. IA: por que Azure AI Foundry

### O sistema não depende dele

`internal/platform/aiclient` existe para que o resto do código dependa da **interface `Model`**, e
nunca da implementação. Trocar de provedor é escrever outro arquivo nesse pacote; nenhuma regra de
negócio muda. Os dois assistentes (rascunho e busca) compartilham o mesmo _deployment_ pela mesma
interface.

### As vantagens que pesaram

**Fica dentro da assinatura da empresa.** O _deployment_ vive numa subscription Azure, com região,
quota e faturamento próprios — não numa conta pessoal de terceiro com uma chave avulsa. Para um
sistema que lida com nota fiscal, onde o dado sai da rede, sob que contrato e em que região são
perguntas que aparecem cedo.

**A API clássica é HTTP puro.** `POST {endpoint}/openai/deployments/{deployment}/chat/completions`
com o header `api-key`. Sem SDK: são ~200 linhas que controlamos, com _timeout_ de 20s, política de
_retry_ que distingue erro transitório de rejeição, e teto no tamanho da resposta lida. Uma
dependência a menos, e nenhuma surpresa escondida.

**O custo é limitado por configuração, não por esperança.** A quota é definida por _deployment_ em
tokens por minuto, e a versão do modelo é fixada. Somando aos limites do lado da aplicação — texto
de entrada com teto, catálogo no prompt limitado, resposta limitada, e a chamada contando no
_rate limit_ do usuário — o gasto tem um teto conhecido.

**Desligado, o sistema continua inteiro.** Sem as variáveis de ambiente, `GET /invoices/draft`
responde `available:false`, as telas não oferecem os campos, e nada mais degrada. A IA é um
acréscimo, nunca um caminho crítico.

### O que custou tempo, e vale saber

- Subscription nova vem com o provider `Microsoft.CognitiveServices` **não registrado**; sem
  registrar, qualquer criação falha.
- `gpt-4o-mini`, a escolha óbvia por preço, só existe na versão `2024-07-18`, **depreciada em
  31/03/2026** — a Azure recusa novos _deployments_ dela.
- Os modelos de raciocínio (`o1`, `o3`, `gpt-5`) **rejeitam `temperature`** e esperam
  `max_completion_tokens` no lugar de `max_tokens`, então quebram este cliente sem alteração de
  código. `gpt-4o` é o mais barato que aceita os parâmetros enviados e tem quota Standard.

O README traz os comandos `az` para provisionar o mínimo que funciona.

---

## Limitações conhecidas

Registradas por honestidade, com o que cada uma custaria:

- **Testes end-to-end (Playwright) escritos e não executados na suíte.** Os binários instalados na
  máquina são de uma build diferente da que o `playwright-core` do projeto procura; `npx playwright
  install` resolve. Os fluxos foram verificados em navegador real dirigindo o Chrome instalado por
  `executablePath` — inclusive a revisão visual tela a tela, que encontrou quatro defeitos que os
  testes unitários não pegam: um ícone inexistente pedido pelos links de voltar, filtros desligados
  invisíveis no tema escuro, um botão desabilitado que ainda lia como disponível, e o campo de
  busca colapsando em um quadrado no telefone.
- **Estoque baixo é um limiar único** (5), não um ponto de reposição por produto. O catálogo não
  tem esse campo, e acrescentar um que ninguém mantém seria pior do que um número legível por
  todos; passa a valer a pena quando os produtos tiverem giros muito diferentes.
- **O histórico de estoque começa na migração `0008`.** Produtos registrados antes não têm
  movimentos, e a tela diz isso em vez de afirmar que nada se moveu. Reconstruir o passado não é
  possível: os débitos antigos guardam apenas o `invoice_id`, não as quantidades.
- **16 avisos de acessibilidade** no _layout_ herdado do template (menus que abrem por clique sem
  equivalente de teclado). As regras ficaram ligadas como aviso em vez de desligadas, para que a
  dívida continue contada; `ng lint` passa sem erros e roda no CI.
- **Assistente de IA sem credenciais reais.** O código trata endpoint indisponível como caso
  normal (a tela some) e foi validado contra um _stand-in_ que reproduz o contrato da Azure AI
  Foundry.
- **Busca textual não usa os índices trigram** e degrada linearmente com o catálogo. Foi medido,
  documentado em [`performance.md`](performance.md) e **deliberadamente não otimizado**: forçar o
  índice é 3x melhor para termo seletivo e 80x pior para termo amplo.
- **Rate limiting é por instância.** Com várias réplicas, o limite se divide — documentado.
