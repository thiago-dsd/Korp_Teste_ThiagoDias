import { HttpClient, HttpParams } from '@angular/common/http';
import { InjectionToken, Injectable, inject } from '@angular/core';
import { Observable, map, switchMap, takeWhile, timer } from 'rxjs';

import { environment } from 'src/environments/environment';
import { BulkResponse } from '../models/bulk.model';
import {
  DraftLine,
  Invoice,
  InvoiceAuthor,
  InvoiceDraft,
  InvoiceItem,
  InvoiceSearch,
  InvoiceStatus,
  NewInvoiceItem,
} from '../models/invoice.model';
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

/** Filters the invoice listing accepts. */
export interface InvoiceFilters {
  /** Several states at once, which is how "not closed yet" is asked for. */
  statuses?: InvoiceStatus[];
  /** Finds one invoice by its number. */
  number?: number;
  /** Dates as YYYY-MM-DD; the end date covers the whole day. */
  createdFrom?: string;
  createdTo?: string;
  /** Lists the invoices that used a product. */
  productCode?: string;
  /** Separates the invoices whose last print attempt failed. */
  hasFailure?: boolean;
}

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
  issued_by?: { id: string; email?: string } | null;
  printed_by?: { id: string; email?: string } | null;
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

interface SearchPayload {
  filters: Record<string, string>;
  warnings: string[];
  model: string;
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
   * Reads a page of invoices, newest first, for the given filters.
   *
   * Pass the cursor of the previous page to read the next one; an empty cursor
   * in the answer means there is nothing left.
   */
  list(filters: InvoiceFilters = {}, cursor = ''): Observable<Page<Invoice>> {
    let params = new HttpParams();
    if (filters.statuses?.length) {
      params = params.set('status', filters.statuses.join(','));
    }
    if (filters.number !== undefined) {
      params = params.set('number', filters.number);
    }
    if (filters.createdFrom) {
      params = params.set('created_from', filters.createdFrom);
    }
    if (filters.createdTo) {
      params = params.set('created_to', filters.createdTo);
    }
    if (filters.productCode?.trim()) {
      params = params.set('product_code', filters.productCode.trim());
    }
    if (filters.hasFailure !== undefined) {
      params = params.set('has_failure', filters.hasFailure);
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

  /**
   * Asks the assistant to turn a question into listing filters.
   *
   * It answers filters, never invoices: the screen puts them where the filters
   * already live — the URL — and the ordinary listing reads them. That is what
   * keeps the result visible and editable instead of a black box, and it is why
   * the filter names are translated here from the ones the API speaks.
   */
  searchByText(text: string): Observable<InvoiceSearch> {
    return this.http.post<SearchPayload>(`${this.baseUrl}/search`, { text }).pipe(
      map((payload) => {
        const filters = payload.filters ?? {};
        // The listing offers one status at a time, so a question covering
        // several narrows to the first rather than to none.
        const status = (filters['status'] ?? '').split(',')[0] ?? '';

        return {
          filters: {
            status,
            number: filters['number'] ?? '',
            from: filters['created_from'] ?? '',
            to: filters['created_to'] ?? '',
            product: filters['product_code'] ?? '',
            attention: filters['has_failure'] === 'true',
          },
          warnings: payload.warnings ?? [],
        };
      }),
    );
  }

  /** Asks the service to print an invoice. It answers as soon as the request is accepted. */
  requestPrint(id: string): Observable<Invoice> {
    return this.http
      .post<InvoicePayload>(`${this.baseUrl}/${id}/print`, {}, { headers: idempotencyHeaders() })
      .pipe(map(toInvoice));
  }

  /**
   * Starts printing several invoices at once, which is what closing a day of
   * work looks like.
   *
   * The invoices are independent: one that cannot be printed does not hold the
   * others back, so the answer reports each of them separately and the screen
   * shows which ones need attention.
   */
  printMany(ids: string[]): Observable<BulkResponse> {
    return this.http.post<BulkResponse>(
      `${this.baseUrl}/print`,
      { invoice_ids: ids },
      { headers: idempotencyHeaders() },
    );
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
    issuedBy: toAuthor(payload.issued_by),
    printedBy: toAuthor(payload.printed_by),
  };
}

/** An invoice issued before authorship was recorded simply has none. */
function toAuthor(payload: { id: string; email?: string } | null | undefined): InvoiceAuthor | null {
  if (!payload?.id) {
    return null;
  }
  return { id: payload.id, email: payload.email ?? '' };
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
