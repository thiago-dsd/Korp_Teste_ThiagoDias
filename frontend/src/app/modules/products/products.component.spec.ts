import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';

import { environment } from 'src/environments/environment';
import { apiErrorInterceptor } from 'src/app/core/interceptor/api-error.interceptor';
import { ProductsComponent } from './products.component';

function productPayload(id: string, code: string, description: string, balance: number) {
  return { id, code, description, balance, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
}

describe('ProductsComponent', () => {
  let fixture: ComponentFixture<ProductsComponent>;
  let component: ProductsComponent;
  let http: HttpTestingController;
  const baseUrl = `${environment.stockApiUrl}/products`;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [ProductsComponent],
      providers: [
        provideHttpClient(withInterceptors([apiErrorInterceptor])),
        provideHttpClientTesting(),
        // The filters live in the URL, so the listing needs a real router.
        provideRouter([]),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ProductsComponent);
    component = fixture.componentInstance;
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    // Icons are fetched over HTTP by the icon component; they are not part of
    // what these tests assert on.
    http.match((request) => request.url.startsWith('assets/')).forEach((request) => request.flush('<svg></svg>'));
    http.verify();
  });

  it('should list the products returned by the service', async () => {
    http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('P-1');
    expect(text()).toContain('Steel bolt');
    expect(component.items().length).toBe(1);
  });

  it('should say how much of the catalogue is on screen, agreeing with the count', async () => {
    http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)] });
    await fixture.whenStable();
    fixture.detectChanges();

    // Singular for one row: the noun has to agree, not read "1 produtos".
    expect(component.showingLabel()).toBe('Mostrando 1 produto');
    expect(text()).toContain('Mostrando 1 produto');
  });

  it('should count in the plural once there is more than one product', async () => {
    http.expectOne(baseUrl).flush({
      items: [productPayload('p-1', 'P-1', 'Steel bolt', 10), productPayload('p-2', 'P-2', 'Hammer', 4)],
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.showingLabel()).toBe('Mostrando 2 produtos');
  });

  it('should tell the operator when there is nothing registered yet', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('Ainda não há produtos.');
  });

  it('should show a recoverable message when the service is unreachable', async () => {
    http.expectOne(baseUrl).error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown Error' });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('Não foi possível contatar o serviço.');
    expect(text()).toContain('Tentar novamente');
    expect(component.loading()).toBe(false);
  });

  it('should reload the list after registering a product', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.openCreateForm();
    component.onSave({ code: 'P-1', description: 'Steel bolt', balance: 10 });

    const create = http.expectOne((request) => request.method === 'POST');
    expect(create.request.body).toEqual({ code: 'P-1', description: 'Steel bolt', balance: 10 });
    create.flush(productPayload('p-1', 'P-1', 'Steel bolt', 10));
    await fixture.whenStable();

    http
      .expectOne((request) => request.method === 'GET' && request.url === baseUrl)
      .flush({
        items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)],
      });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.formOpen()).toBe(false);
    expect(text()).toContain('Steel bolt');
  });

  it('should keep the form open and show the reason when the service rejects the product', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.openCreateForm();
    component.onSave({ code: 'P-1', description: 'Steel bolt', balance: 10 });

    http
      .expectOne((request) => request.method === 'POST')
      .flush(
        { error: { code: 'duplicated_product_code', message: 'A product with this code already exists.' } },
        { status: 409, statusText: 'Conflict' },
      );
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.formOpen()).toBe(true);
    expect(component.saving()).toBe(false);
    expect(component.saveFailure()?.code).toBe('duplicated_product_code');
    expect(text()).toContain('Já existe um produto com este código.');
  });

  it('should send an update when a product is being edited', async () => {
    http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)] });
    await fixture.whenStable();

    component.openEditForm(component.items()[0]);
    component.onSave({ code: 'P-1', description: 'Stainless bolt', balance: 42 });

    const update = http.expectOne((request) => request.method === 'PUT');
    expect(update.request.url).toBe(`${baseUrl}/p-1`);
    expect(update.request.body).toEqual({ description: 'Stainless bolt', balance: 42 });
    update.flush(productPayload('p-1', 'P-1', 'Stainless bolt', 42));
    await fixture.whenStable();

    http.expectOne((request) => request.method === 'GET' && request.url === baseUrl).flush({ items: [] });
    await fixture.whenStable();
  });

  it('should append the next page when there is more to read', async () => {
    http.expectOne(baseUrl).flush({
      items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)],
      next_cursor: 'cursor-1',
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasMore()).toBe(true);
    expect(text()).toContain('Carregar mais');

    component.loadMore();

    const next = http.expectOne((request) => request.params.get('cursor') === 'cursor-1');
    next.flush({ items: [productPayload('p-2', 'P-2', 'Hammer', 3)] });
    await fixture.whenStable();
    fixture.detectChanges();

    // The page is appended, not replaced.
    expect(component.items().map((product) => product.code)).toEqual(['P-1', 'P-2']);
    expect(component.hasMore()).toBe(false);
    expect(text()).not.toContain('Carregar mais');
  });

  it('should not offer more pages when the first one is the last', async () => {
    http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasMore()).toBe(false);
    component.loadMore();
    http.expectNone((request) => request.params.has('cursor'));
  });

  it('should keep the search term while paging', async () => {
    http.expectOne(baseUrl).flush({ items: [], next_cursor: 'cursor-1' });
    await fixture.whenStable();

    component.searchControl.setValue('bolt');
    component.loadMore();

    const next = http.expectOne(
      (request) => request.params.get('cursor') === 'cursor-1' && request.params.get('search') === 'bolt',
    );
    next.flush({ items: [] });
    await fixture.whenStable();
  });

  it('should ask only for what is out of stock when that filter is on', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.selectStockFilter('out');
    await fixture.whenStable();

    const filtered = http.expectOne((request) => request.params.get('max_balance') === '0');
    filtered.flush({ items: [productPayload('p-1', 'P-1', 'Empty', 0)] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.stockFilter()).toBe('out');
    expect(component.items().length).toBe(1);
  });

  it('should order by balance when asked for what is running out', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.toggleLowestBalanceFirst();
    await fixture.whenStable();

    http.expectOne((request) => request.params.get('sort') === 'balance').flush({ items: [] });
    await fixture.whenStable();
  });

  it('should keep the filters while paging', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.selectStockFilter('out');
    await fixture.whenStable();
    http
      .expectOne((request) => request.params.get('max_balance') === '0')
      .flush({
        items: [productPayload('p-1', 'P-1', 'Empty', 0)],
        next_cursor: 'cursor-1',
      });
    await fixture.whenStable();

    component.loadMore();

    const next = http.expectOne(
      (request) => request.params.get('cursor') === 'cursor-1' && request.params.get('max_balance') === '0',
    );
    next.flush({ items: [] });
    await fixture.whenStable();
  });

  it('should combine the search with the stock filter', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.searchControl.setValue('bolt');
    component.selectStockFilter('out');
    await fixture.whenStable();

    const combined = http.match(
      (request) => request.params.get('search') === 'bolt' && request.params.get('max_balance') === '0',
    );
    expect(combined.length).toBeGreaterThan(0);
    combined.forEach((request) => request.flush({ items: [] }));
    await fixture.whenStable();
  });

  describe('stock adjustments', () => {
    const adjustmentsUrl = `${baseUrl}/adjustments`;

    async function listTwoProducts() {
      http.expectOne(baseUrl).flush({
        items: [productPayload('p-1', 'P-1', 'Steel bolt', 10), productPayload('p-2', 'P-2', 'Hammer', 5)],
      });
      await fixture.whenStable();
      fixture.detectChanges();
    }

    /** Picks both products and writes a movement for each. */
    async function draftMovements(first: string, second: string) {
      component.toggleSelection('p-1');
      component.toggleSelection('p-2');
      component.openAdjustments();
      fixture.detectChanges();

      component.onDeltaInput(component.drafts()[0], first);
      component.onDeltaInput(component.drafts()[1], second);
      fixture.detectChanges();
    }

    it('should send one movement per line that was filled in', async () => {
      await listTwoProducts();
      // The second line is left blank on purpose: picking a product without
      // typing a movement must not send anything for it.
      await draftMovements('100', '');

      expect(component.pendingAdjustments().length).toBe(1);

      component.applyAdjustments();

      const request = http.expectOne(adjustmentsUrl);
      expect(request.request.body).toEqual({ items: [{ product_id: 'p-1', delta: 100, reason: undefined }] });
      request.flush({
        atomic: true,
        summary: { requested: 1, succeeded: 1, failed: 0, skipped: 0 },
        results: [{ index: 0, status: 'succeeded', id: 'p-1', reference: 'P-1' }],
      });
      await fixture.whenStable();

      http.expectOne(baseUrl).flush({ items: [] });
      await fixture.whenStable();
      fixture.detectChanges();

      expect(component.adjustmentOpen()).toBe(false);
      expect(component.selectedCount()).toBe(0);
    });

    it('should ignore a movement of zero', async () => {
      await listTwoProducts();
      await draftMovements('0', '-3');

      expect(component.pendingAdjustments().map((line) => line.delta)).toEqual([-3]);
    });

    it('should keep what was typed when the whole adjustment is refused', async () => {
      await listTwoProducts();
      await draftMovements('100', '-999');

      component.applyAdjustments();

      http.expectOne(adjustmentsUrl).flush(
        {
          atomic: true,
          summary: { requested: 2, succeeded: 0, failed: 1, skipped: 1 },
          results: [
            { index: 0, status: 'skipped', reference: 'P-1' },
            {
              index: 1,
              status: 'failed',
              reference: 'P-2',
              error: {
                code: 'insufficient_balance',
                message: 'Balance is not enough.',
                details: { available: '5', requested: '-999' },
              },
            },
          ],
        },
        { status: 409, statusText: 'Conflict' },
      );
      await fixture.whenStable();
      fixture.detectChanges();

      // Nothing was applied, so the panel stays open with the values, saying
      // which line stopped it.
      expect(component.adjustmentOpen()).toBe(true);
      expect(component.drafts()[0].delta()).toBe('100');
      expect(text()).toContain('Nada foi aplicado.');
      expect(text()).toContain('O saldo do produto não é suficiente.');
      expect(component.selectedCount()).toBe(2);
    });

    it('should name a refused line by its product code, not by its id', async () => {
      await listTwoProducts();
      await draftMovements('', '-999');

      component.applyAdjustments();

      // The movement was sent by id, so that is what the service reports back.
      http.expectOne(adjustmentsUrl).flush(
        {
          atomic: true,
          summary: { requested: 1, succeeded: 0, failed: 1, skipped: 0 },
          results: [
            {
              index: 0,
              status: 'failed',
              reference: 'p-2',
              error: { code: 'insufficient_balance', message: 'Balance is not enough.' },
            },
          ],
        },
        { status: 409, statusText: 'Conflict' },
      );
      await fixture.whenStable();
      fixture.detectChanges();

      expect(text()).toContain('P-2');
      expect(text()).not.toContain('p-2');
    });

    it('should reuse the idempotency key while the movements are unchanged', async () => {
      await listTwoProducts();
      await draftMovements('100', '');

      component.applyAdjustments();
      const first = http.expectOne(adjustmentsUrl);
      const key = first.request.headers.get('Idempotency-Key');
      first.error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown Error' });
      await fixture.whenStable();

      // The answer was lost, not refused: sending the same movements again
      // must not apply the delivery note twice.
      component.applyAdjustments();
      const retry = http.expectOne(adjustmentsUrl);

      expect(retry.request.headers.get('Idempotency-Key')).toBe(key);
      retry.flush({
        atomic: true,
        summary: { requested: 1, succeeded: 1, failed: 0, skipped: 0 },
        results: [{ index: 0, status: 'succeeded', id: 'p-1', reference: 'P-1' }],
      });
      await fixture.whenStable();
      http.expectOne(baseUrl).flush({ items: [] });
      await fixture.whenStable();
    });

    it('should use a new idempotency key once a movement changes', async () => {
      await listTwoProducts();
      await draftMovements('100', '');

      component.applyAdjustments();
      const first = http.expectOne(adjustmentsUrl);
      const key = first.request.headers.get('Idempotency-Key');
      first.flush(
        {
          atomic: true,
          summary: { requested: 1, succeeded: 0, failed: 1, skipped: 0 },
          results: [
            {
              index: 0,
              status: 'failed',
              reference: 'P-1',
              error: { code: 'invalid_adjustment', message: 'Too large.' },
            },
          ],
        },
        { status: 409, statusText: 'Conflict' },
      );
      await fixture.whenStable();
      fixture.detectChanges();

      // The operator corrects the line. Reusing the key here would be refused
      // by the service as the same key with a different payload.
      component.onDeltaInput(component.drafts()[0], '50');
      component.applyAdjustments();

      const corrected = http.expectOne(adjustmentsUrl);
      expect(corrected.request.headers.get('Idempotency-Key')).not.toBe(key);
      corrected.flush({
        atomic: true,
        summary: { requested: 1, succeeded: 1, failed: 0, skipped: 0 },
        results: [{ index: 0, status: 'succeeded', id: 'p-1', reference: 'P-1' }],
      });
      await fixture.whenStable();
      http.expectOne(baseUrl).flush({ items: [] });
      await fixture.whenStable();
    });

    it('should not send an adjustment with no movement at all', async () => {
      await listTwoProducts();
      await draftMovements('', '');

      expect(component.canApply()).toBe(false);
      component.applyAdjustments();

      http.expectNone(adjustmentsUrl);
    });

    it('should keep the selection while reading another page', async () => {
      http.expectOne(baseUrl).flush({
        items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)],
        next_cursor: 'cursor-1',
      });
      await fixture.whenStable();
      fixture.detectChanges();

      component.toggleSelection('p-1');
      component.loadMore();

      http
        .expectOne((request) => request.params.get('cursor') === 'cursor-1')
        .flush({ items: [productPayload('p-2', 'P-2', 'Hammer', 5)] });
      await fixture.whenStable();
      fixture.detectChanges();

      expect([...component.selected()]).toEqual(['p-1']);
    });
  });

  describe('stock history', () => {
    it('should open the history of a product and explain each movement', async () => {
      http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 7)] });
      await fixture.whenStable();
      fixture.detectChanges();

      component.openHistory(component.items()[0]);
      fixture.detectChanges();

      http.expectOne(`${baseUrl}/p-1/movements`).flush({
        items: [
          {
            id: 'm-2',
            delta: -3,
            balance_after: 7,
            source: 'invoice',
            invoice_id: 'i-1',
            created_at: '2026-01-02T00:00:00Z',
          },
          {
            id: 'm-1',
            delta: 10,
            balance_after: 10,
            source: 'registration',
            actor_email: 'admin@example.com',
            created_at: '2026-01-01T00:00:00Z',
          },
        ],
      });
      await fixture.whenStable();
      fixture.detectChanges();

      // The reason a balance changed is the whole point of the panel.
      expect(text()).toContain('Retirado por uma nota impressa');
      expect(text()).toContain('Saldo de abertura');
      expect(text()).toContain('-3');
    });

    it('should say nothing has moved rather than showing an empty box', async () => {
      http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 0)] });
      await fixture.whenStable();
      fixture.detectChanges();

      component.openHistory(component.items()[0]);
      fixture.detectChanges();

      http.expectOne(`${baseUrl}/p-1/movements`).flush({ items: [] });
      await fixture.whenStable();
      fixture.detectChanges();

      expect(text()).toContain('Nenhum movimento registrado para este produto');
    });
  });

  describe('filters in the url', () => {
    it('should ask for what is running out, not only for what already hit zero', async () => {
      http.expectOne(baseUrl).flush({ items: [] });
      await fixture.whenStable();

      component.selectStockFilter('low');
      await fixture.whenStable();

      // Ordering by balance comes with it: the point of the filter is to see
      // the worst first.
      const filtered = http.expectOne(
        (request) => request.params.get('max_balance') === '5' && request.params.get('sort') === 'balance',
      );
      filtered.flush({ items: [] });
      await fixture.whenStable();

      expect(component.stockFilter()).toBe('low');
    });

    it('should read the filters out of the url so a filtered listing survives a reload', async () => {
      // The first request is the one the component made on init; the state
      // comes from the query string, which is what a shared link carries.
      http.expectOne(baseUrl).flush({ items: [] });
      await fixture.whenStable();

      component.selectStockFilter('out');
      await fixture.whenStable();
      http.expectOne((request) => request.params.get('max_balance') === '0').flush({ items: [] });
      await fixture.whenStable();

      const router = TestBed.inject(Router);
      expect(router.url).toContain('stock=out');
    });
  });

  describe('keyboard', () => {
    it('should jump to the search box on /', async () => {
      http.expectOne(baseUrl).flush({ items: [] });
      await fixture.whenStable();
      fixture.detectChanges();

      document.dispatchEvent(new KeyboardEvent('keydown', { key: '/' }));

      const search = (fixture.nativeElement as HTMLElement).querySelector('#product-search');
      expect(document.activeElement).toBe(search);
    });

    it('should leave a slash alone while something is being typed', async () => {
      http.expectOne(baseUrl).flush({ items: [] });
      await fixture.whenStable();
      fixture.detectChanges();

      const typing = document.createElement('input');
      document.body.appendChild(typing);
      typing.focus();

      document.dispatchEvent(new KeyboardEvent('keydown', { key: '/' }));

      expect(document.activeElement).toBe(typing);
      typing.remove();
    });
  });
});
