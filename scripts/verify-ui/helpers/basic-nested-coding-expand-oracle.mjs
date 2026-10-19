import assert from 'node:assert/strict';

const hasOwn = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
const requiredObservationCode = Object.freeze({ text: 'Basic fixture Observation' });

export const classifyNestedCodingFixture = resource => {
  assert.equal(resource?.resourceType, 'Observation', 'Every fixture row must be an Observation.');
  assert.equal(typeof resource.id, 'string');
  assert(resource.id.trim(), 'Observation ID must be nonempty.');
  assert.deepEqual(resource.code, requiredObservationCode,
    `${resource.id}: the valid top-level Observation code must stay distinct from nested component coding.`);

  if (!hasOwn(resource, 'component')) {
    return { componentShape: 'missing', ownerLabel: null, codingShape: 'not-applicable', codes: [] };
  }
  if (resource.component === null) {
    return { componentShape: 'null', ownerLabel: null, codingShape: 'not-applicable', codes: [] };
  }
  assert(Array.isArray(resource.component), `${resource.id}: component must be an array, null, or missing.`);
  if (resource.component.length === 0) {
    return { componentShape: 'empty', ownerLabel: null, codingShape: 'not-applicable', codes: [] };
  }
  assert.equal(resource.component.length, 1,
    `${resource.id}: one component owner per Observation keeps each nested item-to-owner assertion literal.`);
  const component = resource.component[0];
  assert.equal(typeof component.valueString, 'string', `${resource.id}: component owner label must be populated.`);
  assert(component.valueString.trim(), `${resource.id}: component owner label must be nonempty.`);
  assert(component.code && typeof component.code === 'object' && !Array.isArray(component.code),
    `${resource.id}: component code must be an object.`);

  if (!hasOwn(component.code, 'coding')) {
    return { componentShape: 'one-owner', ownerLabel: component.valueString, codingShape: 'missing', codes: [] };
  }
  if (component.code.coding === null) {
    return { componentShape: 'one-owner', ownerLabel: component.valueString, codingShape: 'null', codes: [] };
  }
  assert(Array.isArray(component.code.coding), `${resource.id}: code.coding must be an array, null, or missing.`);
  if (component.code.coding.length === 0) {
    return { componentShape: 'one-owner', ownerLabel: component.valueString, codingShape: 'empty', codes: [] };
  }

  const codes = component.code.coding.map((coding, ordinal) => {
    assert.equal(typeof coding?.code, 'string', `${resource.id} coding ${ordinal}: code must be a string.`);
    assert(coding.code.trim(), `${resource.id} coding ${ordinal}: code must be nonempty.`);
    return coding.code;
  });
  assert.equal(new Set(codes).size, codes.length, `${resource.id}: coding values must distinguish multiplicity in the visible oracle.`);
  return { componentShape: 'one-owner', ownerLabel: component.valueString, codingShape: 'populated', codes };
};

export const buildBasicNestedCodingExpandOracle = resources => {
  assert(Array.isArray(resources), 'The fixture oracle requires an array of source resources.');
  assert.equal(resources.length, 8, 'The fixture must retain two populated and six explicit absent/null/empty cases.');
  const ids = resources.map(resource => resource?.id);
  assert.equal(new Set(ids).size, resources.length, 'Observation IDs must be unique.');

  const sourceRows = resources.map(resource => ({ id: resource.id, ...classifyNestedCodingFixture(resource) }));
  const expectedRows = sourceRows.flatMap(source => source.codes.length
    ? source.codes.map((code, ordinal) => ({
      id: source.id, ownerLabel: source.ownerLabel, ordinal, code,
      componentShape: source.componentShape, codingShape: source.codingShape,
    }))
    : [{
      id: source.id, ownerLabel: source.ownerLabel, ordinal: null, code: null,
      componentShape: source.componentShape, codingShape: source.codingShape,
    }]);

  const byShape = Object.fromEntries(['populated', 'missing', 'null', 'empty', 'not-applicable']
    .map(shape => [shape, sourceRows.filter(row => row.codingShape === shape).length]));
  assert.deepEqual(byShape, { populated: 2, missing: 1, null: 1, empty: 1, 'not-applicable': 3 },
    'Missing coding, null coding, literal empty coding, and outer component shapes must remain distinct.');
  assert.equal(expectedRows.length, 9,
    'Three coding items plus six PRESERVE_PARENT rows must produce exactly nine rows.');
  assert.deepEqual(expectedRows.filter(row => row.code !== null).map(({ id, ownerLabel, ordinal, code }) =>
    [id, ownerLabel, ordinal, code]), [
    ['basic-nested-001', 'owner-alpha', 0, 'ALPHA-1'],
    ['basic-nested-001', 'owner-alpha', 1, 'ALPHA-2'],
    ['basic-nested-002', 'owner-beta', 0, 'BETA-1'],
  ], 'Each populated nested code must retain its unique Observation and component owner.');

  return { sourceRows, expectedRows, shapeCounts: byShape };
};
