// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ConstructionTableNavigation } from './ConstructionTableNavigation';

it('keeps table actions anchored when the page scrolls or the viewport resizes', () => {
  const onAction = vi.fn();
  render(<ConstructionTableNavigation
    tables={[{ outputId: 'subjects', title: 'Subjects' }]}
    selectedOutputId="subjects"
    onSelectTable={onAction}
    onNewTable={onAction}
    onDuplicateTable={onAction}
    onDeleteTable={onAction}
    onRenameTable={onAction}
  />);

  const anchor = screen.getByRole('button', { name: 'Actions for Subjects' });
  const bounds = vi.spyOn(anchor, 'getBoundingClientRect')
    .mockReturnValue(new DOMRect(window.innerWidth - 64, 100, 32, 20));
  const menu = () => screen.getByTestId('construction-table-actions-menu');

  fireEvent.click(anchor);
  expect(menu().style).toMatchObject({ top: '124px', right: '32px' });

  bounds.mockReturnValue(new DOMRect(window.innerWidth - 100, window.innerHeight - 80, 32, 20));
  fireEvent.scroll(document.body);
  expect(menu().style).toMatchObject({ top: `${window.innerHeight - 204}px`, right: '68px' });

  bounds.mockReturnValue(new DOMRect(window.innerWidth - 35, 140, 32, 20));
  fireEvent.resize(window);
  expect(menu().style).toMatchObject({ top: '164px', right: '8px' });

  fireEvent.click(anchor);
  expect(screen.queryByTestId('construction-table-actions-menu')).toBeNull();
  bounds.mockReturnValue(new DOMRect(window.innerWidth - 55, 200, 32, 20));
  fireEvent.click(anchor);
  expect(menu().style).toMatchObject({ top: '224px', right: '23px' });
});
