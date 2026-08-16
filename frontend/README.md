# Invoice System — frontend

Angular application of the invoice system: product registration, invoice creation and printing.
It talks to two services: stock (`:8081`) and billing (`:8082`).

## Requirements

- Node.js 22+ and npm
- The backend services running (see the README at the root of the repository)

## Commands

| Command | What it does |
| --- | --- |
| `npm start` | Starts the dev server on <http://localhost:4200> |
| `npm run build` | Builds the production bundle into `dist/invoice-system` |
| `npm test` | Runs the unit tests once |
| `npm run test:watch` | Runs the unit tests in watch mode |
| `npm run test:e2e` | Runs the Playwright end-to-end tests with the UI |
| `npm run prettier` | Formats the source |
| `npm run prettier:verify` | Fails when something is not formatted |

## Stack

Angular 22 with standalone components, signals and zoneless change detection, Tailwind CSS 4 for
styling, `angular-svg-icon` for inline icons, `ngx-sonner` for toasts, and `apexcharts` for charts.
Unit tests run on the Angular unit-test builder (Vitest + jsdom); end-to-end tests run on
Playwright.

## Layout

```
src/app
  core/        services, models and constants shared by the whole app
  shared/      reusable components, directives, pipes and utilities
  modules/
    layout/    application shell: sidebar, navbar and footer
    auth/      authentication screens
    error/     404 and 500 pages
    uikit/     reusable table building blocks
```

Environment settings live in `src/environments`.
