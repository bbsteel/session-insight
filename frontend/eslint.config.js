import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import tseslint from 'typescript-eslint'

/**
 * ESLint flat config for the React + TypeScript frontend.
 * Complements tsc --noEmit with style/bug rules that typecheck alone misses.
 */
export default tseslint.config(
  {
    ignores: [
      'dist/**',
      'node_modules/**',
      'scripts/**',
      'public/**',
      '*.config.js',
      '*.config.ts',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2020,
      sourceType: 'module',
      globals: {
        ...globals.browser,
      },
      parserOptions: {
        ecmaFeatures: { jsx: true },
      },
    },
    plugins: {
      'react-hooks': reactHooks,
    },
    rules: {
      // Classic hooks rules only. The newer React Compiler rules in
      // recommended (set-state-in-effect, etc.) are too noisy for this codebase
      // and would require a separate migration.
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      // The codebase uses `any` in several API/event boundary maps; enforce
      // elsewhere via review rather than blocking CI on historical surfaces.
      '@typescript-eslint/no-explicit-any': 'off',
      // Allow intentional unused bindings with a leading underscore.
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      // Prefer const when variables are never reassigned.
      'prefer-const': 'error',
      // Empty catch blocks are rare and usually intentional; keep as error.
      'no-empty': ['error', { allowEmptyCatch: true }],
    },
  },
)
