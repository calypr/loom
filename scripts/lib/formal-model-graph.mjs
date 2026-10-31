import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { tools, digest, root } from './formal-tools.mjs';
/** Dump the same checked bounded model; parse it with pstack's own IR frontend. */
export async function modelGraph(modelDirectory, output) {
    const { scripts, jar } = tools();
    const directory = mkdtempSync(join(tmpdir(), 'loom-model-graph-'));
    try {
        const result = spawnSync('java', ['-XX:+UseParallelGC', '-cp', jar, 'tlc2.TLC', '-workers', '1',
            '-metadir', join(directory, 'states'), '-config', 'ApplySettlementRace.cfg',
            '-dump', 'dot,actionlabels', join(directory, 'graph'), 'ApplySettlementRace.tla'], { cwd: modelDirectory, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(result.error);
        assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
        const { parseDot } = await import(pathToFileURL(join(scripts, 'frontends/dot.ts')).href);
        const { json } = await import(pathToFileURL(join(scripts, 'frontends/view.ts')).href);
        const graph = parseDot(readFileSync(join(directory, 'graph.dot'), 'utf8'));
        const model = join(modelDirectory, 'ApplySettlementRace.tla');
        const production = join(root, 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/constructionApplyOwnership.ts');
        const serialized = { schemaVersion: 1, modelSha256: digest(model), configSha256: digest(join(modelDirectory, 'ApplySettlementRace.cfg')), productionSha256: digest(production),
            states: [...graph.states].map(([id, state]) => ({ id, start: state.start,
                vars: Object.fromEntries(Object.entries(state.vars).map(([key, value]) => [key, json(value)])) })),
            steps: graph.steps };
        assert.ok(serialized.states.length > 1 && serialized.steps.length > 0, 'Model graph is empty');
        writeFileSync(output, JSON.stringify(serialized));
        return serialized;
    }
    finally {
        rmSync(directory, { recursive: true, force: true });
    }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
    await modelGraph(process.argv[2] || join(root, 'verification/builder-interactions'), process.argv[3]);
}
