import { HttpClient, HttpParams } from '@angular/common/http';
import { InjectionToken, Injectable, inject } from '@angular/core';
import { Observable, map, switchMap, takeWhile, timer } from 'rxjs';

import { environment } from 'src/environments/environment';
import { DraftLine, Invoice, InvoiceDraft, InvoiceItem, InvoiceStatus, NewInvoiceItem } from '../models/invoice.model';
import { Page } from '../models/page.model';
import { idempotencyHeaders } from './product.service';

/**
 * How often the screen asks the service whether printing has finished.
 * It is a token so tests can shorten it instead of waiting on real seconds.
 */
export const PRINT_POLL_INTERVAL = new InjectionToken<number>('PRINT_POLL_INTERVAL', {
  providedIn: 'root',
  factory: () => 1000,
});

/** Invoice as it travels on the wire. */
interface InvoicePayload {
  id: string;
  number: number;
  status: InvoiceStatus;
  items: InvoiceItemPayload[];
  failure: { code: string; message: string } | null;
  created_at: string;
  updated_at: string;
  printed_at: string | null;
}

interface InvoiceItemPayload {
  id: string;
  product_id: string;
  product_code: string;
  product_description: string;
  quantity: number;
}

interface InvoiceListPayload {
  items: InvoicePayload[];
  next_cursor?: string;
}

interface DraftPayload {
  items: {
    product_id: string;
    product_code: string;
    product_description: string;
    quantity: number;
    balance: number;
  }[];
  warnings: string[];
  model: string;
}

/**
 * Reads and writes invoices on the billing service.
 *
 * Printing is asynchronous: the request is accepted right away and the invoice
 * stays in PRINTING until the stock balances are debited, so the screen follows
 * the outcome with {@link InvoiceService.watchUntilPrinted}.
 */
@Injectable({ providedIn: 'root' })
export class InvoiceService {
  private readonly http = inject(HttpClient);
  private readonly pollInterval = inject(PRINT_POLL_INTERVAL);
  private readonly baseUrl = `${environment.billingApiUrl}/invoices`;

  /**
   * Reads a page of invoices, newest first, optionally filtered by status.
   *
   * Pass the cursor of the previous page to read the next one; an empty cursor
   * in the answer means there is nothing left.
   */
  list(status: InvoiceStatus | '' = '', cursor = ''): Observable<Page<Invoice>> {
    let params = new HttpParams();
    if (status) {
      params = params.set('status', status);
    }
    if (cursor) {
      params = params.set('cursor', cursor);
    }

    return this.http.get<InvoiceListPayload>(this.baseUrl, { params }).pipe(
      map((payload) => ({
        items: payload.items.map(toInvoice),
        nextCursor: payload.next_cursor ?? '',
      })),
    );
  }

  /** Loads a single invoice with its items. */
  get(id: string): Observable<Invoice> {
    return this.http.get<InvoicePayload>(`${this.baseUrl}/${id}`).pipe(map(toInvoice));
  }

  /** Creates an open invoice with the given lines. */
  create(items: NewInvoiceItem[]): Observable<Invoice> {
    const body = {
      items: items.map((item) => ({ product_id: item.productId, quantity: item.quantity })),
    };

    return this.http.post<InvoicePayload>(this.baseUrl, body, { headers: idempotencyHeaders() }).pipe(map(toInvoice));
  }

  /** Reports whether the drafting assistant is configured on this environment. */
  assistantAvailable(): Observable<boolean> {
    return this.http.get<{ available: boolean }>(`${this.baseUrl}/draft`).pipe(map((payload) => payload.available));
  }

  /**
   * Asks the assistant to turn a sentence into invoice lines.
   *
   * What comes back is a suggestion: the service already checked every product
   * against the catalogue, and the invoice is only created when the operator
   * confirms it on screen.
   */
  draft(text: string): Observable<InvoiceDraft> {
    return this.http.post<DraftPayload>(`${this.baseUrl}/draft`, { text }).pipe(
      map((payload) => ({
        lines: payload.items.map(toDraftLine),
        warnings: payload.warnings ?? [],
        model: payload.model,
      })),
    );
  }

  /** Asks the service to print an invoice. It answers as soon as the request is accepted. */
  requestPrint(id: string): Observable<Invoice> {
    return this.http
      .post<InvoicePayload>(`${this.baseUrl}/${id}/print`, {}, { headers: idempotencyHeaders() })
      .pipe(map(toInvoice));
  }

  /**
   * Emits the invoice while it is being printed and completes on the first
   * state that is not PRINTING, so the caller receives the final invoice as
   * the last emission.
   */
  watchUntilPrinted(id: string, intervalMs = this.pollInterval): Observable<Invoice> {
    return timer(intervalMs, intervalMs).pipe(
      switchMap(() => this.get(id)),
      takeWhile((invoice) => invoice.status === 'PRINTING', true),
    );
  }
}

function toInvoice(payload: InvoicePayload): Invoice {
  return {
    id: payload.id,
    number: payload.number,
    status: payload.status,
    items: payload.items.map(toInvoiceItem),
    failure: payload.failure,
    createdAt: payload.created_at,
    updatedAt: payload.updated_at,
    printedAt: payload.printed_at,
  };
}

function toDraftLine(payload: DraftPayload['items'][number]): DraftLine {
  return {
    productId: payload.product_id,
    productCode: payload.product_code,
    productDescription: payload.product_description,
    quantity: payload.quantity,
    balance: payload.balance,
  };
}

function toInvoiceItem(payload: InvoiceItemPayload): InvoiceItem {
  return {
    id: payload.id,
    productId: payload.product_id,
    productCode: payload.product_code,
    productDescription: payload.product_description,
    quantity: payload.quantity,
  };
}
