// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ConstructionTableNavigation } from './ConstructionTableNavigation';

it('dismisses table actions on Escape after focus leaves the menu', () => {
  const onAction = vi.fn();
  render(
    <>
      <ConstructionTableNavigation
        tables={[{ outputId: 'subjects', title: 'Subjects' }]}
        selectedOutputId="subjects"
        onSelectTable={onAction}
        onNewTable={onAction}
        onDuplicateTable={onAction}
        onDeleteTable={onAction}
        onRenameTable={onAction}
      />
      <button type="button">Next control</button>
    </>,
  );

  fireEvent.click(screen.getByRole('button', { name: 'Actions for Subjects' }));
  expect(screen.getByRole('button', { name: 'Duplicate table' })).toBeInTheDocument();

  const nextControl = screen.getByRole('button', { name: 'Next control' });
  nextControl.focus();
  fireEvent.keyDown(nextControl, { key: 'Escape' });

  expect(screen.queryByRole('button', { name: 'Duplicate table' })).toBeNull();
  expect(document.activeElement).toBe(nextControl);
});
