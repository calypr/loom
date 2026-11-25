// @vitest-environment jsdom
import React, { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { useDismissibleLayer } from './useDismissibleLayer';
import { useEscapeToClose } from './useEscapeToClose';

it('dismisses the inner layer before the outer editor on the same Escape keypress', () => {
  const order: string[] = [];

  function Inner() {
    const [open, setOpen] = useState(true);
    const ref = useDismissibleLayer<HTMLDivElement>(open, (next) => {
      order.push('inner');
      setOpen(next);
    });
    return open ? <div ref={ref} role="dialog"><button type="button">Inner action</button></div> : null;
  }

  function Outer() {
    const [open, setOpen] = useState(true);
    useEscapeToClose(open, (next) => {
      order.push('outer');
      setOpen(next);
    });
    return open ? <section aria-label="Editor"><Inner /></section> : null;
  }

  render(<Outer />);
  fireEvent.keyDown(screen.getByRole('button', { name: 'Inner action' }), { key: 'Escape' });
  expect(order).toEqual(['inner', 'outer']);
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(screen.queryByRole('region', { name: 'Editor' })).toBeNull();
});
