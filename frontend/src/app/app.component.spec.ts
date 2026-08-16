import { TestBed } from '@angular/core/testing';
import { RouterOutlet } from '@angular/router';
import { AppComponent } from './app.component';

describe('AppComponent', () => {
  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppComponent],
    }).compileComponents();
  });

  it('should create the app', () => {
    const fixture = TestBed.createComponent(AppComponent);

    expect(fixture.componentInstance).toBeTruthy();
  });

  it("should have as title 'Invoice System'", () => {
    const fixture = TestBed.createComponent(AppComponent);

    expect(fixture.componentInstance.title).toEqual('Invoice System');
  });

  it('should render the routed page outlet', () => {
    const fixture = TestBed.createComponent(AppComponent);
    fixture.detectChanges();

    expect(fixture.debugElement.query((node) => node.providerTokens?.includes(RouterOutlet) ?? false)).toBeTruthy();
  });
});
