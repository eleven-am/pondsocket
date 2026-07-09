import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const packageDirectories = ['comnon', 'core', 'client', 'express', 'nest'];
const rootDirectory = join(dirname(fileURLToPath(import.meta.url)), '..');
const dryRun = process.argv.includes('--dry-run');

if (!dryRun && process.env.CI !== 'true' && !process.argv.includes('--allow-local')) {
    throw new Error('Package publication is restricted to CI. Pass --allow-local only for an intentional manual release.');
}

const run = (args, stdio = 'inherit') => spawnSync('npm', args, {
    cwd: rootDirectory,
    encoding: 'utf8',
    stdio,
});

for (const directory of packageDirectories) {
    const manifest = JSON.parse(readFileSync(join(rootDirectory, directory, 'package.json'), 'utf8'));
    const packageSpecifier = `${manifest.name}@${manifest.version}`;
    const lookup = run(['view', packageSpecifier, 'version', '--json'], 'pipe');

    if (lookup.status === 0) {
        process.stdout.write(`already published ${packageSpecifier}\n`);
        continue;
    }

    const lookupError = `${lookup.stdout ?? ''}\n${lookup.stderr ?? ''}`;

    if (!lookupError.includes('E404') && !lookupError.includes('is not in this registry')) {
        throw new Error(`Could not check ${packageSpecifier}: ${lookupError.trim()}`);
    }

    if (dryRun) {
        process.stdout.write(`would publish ${packageSpecifier}\n`);
        continue;
    }

    process.stdout.write(`publishing ${packageSpecifier}\n`);
    const publication = run(['publish', '--workspace', manifest.name]);

    if (publication.status !== 0) {
        throw new Error(`Failed to publish ${packageSpecifier}`);
    }
}
