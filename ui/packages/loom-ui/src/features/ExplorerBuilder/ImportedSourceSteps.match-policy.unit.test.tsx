// @vitest-environment jsdom
import React from 'react';
import { expect, it, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ImportedSourceSteps } from './ImportedSourceSteps';

it('shows the authored match override separately from the immutable pin', () => {
  render(<ImportedSourceSteps outputId="subjects" importedSource={{
    steps: [{ id: 'traversals[0]', occurrenceId: 'patient_occ', matchMode: 'OPTIONAL', kind: 'TRAVERSAL',
      title: 'Traversal to Patient via subject_Patient', summary: 'Optional relationship with nested source steps.', enabled: true, editable: true }],
    relationships: { matchOverrides: [{ occurrenceId: 'patient_occ', matchMode: 'REQUIRED' }] },
  }} onRefresh={vi.fn()} onApply={vi.fn()} />);
  fireEvent.click(screen.getByTestId('imported-source-step-traversals[0]'));
  expect(screen.getByTestId('imported-source-step-editor').textContent).toContain('A match is required.');
  expect(screen.getByText('Pinned recipe setting: Optional relationship with nested source steps.')).toBeTruthy();
});
