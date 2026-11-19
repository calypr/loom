import { useId, useRef, type KeyboardEvent, type ReactNode } from 'react';

export function FieldPickerDropdown({ label, summary, choicesLabel, disabled, children }: {
  readonly label: string;
  readonly summary: string;
  readonly choicesLabel: string;
  readonly disabled?: boolean;
  readonly children: ReactNode;
}) {
  const id = useId();
  const popupRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const closeOnEscape = (event: KeyboardEvent<HTMLElement>) => {
    const popup = popupRef.current;
    if (event.key !== 'Escape' || !popup?.hidePopover || !popup.matches(':popover-open')) return;
    event.preventDefault();
    event.stopPropagation();
    popup.hidePopover();
    triggerRef.current?.focus();
  };
  return <>
    <button type="button" aria-label={label} aria-controls={id} popoverTarget={id}
      ref={triggerRef} onKeyDown={closeOnEscape} disabled={disabled}
      onClick={(event) => {
        const popup = popupRef.current;
        if (!popup) return;
        const rect = event.currentTarget.getBoundingClientRect();
        const below = window.innerHeight - rect.bottom;
        const above = rect.top;
        const openBelow = below >= 240 || below >= above;
        popup.style.left = `${rect.left}px`;
        popup.style.width = `${rect.width}px`;
        popup.style.top = openBelow ? `${rect.bottom + 4}px` : 'auto';
        popup.style.bottom = openBelow ? 'auto' : `${window.innerHeight - rect.top + 4}px`;
        popup.style.maxHeight = `${Math.max(0, (openBelow ? below : above) - 8)}px`;
      }}
      className="flex min-w-0 items-center justify-between gap-2 rounded border border-slate-300 bg-white px-2 py-1.5 text-left text-sm disabled:opacity-50">
      <span className="truncate" title={summary}>{summary}</span>
      <span className="shrink-0 text-slate-500" aria-hidden="true">⌄</span>
    </button>
    <div id={id} ref={popupRef} popover="auto" role="group" aria-label={choicesLabel} onKeyDown={closeOnEscape}
      className="fixed inset-auto m-0 overflow-y-auto rounded border border-slate-300 bg-white p-2 text-slate-900 shadow-lg">
      {children}
    </div>
  </>;
}
