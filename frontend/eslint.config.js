// Flat config, which is what ESLint 10 and angular-eslint 22 expect. The old
// .eslintrc.json inherited from the template was never wired to a build target,
// so `npm run lint` had nothing to run.
const eslint = require('@eslint/js');
const tseslint = require('typescript-eslint');
const angular = require('angular-eslint');

module.exports = tseslint.config(
  {
    files: ['**/*.ts'],
    extends: [eslint.configs.recommended, ...tseslint.configs.recommended, ...angular.configs.tsRecommended],
    processor: angular.processInlineTemplates,
    rules: {
      '@angular-eslint/directive-selector': ['error', { type: 'attribute', prefix: 'app', style: 'camelCase' }],
      // Most components are elements, but a couple are attributes on a host
      // element (`div[appNavbarSubmenu]`), so both shapes are accepted.
      '@angular-eslint/component-selector': [
        'error',
        { type: ['element', 'attribute'], prefix: 'app', style: 'kebab-case' },
      ],
      // The application is zoneless: change detection is driven by signals, so
      // OnPush is not the lever it is in a Zone.js application and requiring it
      // everywhere would be noise rather than a finding.
      '@angular-eslint/prefer-on-push-component-change-detection': 'off',
    },
  },
  {
    files: ['**/*.html'],
    extends: [...angular.configs.templateRecommended, ...angular.configs.templateAccessibility],
    rules: {
      // The accessibility findings are real, and all of them are in the layout
      // scaffolding that came with the template — menus that open on click with
      // no keyboard equivalent. Fixing them is worth doing and is its own piece
      // of work; leaving the rules as warnings keeps them counted and visible
      // instead of silently switched off.
      '@angular-eslint/template/click-events-have-key-events': 'warn',
      '@angular-eslint/template/interactive-supports-focus': 'warn',
    },
  },
);
