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
        "const client = require('@eleven-am/pondsocket-client')",
        "const browserClient = require('@eleven-am/pondsocket-client/browser')",
        "const nodeClient = require('@eleven-am/pondsocket-client/node')",
        "if (typeof client.PondClient !== 'function' || typeof client.SSEClient !== 'function') throw new Error('client root exports are incomplete')",
        "if (typeof browserClient.PondClient !== 'function' || typeof browserClient.SSEClient !== 'function') throw new Error('browser client exports are incomplete')",
        "if (typeof nodeClient.PondClient !== 'function' || typeof nodeClient.SSEClient !== 'function') throw new Error('node client exports are incomplete')",
        "require('@eleven-am/pondsocket-express')",
        "require('@eleven-am/pondsocket-nest')",
    ].join(';');

    execFileSync(process.execPath, ['--eval', smokeTest], {
        cwd: packDirectory,
        stdio: 'inherit',
    });
    process.stdout.write('verified package installation and public runtime exports\n');

    const typeSmokeTest = [
        "import type { PondSocketOptions } from '@eleven-am/pondsocket/types';",
        "import type { SSEClient as RootSSEClient } from '@eleven-am/pondsocket-client';",
        "import type { ClientOptions as BrowserClientOptions } from '@eleven-am/pondsocket-client/browser';",
        "import type { ClientOptions as NodeClientOptions, SSEClient as NodeSSEClient } from '@eleven-am/pondsocket-client/node';",
        'declare const serverOptions: PondSocketOptions;',
        'declare const rootSSEClient: RootSSEClient;',
        'declare const browserOptions: BrowserClientOptions;',
        'declare const nodeOptions: NodeClientOptions;',
        'declare const nodeSSEClient: NodeSSEClient;',
        'void serverOptions;',
        'void rootSSEClient;',
        'void browserOptions;',
        'void nodeOptions;',
        'void nodeSSEClient;',
    ].join('\n');
    writeFileSync(join(packDirectory, 'consumer.ts'), typeSmokeTest);

    for (const moduleResolution of ['node', 'node16']) {
        writeFileSync(join(packDirectory, 'tsconfig.json'), JSON.stringify({
            compilerOptions: {
                module: moduleResolution === 'node16' ? 'Node16' : 'commonjs',
                moduleResolution,
                noEmit: true,
                skipLibCheck: false,
                strict: true,
                target: 'ES2022',
            },
            files: ['consumer.ts'],
        }));
        run(process.execPath, [
            join(workingDirectory, 'node_modules', 'typescript', 'bin', 'tsc'),
            '--project',
            'tsconfig.json',
        ], {
            cwd: packDirectory,
            stdio: 'inherit',
        });
        process.stdout.write(`verified packed declarations with ${moduleResolution} module resolution\n`);
    }
} finally {
    rmSync(packDirectory, { force: true, recursive: true });
}
