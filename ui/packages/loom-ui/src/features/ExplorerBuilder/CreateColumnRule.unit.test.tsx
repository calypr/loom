// @vitest-environment jsdom
import React, { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ComponentProps } from 'react';
import { CreateColumnRule } from './CreateColumnRule';
import { columnRuleDefaults, columnRulePresets } from './model/sourceColumnRule';

function reviewButton(
  onClick: () => void,
  onPreview: ComponentProps<typeof CreateColumnRule>['renderReview'],
): ComponentProps<typeof CreateColumnRule>['renderReview'] {
  return (rule, cancel, disabled, label, automatic) => <button disabled={disabled} onClick={() => {
    onPreview(rule, cancel, disabled, label, automatic);
    onClick();
  }}>{label}</button>;
}

describe('creating a column rule', () => {
  it('reviews the configured rule and returns to the same form on cancel', () => {
    const renderReview = vi.fn<ComponentProps<typeof CreateColumnRule>['renderReview']>(
      () => null,
    );
    function Harness() {
      const [reviewing, setReviewing] = useState(false);
      const identifier = columnRuleDefaults.identifier;
      if (!identifier) throw new Error('Identifier rule preset must be available.');
      return <CreateColumnRule kind="DYNAMIC_COLUMN" occurrence={{ id: 'patient-related', resourceType: 'Patient', label: 'Related Patient' }}
        preset={columnRulePresets[0]} initialParameters={identifier} disabled={false} reviewing={reviewing}
        onCancel={() => setReviewing(false)} renderReview={(rule, cancel, disabled, label) => reviewing
          ? <button onClick={cancel}>Cancel review</button>
          : reviewButton(() => setReviewing(true), renderReview)(rule, cancel, disabled, label, true)} />;
    }
    render(<Harness />);

    expect(screen.queryByRole('button', { name: 'Review column rule' })).not.toBeInTheDocument();
    const name = screen.getByRole('textbox', { name: 'Rule name' });
    fireEvent.change(name, { target: { value: 'other_identifiers' } });
    expect(screen.getByRole('button', { name: 'Review column rule' })).toBeEnabled();
    fireEvent.change(name, { target: { value: 'identifiers' } });
    expect(screen.queryByRole('button', { name: 'Review column rule' })).not.toBeInTheDocument();
    fireEvent.change(name, { target: { value: 'external IDs' } });
    expect(screen.getByRole('button', { name: 'Review column rule' })).toBeDisabled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Rule name' }), { target: { value: 'external_ids' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Name columns using' }), { target: { value: 'suffix' } });
    fireEvent.click(screen.getByRole('button', { name: 'Review column rule' }));

    expect(renderReview).toHaveBeenCalledWith({
      kind: 'DYNAMIC_COLUMN',
      occurrenceId: 'patient-related',
      parameters: { dynamic: {
        name: 'external_ids',
        source: { select: 'identifier[]' },
        key: { call: 'last_segment', args: [{ select: 'item.system' }] },
        value: { select: 'item.value' },
      } },
    }, expect.any(Function), false, 'Review column rule', true);
    expect(screen.getByRole('group', { name: 'Source step parameters' })).toBeEnabled();

    fireEvent.click(screen.getByRole('button', { name: 'Cancel review' }));
    expect(screen.getByRole('textbox', { name: 'Rule name' }).getAttribute('value')).toBe('external_ids');
    expect(screen.getByRole('textbox', { name: 'Rule name' })).toBeEnabled();
  });

  it('reviews a category coding preset without exposing the expression editor', () => {
    const category = columnRuleDefaults.categoryCoding;
    if (!category) throw new Error('Category coding preset must be available.');
    const renderReview = vi.fn<ComponentProps<typeof CreateColumnRule>['renderReview']>(() => null);
    const onReview = vi.fn();
    const props = {
      kind: 'DYNAMIC_COLUMN',
      occurrence: { id: 'observation-related', resourceType: 'Observation', label: 'Observation via subject' },
      preset: columnRulePresets[2], initialParameters: category,
      disabled: false, onCancel: vi.fn(), renderReview: reviewButton(onReview, renderReview),
    } satisfies Omit<ComponentProps<typeof CreateColumnRule>, 'reviewing'>;
    const view = render(<CreateColumnRule {...props} reviewing={false} />);
    expect(screen.queryByText('Advanced expressions')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Review column rule' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add column rule' })).toBeEnabled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Rule name' }), { target: { value: 'custom_category_codings' } });
    fireEvent.click(screen.getByRole('button', { name: 'Review column rule' }));
    expect(onReview).toHaveBeenCalledOnce();
    view.rerender(<CreateColumnRule {...props} reviewing />);
    expect(renderReview).toHaveBeenCalledWith({
      kind: 'DYNAMIC_COLUMN', occurrenceId: 'observation-related', parameters: { dynamic: { ...category.dynamic, name: 'custom_category_codings' } },
    }, expect.any(Function), false, 'Review column rule', true);
  });


  it('adds a valid rule with its defaults without asking for an edit', () => {
    const identifier = columnRuleDefaults.identifier;
    if (!identifier) throw new Error('Identifier rule preset must be available.');
    const onReview = vi.fn();
    render(<CreateColumnRule kind="DYNAMIC_COLUMN" occurrence={{ id: '', resourceType: 'Patient', label: 'Patient' }}
      preset={columnRulePresets[0]} initialParameters={identifier} disabled={false} reviewing={false}
      onCancel={vi.fn()} renderReview={reviewButton(onReview, () => null)} />);
    expect(screen.queryByRole('button', { name: 'Review column rule' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add column rule' }));
    expect(onReview).toHaveBeenCalledOnce();
  });

  it('previews only the selected identifier systems', () => {
    const identifier = columnRuleDefaults.identifier;
    const renderReview = vi.fn<ComponentProps<typeof CreateColumnRule>['renderReview']>(() => null);
    function Harness() {
      const [reviewing, setReviewing] = useState(false);
      if (!identifier) throw new Error('Identifier rule preset must be available.');
      return <CreateColumnRule kind="DYNAMIC_COLUMN" occurrence={{ id: '', resourceType: 'Patient', label: 'Patient' }}
        preset={columnRulePresets[0]} initialParameters={identifier} identifierSystems={['urn:one', 'urn:two']}
        disabled={false} reviewing={reviewing} onCancel={vi.fn()} renderReview={reviewButton(() => setReviewing(true), renderReview)} />;
    }
    render(<Harness />);
    expect(screen.queryByRole('button', { name: 'Add column rule' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Include urn:one' }));
    fireEvent.click(screen.getByRole('button', { name: 'Review column rule' }));
    expect(renderReview).toHaveBeenCalledWith(expect.objectContaining({
      parameters: { dynamic: { ...identifier?.dynamic, outputs: [{ key: 'urn:two', enabled: false }] } },
    }), expect.any(Function), false, 'Review column rule', true);
  });

  it('reviews selected catalog paths and the requested column limit', () => {
    const catalog = columnRuleDefaults.catalog;
    const renderReview = vi.fn<ComponentProps<typeof CreateColumnRule>['renderReview']>(() => null);
    function Harness() {
      const [reviewing, setReviewing] = useState(false);
      if (!catalog) throw new Error('Catalog preset must be available.');
      return <CreateColumnRule kind="CATALOG_PROJECTION" occurrence={{ id: '', resourceType: 'Patient', label: 'Patient' }}
        preset={columnRulePresets[3]} initialParameters={catalog} fieldPathChoices={['birthDate', 'gender']}
        disabled={false} reviewing={reviewing} onCancel={() => setReviewing(false)}
        renderReview={reviewButton(() => setReviewing(true), renderReview)} />;
    }
    render(<Harness />);
    expect(screen.queryByRole('button', { name: 'Review column rule' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Include birthDate' }));
    const review = screen.getByRole('button', { name: 'Review column rule' });

    fireEvent.click(screen.getByRole('checkbox', { name: 'Exclude gender' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Filter scalar' }));
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum columns' }), { target: { value: '129' } });
    expect(review).toBeDisabled();
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Maximum columns' }), { target: { value: '4' } });
    fireEvent.click(review);
    expect(renderReview).toHaveBeenCalledWith({
      kind: 'CATALOG_PROJECTION', maxColumns: 4,
      parameters: { catalog: { name: 'catalog_fields', naming: 'PATH', includePaths: ['birthDate'], excludePaths: ['gender'], kinds: ['scalar'] } },
    }, expect.any(Function), false, 'Review column rule', true);
  });

});
