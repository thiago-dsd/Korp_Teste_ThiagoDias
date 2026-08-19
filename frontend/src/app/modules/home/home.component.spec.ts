import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';

import { apiErrorInterceptor } from 'src/app/core/interceptor/api-error.interceptor';
import { environment } from 'src/environments/environment';
import { HomeComponent } from './home.component';

describe('HomeComponent', () => {
  let component: HomeComponent;
  let fixture: ComponentFixture<HomeComponent>;
  let http: HttpTestingController;

  const productsUrl = `${environment.stockApiUrl}/products`;
  const invoicesUrl = `${environment.billingApiUrl}/invoices`;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  /** Answers the four panels the dashboard opens with. */
  function answerPanels(options: {
    failed?: unknown[];
    open?: unknown[];
    low?: unknown[];
    out?: unknown[];
    lowCursor?: string;
  }): void {
    http
      .expectOne((request) => request.url === invoicesUrl && request.params.get('has_failure') === 'true')
      .flush({ items: options.failed ?? [] });
    http
      .expectOne((request) => request.url === invoicesUrl && request.params.get('status') === 'OPEN')
      .flush({ items: options.open ?? [] });
    http
      .expectOne((request) => request.url === productsUrl && request.params.get('max_balance') === '5')
      .flush({ items: options.low ?? [], next_cursor: options.lowCursor ?? '' });
    http
      .expectOne((request) => request.url === productsUrl && request.params.get('max_balance') === '0')
      .flush({ items: options.out ?? [] });
  }

  function productPayload(id: string, code: string, description: string, balance: number) {
    return {
      id,
      code,
      description,
      balance,
      version: 1,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
  }

  function invoicePayload(id: string, number: number, failure: { code: string; message: string } | null) {
    return {
      id,
      number,
      status: 'OPEN',
      items: [],
      failure,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
      printed_at: null,
    };
  }

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [HomeComponent],
      providers: [
        provideHttpClient(withInterceptors([apiErrorInterceptor])),
        provideHttpClientTesting(),
        provideRouter([]),
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(HomeComponent);
    component = fixture.componentInstance;
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.match((request) => request.url.startsWith('assets/')).forEach((request) => request.flush('<svg></svg>'));
    http.verify();
  });

  it('should create', () => {
    answerPanels({});
    expect(component).toBeTruthy();
  });

  it('should say plainly when nothing needs attention', async () => {
    answerPanels({});
    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.allClear()).toBe(true);
    expect(text()).toContain('Nada precisa de atenção.');
  });

  it('should lead with the invoices that failed to print', async () => {
    answerPanels({
      failed: [invoicePayload('i-1', 7, { code: 'insufficient_balance', message: 'Not enough stock.' })],
    });
    await fixture.whenStable();
    fixture.detectChanges();

    expect(text()).toContain('#7');
    expect(text()).toContain('O saldo do produto não é suficiente.');
    expect(component.allClear()).toBe(false);
  });

  // Marking only what already reached zero warns too late: by then an invoice
  // has already been refused.
  it('should separate what is out of stock from what is running out', async () => {
    answerPanels({
      low: [productPayload('p-1', 'P-1', 'Steel bolt', 3), productPayload('p-2', 'P-2', 'Hammer', 0)],
      out: [productPayload('p-2', 'P-2', 'Hammer', 0)],
    });
    await fixture.whenStable();
    fixture.detectChanges();

    // The zero balance belongs to "out of stock" alone; counting it twice
    // would overstate what is worth reordering.
    expect(component.lowStock().map((product) => product.code)).toEqual(['P-1']);
    expect(component.outOfStock().map((product) => product.code)).toEqual(['P-2']);
  });

  // The listings are paged and carry no total, so a full page must not be
  // reported as if it were the whole count.
  it('should mark a count that is only the first page', async () => {
    answerPanels({ low: [productPayload('p-1', 'P-1', 'Steel bolt', 3)], lowCursor: 'cursor-1' });
    await fixture.whenStable();
    fixture.detectChanges();

    const runningOut = component.tiles().find((tile) => tile.labelKey === 'home.tiles.lowLabel');
    expect(runningOut?.value).toBe('1+');
  });

  // One service being down must not leave the operator with a blank page.
  it('should keep showing what it could read when a service is unreachable', async () => {
    http
      .expectOne((request) => request.url === invoicesUrl && request.params.get('has_failure') === 'true')
      .error(new ProgressEvent('error'), { status: 0 });
    http
      .expectOne((request) => request.url === invoicesUrl && request.params.get('status') === 'OPEN')
      .flush({ items: [] });
    http
      .expectOne((request) => request.url === productsUrl && request.params.get('max_balance') === '5')
      .flush({ items: [productPayload('p-1', 'P-1', 'Steel bolt', 3)] });
    http
      .expectOne((request) => request.url === productsUrl && request.params.get('max_balance') === '0')
      .flush({ items: [] });

    await fixture.whenStable();
    fixture.detectChanges();

    expect(component.partial()).toBe(true);
    expect(text()).toContain('Parte desta página não pôde ser carregada.');
    expect(text()).toContain('P-1');
  });
});
