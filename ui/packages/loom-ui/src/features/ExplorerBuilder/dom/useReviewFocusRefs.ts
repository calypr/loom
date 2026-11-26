import { useCallback } from 'react';
import { useWorkspaceUi, useWorkspaceUiActions } from '../session/useWorkspaceUi';

export function useReviewFocusRefs(selectedOutputId: string | undefined) {
  const pending = useWorkspaceUi(state => state.pendingReviewFocus);
  const { consumeReviewFocus } = useWorkspaceUiActions();
  const tableNameRef = useCallback((input: HTMLInputElement | null) => {
    if (input && pending?.kind === 'table-name' && consumeReviewFocus(pending)) {
      input.focus();
    }
  }, [consumeReviewFocus, pending]);
  const rowTypeRef = useCallback((input: HTMLInputElement | null) => {
    if (input && pending?.kind === 'row-type' && pending.outputId === selectedOutputId && consumeReviewFocus(pending)) {
      input.closest('section')?.scrollIntoView({ block: 'center' });
      input.focus();
    }
  }, [consumeReviewFocus, pending, selectedOutputId]);
  return { tableNameRef, rowTypeRef };
}
