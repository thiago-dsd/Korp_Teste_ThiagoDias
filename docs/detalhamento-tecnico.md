# Detalhamento técnico

Documento exigido pelo desafio. Responde item a item o que a especificação pede, com o motivo de
cada escolha — não só o que foi usado, mas por que valia a pena.

Autor: Thiago Dias · Repositório: <https://github.com/thiago-dsd/Korp_Teste_ThiagoDias>

---

## 1. Ciclos de vida do Angular utilizados

O Angular 22 permite resolver com _signals_ e injeção boa parte do que antes exigia um ciclo de
vida. Por isso os que aparecem no código são poucos e cada um tem uma razão concreta.

| Ciclo de vida | Onde | Por quê |
|---|---|---|
| `ngOnInit` | telas de produtos, notas, detalhe da nota, formulários | Ponto em que os _streams_ de busca e recarga são montados. Não fica no construtor porque montar assinaturas ali dificulta o teste e roda antes de os `input()` estarem disponíveis. |
| `ngOnDestroy` | `ClickOutsideDirective`, `MenuService` | Os dois guardam recursos que o Angular não recolhe sozinho: um ouvinte de evento no `document` e uma assinatura de longa duração. |
| `ngAfterViewInit` | `ClickOutsideDirective`, `NavbarSubmenuComponent` | Precisam do elemento já renderizado para medir/ouvir. |

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
`tap`, `map`, `of`, `throwError`, `finalize`, `shareReplay`, `takeWhile`, `timer`,
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
componentes próprios (`app-button`, `app-bulk-result`, `app-invoice-status`, `app-product-form`).

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

## Limitações conhecidas

Registradas por honestidade, com o que cada uma custaria:

- **Testes end-to-end (Playwright) escritos e não executados na suíte.** Os binários instalados na
  máquina são de uma build diferente da que o `playwright-core` do projeto procura; `npx playwright
  install` resolve. Os fluxos foram verificados manualmente em navegador real (Chromium),
  incluindo recusa atômica de ajuste e impressão em lote.
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
