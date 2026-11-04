import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, lstatSync, existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
export const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
export const lock = JSON.parse(readFileSync(join(root, 'verification/builder-interactions/tools.lock.json'), 'utf8'));
export const digest = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');
export function scriptsDigest(directory) {
    const hash = createHash('sha256');
    const paths = [];
    const visit = (prefix) => {
        for (const entry of readdirSync(join(directory, prefix), { withFileTypes: true })) {
            const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
            if (entry.isDirectory())
                visit(relative);
            else {
                assert.ok(entry.isFile(), `Unexpected tool entry: ${relative}`);
                paths.push(relative);
            }
        }
    };
    visit('');
    for (const path of paths.sort())
        hash.update(path).update('\0').update(readFileSync(join(directory, path))).update('\0');
    return hash.digest('hex');
}
export function tools() {
    const cachedScripts = '/tmp/loom-formal-tools/pstack/skills/statechart/scripts';
    const scripts = process.env.PSTACK_STATECHART_ROOT || (existsSync(cachedScripts) ? cachedScripts : '/workspace/.loom-setup/pstack/skills/statechart/scripts');
    const cachedJar = '/tmp/loom-formal-tools/tla2tools.jar';
    const jar = process.env.TLA2TOOLS_JAR || (existsSync(cachedJar) ? cachedJar : '/workspace/.loom-setup/tla2tools.jar');
    assert.equal(scriptsDigest(scripts), lock.pstack.scriptsSha256, 'pstack statechart source differs from tools.lock.json');
    assert.ok(lstatSync(jar).isFile(), 'TLC must be a regular file');
    assert.equal(digest(jar), lock.tlc.sha256, 'TLC digest mismatch');
    return { scripts: resolve(scripts), jar: resolve(jar) };
}
