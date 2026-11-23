#!/usr/bin/env bun
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

const [, , loomDirectory, evaluatorDirectory] = process.argv;
if (!loomDirectory || !evaluatorDirectory) {
  throw new Error('Usage: bun viewdefinition.mjs LOOM_ROOT SOF_JS_ROOT');
}
const loomRoot = path.resolve(loomDirectory);
const sofRoot = path.resolve(evaluatorDirectory);
const fixturesRoot = path.join(loomRoot, 'tests/conformance/fixtures');
const defaultRecipePath = path.join(loomRoot, 'internal/explorer/recipeimport/testdata/default-dataframer.json');
const { evaluate } = await import(path.join(sofRoot, 'src/index.js'));

function fail(reason) {
  throw new Error(reason);
}

function stable(value) {
  if (Array.isArray(value)) return value.map(stable);
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.keys(value).sort().map(key => [key, stable(value[key])]));
  }
  return value;
}

function fhirPath(expression, context = 'root') {
  if (!expression || typeof expression.select !== 'string') {
    fail('selection is not a pure select expression');
  }
  let value = expression.select.trim();
  if (context === 'root') {
    if (value.startsWith('root.')) value = value.slice('root.'.length);
    else if (value.startsWith('item.')) fail(`item selector used in root context: ${value}`);
  } else if (context === 'item') {
    if (!value.startsWith('item.')) fail(`expansion selector lacks item. prefix: ${value}`);
    value = value.slice('item.'.length);
  } else {
    fail(`unsupported expression context ${context}`);
  }
  if (!value || value.includes('..') || value.startsWith('.') || value.endsWith('.')) {
    fail(`selector is not a simple FHIRPath: ${expression.select}`);
  }
  return { path: value.replaceAll('[]', ''), repeated: value.includes('[]') };
}

function columnForField(field, context = 'root', type = 'string') {
  const selected = fhirPath(field.expr, context);
  let expression = selected.path;
  let collection = false;
  const mode = field.valueMode || 'AUTO';
  if (mode === 'FIRST' || (mode === 'AUTO' && selected.repeated)) {
    if (selected.repeated) expression += '.first()';
  } else if (mode === 'ALL') {
    collection = true;
  } else if (mode === 'DISTINCT') {
    expression += '.distinct()';
    collection = true;
  } else if (mode !== 'AUTO') {
    fail(`unsupported ValueMode ${mode}`);
  }
  return { name: field.name, path: expression, type, ...(collection ? { collection: true } : {}) };
}

function literal(value) {
  switch (value.kind) {
    case 'STRING':
    case 'CODE':
      return `'${String(value.string).replaceAll("'", "''")}'`;
    case 'BOOLEAN':
      return value.boolean ? 'true' : 'false';
    case 'INTEGER':
    case 'DECIMAL':
      return String(value[value.kind.toLowerCase()]);
    case 'DATE':
      return `@${value.date}`;
    case 'DATE_TIME':
      return `@${value.dateTime}`;
    default:
      fail(`filter literal kind ${value.kind} has no probe mapping`);
  }
}

function whereForFilter(filter) {
  const selected = fhirPath({ select: filter.select }, 'root');
  const p = selected.path;
  if (filter.quantifier) fail(`filter quantifier ${filter.quantifier} needs a separate semantic mapping`);
  switch (filter.operator) {
    case 'EXISTS': return `${p}.exists()`;
    case 'MISSING': return `${p}.empty()`;
    case 'EQUALS': return `${p} = ${literal(filter.values[0])}`;
    case 'NOT_EQUALS': return `${p} != ${literal(filter.values[0])}`;
    case 'GT': return `${p} > ${literal(filter.values[0])}`;
    case 'GTE': return `${p} >= ${literal(filter.values[0])}`;
    case 'LT': return `${p} < ${literal(filter.values[0])}`;
    case 'LTE': return `${p} <= ${literal(filter.values[0])}`;
    default: fail(`filter operator ${filter.operator} needs a separate semantic mapping`);
  }
}

export function translateProject(output, typeOfField = () => 'string') {
  if (output.traversals?.length) fail('resource traversal requires joining resources');
  if (output.pivots?.length || output.aggregates?.length || output.slices?.length ||
      output.ownerRecords?.length || output.dynamicColumns?.length || output.extensionColumns?.length ||
      output.catalogProjections?.length || output.derivedColumns?.length || output.construction ||
      output.groupRows || output.identity || output.population) {
    fail('output contains shaping or cross-resource features outside PROJECT');
  }
  if (output.expand) fail('use translateExpandedProject for a nested row expansion');
  if (!output.fields?.length) fail('output has no root fields');
  const where = (output.filters || []).map(whereForFilter);
  return {
    resource: output.rootResourceType,
    status: 'active',
    ...(where.length ? { where: where.map(path => ({ path })) } : {}),
    select: [{ column: output.fields.map(field => columnForField(field, 'root', typeOfField(field))) }],
  };
}

