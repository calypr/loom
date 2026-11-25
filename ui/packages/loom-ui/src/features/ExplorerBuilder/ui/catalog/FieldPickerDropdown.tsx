import { HeadlessMantineProvider, Popover } from '@mantine/core';
import { anchoredPopoverTheme } from '../../dom/anchoredPopover';
import { useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { useEscapeToClose } from '../../dom/useEscapeToClose';

export function FieldPickerDropdown({ label, summary, choicesLabel, disabled, children }: {
  readonly label: string;
  readonly summary: string;
  readonly choicesLabel: string;
  readonly disabled?: boolean;
  readonly children: ReactNode;
}) {
  const id = useId();
  const [opened, setOpened] = useState(false);
  const [position, setPosition] = useState<'top-start' | 'bottom-start'>('bottom-start');
  useEscapeToClose(opened, setOpened, 'document');
  const triggerRef = useRef<HTMLButtonElement>(null);
  const closeOnEscape = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key !== 'Escape' || !opened) return;
    event.preventDefault();
    event.stopPropagation();
    setOpened(false);
    triggerRef.current?.focus();
  };

  return (
    <HeadlessMantineProvider theme={anchoredPopoverTheme}>
      <Popover
        id={id}
        opened={opened}
        onChange={setOpened}
        keepMounted
        width="target"
        position={position}
        middlewares={{ flip: false, size: { padding: 4 } }}
        zIndex={1200}
      >
        <Popover.Target>
          <button
            type="button"
            aria-label={label}
            aria-controls={id}
            aria-expanded={opened}
            ref={triggerRef}
            disabled={disabled}
            onClick={(event) => {
              if (!opened) {
                const { top, bottom } = event.currentTarget.getBoundingClientRect();
                const spaceBelow = window.innerHeight - bottom;
                setPosition(spaceBelow >= 240 || spaceBelow >= top ? 'bottom-start' : 'top-start');
              }
              setOpened(!opened);
            }}
            onKeyDown={closeOnEscape}
            className="flex min-w-0 items-center justify-between gap-2 rounded border border-slate-300 bg-white px-2 py-1.5 text-left text-sm disabled:opacity-50"
          >
            <span className="truncate" title={summary}>{summary}</span>
            <span className="shrink-0 text-slate-500" aria-hidden="true">⌄</span>
          </button>
        </Popover.Target>
        <Popover.Dropdown
          id={id}
          data-dismissible-layer
          role="group"
          aria-label={choicesLabel}
          onKeyDownCapture={closeOnEscape}
          className="m-0 overflow-y-auto rounded border border-slate-300 bg-white p-2 text-slate-900 shadow-lg"
          style={{ position: 'fixed' }}
        >
          {children}
        </Popover.Dropdown>
      </Popover>
    </HeadlessMantineProvider>
  );
}
