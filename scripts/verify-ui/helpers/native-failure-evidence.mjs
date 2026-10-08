import { performance } from 'node:perf_hooks';
import { sanitizePayload, sanitizeText } from './playwright-browser.mjs';

export const MAX_NATIVE_FAILURE_CAPTURE_MS = 1_000;
const MAX_BODY_TEXT = 12_000;
const MAX_CONTROL_TEXT = 500;
const MAX_CONTROL_VALUE = 1_000;
const MAX_CONTEXTS = 3;
const MAX_CONTEXT_TEXT = 1_600;
const MAX_VISIBLE_ALERTS = 4;
const MAX_ALERT_TEXT = 800;
const sensitiveControlName = /authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key|csrf/i;

const safeText = (value, limit) => {
  try { return sanitizeText(value).slice(0, limit); }
  catch { return '[unavailable]'; }
};

function failureText(value, limit, ownedOrigins) {
  const text = safeText(value, Math.max(limit, 2_000));
  return text.replace(/\b((?:authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key|csrf)[\w-]*)\s*([=:])\s*("[^"]*"|'[^']*'|[^\s,;}\]]+)/gi, '$1$2[REDACTED]')
    .replace(/\bhttps?:\/\/[^\s"'<>)}\]]+/gi, rawURL => {
    try {
      const url = new URL(rawURL);
      return ownedOrigins.has(url.origin) ? `${url.origin}${url.pathname}` : '[external URL]';
    } catch {
      return '[URL]';
    }
  }).slice(0, limit);
}

function boundedRead(operation, deadline) {
  const remaining = Math.max(0, deadline - performance.now());
  if (!remaining) return Promise.resolve({ state: 'timed-out' });

  let timer;
  const read = Promise.resolve()
    .then(operation)
    .then(value => ({ state: 'captured', value }), error => ({
      state: 'error',
      error: safeText(error?.message ?? error, MAX_CONTROL_TEXT),
    }));
  const timeout = new Promise(resolve => {
    timer = setTimeout(() => resolve({ state: 'timed-out' }), remaining);
  });
  return Promise.race([read, timeout]).finally(() => clearTimeout(timer));
}

function ownedPagePath(rawURL, ownedOrigins) {
  if (rawURL === 'about:blank') return 'about:blank';
  try {
    const url = new URL(rawURL);
    if (!ownedOrigins.has(url.origin)) return undefined;
    return safeText(`${url.origin}${url.pathname}`, 1_000);
  } catch {
    return undefined;
  }
}

function captureState(result) {
  if (result.state === 'captured') return { state: 'captured', value: result.value };
  return result.error ? { state: result.state, error: result.error } : { state: result.state };
}

function redactUnownedURLs(value, ownedOrigins) {
  if (typeof value === 'string') return failureText(value, MAX_BODY_TEXT, ownedOrigins);
  if (Array.isArray(value)) return value.map(item => redactUnownedURLs(item, ownedOrigins));
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, redactUnownedURLs(item, ownedOrigins)]));
  }
  return value;
}

