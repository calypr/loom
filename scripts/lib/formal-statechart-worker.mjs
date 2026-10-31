// Integration with the pinned upstream API, not a copy of its CLI or compiler.
import { copyFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { basename, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { inspect } from 'node:util';
import { tools } from './formal-tools.mjs';
const [spec, config, viewFile, output] = process.argv.slice(2);
const { scripts, jar } = tools();
process.env.TLA2TOOLS = jar;
const upstream = (file) => import(pathToFileURL(join(scripts, file)).href);
const [{ check }, { compile }, { checkView, json }, { readTrace }] = await Promise.all([
    upstream('run.ts'), upstream('pipeline.ts'), upstream('frontends/view.ts'), upstream('frontends/trace.ts'),
]);
mkdirSync(output, { recursive: true });
const copiedSpec = join(output, basename(spec)), copiedConfig = join(output, basename(config)), copiedView = join(output, 'statechart.view.mjs');
copyFileSync(spec, copiedSpec);
copyFileSync(config, copiedConfig);
copyFileSync(viewFile, copiedView);
const view = await import(pathToFileURL(copiedView).href);
const result = await check(copiedSpec, copiedConfig);
const counterexamples = [];
for (const verdict of result.rules)
    if (verdict.outcome === 'broken' && verdict.messages) {
        const trace = readTrace(verdict.messages);
        counterexamples.push({ property: verdict.rule.name,
            states: trace.seq.map(({ action, vars }) => ({ action, vars: vars && Object.fromEntries(Object.entries(vars).map(([key, value]) => [key, json(value)])) })) });
    }
const compiled = compile(result, checkView(view, inspect));
mkdirSync(output, { recursive: true });
for (const [name, chunks] of compiled.files) {
    const parts = [...chunks].map((part) => typeof part === 'string' ? Buffer.from(part) : Buffer.from(part));
    writeFileSync(join(output, name), Buffer.concat(parts));
}
const summary = {
    exit: compiled.exit, states: result.states,
    rules: [...result.rules, ...result.properties].map(({ rule, outcome }) => ({ name: rule.name, outcome })),
    counterexamples, chart: compiled.files.has('chart.mmd'),
};
writeFileSync(join(output, 'result.json'), JSON.stringify(summary));
console.log(compiled.report);
process.exitCode = compiled.exit;
