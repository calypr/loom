type EscapeHandler = () => void;

const documentHandlers = new Set<EscapeHandler>();
const windowHandlers: EscapeHandler[] = [];

function onDocumentKeyDown(event: KeyboardEvent) {
  if (event.key !== 'Escape') return;
  for (const handler of [...documentHandlers]) {
    if (documentHandlers.has(handler)) handler();
  }
}

function onWindowKeyDown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.repeat || event.defaultPrevented) return;
  const handler = windowHandlers.at(-1);
  if (!handler) return;
  event.preventDefault();
  handler();
}

export function registerEscape(phase: 'document' | 'window', handler: EscapeHandler) {
  if (phase === 'document') {
    if (documentHandlers.size === 0) document.addEventListener('keydown', onDocumentKeyDown);
    documentHandlers.add(handler);
    return () => {
      documentHandlers.delete(handler);
      if (documentHandlers.size === 0) document.removeEventListener('keydown', onDocumentKeyDown);
    };
  }

  if (windowHandlers.length === 0) window.addEventListener('keydown', onWindowKeyDown);
  windowHandlers.push(handler);
  return () => {
    const index = windowHandlers.lastIndexOf(handler);
    if (index >= 0) windowHandlers.splice(index, 1);
    if (windowHandlers.length === 0) window.removeEventListener('keydown', onWindowKeyDown);
  };
}
