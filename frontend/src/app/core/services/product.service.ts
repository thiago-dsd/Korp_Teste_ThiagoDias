import { HttpClient, HttpHeaders, HttpParams } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable, map } from 'rxjs';

import { environment } from 'src/environments/environment';
import { NewProduct, Product, ProductUpdate } from '../models/product.model';

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

  /** Lists products, optionally filtered by code or description. */
  list(search = ''): Observable<Product[]> {
    let params = new HttpParams();
    if (search.trim()) {
      params = params.set('search', search.trim());
    }

    return this.http
      .get<ProductListPayload>(this.baseUrl, { params })
      .pipe(map((payload) => payload.items.map(toProduct)));
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
