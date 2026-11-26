import { createHash } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { relative, resolve } from 'node:path';
import { configure, getConfig } from '@testing-library/dom';
import { afterEach, beforeEach, expect } from 'vitest';

type TraceEvent = Record<string, unknown>;

type ClientTraceHandle = {
  settle(): void;
  abort(): void;
};

type ActiveTrace = {
  events: TraceEvent[];
  calls: Array<Record<string, unknown>>;
  dispatches: Array<{ step: number; type: string; target: string; captured: boolean }>;
  step: number;
  ids: Map<string, string>;
  nextId: number;
};

let activeTrace: ActiveTrace | undefined;
const duplicateNameOrdinals = new Map<string, number>();
const writtenTracePaths = new Set<string>();
let eventWrapperInstalled = false;
const finishTraceKey = Symbol.for('loom.ui.trace.finish-test');

const uuidPattern = /[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/gi;
const reactIdPattern = /:r[a-z0-9]+:|_[Rr]_[a-z0-9]+_|«r[a-z0-9]+»/gi;
const mantineIdPattern = /mantine-[a-z0-9]{9}/g;
const htmlIdAttributePattern = /(\s(?:id|for|form|headers|list|aria-controls|aria-labelledby|aria-describedby|aria-details|aria-errormessage|aria-owns|aria-activedescendant|href|xlink:href|data-target|data-id)=["'])([^"']+)(["'])/gi;

function normalizeId(value: string, trace: ActiveTrace): string {
  const previous = trace.ids.get(value);
  if (previous) return previous;
  const normalized = `__TRACE_ID_${trace.nextId++}__`;
  trace.ids.set(value, normalized);
  return normalized;
}

function normalizeString(value: string, trace: ActiveTrace): string {
  return value
    .replace(reactIdPattern, (id) => normalizeId(id, trace))
    .replace(uuidPattern, (id) => normalizeId(id, trace))
    .replace(mantineIdPattern, (id) => normalizeId(id, trace));
}

function normalizeDom(value: string, trace: ActiveTrace): string {
  return value.replace(htmlIdAttributePattern, (_attribute, prefix: string, idValue: string, suffix: string) => {
    const normalized = idValue
      .replace(reactIdPattern, (id) => normalizeId(id, trace))
      .replace(uuidPattern, (id) => normalizeId(id, trace))
      .replace(mantineIdPattern, (id) => normalizeId(id, trace));
    return `${prefix}${normalized}${suffix}`;
  });
}

function normalizeValue(value: unknown, trace: ActiveTrace, seen = new WeakSet<object>()): unknown {
  if (value === undefined) return { type: 'undefined' };
  if (typeof value === 'string') return normalizeString(value, trace);
  if (typeof value === 'bigint') return value.toString();
  if (typeof value === 'function') return `[Function ${value.name || 'anonymous'}]`;
  if (value === null || typeof value !== 'object') return value;
  if (value instanceof AbortSignal) return { type: 'AbortSignal', aborted: value.aborted };
  if (value instanceof Date) return value.toISOString();
  if (value instanceof URLSearchParams) return value.toString();
  if (seen.has(value)) throw new Error('Cannot trace a circular LoomClient argument');
  seen.add(value);
  const normalized = Array.isArray(value)
    ? value.map((entry) => normalizeValue(entry, trace, seen))
    : Object.fromEntries(
    Object.entries(value).sort(([left], [right]) => left.localeCompare(right)).map(([key, entry]) => [
      key,
      normalizeValue(entry, trace, seen),
    ]),
  );
  seen.delete(value);
  return normalized;
}

export function traceClientCall(method: string, args: unknown[]): ClientTraceHandle | undefined {
  const trace = activeTrace;
  if (!trace) return undefined;
  let status: 'pending' | 'settled' | 'aborted' = 'pending';
  const call: Record<string, unknown> = {
    method,
    step: trace.step,
    args: normalizeValue(args, trace),
    status,
    lifecycle: [],
  };
  trace.calls.push(call);
  return {
    settle() {
      (call.lifecycle as Array<Record<string, unknown>>).push({ type: 'settle', step: trace.step });
      if (status === 'pending') status = 'settled';
      call.status = status;
    },
    abort() {
      (call.lifecycle as Array<Record<string, unknown>>).push({ type: 'abort', step: trace.step });
      if (status === 'pending') status = 'aborted';
      call.status = status;
    },
  };
}

function currentTestIdentity(): string {
  const test = expect.getState();
  const filepath = test.testPath ?? 'unknown-test-file';
  const marker = `${resolve(filepath)}`.replaceAll('\\', '/');
  const packageMarker = '/ui/packages/loom-ui/';
  const markerIndex = marker.lastIndexOf(packageMarker);
  const stablePath = markerIndex >= 0 ? marker.slice(markerIndex + 1) : relative(process.cwd(), marker);
  const fullName = test.currentTestName ?? 'unknown-test';
  const nameKey = `${stablePath}\n${fullName}`;
  const ordinal = (duplicateNameOrdinals.get(nameKey) ?? 0) + 1;
  duplicateNameOrdinals.set(nameKey, ordinal);
  return `${stablePath}\n${fullName}\n${ordinal}`;
}

function writeTrace(trace: ActiveTrace): void {
  const outputDirectory = process.env.LOOM_UI_TRACE;
  if (!outputDirectory) return;
  const identity = currentTestIdentity();
  const filename = createHash('sha256').update(identity).digest('hex').slice(0, 20);
  const outputPath = resolve(outputDirectory, `${filename}.json.gz`);
  if (writtenTracePaths.has(outputPath)) throw new Error(`duplicate UI trace identity: ${identity}`);
  writtenTracePaths.add(outputPath);
  mkdirSync(outputDirectory, { recursive: true });
  const data = JSON.stringify({ test: identity, events: trace.events, calls: trace.calls });
  writeFileSync(outputPath, gzipSync(data, { level: 9 }));
}

function snapshotDom(trace: ActiveTrace, type: 'dispatch' | 'test-end', dispatch?: { type: string; target: string }, step = trace.step): void {
  if (typeof document === 'undefined') return;
  const dom = normalizeDom(document.body.innerHTML, trace);
  const active = document.activeElement;
  const activeElement = active instanceof HTMLElement
    ? normalizeDom(active.outerHTML, trace)
    : null;
  const focusPath: string[] = [];
  let focusNode: Element | null = active instanceof Element ? active : null;
  while (focusNode && focusNode !== document.body) {
    const siblings = focusNode.parentElement ? [...focusNode.parentElement.children] : [];
    const index = siblings.indexOf(focusNode) + 1;
    const id = focusNode.id ? `#${normalizeString(focusNode.id, trace)}` : '';
    focusPath.unshift(`${focusNode.tagName.toLowerCase()}${id}:nth-child(${index})`);
    focusNode = focusNode.parentElement;
  }
  trace.events.push({ step, type, dispatch, dom, activeElement, focusPath: focusNode === document.body ? focusPath : null });
}

export function installUiTrace(): void {
  if (!process.env.LOOM_UI_TRACE) return;
  Object.defineProperty(globalThis, finishTraceKey, {
    configurable: true,
    value() {
      if (activeTrace && !activeTrace.events.some((event) => event.type === 'test-end')) {
        snapshotDom(activeTrace, 'test-end');
      }
    },
  });
  const installDomInstrumentation = () => {
    if (typeof document === 'undefined' || typeof window === 'undefined') return;
    const prototype = EventTarget.prototype;
    const instrumentationKey = Symbol.for('loom.ui.trace.dispatchEvent');
    if (!(prototype as EventTarget & { [key: symbol]: boolean })[instrumentationKey]) {
      const originalDispatchEvent = prototype.dispatchEvent;
      prototype.dispatchEvent = function dispatchEvent(event: Event): boolean {
        const trace = activeTrace;
        const isDomEventTarget = this === window || this instanceof Node;
        const dispatch = trace && isDomEventTarget && {
          step: ++trace.step,
          type: event.type,
          target: '',
          captured: false,
        };
        if (trace && dispatch) trace.dispatches.push(dispatch);
        const result = originalDispatchEvent.call(this, event);
        if (trace && dispatch) {
          dispatch.target = event.target instanceof Element
            ? `${event.target.tagName.toLowerCase()}${event.target.id ? `#${normalizeString(event.target.id, trace)}` : ''}`
            : event.target instanceof Document ? 'document'
              : event.target === window ? 'window'
                : event.target instanceof Node ? event.target.nodeName.toLowerCase() : 'node';
          queueMicrotask(() => {
            if (!dispatch.captured) {
              snapshotDom(trace, 'dispatch', { type: dispatch.type, target: dispatch.target }, dispatch.step);
              dispatch.captured = true;
            }
          });
        }
        return result;
      };
      Object.defineProperty(prototype, instrumentationKey, { value: true });
    }
    if (eventWrapperInstalled) return;
    const originalEventWrapper = getConfig().eventWrapper;
    configure({
      eventWrapper(callback) {
        const trace = activeTrace;
        const firstDispatch = trace?.dispatches.length ?? 0;
        const result = originalEventWrapper(callback);
        if (trace) {
          for (const dispatch of trace.dispatches.slice(firstDispatch)) {
            if (dispatch.captured) continue;
            snapshotDom(trace, 'dispatch', { type: dispatch.type, target: dispatch.target }, dispatch.step);
            dispatch.captured = true;
          }
        }
        return result;
      },
    });
    eventWrapperInstalled = true;
  };
  beforeEach(() => {
    activeTrace = { events: [], calls: [], dispatches: [], step: 0, ids: new Map(), nextId: 1 };
    installDomInstrumentation();
  });
  afterEach(async () => {
    const trace = activeTrace;
    if (trace) {
      if (!trace.events.some((event) => event.type === 'test-end')) snapshotDom(trace, 'test-end');
      writeTrace(trace);
    }
    activeTrace = undefined;
  });
}