/** Collect only visible text near the active native action and visible alerts. */
export function captureNativeActionContext(element) {
  const contextLimit = 1_600;
  const alertLimit = 800;
  const contexts = [];
  const seenContexts = new Set();
  const document = element?.ownerDocument;

  const isVisible = node => {
    if (!node || node.hidden || node.getAttribute?.('aria-hidden') === 'true') return false;
    if (typeof node.getClientRects === 'function' && node.getClientRects().length === 0) return false;
    for (let ancestor = node; ancestor; ancestor = ancestor.parentElement) {
      if (ancestor.hidden || ancestor.getAttribute?.('aria-hidden') === 'true') return false;
      const style = ancestor.ownerDocument?.defaultView?.getComputedStyle?.(ancestor);
      if (style && (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0')) return false;
    }
    return true;
  };

  const textOf = (node, limit) => String(node?.innerText ?? node?.textContent ?? '')
    .slice(0, limit * 2).replace(/\s+/g, ' ').trim().slice(0, limit);

  const addContext = (kind, node) => {
    if (!node || seenContexts.has(node) || !isVisible(node)) return;
    seenContexts.add(node);
    const text = textOf(node, contextLimit);
    if (text) contexts.push({ kind, text });
  };

  const dialog = element?.closest?.('[role="dialog"], dialog');
  const form = element?.closest?.('form');
  const editorSelector = '[data-testid*="editor"], .monaco-editor, [contenteditable="true"], [role="textbox"]';
  addContext('dialog', dialog);
  addContext('form', form);
  let editor = element?.closest?.(editorSelector);
  if (!editor) {
    for (const root of [form, dialog]) {
      const candidates = root?.querySelectorAll?.(editorSelector) ?? [];
      for (let index = 0; index < Math.min(candidates.length, 50); index++) {
        if (isVisible(candidates[index])) {
          editor = candidates[index];
          break;
        }
      }
      if (editor) break;
    }
  }
  addContext('editor', editor);

  const alerts = [];
  const alertNodes = document?.querySelectorAll?.('[role="alert"], [aria-live="assertive"], [aria-live="polite"]') ?? [];
  let examinedAlerts = 0;
  for (const node of alertNodes) {
    if (examinedAlerts++ >= 50) break;
    if (!isVisible(node)) continue;
    const text = textOf(node, alertLimit);
    if (text && !alerts.includes(text)) alerts.push(text);
    if (alerts.length >= 4) break;
  }

  return { containers: contexts.slice(0, 3), alerts };
}

function capturedNativeContext(result, ownedOrigins) {
  if (result.state !== 'captured') return captureState(result);
  const value = result.value && typeof result.value === 'object' ? result.value : {};
  const containers = Array.isArray(value.containers) ? value.containers.slice(0, MAX_CONTEXTS) : [];
  const alerts = Array.isArray(value.alerts) ? value.alerts.slice(0, MAX_VISIBLE_ALERTS) : [];
  return {
    state: 'captured',
    containers: containers.map(item => ({
      kind: safeText(item?.kind ?? 'nearby', 40),
      text: failureText(item?.text ?? '', MAX_CONTEXT_TEXT, ownedOrigins),
    })).filter(item => item.text),
    alerts: alerts.map(text => failureText(text, MAX_ALERT_TEXT, ownedOrigins)).filter(Boolean),
  };
}

/** Read a compact failure-only snapshot from the active native Playwright page. */
export async function captureNativeFailureEvidence({
  page,
  ownedOrigins,
  reason,
  label,
  locator,
  elapsedMs,
  startedAt,
}) {
  const allowedOrigins = new Set(ownedOrigins ?? []);
  let locatorDescription;
  try {
    locatorDescription = locator?.toString ? failureText(locator.toString(), 500, allowedOrigins) : undefined;
  } catch {
    locatorDescription = '[unavailable]';
  }
  const action = {
    label: label ? failureText(label, 300, allowedOrigins) : undefined,
    locator: locatorDescription,
    elapsedMs: Number.isFinite(elapsedMs)
      ? Math.max(0, Math.round(elapsedMs))
      : Number.isFinite(startedAt) ? Math.max(0, Math.round(Date.now() - startedAt)) : undefined,
  };
  const evidence = {
    capturedAt: new Date().toISOString(),
    reason: failureText(reason ?? 'Playwright workflow failed', 2_000, allowedOrigins),
    action,
    page: {},
    locator: { state: 'unavailable' },
    captureLimitMs: MAX_NATIVE_FAILURE_CAPTURE_MS,
  };

  try {
    if (!page || page.isClosed()) {
      evidence.page = { state: 'closed' };
      return redactUnownedURLs(sanitizePayload(evidence), allowedOrigins);
    }

    const pagePath = ownedPagePath(page.url(), allowedOrigins);
    if (pagePath === undefined) {
      evidence.page = { state: 'skipped', reason: 'page origin is outside the owned fixture' };
      evidence.locator = { state: 'skipped', reason: 'page origin is outside the owned fixture' };
      return redactUnownedURLs(sanitizePayload(evidence), allowedOrigins);
    }
    evidence.page.url = pagePath;

    const deadline = performance.now() + MAX_NATIVE_FAILURE_CAPTURE_MS;
    const captureBody = () => boundedRead(
      () => page.locator('body').evaluate(body => String(body.innerText ?? '').slice(0, 12_000)),
      deadline,
    );
    const captureTarget = async () => {
      if (!locator) return { state: 'unavailable' };
      const countRead = await boundedRead(() => locator.count(), deadline);
      if (countRead.state !== 'captured') return captureState(countRead);
      const count = Number(countRead.value);
      if (!Number.isFinite(count)) return { state: 'error', error: 'Locator count was not numeric.' };
      if (count !== 1) return { state: 'captured', count };

      const [visible, enabled, editable, control, context] = await Promise.all([
        boundedRead(() => locator.isVisible(), deadline),
        boundedRead(() => locator.isEnabled(), deadline),
        boundedRead(() => locator.isEditable(), deadline),
        boundedRead(() => locator.evaluate(element => {
          const type = element.getAttribute('type') ?? '';
          const label = element.getAttribute('aria-label') ??
            Array.from(element.labels ?? []).map(item => item.innerText ?? item.textContent ?? '').join(' ').trim() ?? '';
          const name = element.getAttribute('name') ?? '';
          const id = element.id ?? '';
          const placeholder = element.getAttribute('placeholder') ?? '';
          const autocomplete = element.getAttribute('autocomplete') ?? '';
          const testId = element.getAttribute('data-testid') ?? '';
          const redactValue = type.toLowerCase() === 'password' || type.toLowerCase() === 'hidden' ||
            /authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key|csrf/i
              .test([type, label, name, id, placeholder, autocomplete, testId].join(' '));
          const value = 'value' in element ? String(element.value ?? '') : undefined;
          return {
            tag: element.tagName.toLowerCase(),
            type: type || undefined,
            label: label.slice(0, 500),
            name: name || undefined,
            id: id || undefined,
            placeholder: placeholder || undefined,
            autocomplete: autocomplete || undefined,
            testId: testId || undefined,
            value: value === undefined ? undefined : redactValue ? '[REDACTED]' : value.slice(0, 1_000),
            checked: 'checked' in element ? Boolean(element.checked) : undefined,
          };
        }), deadline),
        boundedRead(() => locator.evaluate(captureNativeActionContext), deadline),
      ]);

      const target = {
        count,
        visible: captureState(visible),
        enabled: captureState(enabled),
        editable: captureState(editable),
        control: captureState(control),
        context: capturedNativeContext(context, allowedOrigins),
      };
      if (target.control.state === 'captured' && target.control.value) {
        const value = target.control.value;
        const identity = [value.type, value.label, value.name, value.id, value.placeholder,
          value.autocomplete, value.testId].join(' ');
        if (sensitiveControlName.test(identity)) value.value = '[REDACTED]';
        else if (typeof value.value === 'string') value.value = safeText(value.value, MAX_CONTROL_VALUE);
        for (const key of ['tag', 'type', 'label', 'name', 'id', 'placeholder', 'autocomplete', 'testId']) {
          if (value[key] !== undefined) value[key] = safeText(value[key], key === 'label' ? MAX_CONTROL_TEXT : 200);
        }
      }
      return { state: 'captured', ...target };
    };

    const target = await captureTarget();
    const body = await captureBody();
    evidence.page.bodyText = body.state === 'captured'
      ? failureText(body.value, MAX_BODY_TEXT, allowedOrigins)
      : undefined;
    evidence.page.bodyTextState = body.state;
    if (body.error) evidence.page.bodyTextError = body.error;
    evidence.locator = target;
    evidence.captureDurationMs = Math.min(MAX_NATIVE_FAILURE_CAPTURE_MS,
      Math.max(0, Math.round(performance.now() - (deadline - MAX_NATIVE_FAILURE_CAPTURE_MS))));
  } catch (error) {
    evidence.captureError = safeText(error?.message ?? error, MAX_CONTROL_TEXT);
  }
  return redactUnownedURLs(sanitizePayload(evidence), allowedOrigins);
}