export function translateExpandedProject(output, typeOfField = () => 'string') {
  if (output.traversals?.length || output.pivots?.length || output.aggregates?.length ||
      output.slices?.length || output.ownerRecords?.length || output.dynamicColumns?.length ||
      output.extensionColumns?.length || output.catalogProjections?.length || output.derivedColumns?.length ||
      output.construction || output.groupRows || output.identity || output.population) {
    fail('expanded output contains shaping or cross-resource features outside PROJECT');
  }
  if (!output.expand) fail('output has no Expand');
  const itemFields = [];
  const rootFields = [];
  for (const field of output.fields || []) {
    if (field.expr?.select?.startsWith('item.')) itemFields.push(columnForField(field, 'item', typeOfField(field)));
    else rootFields.push(columnForField(field, 'root', typeOfField(field)));
  }
  if (!itemFields.length) fail('expanded PROJECT has no item fields');
  let forEach;
  try {
    forEach = fhirPath(output.expand.from, 'root');
  } catch {
    fail('Expand source is not a simple root FHIRPath');
  }
  if (forEach.repeated === false) fail('Expand source is not marked as a repeated path');
  if (output.expand.emptyPolicy === 'ERROR') fail('ViewDefinition has no error-on-empty forEach mode');
  const nestedColumn = [...itemFields];
  if (output.expand.ordinality) nestedColumn.push({ name: output.expand.ordinality, path: '%rowIndex', type: 'integer' });
  const child = {
    [output.expand.emptyPolicy === 'PRESERVE_PARENT' ? 'forEachOrNull' : 'forEach']: forEach.path,
    column: nestedColumn,
  };
  return {
    resource: output.rootResourceType,
    status: 'active',
    ...((output.filters || []).length ? { where: output.filters.map(filter => ({ path: whereForFilter(filter) })) } : {}),
    select: [{ ...(rootFields.length ? { column: rootFields } : {}), select: [child] }],
  };
}

function bundleOf(recipe) {
  return recipe.recipe || recipe;
}

function collectCorpusRecipes() {
  const recipes = [];
  for (const filename of fs.readdirSync(fixturesRoot).filter(name => name.endsWith('.json')).sort()) {
    const fixture = JSON.parse(fs.readFileSync(path.join(fixturesRoot, filename), 'utf8'));
    recipes.push({ source: `conformance:${filename}`, bundle: bundleOf(fixture) });
  }
  recipes.push({
    source: 'default-dataframer',
    bundle: JSON.parse(fs.readFileSync(defaultRecipePath, 'utf8')),
  });
  return recipes;
}

function inventory(recipes) {
  const stats = {
    sources: recipes.length,
    outputs: 0,
    rootFields: 0,
    rootFieldsDirectSelect: 0,
    rootFieldsFirstCalls: 0,
    outputTraversals: 0,
    traversalHops: 0,
    outputsWithGraphTraversals: 0,
    expands: 0,
    filters: 0,
    pivots: 0,
    aggregates: 0,
    slices: 0,
    ownerRecords: 0,
    dynamicColumns: 0,
    extensionColumns: 0,
    catalogProjections: 0,
    derivedColumns: 0,
    constructions: 0,
    groupRows: 0,
    populations: 0,
    outputsWithRootProjectionRepresentable: 0,
    outputsWhoseWholeLegacyShapeIsOneViewDefinition: 0,
  };
  const unrepresentable = [];
  const viewDefFeatures = new Set();
  function walkTraversals(traversals) {
    let total = 0;
    for (const traversal of traversals || []) {
      stats.traversalHops++;
      total++;
      total += walkTraversals(traversal.traversals);
    }
    return total;
  }
  for (const { source, bundle } of recipes) {
    for (const output of bundle.outputs || []) {
      stats.outputs++;
      stats.rootFields += (output.fields || []).length;
      for (const field of output.fields || []) {
        if (field.expr?.select) stats.rootFieldsDirectSelect++;
        if (field.expr?.call === 'first') stats.rootFieldsFirstCalls++;
      }
      stats.filters += (output.filters || []).length;
      stats.expands += Number(Boolean(output.expand));
      stats.pivots += (output.pivots || []).length;
      stats.aggregates += (output.aggregates || []).length;
      stats.slices += (output.slices || []).length;
      stats.ownerRecords += (output.ownerRecords || []).length;
      stats.dynamicColumns += (output.dynamicColumns || []).length;
      stats.extensionColumns += (output.extensionColumns || []).length;
      stats.catalogProjections += (output.catalogProjections || []).length;
      stats.derivedColumns += (output.derivedColumns || []).length;
      stats.constructions += Number(Boolean(output.construction));
      stats.groupRows += Number(Boolean(output.groupRows));
      stats.populations += Number(Boolean(output.population));
      const hops = walkTraversals(output.traversals);
      stats.outputTraversals += hops;
      if (hops) stats.outputsWithGraphTraversals++;
      const view = {
        resource: output.rootResourceType,
        status: 'active',
        select: [{ column: [] }],
      };
      let mapError = '';
      try {
        view.select[0].column = (output.fields || []).map(field => columnForField(field));
        view.where = (output.filters || []).map(filter => ({ path: whereForFilter(filter) }));
      } catch (error) {
        mapError = error.message;
      }
      if (!mapError) stats.outputsWithRootProjectionRepresentable++;
      let wholeViewError = '';
      try {
        if (output.expand) translateExpandedProject(output);
        else translateProject(output);
      } catch (error) {
        wholeViewError = error.message;
      }
      const blockers = [];
      if (hops) blockers.push(`${hops} cross-resource Traversal hop(s)`);
      for (const feature of ['pivots','aggregates','slices','ownerRecords','dynamicColumns','extensionColumns','catalogProjections','derivedColumns','construction','groupRows','population']) {
        if (Array.isArray(output[feature]) && output[feature].length) blockers.push(feature);
        if (!Array.isArray(output[feature]) && output[feature]) blockers.push(feature);
      }
      if (mapError) blockers.push(mapError);
      if (wholeViewError && !blockers.length) blockers.push(wholeViewError);
      if (!blockers.length) stats.outputsWhoseWholeLegacyShapeIsOneViewDefinition++;
      else unrepresentable.push({ source, output: output.name, blockers });
      for (const f of output.filters || []) viewDefFeatures.add(`Filter:${f.operator}`);
      for (const field of output.fields || []) {
        if (field.expr?.call) viewDefFeatures.add(`ExpressionCall:${field.expr.call}`);
        if (field.expr?.select?.includes('[]')) viewDefFeatures.add('RepeatedPath');
        if (field.valueMode && field.valueMode !== 'AUTO') viewDefFeatures.add(`ValueMode:${field.valueMode}`);
      }
    }
  }
  return { stats, unrepresentable, observedViewInputs: [...viewDefFeatures].sort() };
}

