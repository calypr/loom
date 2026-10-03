// @vitest-environment jsdom
import React from 'react';
import { cleanup, render } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import type { ConstructionRouteStep } from '../../../types';
import { TraversalPath } from './TraversalPath';

afterEach(cleanup);
const edge = (from: string, to: string, relationship: string): ConstructionRouteStep => ({
  edgeId: relationship, fromNodeId: from, toNodeId: to, fromResourceType: from,
  toResourceType: to, relationship, storageDirection: 'OUTBOUND', matchMode: 'OPTIONAL',
});
const subject = edge('Specimen', 'Patient', 'subject');
const medication = edge('Patient', 'Medication', 'medication_reference');

it('highlights an inserted parent without highlighting the shared suffix', () => {
  const { container } = render(<TraversalPath route={[edge('Specimen', 'Specimen', 'parent'), subject, medication]} referenceRoute={[subject, medication]} />);
  const changes = [...container.querySelectorAll('[data-different="true"]')];
  expect(changes).toHaveLength(1);
  expect(changes[0].textContent).toContain('[parent]');
});

it('distinguishes different fields on the same resource chain', () => {
  const { container } = render(<TraversalPath route={[edge('Specimen', 'Patient', 'focus'), medication]} referenceRoute={[subject, medication]} />);
  const changes = [...container.querySelectorAll('[data-different="true"]')];
  expect(changes).toHaveLength(1);
  expect(changes[0].textContent).toContain('[focus]');
});
