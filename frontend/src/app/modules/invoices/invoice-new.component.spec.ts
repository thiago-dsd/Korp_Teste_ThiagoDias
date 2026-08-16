import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { apiErrorInterceptor } from 'src/app/core/interceptor/api-error.interceptor';
import { environment } from 'src/environments/environment';
import { InvoiceNewComponent } from './invoice-new.component';

const productsUrl = `${environment.stockApiUrl}/products`;
const invoicesUrl = `${environment.billingApiUrl}/invoices`;

function productPayload(id: string, code: string, description: string, balance: number) {
  return { id, code, description, balance, created_at: '', updated_at: '' };
}

describe('InvoiceNewComponent', () => {
  let fixture: ComponentFixture<InvoiceNewComponent>;
  let component: InvoiceNewComponent;
  let http: HttpTestingController;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [InvoiceNewComponent],
      providers: [
        provideHttpClient(withInterceptors([apiErrorInterceptor])),
        provideHttpClientTesting(),
        provideRouter([]),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(InvoiceNewComponent);
    component = fixture.componentInstance;
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.match((request) => request.url.startsWith('assets/')).forEach((request) => request.flush('<svg></svg>'));
  });

  function loadCatalogue(): void {
    http.expectOne(productsUrl).flush({
      items: [productPayload('p-1', 'P-1', 'Steel bolt', 10), productPayload('p-2', 'P-2', 'Hammer', 1)],
    });
  }

  function addLine(productId: string, quantity: number): void {
    component.lineForm.setValue({ productId, quantity });
    component.addLine();
    fixture.detectChanges();
  }

  it('should offer the catalogue coming from the stock service', async () => {
    loadCatalogue();
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.catalogue().length).toBe(2);
    expect(text()).toContain('Steel bolt');
  });

  it('should add lines and keep the same product from being added twice', async () => {
    loadCatalogue();
    await fixture.whenStable();

    addLine('p-1', 2);

    expect(component.lines().length).toBe(1);
    expect(component.totalQuantity()).toBe(2);
    expect(component.available().map((product) => product.id)).toEqual(['p-2']);
  });

  it('should refuse a line without a product or with a quantity below one', async () => {
    loadCatalogue();
    await fixture.whenStable();

    addLine('', 1);
    expect(component.lines().length).toBe(0);

    addLine('p-1', 0);
    expect(component.lines().length).toBe(0);
  });

  it('should remove a line', async () => {
    loadCatalogue();
    await fixture.whenStable();
    addLine('p-1', 2);

    component.removeLine('p-1');
    fixture.detectChanges();

    expect(component.lines().length).toBe(0);
    expect(text()).toContain('No products yet');
  });

  it('should warn when a quantity is above the balance without blocking the invoice', async () => {
    loadCatalogue();
    await fixture.whenStable();

    addLine('p-2', 5);

    expect(component.linesOverBalance().length).toBe(1);
    expect(text()).toContain('above the current balance');
  });

  it('should create the invoice and open it', async () => {
    const router = TestBed.inject(Router);
    const navigate = vi.spyOn(router, 'navigate').mockResolvedValue(true);

    loadCatalogue();
    await fixture.whenStable();
    addLine('p-1', 2);

    component.save();

    const request = http.expectOne(invoicesUrl);
    expect(request.request.body).toEqual({ items: [{ product_id: 'p-1', quantity: 2 }] });
    request.flush({
      id: 'i-1',
      number: 3,
      status: 'OPEN',
      items: [],
      failure: null,
      created_at: '',
      updated_at: '',
      printed_at: null,
    });
    await fixture.whenStable();

    expect(navigate).toHaveBeenCalledWith(['/invoices', 'i-1']);
  });

  it('should show why the invoice could not be created', async () => {
    loadCatalogue();
    await fixture.whenStable();
    addLine('p-1', 2);

    component.save();
    http
      .expectOne(invoicesUrl)
      .flush(
        { error: { code: 'stock_unavailable', message: 'The stock service is unavailable.' } },
        { status: 503, statusText: 'Service Unavailable' },
      );
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.saving()).toBe(false);
    expect(text()).toContain('The stock service is unavailable.');
  });

  it('should explain when the catalogue cannot be loaded', async () => {
    http.expectOne(productsUrl).error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown Error' });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('could not be reached');
  });
});