const corpus = collectCorpusRecipes();
const report = {
  pin: {
    sqlOnFhirRelease: '2.0.0',
    releaseRepository: 'FHIR/sql-on-fhir',
    releaseCommit: '26e7033df379544d6526495a108bd95d3eac0d44',
    releaseZipSha256: '4fee7ad59558b6b2040124bd5e3e578b59233bdb8a3b73cf238af93e778814a9',
    conformanceRepository: 'FHIR/sql-on-fhir.js',
    conformanceCommit: '80854a0aba559d9f0644d95e846e4c83e6b7baac',
  },
  projectCorpus: inventory(corpus),
  executableExamples: [],
};

const temporalFixture = JSON.parse(fs.readFileSync(path.join(fixturesRoot, 'patient-temporal-filter.json'), 'utf8'));
const temporalRecipe = temporalFixture.recipe.outputs[0];
const temporalView = translateProject(temporalRecipe, field => field.name === 'birth_date' ? 'date' : 'string');
const temporalResources = [
  { resourceType: 'Patient', id: 'before', birthDate: '1979-12-31' },
  { resourceType: 'Patient', id: 'at', birthDate: '1980-01-01' },
  { resourceType: 'Patient', id: 'after', birthDate: '1980-01-02' },
];
const temporalRows = evaluate(temporalView, temporalResources);
report.executableExamples.push({
  name: 'patient-temporal-filter',
  view: temporalView,
  rows: temporalRows,
  expectedIds: ['at','after'],
  passed: JSON.stringify(temporalRows.map(r => r.birth_date).sort()) === JSON.stringify(['1980-01-01','1980-01-02']),
});

const expandedRecipe = {
  rootResourceType: 'Patient',
  fields: [
    { name: 'id', expr: { select: 'root.id' } },
    { name: 'family', expr: { select: 'item.family' } },
    { name: 'first_given', expr: { select: 'item.given[]' }, valueMode: 'FIRST' },
    { name: 'given_values', expr: { select: 'item.given[]' }, valueMode: 'ALL' },
  ],
  expand: { from: { select: 'root.name[]' }, as: 'item', ordinality: 'name_position', emptyPolicy: 'EXCLUDE' },
};
const expandedView = translateExpandedProject(expandedRecipe);
const expandedResources = [
  { resourceType: 'Patient', id: 'p1', name: [{ family: 'F1', given: ['A','B'] }, { family:'F2', given:['C'] }] },
  { resourceType: 'Patient', id: 'p2' },
];
const expandedRows = evaluate(expandedView, expandedResources);
report.executableExamples.push({
  name: 'nested-expand',
  view: expandedView,
  rows: expandedRows,
  expected: [
    { id:'p1', family:'F1', first_given:'A', given_values:['A','B'], name_position:0 },
    { id:'p1', family:'F2', first_given:'C', given_values:['C'], name_position:1 },
  ],
  passed: JSON.stringify(stable(expandedRows)) === JSON.stringify(stable([
    { id:'p1', family:'F1', first_given:'A', given_values:['A','B'], name_position:0 },
    { id:'p1', family:'F2', first_given:'C', given_values:['C'], name_position:1 },
  ])),
});

console.log(JSON.stringify(report, null, 2));
if (report.executableExamples.some(example => !example.passed)) process.exitCode = 1;
