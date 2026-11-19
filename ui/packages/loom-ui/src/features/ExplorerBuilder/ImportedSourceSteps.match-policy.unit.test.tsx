// @vitest-environment jsdom
import React from 'react';
import { expect, it, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ImportedSourceSteps } from './ImportedSourceSteps';

it('opens the editor for a relationship with an authored match override', () => {
  render(<ImportedSourceSteps outputId="subjects" importedSource={{
    steps: [{ id: 'traversals[0]', occurrenceId: 'patient_occ', matchMode: 'OPTIONAL', kind: 'TRAVERSAL',
      title: 'Traversal to Patient via subject_Patient', summary: 'Optional relationship with nested source steps.', editable: true }],
    relationships: { matchOverrides: [{ occurrenceId: 'patient_occ', matchMode: 'REQUIRED' }] },
  }} onRefresh={vi.fn()} onApply={vi.fn()} />);
  fireEvent.click(screen.getByTestId('imported-source-step-traversals[0]'));
  expect(screen.getByTestId('imported-source-step-editor')).toBeInTheDocument();
});
