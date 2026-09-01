import '@vitest/expect';

declare module '@vitest/expect' {
  interface Assertion<T = any> {
    toBeInTheDocument(): void;
    toBeEnabled(): void;
    toBeDisabled(): void;
    toHaveAttribute(name: string, value?: string): void;
    toHaveClass(...classes: string[]): void;
    toHaveTextContent(expected: string | RegExp): void;
    toBeEmptyDOMElement(): void;
  }
}
