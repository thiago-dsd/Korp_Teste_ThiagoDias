import { Title } from '@angular/platform-browser';
import { TestBed } from '@angular/core/testing';
import { RouterOutlet } from '@angular/router';
import { AppComponent } from './app.component';

describe('AppComponent', () => {
  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [AppComponent],
    }).compileComponents();
  });

  it('should create the app', () => {
    const fixture = TestBed.createComponent(AppComponent);

    expect(fixture.componentInstance).toBeTruthy();
  });

  it('should set the tab title to the localized app name', () => {
    const fixture = TestBed.createComponent(AppComponent);
    // The service sets the title through an effect, which needs a change
    // detection pass to flush before the assertion below can see it.
    fixture.detectChanges();

    // pt-BR is the default locale, so this is what a first visit sees.
    expect(TestBed.inject(Title).getTitle()).toEqual('Stockly');
  });

  it('should render the routed page outlet', () => {
    const fixture = TestBed.createComponent(AppComponent);
    fixture.detectChanges();

    expect(fixture.debugElement.query((node) => node.providerTokens?.includes(RouterOutlet) ?? false)).toBeTruthy();
  });
});
