import { HttpClient, HttpHeaders, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, map } from 'rxjs';

import { environment } from 'src/environments/environment';
import { Page } from '../models/page.model';
import { NewProduct, Product, ProductUpdate } from '../models/product.model';

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
  created_at: string;
  updated_at: string;
}

interface ProductListPayload {
  items: ProductPayload[];
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
}

function toProduct(payload: ProductPayload): Product {
  return {
    id: payload.id,
    code: payload.code,
    description: payload.description,
    balance: payload.balance,
    createdAt: payload.created_at,
    updatedAt: payload.updated_at,
  };
}

/** Builds the header that makes a write safe to retry. */
export function idempotencyHeaders(): HttpHeaders {
  return new HttpHeaders({ 'Idempotency-Key': crypto.randomUUID() });
}
