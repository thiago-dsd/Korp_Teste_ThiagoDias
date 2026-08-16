import { ComponentFixture, TestBed } from '@angular/core/testing';

import { BulkResponse, BulkResult } from 'src/app/core/models/bulk.model';
import { BulkResultComponent } from './bulk-result.component';

function response(atomic: boolean, results: BulkResult[]): BulkResponse {
  return {
    atomic,
    summary: {
      requested: results.length,
      succeeded: results.filter((result) => result.status === 'succeeded').length,
      failed: results.filter((result) => result.status === 'failed').length,
      skipped: results.filter((result) => result.status === 'skipped').length,
    },
    results,
  };
}

describe('BulkResultComponent', () => {
  let fixture: ComponentFixture<BulkResultComponent>;

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent ?? '';
  }

  function render(value: BulkResponse, noun = 'item') {
    fixture = TestBed.createComponent(BulkResultComponent);
    fixture.componentRef.setInput('response', value);
    fixture.componentRef.setInput('noun', noun);
    fixture.detectChanges();
  }

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [BulkResultComponent] }).compileComponents();
  });

  it('should report that everything went through', () => {
    render(
      response(false, [
        { index: 0, status: 'succeeded', reference: '1' },
        { index: 1, status: 'succeeded', reference: '2' },
      ]),
      'invoice',
    );

    expect(text()).toContain('All 2 invoices went through.');
  });

  it('should count what went through when only some items failed', () => {
    render(
      response(false, [
        { index: 0, status: 'succeeded', reference: '1' },
        { index: 1, status: 'failed', reference: '2', error: { code: 'not_printable', message: 'Already closed.' } },
      ]),
      'invoice',
    );

    expect(text()).toContain('1 of 2 invoices went through.');
    expect(text()).toContain('Already closed.');
  });

  it('should say that nothing was applied when an atomic call was refused', () => {
    render(
      response(true, [
        { index: 0, status: 'skipped', reference: 'P-1' },
        {
          index: 1,
          status: 'failed',
          reference: 'P-2',
          error: { code: 'insufficient_balance', message: 'Not enough.' },
        },
      ]),
      'movement',
    );

    // "Nothing was applied" and "one of your items failed" ask for different
    // next steps, so they must not read the same.
    expect(text()).toContain('Nothing was applied.');
    expect(text()).not.toContain('went through');
  });

  it('should only list the items that were refused', () => {
    render(
      response(false, [
        { index: 0, status: 'succeeded', reference: 'KEPT' },
        { index: 1, status: 'failed', reference: 'SHOWN', error: { code: 'invalid', message: 'Bad line.' } },
      ]),
    );

    expect(text()).toContain('SHOWN');
    expect(text()).not.toContain('KEPT');
  });

  it('should show the details that say what to correct', () => {
    render(
      response(true, [
        {
          index: 0,
          status: 'failed',
          reference: 'P-2',
          error: {
            code: 'insufficient_balance',
            message: 'Not enough.',
            details: { available: '5', requested: '-999' },
          },
        },
      ]),
    );

    expect(text()).toContain('available');
    expect(text()).toContain('5');
  });

  it('should fall back to the position when an item has no reference', () => {
    render(response(false, [{ index: 3, status: 'failed', error: { code: 'invalid', message: 'Bad line.' } }]));

    expect(text()).toContain('Item 4');
  });
});
