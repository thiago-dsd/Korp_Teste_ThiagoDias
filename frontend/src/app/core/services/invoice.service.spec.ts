import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { environment } from 'src/environments/environment';
import { apiErrorInterceptor } from '../interceptor/api-error.interceptor';
import { ApiError } from '../models/api-error.model';
import { Invoice, InvoiceStatus } from '../models/invoice.model';
import { InvoiceService } from './invoice.service';

function invoicePayload(status: InvoiceStatus, overrides: Record<string, unknown> = {}) {
  return {
    id: 'i-1',
    number: 1,
    status,
    items: [
      {
        id: 'item-1',
        product_id: 'p-1',
        product_code: 'P-1',
        product_description: 'Steel bolt',
        quantity: 2,
      },
    ],
    failure: null,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    printed_at: null,
    ...overrides,
  };
}

describe('InvoiceService', () => {
  let service: InvoiceService;
  let http: HttpTestingController;
  const baseUrl = `${environment.billingApiUrl}/invoices`;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(withInterceptors([apiErrorInterceptor])), provideHttpClientTesting()],
    });
    service = TestBed.inject(InvoiceService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('should list invoices mapping items and failure', () => {
    let invoices: Invoice[] | undefined;
    service.list().subscribe((result) => (invoices = result));

    http.expectOne(baseUrl).flush({
      items: [invoicePayload('OPEN', { failure: { code: 'insufficient_balance', message: 'Not enough.' } })],
    });

    expect(invoices?.length).toBe(1);
    expect(invoices?.[0].items[0]).toEqual({
      id: 'item-1',
      productId: 'p-1',
      productCode: 'P-1',
      productDescription: 'Steel bolt',
      quantity: 2,
    });
    expect(invoices?.[0].failure?.code).toBe('insufficient_balance');
  });

  it('should filter the listing by status', () => {
    service.list('CLOSED').subscribe();
    expect(http.expectOne((request) => request.params.get('status') === 'CLOSED').request.method).toBe('GET');

    service.list().subscribe();
    http.expectOne((request) => !request.params.has('status'));
  });

  it('should create an invoice translating the lines to the API shape', () => {
    service.create([{ productId: 'p-1', quantity: 2 }]).subscribe();

    const request = http.expectOne(baseUrl);
    expect(request.request.method).toBe('POST');
    expect(request.request.body).toEqual({ items: [{ product_id: 'p-1', quantity: 2 }] });
    expect(request.request.headers.get('Idempotency-Key')).toBeTruthy();
    request.flush(invoicePayload('OPEN'));
  });

  it('should request printing', () => {
    let invoice: Invoice | undefined;
    service.requestPrint('i-1').subscribe((result) => (invoice = result));

    const request = http.expectOne(`${baseUrl}/i-1/print`);
    expect(request.request.method).toBe('POST');
    request.flush(invoicePayload('PRINTING'));

    expect(invoice?.status).toBe('PRINTING');
  });

  // The application runs without zone.js, so the polling timer is driven by
  // the test runner's fake timers instead of fakeAsync.
  it('should keep polling while the invoice is printing and stop on the final state', () => {
    vi.useFakeTimers();
    try {
      const seen: InvoiceStatus[] = [];
      let completed = false;

      service.watchUntilPrinted('i-1', 10).subscribe({
        next: (invoice) => seen.push(invoice.status),
        complete: () => (completed = true),
      });

      vi.advanceTimersByTime(10);
      http.expectOne(`${baseUrl}/i-1`).flush(invoicePayload('PRINTING'));

      vi.advanceTimersByTime(10);
      http.expectOne(`${baseUrl}/i-1`).flush(invoicePayload('CLOSED', { printed_at: '2026-01-01T00:00:05Z' }));

      expect(seen).toEqual(['PRINTING', 'CLOSED']);
      expect(completed).toBe(true);

      // Nothing else is requested once the invoice reached its final state.
      vi.advanceTimersByTime(50);
      http.verify();
    } finally {
      vi.useRealTimers();
    }
  });

  it('should stop polling when the invoice is reopened after a failure', () => {
    vi.useFakeTimers();
    try {
      const seen: InvoiceStatus[] = [];
      service.watchUntilPrinted('i-1', 10).subscribe((invoice) => seen.push(invoice.status));

      vi.advanceTimersByTime(10);
      http.expectOne(`${baseUrl}/i-1`).flush(
        invoicePayload('OPEN', {
          failure: { code: 'insufficient_balance', message: 'Product balance is not enough.' },
        }),
      );

      expect(seen).toEqual(['OPEN']);
      vi.advanceTimersByTime(50);
      http.verify();
    } finally {
      vi.useRealTimers();
    }
  });

  it('should report a conflict when the invoice cannot be printed', () => {
    let failure: unknown;
    service.requestPrint('i-1').subscribe({ error: (error) => (failure = error) });

    http
      .expectOne(`${baseUrl}/i-1/print`)
      .flush(
        { error: { code: 'invoice_not_printable', message: 'Only invoices with status OPEN can be printed.' } },
        { status: 409, statusText: 'Conflict' },
      );

    const error = failure as ApiError;
    expect(error.code).toBe('invoice_not_printable');
    expect(error.isConflict).toBe(true);
  });

  it('should report that the stock service is unavailable', () => {
    let failure: unknown;
    service.create([{ productId: 'p-1', quantity: 1 }]).subscribe({ error: (error) => (failure = error) });

    http
      .expectOne(baseUrl)
      .flush(
        { error: { code: 'stock_unavailable', message: 'The stock service is unavailable.' } },
        { status: 503, statusText: 'Service Unavailable' },
      );

    const error = failure as ApiError;
    expect(error.isUnavailable).toBe(true);
  });
});
