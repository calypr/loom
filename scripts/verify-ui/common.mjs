import { createReport, writeReport } from './report.mjs';
import { parseArgs, createRunContext, makeReportLocation, printList, scenarioFor, validateScenarioCase, usage } from './cli.mjs';
import { resolve } from 'node:path';

export { runPlaywrightCase } from './playwright-case.mjs';

const safeTarget = (target) => {
  let url;
  try { url = new URL(target.uiUrl ?? 'http://127.0.0.1'); } catch { url = new URL('http://127.0.0.1'); }
  return { kind: target.kind ?? 'owned-dev-fixture', uiUrl: url.origin + url.pathname, sourceRoot: target.sourceRoot ?? null, project: target.fixtureProject ?? null, explorer: target.bootstrapExplorerId ?? null };
};

export const browserURL = (target, project, explorer, mode) => {
  const url = new URL(target.uiUrl);
  url.searchParams.set('project', project);
  url.searchParams.set('explorer', explorer);
  url.searchParams.set('mode', mode);
  return url.toString();
};

const writeUnreachable = (scenario, args, error) => {
  const path = args.reportPath ? resolve(args.reportPath) : resolve('.artifacts/verify-ui', scenario.id + '-unreachable-' + Date.now() + '.json');
  const report = createReport({ scenario: scenario.id, target: { kind: args.url ? 'read-only-custom' : 'owned-dev-fixture' }, evidenceDirectory: resolve('.artifacts/verify-ui') });
  report.status = 'unreachable';
  report.errors.push({ kind: 'unreachable', message: error instanceof Error ? error.message : String(error) });
  for (const dimension of Object.values(report.dimensions)) dimension.status = 'unreachable';
  writeReport(path, report);
  console.error('UI_VERIFY scenario=' + scenario.id + ' status=unreachable report=' + path + ' error=' + report.errors[0].message);
  process.exitCode = 1;
};

export const executeScenario = async ({ id, argv, runner, mutating = false }) => {
  const scenario = scenarioFor(id);
  let args;
  try { args = parseArgs(argv); } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 2;
    return [];
  }
  if (args.help) { console.log(usage(id)); return []; }
  if (args.list) { printList(); return []; }
  try { validateScenarioCase(scenario, args.caseName); } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 2;
    return [];
  }
  let context;
  try { context = await createRunContext(args, scenario, { mutating }); } catch (error) {
    writeUnreachable(scenario, args, error);
    return [];
  }
  const cases = args.caseName ? [args.caseName] : scenario.cases;
  const reports = await runner(context, cases);
  if (reports.some((report) => report.status !== 'passed')) process.exitCode = 1;
  return reports;
};
