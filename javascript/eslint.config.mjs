import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default tseslint.config(
    {
        ignores: [
            '**/coverage/**',
            '**/dist/**',
            '**/node_modules/**',
            '.pack/**',
        ],
    },
    js.configs.recommended,
    ...tseslint.configs.recommended,
    {
        files: ['**/*.{js,mjs}'],
        languageOptions: {
            ecmaVersion: 'latest',
            globals: globals.node,
            sourceType: 'module',
        },
    },
    {
        files: ['**/*.ts'],
        linterOptions: {
            reportUnusedDisableDirectives: 'error',
        },
        languageOptions: {
            ecmaVersion: 'latest',
            globals: {
                ...globals.browser,
                ...globals.jest,
                ...globals.node,
            },
            parserOptions: {
                sourceType: 'module',
            },
        },
        rules: {
            'curly': ['error', 'all'],
            'eqeqeq': ['error', 'always'],
            'no-console': ['error', { allow: ['error', 'warn'] }],
            'require-await': 'error',
            'no-unused-vars': 'off',
            'prefer-const': 'error',
            '@typescript-eslint/no-empty-object-type': 'off',
            '@typescript-eslint/no-explicit-any': 'off',
            '@typescript-eslint/no-unused-vars': [
                'error',
                {
                    argsIgnorePattern: '^_',
                    caughtErrorsIgnorePattern: '^_',
                    varsIgnorePattern: '^_',
                },
            ],
        },
    },
    {
        files: ['type-tests/**/*.ts'],
        rules: {
            '@typescript-eslint/no-unused-expressions': 'off',
        },
    },
    {
        files: ['**/*.test.ts', '**/tests/mocks/**/*.ts'],
        rules: {
            'require-await': 'off',
        },
    },
);
