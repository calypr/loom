import { expect } from 'vitest';

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => undefined,
      removeListener: () => undefined,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      dispatchEvent: () => false,
    }),
  });
}

if (!globalThis.ResizeObserver) {
  class TestResizeObserver implements ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  Object.defineProperty(globalThis, 'ResizeObserver', {
    configurable: true,
    value: TestResizeObserver,
  });
}

const element = (value: unknown): value is HTMLElement =>
  value instanceof HTMLElement;

expect.extend({
  toBeInTheDocument(received: unknown) {
    const pass = element(received) && document.body.contains(received);
    return {
      pass,
      message: () =>
        `expected ${String(received)} ${pass ? 'not ' : ''}to be in the document`,
    };
  },
  toBeEnabled(received: unknown) {
    const pass = element(received) && !received.hasAttribute('disabled');
    return {
      pass,
      message: () =>
        `expected ${String(received)} ${pass ? 'not ' : ''}to be enabled`,
    };
  },
  toBeDisabled(received: unknown) {
    const pass = element(received) && received.hasAttribute('disabled');
    return {
      pass,
      message: () =>
        `expected ${String(received)} ${pass ? 'not ' : ''}to be disabled`,
    };
  },
  toHaveAttribute(received: unknown, name: string, expected?: unknown) {
    const actual = element(received) ? received.getAttribute(name) : null;
    const pass =
      element(received) &&
      (expected === undefined
        ? received.hasAttribute(name)
        : actual === String(expected));
    return {
      pass,
      message: () =>
        `expected ${String(received)} ${pass ? 'not ' : ''}to have attribute ${name}`,
    };
  },
  toHaveClass(received: unknown, ...classes: string[]) {
    const actual = element(received) ? received.classList : undefined;
    const pass = Boolean(actual && classes.every((name) => actual.contains(name)));
    return {
      pass,
      message: () =>
        `expected ${String(received)} ${pass ? 'not ' : ''}to have classes ${classes.join(' ')}`,
    };
  },
  toHaveTextContent(received: unknown, expected: string | RegExp) {
    const text = element(received) ? received.textContent ?? '' : '';
    const pass = expected instanceof RegExp ? expected.test(text) : text.includes(expected);
    return {
      pass,
      message: () =>
        `expected ${String(received)} ${pass ? 'not ' : ''}to contain text ${String(expected)}`,
    };
  },
  toBeEmptyDOMElement(received: unknown) {
    const pass = element(received) && received.childElementCount === 0 && received.textContent === '';
    return {
      pass,
      message: () =>
        `expected ${String(received)} ${pass ? 'not ' : ''}to be empty`,
    };
  },
});
