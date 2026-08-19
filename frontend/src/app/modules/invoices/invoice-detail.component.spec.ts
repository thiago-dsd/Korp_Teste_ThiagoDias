import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap } from '@angular/router';
import { of } from 'rxjs';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { apiErrorInterceptor } from 'src/app/core/interceptor/api-error.interceptor';
import { InvoiceStatus } from 'src/app/core/models/invoice.model';
import { PRINT_POLL_INTERVAL } from 'src/app/core/services/invoice.service';
import { environment } from 'src/environments/environment';
import { InvoiceDetailComponent } from './invoice-detail.component';

const INVOICE_ID = 'i-1';
/** The screen polls quickly here, so the tests wait on real but tiny delays. */
const POLL_INTERVAL_MS = 5;

function invoicePayload(status: InvoiceStatus, overrides: Record<string, unknown> = {}) {
  return {
    id: INVOICE_ID,
    number: 7,
    status,
    items: [
      { id: 'item-1', product_id: 'p-1', product_code: 'P-1', product_description: 'Steel bolt', quantity: 2 },
      { id: 'item-2', product_id: 'p-2', product_code: 'P-2', product_description: 'Hammer', quantity: 3 },
    ],
    failure: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    printed_at: null,
    ...overrides,
  };
}

describe('InvoiceDetailComponent', () => {
  let fixture: ComponentFixture<InvoiceDetailComponent>;
  let component: InvoiceDetailComponent;
  let http: HttpTestingController;
  const invoiceUrl = `${environment.billingApiUrl}/invoices/${INVOICE_ID}`;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  function printButton(): HTMLButtonElement {
    return (fixture.nativeElement as HTMLElement).querySelector('button')!;
  }

  async function settle(): Promise<void> {
    await fixture.whenStable();
    fixture.detectChanges();
  }

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [InvoiceDetailComponent],
      providers: [
        provideHttpClient(withInterceptors([apiErrorInterceptor])),
        provideHttpClientTesting(),
        {
          provide: ActivatedRoute,
          useValue: { paramMap: of(convertToParamMap({ id: INVOICE_ID })) },
        },
        { provide: PRINT_POLL_INTERVAL, useValue: POLL_INTERVAL_MS },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(InvoiceDetailComponent);
    component = fixture.componentInstance;
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.match((request) => request.url.startsWith('assets/')).forEach((request) => request.flush('<svg></svg>'));
  });

  /**
   * Waits for the screen to poll the invoice again and answers with the given
   * payload. Polls that overlapped while waiting are answered too, since the
   * component only keeps the newest one.
   */
  async function answerNextPoll(payload: Record<string, unknown>): Promise<void> {
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS * 3));

    // switchMap cancels polls that were superseded; only the live one matters.
    const polls = http.match(invoiceUrl).filter((poll) => !poll.cancelled);
    expect(polls.length).toBeGreaterThan(0);
    polls.forEach((poll) => poll.flush(payload));

    await settle();
  }

  it('should show the invoice with its items and total', async () => {
    http.expectOne(invoiceUrl).flush(invoicePayload('OPEN'));
    await settle();

    expect(text()).toContain('#7');
    expect(text()).toContain('Steel bolt');
    expect(text()).toContain('Hammer');
    expect(component.totalQuantity()).toBe(5);
  });

  it('should only allow printing an open invoice', async () => {
    http.expectOne(invoiceUrl).flush(invoicePayload('CLOSED', { printed_at: '2026-01-01T01:00:00Z' }));
    await settle();

    expect(component.canPrint()).toBe(false);
    expect(printButton().disabled).toBe(true);
    expect(text()).toContain('saldos de estoque debitados');
  });

  it('should show progress while printing and close the invoice when the stock answers', async () => {
    http.expectOne(invoiceUrl).flush(invoicePayload('OPEN'));
    await settle();

    printButton().click();

    const print = http.expectOne(`${invoiceUrl}/print`);
    expect(print.request.method).toBe('POST');
    print.flush(invoicePayload('PRINTING'));
    await settle();

    expect(component.printing()).toBe(true);
    expect(text()).toContain('aguardando o serviço de estoque');
    expect(printButton().disabled).toBe(true);

    await answerNextPoll(invoicePayload('CLOSED', { printed_at: '2026-01-01T01:00:00Z' }));

    expect(component.printing()).toBe(false);
    expect(component.invoice()?.status).toBe('CLOSED');
    expect(text()).toContain('saldos de estoque debitados');
  });

  it('should explain why printing failed and offer the invoice for another attempt', async () => {
    http.expectOne(invoiceUrl).flush(invoicePayload('OPEN'));
    await settle();

    printButton().click();
    http.expectOne(`${invoiceUrl}/print`).flush(invoicePayload('PRINTING'));
    await settle();

    await answerNextPoll(
      invoicePayload('OPEN', {
        failure: { code: 'insufficient_balance', message: 'Product balance is not enough.' },
      }),
    );

    expect(component.invoice()?.status).toBe('OPEN');
    expect(text()).toContain('A impressão não foi concluída');
    expect(text()).toContain('O saldo do produto não é suficiente.');
    expect(component.canPrint()).toBe(true);
    expect(printButton().disabled).toBe(false);
  });

  it('should report that the service is unavailable without changing the invoice', async () => {
    http.expectOne(invoiceUrl).flush(invoicePayload('OPEN'));
    await settle();

    printButton().click();
    http.expectOne(`${invoiceUrl}/print`).error(new ProgressEvent('error'), {
      status: 0,
      statusText: 'Unknown Error',
    });
    await settle();

    expect(component.invoice()?.status).toBe('OPEN');
    expect(component.printFailure()?.isUnavailable).toBe(true);
    expect(text()).toContain('Nada foi alterado');
  });

  it('should refresh the invoice when printing is refused because it is not open', async () => {
    http.expectOne(invoiceUrl).flush(invoicePayload('OPEN'));
    await settle();

    printButton().click();
    http
      .expectOne(`${invoiceUrl}/print`)
      .flush(
        { error: { code: 'invoice_not_printable', message: 'Only invoices with status OPEN can be printed.' } },
        { status: 409, statusText: 'Conflict' },
      );
    await settle();

    // The screen reloads the invoice to show the state it is really in.
    http.expectOne(invoiceUrl).flush(invoicePayload('CLOSED', { printed_at: '2026-01-01T01:00:00Z' }));
    await settle();

    expect(component.invoice()?.status).toBe('CLOSED');
  });

  it('should follow an invoice that was already printing when the page opened', async () => {
    http.expectOne(invoiceUrl).flush(invoicePayload('PRINTING'));
    await settle();

    expect(component.printing()).toBe(true);

    await answerNextPoll(invoicePayload('CLOSED', { printed_at: '2026-01-01T01:00:00Z' }));

    expect(component.invoice()?.status).toBe('CLOSED');
  });

  it('should show a message when the invoice cannot be loaded', async () => {
    http
      .expectOne(invoiceUrl)
      .flush(
        { error: { code: 'invoice_not_found', message: 'Invoice was not found.' } },
        { status: 404, statusText: 'Not Found' },
      );
    await settle();

    expect(text()).toContain('Nota fiscal não encontrada.');
  });
});
