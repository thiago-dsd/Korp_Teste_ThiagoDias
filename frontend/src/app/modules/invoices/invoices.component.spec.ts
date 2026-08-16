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
});
