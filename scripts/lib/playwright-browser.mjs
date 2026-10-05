const maxBodyLength = 12_000;
const sensitiveName = /authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i;

export function sanitizeText(value) {
  return String(value ?? '')
    .replaceAll(process.cwd(), '$CHECKOUT')
    .replace(/(?:file:\/\/)?\/(?:private\/)?tmp\/[^\s)]+/g, '$TMP/<path>')
    .replace(/\/Users\/[^/\s]+/g, '$HOME')
    .replace(/\bBearer\s+[A-Za-z0-9._~+/-]+=*/gi, 'Bearer [REDACTED]')
    .replace(/(?<![\w-])["']?[\w-]*(?:token|authorization|set-cookie|cookie|password|passwd|secret|credential|session(?:[_-]?id)?|api[_-]?key)[\w-]*["']?\s*[=:]\s*(?:"[^"]*"|'[^']*'|[^,;\s}\]]+)/gi, '[REDACTED]')
    .replace(/<input\b[^>]*>/gi, tag => sensitiveName.test(tag)
      ? tag.replace(/(\bvalue\s*=\s*)(["'])(.*?)\2/gi, '$1$2[REDACTED]$2')
      : tag)
    .replace(/\beyJ[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\b/g, '[REDACTED_TOKEN]')
    .replace(/\bsk-[A-Za-z0-9]{16,}\b/g, '[REDACTED_TOKEN]');
}

export function sanitizePayload(value, key = '') {
  if (key === 'snapshotToken' && typeof value === 'string' && /^sha256:[a-f0-9]{64}$/.test(value)) return value;
  if ((key === 'authorizationHeaderPresent' || key === 'snapshotTokenMatched') && typeof value === 'boolean') return value;
  if (sensitiveName.test(key)) return '[REDACTED]';
  if (typeof value === 'string') return sanitizeText(value);
  if (Array.isArray(value)) return value.map(item => sanitizePayload(item));
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [childKey, sanitizePayload(childValue, childKey)]));
  }
  return value;
}

export function sanitizeBody(body) {
  const text = String(body ?? '').slice(0, maxBodyLength);
  try {
    return JSON.stringify(sanitizePayload(JSON.parse(text)));
  } catch {
    return sanitizeText(text);
  }
}
