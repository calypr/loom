// @vitest-environment jsdom
import React, { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ComponentProps } from 'react';
import { CreateIdentifierColumnRule } from './CreateIdentifierColumnRule';

describe('creating an identifier column rule', () => {
  it('reviews the configured rule and returns to the same form on cancel', () => {
    const renderReview = vi.fn<ComponentProps<typeof CreateIdentifierColumnRule>['renderReview']>(
      (_, cancel) => <button onClick={cancel}>Cancel review</button>,
    );
    function Harness() {
      const [reviewing, setReviewing] = useState(false);
      return <CreateIdentifierColumnRule disabled={false} reviewing={reviewing} onReview={() => setReviewing(true)}
        onCancel={() => setReviewing(false)} renderReview={renderReview} />;
    }
    render(<Harness />);

    fireEvent.change(screen.getByRole('textbox', { name: 'Rule name' }), { target: { value: 'external IDs' } });
    expect(screen.getByRole('button', { name: 'Review column rule' })).toBeDisabled();
    fireEvent.change(screen.getByRole('textbox', { name: 'Rule name' }), { target: { value: 'external_ids' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'Name columns using' }), { target: { value: 'suffix' } });
    fireEvent.click(screen.getByRole('button', { name: 'Review column rule' }));

    expect(renderReview).toHaveBeenCalledWith({
      createIdentifierRule: true,
      stepId: 'output',
      enabled: true,
      parameters: { dynamic: {
        name: 'external_ids',
        source: { select: 'root.identifier[]' },
        key: { call: 'last_segment', args: [{ select: 'item.system' }] },
        value: { select: 'item.value' },
      } },
    }, expect.any(Function));
    expect(screen.getByRole('group', { name: 'Source step parameters' })).toBeDisabled();

    fireEvent.click(screen.getByRole('button', { name: 'Cancel review' }));
    expect(screen.getByRole('textbox', { name: 'Rule name' }).getAttribute('value')).toBe('external_ids');
    expect(screen.getByRole('textbox', { name: 'Rule name' })).toBeEnabled();
  });
});
