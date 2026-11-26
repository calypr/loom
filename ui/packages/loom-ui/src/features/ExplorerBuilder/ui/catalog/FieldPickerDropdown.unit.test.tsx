// @vitest-environment jsdom
import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { FieldPickerDropdown } from './FieldPickerDropdown';

describe('FieldPickerDropdown', () => {
  it('opens its choices and returns focus to the trigger on Escape without an app provider', async () => {
    render(
      <FieldPickerDropdown
        label="Choose input columns"
        summary="2 fields selected"
        choicesLabel="Input column picker"
      >
        <button type="button">First field</button>
      </FieldPickerDropdown>,
    );

    const trigger = screen.getByRole('button', { name: 'Choose input columns' });
    fireEvent.click(trigger);

    const choices = await screen.findByRole('group', { name: 'Input column picker' });
    expect(trigger.getAttribute('aria-expanded')).toBe('true');
    expect(choices.querySelector('button')?.textContent).toBe('First field');

    fireEvent.keyDown(choices, { key: 'Escape' });

    await waitFor(() => expect(trigger.getAttribute('aria-expanded')).toBe('false'));
    expect(document.activeElement).toBe(trigger);
  });
});

it('dismisses on Escape after focus has moved past the picker', () => {
  render(
    <>
      <FieldPickerDropdown label="Choose input columns" summary="Choose fields" choicesLabel="Input column picker">
        <button type="button">First field</button>
      </FieldPickerDropdown>
      <button type="button">Next control</button>
    </>,
  );

  fireEvent.click(screen.getByRole('button', { name: 'Choose input columns' }));
  expect(screen.getByRole('group', { name: 'Input column picker' })).toBeInTheDocument();

  const nextControl = screen.getByRole('button', { name: 'Next control' });
  nextControl.focus();
  fireEvent.keyDown(nextControl, { key: 'Escape' });

  expect(screen.queryByRole('group', { name: 'Input column picker' })).toBeNull();
  expect(document.activeElement).toBe(nextControl);
});

it('keeps the field choices mounted when the trigger becomes disabled', () => {
  const picker = (disabled: boolean) => (
    <FieldPickerDropdown label="Choose input columns" summary="Choose fields" choicesLabel="Input column picker" disabled={disabled}>
      <button type="button">First field</button>
    </FieldPickerDropdown>
  );
  const { rerender } = render(picker(false));
  fireEvent.click(screen.getByRole('button', { name: 'Choose input columns' }));
  const choices = screen.getByRole('group', { name: 'Input column picker' });

  rerender(picker(true));

  expect(screen.getByRole('button', { name: 'Choose input columns' })).toBeDisabled();
  expect(screen.getByRole('group', { name: 'Input column picker' })).toBe(choices);
});
