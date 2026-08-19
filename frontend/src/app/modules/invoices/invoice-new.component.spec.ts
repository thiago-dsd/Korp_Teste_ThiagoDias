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
    localStorage.clear();
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

  function loadCatalogue(assistantAvailable = false): void {
    http.expectOne(`${invoicesUrl}/draft`).flush({ available: assistantAvailable });
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
    expect(text()).toContain('Ainda não há produtos');
  });

  it('should warn when a quantity is above the balance without blocking the invoice', async () => {
    loadCatalogue();
    await fixture.whenStable();

    addLine('p-2', 5);

    expect(component.linesOverBalance().length).toBe(1);
    expect(text()).toContain('acima do saldo atual');
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
    expect(text()).toContain('O serviço de estoque está indisponível.');
  });

  it('should explain when the catalogue cannot be loaded', async () => {
    http.expectOne(productsUrl).error(new ProgressEvent('error'), { status: 0, statusText: 'Unknown Error' });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('Não foi possível contatar o serviço.');
  });
});

describe('InvoiceNewComponent with the assistant', () => {
  let fixture: ComponentFixture<InvoiceNewComponent>;
  let component: InvoiceNewComponent;
  let http: HttpTestingController;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  function draftPayload(items: { code: string; id: string; quantity: number }[], warnings: string[] = []) {
    return {
      items: items.map((item) => ({
        product_id: item.id,
        product_code: item.code,
        product_description: 'Steel bolt',
        quantity: item.quantity,
        balance: 10,
      })),
      warnings,
      model: 'gpt-test',
    };
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

    http.expectOne(`${invoicesUrl}/draft`).flush({ available: true });
    http.expectOne(productsUrl).flush({
      items: [productPayload('p-1', 'P-1', 'Steel bolt', 10), productPayload('p-2', 'P-2', 'Hammer', 1)],
    });
    await fixture.whenStable();
    fixture.detectChanges();
  });

  afterEach(() => {
    http.match((request) => request.url.startsWith('assets/')).forEach((request) => request.flush('<svg></svg>'));
  });

  it('should offer the assistant when the service has it configured', () => {
    expect(component.assistantAvailable()).toBe(true);
    expect(text()).toContain('Descreva a nota fiscal');
  });

  it('should fill the lines with what the assistant suggests', async () => {
    component.draftControl.setValue('two steel bolts');
    component.askAssistant();

    const request = http.expectOne(`${invoicesUrl}/draft`);
    expect(request.request.body).toEqual({ text: 'two steel bolts' });
    request.flush(draftPayload([{ id: 'p-1', code: 'P-1', quantity: 2 }]));
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.lines().length).toBe(1);
    expect(component.lines()[0].quantity).toBe(2);
    expect(component.draftControl.value).toBe('');
  });

  it('should show what the assistant could not match', async () => {
    component.draftControl.setValue('two bolts and a blue widget');
    component.askAssistant();

    http
      .expectOne(`${invoicesUrl}/draft`)
      .flush(
        draftPayload([{ id: 'p-1', code: 'P-1', quantity: 2 }], ['"a blue widget" was not recognised as a product.']),
      );
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('não foi reconhecido como um produto');
  });

  it('should add to the lines the operator already had', async () => {
    component.lineForm.setValue({ productId: 'p-1', quantity: 1 });
    component.addLine();

    component.draftControl.setValue('two more bolts');
    component.askAssistant();
    http.expectOne(`${invoicesUrl}/draft`).flush(draftPayload([{ id: 'p-1', code: 'P-1', quantity: 2 }]));
    await fixture.whenStable();

    expect(component.lines().length).toBe(1);
    expect(component.lines()[0].quantity).toBe(3);
  });

  it('should keep the screen usable when the assistant fails', async () => {
    component.draftControl.setValue('two bolts');
    component.askAssistant();

    http
      .expectOne(`${invoicesUrl}/draft`)
      .flush(
        { error: { code: 'ai_unavailable', message: 'The assistant is unavailable right now.' } },
        { status: 503, statusText: 'Service Unavailable' },
      );
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.drafting()).toBe(false);
    expect(text()).toContain('O assistente está indisponível no momento.');
    // Adding products by hand still works.
    component.lineForm.setValue({ productId: 'p-1', quantity: 1 });
    component.addLine();
    expect(component.lines().length).toBe(1);
  });

  it('should not call the assistant with an empty sentence', () => {
    component.draftControl.setValue('   ');
    component.askAssistant();

    http.expectNone(`${invoicesUrl}/draft`);
  });
});
