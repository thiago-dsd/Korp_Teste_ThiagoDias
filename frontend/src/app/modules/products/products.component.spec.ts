import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';

import { environment } from 'src/environments/environment';
import { apiErrorInterceptor } from 'src/app/core/interceptor/api-error.interceptor';
import { ProductsComponent } from './products.component';

function productPayload(id: string, code: string, description: string, balance: number) {
  return { id, code, description, balance, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' };
}

describe('ProductsComponent', () => {
  let fixture: ComponentFixture<ProductsComponent>;
  let component: ProductsComponent;
  let http: HttpTestingController;
  const baseUrl = `${environment.stockApiUrl}/products`;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ProductsComponent],
      providers: [provideHttpClient(withInterceptors([apiErrorInterceptor])), provideHttpClientTesting()],
    }).compileComponents();

    fixture = TestBed.createComponent(ProductsComponent);
    component = fixture.componentInstance;
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    // Icons are fetched over HTTP by the icon component; they are not part of
    // what these tests assert on.
    http.match((request) => request.url.startsWith('assets/')).forEach((request) => request.flush('<svg></svg>'));
    http.verify();
  });

  it('should list the products returned by the service', async () => {
    http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('P-1');
    expect(text()).toContain('Steel bolt');
    expect(component.items().length).toBe(1);
  });

  it('should tell the operator when there is nothing registered yet', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('No products yet');
  });

  it('should show a recoverable message when the service is unreachable', async () => {
    http.expectOne(baseUrl).error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown Error' });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('could not be reached');
    expect(text()).toContain('Try again');
    expect(component.loading()).toBe(false);
  });

  it('should reload the list after registering a product', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.openCreateForm();
    component.onSave({ code: 'P-1', description: 'Steel bolt', balance: 10 });

    const create = http.expectOne((request) => request.method === 'POST');
    expect(create.request.body).toEqual({ code: 'P-1', description: 'Steel bolt', balance: 10 });
    create.flush(productPayload('p-1', 'P-1', 'Steel bolt', 10));
    await fixture.whenStable();

    http
      .expectOne((request) => request.method === 'GET' && request.url === baseUrl)
      .flush({
        items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)],
      });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.formOpen()).toBe(false);
    expect(text()).toContain('Steel bolt');
  });

  it('should keep the form open and show the reason when the service rejects the product', async () => {
    http.expectOne(baseUrl).flush({ items: [] });
    await fixture.whenStable();

    component.openCreateForm();
    component.onSave({ code: 'P-1', description: 'Steel bolt', balance: 10 });

    http
      .expectOne((request) => request.method === 'POST')
      .flush(
        { error: { code: 'duplicated_product_code', message: 'A product with this code already exists.' } },
        { status: 409, statusText: 'Conflict' },
      );
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.formOpen()).toBe(true);
    expect(component.saving()).toBe(false);
    expect(component.saveFailure()?.code).toBe('duplicated_product_code');
    expect(text()).toContain('already exists');
  });

  it('should send an update when a product is being edited', async () => {
    http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)] });
    await fixture.whenStable();

    component.openEditForm(component.items()[0]);
    component.onSave({ code: 'P-1', description: 'Stainless bolt', balance: 42 });

    const update = http.expectOne((request) => request.method === 'PUT');
    expect(update.request.url).toBe(`${baseUrl}/p-1`);
    expect(update.request.body).toEqual({ description: 'Stainless bolt', balance: 42 });
    update.flush(productPayload('p-1', 'P-1', 'Stainless bolt', 42));
    await fixture.whenStable();

    http.expectOne((request) => request.method === 'GET' && request.url === baseUrl).flush({ items: [] });
    await fixture.whenStable();
  });

  it('should append the next page when there is more to read', async () => {
    http.expectOne(baseUrl).flush({
      items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)],
      next_cursor: 'cursor-1',
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasMore()).toBe(true);
    expect(text()).toContain('Load more');

    component.loadMore();

    const next = http.expectOne((request) => request.params.get('cursor') === 'cursor-1');
    next.flush({ items: [productPayload('p-2', 'P-2', 'Hammer', 3)] });
    await fixture.whenStable();
    fixture.detectChanges();

    // The page is appended, not replaced.
    expect(component.items().map((product) => product.code)).toEqual(['P-1', 'P-2']);
    expect(component.hasMore()).toBe(false);
    expect(text()).not.toContain('Load more');
  });

  it('should not offer more pages when the first one is the last', async () => {
    http.expectOne(baseUrl).flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 10)] });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.hasMore()).toBe(false);
    component.loadMore();
    http.expectNone((request) => request.params.has('cursor'));
  });

  it('should keep the search term while paging', async () => {
    http.expectOne(baseUrl).flush({ items: [], next_cursor: 'cursor-1' });
    await fixture.whenStable();

    component.searchControl.setValue('bolt');
    component.loadMore();

    const next = http.expectOne(
      (request) => request.params.get('cursor') === 'cursor-1' && request.params.get('search') === 'bolt',
    );
    next.flush({ items: [] });
    await fixture.whenStable();
  });
});
