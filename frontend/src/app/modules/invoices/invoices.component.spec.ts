import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { apiErrorInterceptor } from 'src/app/core/interceptor/api-error.interceptor';
import { InvoiceStatus } from 'src/app/core/models/invoice.model';
import { environment } from 'src/environments/environment';
import { InvoicesComponent } from './invoices.component';

const invoicesUrl = `${environment.billingApiUrl}/invoices`;

function invoicePayload(id: string, number: number, status: InvoiceStatus, overrides: Record<string, unknown> = {}) {
  return {
    id,
    number,
    status,
    items: [{ id: 'item-1', product_id: 'p-1', product_code: 'P-1', product_description: 'Steel bolt', quantity: 2 }],
    failure: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    printed_at: null,
    ...overrides,
  };
}

describe('InvoicesComponent', () => {
  let fixture: ComponentFixture<InvoicesComponent>;
  let component: InvoicesComponent;
  let http: HttpTestingController;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [InvoicesComponent],
      providers: [
        provideHttpClient(withInterceptors([apiErrorInterceptor])),
        provideHttpClientTesting(),
        provideRouter([]),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(InvoicesComponent);
    component = fixture.componentInstance;
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.match((request) => request.url.startsWith('assets/')).forEach((request) => request.flush('<svg></svg>'));
    http.verify();
  });

  it('should list invoices with their status', async () => {
    http.expectOne(invoicesUrl).flush({
      items: [invoicePayload('i-2', 2, 'CLOSED'), invoicePayload('i-1', 1, 'OPEN')],
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.items().length).toBe(2);
    expect(text()).toContain('#2');
    expect(text()).toContain('Closed');
    expect(text()).toContain('Open');
  });

  it('should show the reason of a failed print attempt in the list', async () => {
    http.expectOne(invoicesUrl).flush({
      items: [
        invoicePayload('i-1', 1, 'OPEN', {
          failure: { code: 'insufficient_balance', message: 'Product balance is not enough.' },
        }),
      ],
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('Product balance is not enough.');
  });

  it('should filter by status', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.selectFilter('CLOSED');

    const filtered = http.expectOne((request) => request.params.get('status') === 'CLOSED');
    filtered.flush({ items: [invoicePayload('i-2', 2, 'CLOSED')] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.activeFilter()).toBe('CLOSED');
    expect(component.items().length).toBe(1);
  });

  it('should offer a refresh while an invoice is being printed', async () => {
    http.expectOne(invoicesUrl).flush({ items: [invoicePayload('i-1', 1, 'PRINTING')] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasPrinting()).toBe(true);
    expect(text()).toContain('Refresh');

    component.refresh();
    http.expectOne((request) => request.url === invoicesUrl).flush({ items: [invoicePayload('i-1', 1, 'CLOSED')] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasPrinting()).toBe(false);
  });

  it('should explain when the billing service is unreachable', async () => {
    http.expectOne(invoicesUrl).error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown Error' });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('could not be reached');
    expect(text()).toContain('Try again');
  });

  it('should tell the operator when no invoice matches the filter', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('No invoices with this filter.');
  });

  it('should append the next page of invoices', async () => {
    http.expectOne(invoicesUrl).flush({
      items: [invoicePayload('i-3', 3, 'OPEN')],
      next_cursor: 'cursor-3',
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasMore()).toBe(true);

    component.loadMore();
    http
      .expectOne((request) => request.params.get('cursor') === 'cursor-3')
      .flush({
        items: [invoicePayload('i-2', 2, 'CLOSED')],
      });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.items().map((invoice) => invoice.number)).toEqual([3, 2]);
    expect(component.hasMore()).toBe(false);
  });

  it('should start over when the filter changes', async () => {
    http.expectOne(invoicesUrl).flush({ items: [invoicePayload('i-3', 3, 'OPEN')], next_cursor: 'cursor-3' });
    await fixture.whenStable();

    component.selectFilter('CLOSED');

    // A new filter reads the first page again, without carrying the cursor.
    const filtered = http.expectOne(
      (request) => request.params.get('status') === 'CLOSED' && !request.params.has('cursor'),
    );
    filtered.flush({ items: [invoicePayload('i-2', 2, 'CLOSED')] });
    await fixture.whenStable();

    expect(component.items().map((invoice) => invoice.number)).toEqual([2]);
    expect(component.hasMore()).toBe(false);
  });

  it('should look up a single invoice by number', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.numberControl.setValue('42');
    component.applyFilters();

    const filtered = http.expectOne((request) => request.params.get('number') === '42');
    filtered.flush({ items: [invoicePayload('i-42', 42, 'CLOSED')] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.items().map((invoice) => invoice.number)).toEqual([42]);
  });

  it('should filter by the period the invoices were issued in', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.fromControl.setValue('2026-08-01');
    component.toControl.setValue('2026-08-31');
    component.applyFilters();

    const filtered = http.expectOne(
      (request) =>
        request.params.get('created_from') === '2026-08-01' && request.params.get('created_to') === '2026-08-31',
    );
    filtered.flush({ items: [] });
    await fixture.whenStable();
  });

  it('should list the invoices that used a product', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.productCodeControl.setValue('BOLT-1');
    component.applyFilters();

    http.expectOne((request) => request.params.get('product_code') === 'BOLT-1').flush({ items: [] });
    await fixture.whenStable();
  });

  it('should show only the invoices that need attention', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.toggleNeedsAttention();

    const filtered = http.expectOne((request) => request.params.get('has_failure') === 'true');
    filtered.flush({
      items: [
        invoicePayload('i-1', 1, 'OPEN', {
          failure: { code: 'insufficient_balance', message: 'Product balance is not enough.' },
        }),
      ],
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.needsAttentionOnly()).toBe(true);
    expect(text()).toContain('Product balance is not enough.');
  });

  it('should combine the status filter with the others', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.selectFilter('OPEN');
    http.expectOne((request) => request.params.get('status') === 'OPEN').flush({ items: [] });
    await fixture.whenStable();

    component.productCodeControl.setValue('BOLT-1');
    component.applyFilters();

    const combined = http.expectOne(
      (request) => request.params.get('status') === 'OPEN' && request.params.get('product_code') === 'BOLT-1',
    );
    combined.flush({ items: [] });
    await fixture.whenStable();
  });

  it('should clear every filter at once', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.numberControl.setValue('42');
    component.toggleNeedsAttention();
    http.expectOne((request) => request.params.has('has_failure')).flush({ items: [] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasActiveFilters()).toBe(true);

    component.clearFilters();

    const cleared = http.expectOne(
      (request) => request.url === invoicesUrl && !request.params.has('has_failure') && !request.params.has('number'),
    );
    cleared.flush({ items: [] });
    await fixture.whenStable();

    expect(component.hasActiveFilters()).toBe(false);
  });

  describe('printing in bulk', () => {
    async function listWith(...invoices: ReturnType<typeof invoicePayload>[]) {
      http.expectOne(invoicesUrl).flush({ items: invoices });
      await fixture.whenStable();
      fixture.detectChanges();
    }

    it('should only let open invoices be picked', async () => {
      await listWith(invoicePayload('i-1', 1, 'OPEN'), invoicePayload('i-2', 2, 'CLOSED'));

      expect(component.canSelect(component.items()[0])).toBe(true);
      expect(component.canSelect(component.items()[1])).toBe(false);
    });

    it('should print every selected invoice in one call', async () => {
      await listWith(invoicePayload('i-1', 1, 'OPEN'), invoicePayload('i-2', 2, 'OPEN'));

      component.toggleSelection('i-1');
      component.toggleSelection('i-2');
      expect(component.selectedCount()).toBe(2);

      component.printSelected();

      const request = http.expectOne(`${invoicesUrl}/print`);
      expect(request.request.body).toEqual({ invoice_ids: ['i-1', 'i-2'] });
      request.flush({
        atomic: false,
        summary: { requested: 2, succeeded: 2, failed: 0, skipped: 0 },
        results: [
          { index: 0, status: 'succeeded', id: 'i-1', reference: '1' },
          { index: 1, status: 'succeeded', id: 'i-2', reference: '2' },
        ],
      });
      await fixture.whenStable();

      // The listing is read again, since the invoices are now printing.
      http.expectOne(invoicesUrl).flush({ items: [invoicePayload('i-1', 1, 'PRINTING')] });
      await fixture.whenStable();
      fixture.detectChanges();

      expect(component.selectedCount()).toBe(0);
      expect(text()).toContain('All 2 invoices went through.');
    });

    it('should keep the refused invoices selected so they can be sent again', async () => {
      await listWith(invoicePayload('i-1', 1, 'OPEN'), invoicePayload('i-2', 2, 'OPEN'));

      component.toggleSelection('i-1');
      component.toggleSelection('i-2');
      component.printSelected();

      http.expectOne(`${invoicesUrl}/print`).flush(
        {
          atomic: false,
          summary: { requested: 2, succeeded: 1, failed: 1, skipped: 0 },
          results: [
            { index: 0, status: 'succeeded', id: 'i-1', reference: '1' },
            {
              index: 1,
              status: 'failed',
              id: 'i-2',
              reference: '2',
              error: { code: 'invoice_not_printable', message: 'Only an open invoice can be printed.' },
            },
          ],
        },
        { status: 207, statusText: 'Multi-Status' },
      );
      await fixture.whenStable();

      http.expectOne(invoicesUrl).flush({ items: [] });
      await fixture.whenStable();
      fixture.detectChanges();

      // What worked leaves the selection; what did not stays, with the reason
      // on screen, so the operator resends only the rest.
      expect([...component.selected()]).toEqual(['i-2']);
      expect(text()).toContain('1 of 2 invoices went through.');
      expect(text()).toContain('Only an open invoice can be printed.');
    });

    it('should keep the selection while reading another page', async () => {
      http.expectOne(invoicesUrl).flush({ items: [invoicePayload('i-1', 1, 'OPEN')], next_cursor: 'cursor-1' });
      await fixture.whenStable();
      fixture.detectChanges();

      component.toggleSelection('i-1');
      component.loadMore();

      http
        .expectOne((request) => request.params.get('cursor') === 'cursor-1')
        .flush({ items: [invoicePayload('i-2', 2, 'OPEN')] });
      await fixture.whenStable();
      fixture.detectChanges();

      // The selection holds ids, so a second page does not disturb it.
      expect([...component.selected()]).toEqual(['i-1']);
    });

    it('should pick every open invoice on screen at once', async () => {
      await listWith(
        invoicePayload('i-1', 1, 'OPEN'),
        invoicePayload('i-2', 2, 'CLOSED'),
        invoicePayload('i-3', 3, 'OPEN'),
      );

      component.selectAllOpen();

      expect([...component.selected()].sort()).toEqual(['i-1', 'i-3']);
    });

    it('should not select more invoices than one call may carry', async () => {
      const many = Array.from({ length: 120 }, (_, index) => invoicePayload(`i-${index}`, index + 1, 'OPEN'));
      await listWith(...many);

      component.selectAllOpen();

      expect(component.selectedCount()).toBe(component.maxSelectable);
      expect(component.selectionFull()).toBe(true);
    });

    it('should name a refused invoice by its number, not by its id', async () => {
      await listWith(invoicePayload('i-1', 7, 'OPEN'));

      component.toggleSelection('i-1');
      component.printSelected();

      // An invoice that never started printing has no number to report, so the
      // service falls back to its id. The screen knows better.
      http.expectOne(`${invoicesUrl}/print`).flush(
        {
          atomic: false,
          summary: { requested: 1, succeeded: 0, failed: 1, skipped: 0 },
          results: [
            {
              index: 0,
              status: 'failed',
              reference: 'i-1',
              error: { code: 'invoice_not_printable', message: 'Only an open invoice can be printed.' },
            },
          ],
        },
        { status: 207, statusText: 'Multi-Status' },
      );
      await fixture.whenStable();

      http.expectOne(invoicesUrl).flush({ items: [] });
      await fixture.whenStable();
      fixture.detectChanges();

      expect(text()).toContain('#7');
      expect(text()).not.toContain('i-1');
    });

    it('should not send anything when nothing is selected', async () => {
      await listWith(invoicePayload('i-1', 1, 'OPEN'));

      component.printSelected();

      http.expectNone(`${invoicesUrl}/print`);
    });
  });

  it('should keep the filters while paging', async () => {
    http.expectOne(invoicesUrl).flush({ items: [] });
    await fixture.whenStable();

    component.productCodeControl.setValue('BOLT-1');
    component.applyFilters();
    http
      .expectOne((request) => request.params.get('product_code') === 'BOLT-1')
      .flush({
        items: [invoicePayload('i-5', 5, 'OPEN')],
        next_cursor: 'cursor-5',
      });
    await fixture.whenStable();

    component.loadMore();

    const next = http.expectOne(
      (request) => request.params.get('cursor') === 'cursor-5' && request.params.get('product_code') === 'BOLT-1',
    );
    next.flush({ items: [] });
    await fixture.whenStable();
  });
});
