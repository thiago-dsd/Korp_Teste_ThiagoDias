import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { HttpErrorResponse, provideHttpClient, withInterceptors } from '@angular/common/http';
import { TestBed } from '@angular/core/testing';

import { environment } from 'src/environments/environment';
import { apiErrorInterceptor } from '../interceptor/api-error.interceptor';
import { ApiError } from '../models/api-error.model';
import { ProductService } from './product.service';

describe('ProductService', () => {
  let service: ProductService;
  let http: HttpTestingController;
  const baseUrl = `${environment.stockApiUrl}/products`;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(withInterceptors([apiErrorInterceptor])), provideHttpClientTesting()],
    });
    service = TestBed.inject(ProductService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  it('should list products mapping the payload', () => {
    let page: { items: unknown[]; nextCursor: string } | undefined;
    service.list().subscribe((result) => (page = result));

    const request = http.expectOne(baseUrl);
    expect(request.request.method).toBe('GET');
    request.flush({
      items: [
        {
          id: 'p-1',
          code: 'P-1',
          description: 'Steel bolt',
          balance: 10,
          created_at: '2026-01-01T00:00:00Z',
          updated_at: '2026-01-02T00:00:00Z',
        },
      ],
    });

    expect(page?.items).toEqual([
      {
        id: 'p-1',
        code: 'P-1',
        description: 'Steel bolt',
        balance: 10,
        createdAt: '2026-01-01T00:00:00Z',
        updatedAt: '2026-01-02T00:00:00Z',
      },
    ]);
    expect(page?.nextCursor).toBe('');
  });

  it('should send the search term only when it is filled', () => {
    service.list('  bolt  ').subscribe();
    expect(http.expectOne((request) => request.params.get('search') === 'bolt').request.method).toBe('GET');

    service.list('   ').subscribe();
    expect(http.expectOne((request) => !request.params.has('search')).request.method).toBe('GET');
  });

  it('should read the next page with the cursor it received', () => {
    let page: { nextCursor: string } | undefined;
    service.list().subscribe((result) => (page = result));

    http.expectOne((request) => !request.params.has('cursor')).flush({ items: [], next_cursor: 'cursor-1' });
    expect(page?.nextCursor).toBe('cursor-1');

    service.list('', 'cursor-1').subscribe();
    http.expectOne((request) => request.params.get('cursor') === 'cursor-1').flush({ items: [] });
  });

  it('should create a product with an idempotency key', () => {
    service.create({ code: 'P-1', description: 'Steel bolt', balance: 10 }).subscribe();

    const request = http.expectOne(baseUrl);
    expect(request.request.method).toBe('POST');
    expect(request.request.headers.get('Idempotency-Key')).toBeTruthy();
    expect(request.request.body).toEqual({ code: 'P-1', description: 'Steel bolt', balance: 10 });
    request.flush({
      id: 'p-1',
      code: 'P-1',
      description: 'Steel bolt',
      balance: 10,
      created_at: '',
      updated_at: '',
    });
  });

  it('should use a different idempotency key per request', () => {
    service.create({ code: 'P-1', description: 'Steel bolt', balance: 1 }).subscribe();
    service.create({ code: 'P-2', description: 'Hammer', balance: 1 }).subscribe();

    const requests = http.match(baseUrl);
    const keys = requests.map((request) => request.request.headers.get('Idempotency-Key'));

    expect(keys[0]).not.toBe(keys[1]);
    requests.forEach((request) =>
      request.flush({ id: 'p', code: 'P', description: 'd', balance: 1, created_at: '', updated_at: '' }),
    );
  });

  it('should update a product', () => {
    service.update('p-1', { description: 'Stainless bolt', balance: 42 }).subscribe();

    const request = http.expectOne(`${baseUrl}/p-1`);
    expect(request.request.method).toBe('PUT');
    expect(request.request.body).toEqual({ description: 'Stainless bolt', balance: 42 });
    request.flush({
      id: 'p-1',
      code: 'P-1',
      description: 'Stainless bolt',
      balance: 42,
      created_at: '',
      updated_at: '',
    });
  });

  it('should translate a rejected request into an ApiError', () => {
    let failure: unknown;
    service.create({ code: '', description: '', balance: -1 }).subscribe({ error: (error) => (failure = error) });

    http.expectOne(baseUrl).flush(
      {
        error: {
          code: 'invalid_product',
          message: 'Product data is invalid.',
          details: { code: 'must not be empty' },
          request_id: 'req-1',
        },
      },
      { status: 400, statusText: 'Bad Request' },
    );

    expect(failure).toBeInstanceOf(ApiError);
    const error = failure as ApiError;
    expect(error.code).toBe('invalid_product');
    expect(error.message).toBe('Product data is invalid.');
    expect(error.details['code']).toBe('must not be empty');
    expect(error.requestId).toBe('req-1');
  });

  it('should report an unreachable service', () => {
    let failure: unknown;
    service.list().subscribe({ error: (error) => (failure = error) });

    http.expectOne(baseUrl).error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown Error' });

    const error = failure as ApiError;
    expect(error.code).toBe('service_unreachable');
    expect(error.isUnavailable).toBe(true);
  });

  it('should not lose errors that are not HTTP responses', () => {
    const error = new HttpErrorResponse({ status: 500 });
    expect(error).toBeInstanceOf(HttpErrorResponse);
  });
});
