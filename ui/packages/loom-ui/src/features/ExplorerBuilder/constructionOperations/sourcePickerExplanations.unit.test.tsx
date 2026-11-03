// @vitest-environment jsdom
import React from 'react';
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { classifySourcePickerEmptiness, sourcePickerBoundSet, type SourcePickerChoice } from './sourcePickerExplanations';
import { SourcePickerExplanation } from '../components/SourcePickerExplanation';

const choice = (id: string, fieldPath: string, populated: boolean): SourcePickerChoice => ({
  choiceId: id,
  fieldPath,
  label: fieldPath.split('.').pop() ?? fieldPath,
  isPopulated: populated,
});

describe('classifySourcePickerEmptiness', () => {
  it('reports available when any eligible choice is populated', () => {
    const result = classifySourcePickerEmptiness(
      [choice('c1', 'Patient.gender', true), choice('c2', 'Patient.birthDate', false)],
      new Set<string>(),
    );
    expect(result.emptyClass).toBe('available');
  });

  it('distinguishes all eligible inputs already selected from empty data', () => {
    const allBound = classifySourcePickerEmptiness(
      [choice('c1', 'Patient.gender', true)],
      new Set(['c1']),
    );
    expect(allBound.emptyClass).toBe('all-bound');
    expect(allBound.unpopulated).toEqual([]);

    const nonePopulated = classifySourcePickerEmptiness(
      [choice('c1', 'Patient.gender', false), choice('c2', 'Patient.telecom.value', false)],
      new Set<string>(),
    );
    expect(nonePopulated.emptyClass).toBe('none-populated');
    expect(nonePopulated.unpopulated.map((item) => item.choiceId)).toEqual(['c1', 'c2']);
  });

  it('distinguishes the API listing nothing from every other zero state', () => {
    const noneListed = classifySourcePickerEmptiness([], new Set<string>());
    expect(noneListed.emptyClass).toBe('none-listed');
    expect(noneListed.unpopulated).toEqual([]);
  });

  it('routes a bound populated field with an unpopulated remainder to all-bound rather than falsely claiming empty data', () => {
    const result = classifySourcePickerEmptiness(
      [choice('c1', 'Patient.gender', true), choice('c2', 'Patient.deceasedBoolean', false)],
      new Set(['c1']),
    );
    expect(result.emptyClass).toBe('all-bound');
    expect(result.unpopulated).toEqual([]);
    expect(result.boundCount).toBe(1);
  });

  it('keeps the empty-data claim true when every field, bound or not, lacks values', () => {
    const result = classifySourcePickerEmptiness(
      [choice('c1', 'Patient.gender', false), choice('c2', 'Patient.deceasedBoolean', false)],
      new Set(['c1']),
    );
    expect(result.emptyClass).toBe('none-populated');
    expect(result.unpopulated.map((item) => item.choiceId)).toEqual(['c2']);
    expect(result.boundCount).toBe(1);
  });
});

describe('SourcePickerExplanation', () => {
  it('renders nothing when choices are available', () => {
    const { container } = render(
      <SourcePickerExplanation emptiness={classifySourcePickerEmptiness([choice('c1', 'Patient.gender', true)], new Set<string>())} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('explains all-bound selections with a status message and no invented list', () => {
    render(
      <SourcePickerExplanation emptiness={classifySourcePickerEmptiness([choice('c1', 'Patient.gender', true)], new Set(['c1']))} />,
    );
    const status = screen.getByTestId('source-picker-explanation');
    expect(status.getAttribute('role')).toBe('status');
    expect(status.textContent).toBe('Every eligible dataset field that contains values is already used by another source selection here. Remove one to choose a different field.');
    expect(screen.queryByTestId('source-picker-empty-list')).toBeNull();
  });

  it('lists exactly the server-marked unpopulated fields for the empty-data class', () => {
    render(
      <SourcePickerExplanation
        emptiness={classifySourcePickerEmptiness(
          [choice('c1', 'Observation.valueQuantity.value', false), choice('c2', 'Observation.status', false)],
          new Set<string>(),
        )}
      />,
    );
    const list = screen.getByTestId('source-picker-empty-list');
    const items = within(list).getAllByTestId('source-picker-empty-list-item');
    expect(items).toHaveLength(2);
    expect(items[0]?.textContent).toContain('Observation.valueQuantity.value');
    expect(items[0]?.getAttribute('title')).toBe('Observation.valueQuantity.value');
    expect(screen.getByTestId('source-picker-explanation').textContent).toContain('contains values in this dataset yet');
  });

  it('reports that nothing was listed without inventing a reason when the API supplied none', () => {
    render(<SourcePickerExplanation emptiness={classifySourcePickerEmptiness([], new Set<string>())} />);
    const status = screen.getByTestId('source-picker-explanation');
    expect(status.textContent).toBe('Loom has not listed any dataset fields for this step.');
    expect(status.textContent).not.toMatch(/Loom reported/);
  });

});

describe('sourcePickerBoundSet', () => {
  const binding = (choiceId: string) => ({ choiceId });

  it('excludes the lane sole binding only when it stays visible in the picker', () => {
    expect(sourcePickerBoundSet({
      bindings: [binding('choice-a')],
      isOwn: (b) => b.choiceId === 'choice-a',
      ownVisible: true,
    })).toEqual(new Set<string>());
  });

  it('keeps the lane sole binding when the picker no longer renders it', () => {
    expect(sourcePickerBoundSet({
      bindings: [binding('choice-a')],
      isOwn: (b) => b.choiceId === 'choice-a',
      ownVisible: false,
    })).toEqual(new Set<string>(['choice-a']));
  });

  it('keeps every binding once a second source selection exists', () => {
    expect(sourcePickerBoundSet({
      bindings: [binding('choice-a'), binding('choice-b')],
      isOwn: (b) => b.choiceId === 'choice-a',
      ownVisible: true,
    })).toEqual(new Set<string>(['choice-a', 'choice-b']));
  });

});
