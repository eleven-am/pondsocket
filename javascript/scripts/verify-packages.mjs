import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const packages = [
    {
        name: '@eleven-am/pondsocket-common',
        entries: ['dist/index.js', 'dist/index.d.ts'],
    },
    {
        name: '@eleven-am/pondsocket',
        entries: ['dist/index.js', 'dist/index.d.ts'],
    },
    {
        name: '@eleven-am/pondsocket-client',
        entries: [
            'dist/index.d.ts',
            'dist/browser-entry.js',
            'dist/browser-entry.d.ts',
            'dist/node-entry.js',
            'dist/node-entry.d.ts',
        ],
    },
    {
        name: '@eleven-am/pondsocket-express',
        entries: ['dist/index.js', 'dist/index.d.ts'],
    },
    {
        name: '@eleven-am/pondsocket-nest',
        entries: ['dist/index.js', 'dist/index.d.ts'],
    },
];

const workingDirectory = fileURLToPath(new URL('..', import.meta.url));
const packDirectory = mkdtempSync(join(tmpdir(), 'pondsocket-pack-'));

const run = (command, args, options = {}) => execFileSync(command, args, {
    cwd: workingDirectory,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'inherit'],
    ...options,
});

try {
    const tarballs = [];

    for (const packageDefinition of packages) {
        const output = run('npm', [
            'pack',
            '--workspace',
            packageDefinition.name,
            '--pack-destination',
            packDirectory,
            '--json',
        ]);
        const [result] = JSON.parse(output);
        const paths = new Set(result.files.map((file) => file.path));
        const unexpected = [...paths].filter((path) =>
            path !== 'package.json' &&
            path !== 'README.md' &&
            path !== 'LICENSE' &&
            !path.startsWith('dist/'),
        );

        if (unexpected.length > 0) {
            throw new Error(`${packageDefinition.name} includes unexpected files: ${unexpected.join(', ')}`);
        }

        for (const entry of packageDefinition.entries) {
            if (!paths.has(entry)) {
                throw new Error(`${packageDefinition.name} is missing ${entry}`);
            }
        }

        tarballs.push(join(packDirectory, result.filename));
        process.stdout.write(`verified ${packageDefinition.name}: ${result.entryCount} files\n`);
    }

    writeFileSync(join(packDirectory, 'package.json'), JSON.stringify({
        name: 'pondsocket-package-smoke-test',
        private: true,
        version: '1.0.0',
    }));

    run('npm', [
        'install',
        '--ignore-scripts',
        '--no-audit',
        '--no-package-lock',
        '--fund=false',
        ...tarballs,
    ], {
        cwd: packDirectory,
        stdio: 'inherit',
    });

    const smokeTest = [
        "require('@eleven-am/pondsocket-common')",
        "require('@eleven-am/pondsocket')",
        "require('@eleven-am/pondsocket/types')",
        "require('@eleven-am/pondsocket-client')",
        "require('@eleven-am/pondsocket-client/browser')",
        "require('@eleven-am/pondsocket-client/node')",
        "require('@eleven-am/pondsocket-express')",
        "require('@eleven-am/pondsocket-nest')",
    ].join(';');

    execFileSync(process.execPath, ['--eval', smokeTest], {
        cwd: packDirectory,
        stdio: 'inherit',
    });
    process.stdout.write('verified package installation and public runtime exports\n');
} finally {
    rmSync(packDirectory, { force: true, recursive: true });
}
