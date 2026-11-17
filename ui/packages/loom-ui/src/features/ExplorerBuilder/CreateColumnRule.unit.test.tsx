// @vitest-environment jsdom
import React, { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ComponentProps } from 'react';
import { CreateColumnRule } from './CreateColumnRule';
import { columnRuleDefaults, columnRulePresets } from './sourceColumnRule';

describe('creating a column rule', () => {
  it('reviews the configured rule and returns to the same form on cancel', () => {
    const renderReview = vi.fn<ComponentProps<typeof CreateColumnRule>['renderReview']>(
      (_, cancel) => <button onClick={cancel}>Cancel review</button>,
    );
    function Harness() {
      const [reviewing, setReviewing] = useState(false);
      const identifier = columnRuleDefaults.identifier;
      if (!identifier) throw new Error('Identifier rule preset must be available.');
      return <CreateColumnRule kind="DYNAMIC_COLUMN" occurrence={{ id: 'patient-related', resourceType: 'Patient', label: 'Related Patient' }}
        preset={columnRulePresets[0]} initialParameters={identifier} disabled={false} reviewing={reviewing} onReview={() => setReviewing(true)}
        onCancel={() => setReviewing(false)} renderReview={renderReview} />;
    }
    render(<Harness />);

    fireEvent.change(screen.getByRole('textbox', { name: 'Rule name' }), { target: { value: 'external IDs' } });
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
    }, expect.any(Function));
    expect(screen.getByRole('group', { name: 'Source step parameters' })).toBeDisabled();

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
      disabled: false, onReview, onCancel: vi.fn(), renderReview,
    } satisfies Omit<ComponentProps<typeof CreateColumnRule>, 'reviewing'>;
    const view = render(<CreateColumnRule {...props} reviewing={false} />);
    expect(screen.queryByText('Advanced expressions')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Review column rule' }));
    expect(onReview).toHaveBeenCalledOnce();
    view.rerender(<CreateColumnRule {...props} reviewing />);
    expect(renderReview).toHaveBeenCalledWith({
      kind: 'DYNAMIC_COLUMN', occurrenceId: 'observation-related', parameters: category,
    }, expect.any(Function));
  });

});
