import { HttpClient, HttpHeaders, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, catchError, map, of, throwError } from 'rxjs';

import { environment } from 'src/environments/environment';
import { ApiError } from '../models/api-error.model';
import { BulkResponse, isBulkResponse } from '../models/bulk.model';
import { Page } from '../models/page.model';
import { MovementSource, NewProduct, Product, ProductUpdate, StockMovement } from '../models/product.model';

/**
 * One stock movement: a delivery arriving adds, a loss or a correction
 * subtracts. It is a movement rather than a new balance because writing a
 * balance would silently undo whatever happened in the meantime, an invoice
 * printed at that moment included.
 */
export interface StockAdjustment {
  productId: string;
  /** How much to add; may be negative, never zero. */
  delta: number;
  reason?: string;
}

/** Filters the catalogue listing accepts. */
export interface ProductFilters {
  /** Matches code and description. */
  search?: string;
  /** Balance bounds; a maximum of zero lists what is out of stock. */
  minBalance?: number;
  maxBalance?: number;
  sort?: 'code' | 'balance';
  order?: 'asc' | 'desc';
}

/** Product as it travels on the wire. */
interface ProductPayload {
  id: string;
  code: string;
  description: string;
  balance: number;
  version: number;
  created_at: string;
  updated_at: string;
}

interface ProductListPayload {
  items: ProductPayload[];
  next_cursor?: string;
}

interface MovementPayload {
  id: string;
  delta: number;
  balance_after: number;
  source: MovementSource;
  reason?: string;
  invoice_id?: string;
  actor_email?: string;
  created_at: string;
}

interface MovementListPayload {
  items: MovementPayload[];
  next_cursor?: string;
}

/**
 * Reads and writes products on the stock service.
 *
 * Writes carry an Idempotency-Key: if the answer is lost on the way back and
 * the operator retries, the service replays the original response instead of
 * creating a second product.
 */
@Injectable({ providedIn: 'root' })
export class ProductService {
  private readonly http = inject(HttpClient);
  private readonly baseUrl = `${environment.stockApiUrl}/products`;

  /**
   * Reads a page of products for the given filters.
   *
   * Pass the cursor of the previous page to read the next one; an empty cursor
   * in the answer means there is nothing left.
   */
  list(filters: ProductFilters = {}, cursor = ''): Observable<Page<Product>> {
    let params = new HttpParams();
    if (filters.search?.trim()) {
      params = params.set('search', filters.search.trim());
    }
    if (filters.minBalance !== undefined) {
      params = params.set('min_balance', filters.minBalance);
    }
    if (filters.maxBalance !== undefined) {
      params = params.set('max_balance', filters.maxBalance);
    }
    if (filters.sort) {
      params = params.set('sort', filters.sort);
    }
    if (filters.order) {
      params = params.set('order', filters.order);
    }
    if (cursor) {
      params = params.set('cursor', cursor);
    }

    return this.http.get<ProductListPayload>(this.baseUrl, { params }).pipe(
      map((payload) => ({
        items: payload.items.map(toProduct),
        nextCursor: payload.next_cursor ?? '',
      })),
    );
  }

  /** Loads a single product. */
  get(id: string): Observable<Product> {
    return this.http.get<ProductPayload>(`${this.baseUrl}/${id}`).pipe(map(toProduct));
  }

  /** Creates a product. */
  create(product: NewProduct): Observable<Product> {
    return this.http
      .post<ProductPayload>(this.baseUrl, product, { headers: idempotencyHeaders() })
      .pipe(map(toProduct));
  }

  /** Updates the description and the balance of a product. */
  update(id: string, changes: ProductUpdate): Observable<Product> {
    return this.http
      .put<ProductPayload>(`${this.baseUrl}/${id}`, changes, { headers: idempotencyHeaders() })
      .pipe(map(toProduct));
  }

  /**
   * Registers several products in one call, which is how a catalogue is
   * brought in rather than typed one row at a time.
   *
   * The items are independent: a malformed line does not hold the good ones
   * back, and the answer says which line to fix.
   */
  createMany(products: NewProduct[], idempotencyKey: string): Observable<BulkResponse> {
    return this.http.post<BulkResponse>(
      `${this.baseUrl}/bulk`,
      { items: products },
      { headers: new HttpHeaders({ 'Idempotency-Key': idempotencyKey }) },
    );
  }

  /**
   * Reads why the balance of a product is what it is, newest movement first.
   */
  listMovements(productId: string, cursor = ''): Observable<Page<StockMovement>> {
    let params = new HttpParams();
    if (cursor) {
      params = params.set('cursor', cursor);
    }

    return this.http.get<MovementListPayload>(`${this.baseUrl}/${productId}/movements`, { params }).pipe(
      map((payload) => ({
        items: payload.items.map(toMovement),
        nextCursor: payload.next_cursor ?? '',
      })),
    );
  }

  /**
   * Applies several stock movements together.
   *
   * The movements belong to one document, so the service applies all of them
   * or none: a refusal answers 409 with the same body a success does, saying
   * which item stopped it. That difference is transport, not meaning, so it is
   * flattened here and the caller always receives a {@link BulkResponse}.
   *
   * The key must stay the same across a retry of the same movements and change
   * whenever they do; see the callers, which derive it from the payload.
   */
  adjustBalances(adjustments: StockAdjustment[], idempotencyKey: string): Observable<BulkResponse> {
    const body = {
      items: adjustments.map((adjustment) => ({
        product_id: adjustment.productId,
        delta: adjustment.delta,
        reason: adjustment.reason || undefined,
      })),
    };

    return this.http
      .post<BulkResponse>(`${this.baseUrl}/adjustments`, body, {
        headers: new HttpHeaders({ 'Idempotency-Key': idempotencyKey }),
      })
      .pipe(
        catchError((error: unknown) => {
          if (error instanceof ApiError && error.isConflict && isBulkResponse(error.body)) {
            return of(error.body);
          }
          return throwError(() => error);
        }),
      );
  }
}

function toProduct(payload: ProductPayload): Product {
  return {
    id: payload.id,
    code: payload.code,
    description: payload.description,
    balance: payload.balance,
    version: payload.version,
    createdAt: payload.created_at,
    updatedAt: payload.updated_at,
  };
}

function toMovement(payload: MovementPayload): StockMovement {
  return {
    id: payload.id,
    delta: payload.delta,
    balanceAfter: payload.balance_after,
    source: payload.source,
    reason: payload.reason ?? '',
    invoiceId: payload.invoice_id ?? '',
    actorEmail: payload.actor_email ?? '',
    createdAt: payload.created_at,
  };
}

/** Builds the header that makes a write safe to retry. */
export function idempotencyHeaders(): HttpHeaders {
  return new HttpHeaders({ 'Idempotency-Key': crypto.randomUUID() });
}
