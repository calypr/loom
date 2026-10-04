import { createWriteStream } from 'node:fs';
import { rename, rm } from 'node:fs/promises';
import { pipeline } from 'node:stream/promises';
import { yauzl, yazl } from 'playwright-core/lib/utilsBundle';
import { sanitizeText } from './playwright-browser.mjs';

const binaryExtensions = /\.(?:png|jpe?g|gif|webp|woff2?|ttf|otf|ico|wasm|pdf|zip)$/i;
const sensitiveField = /authorization|cookie|password|passwd|token|secret|credential|session|api[_-]?key/i;

function redactTraceValue(value, key = '') {
  if (sensitiveField.test(key)) return '[REDACTED]';
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value);
      if (parsed && typeof parsed === 'object') return JSON.stringify(redactTraceValue(parsed));
    } catch {}
    return sanitizeText(value);
  }
  if (Array.isArray(value)) return value.map(item => redactTraceValue(item));
  if (value && typeof value === 'object') {
    if (typeof value.name === 'string' && sensitiveField.test(value.name) && Object.hasOwn(value, 'value')) {
      return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [
        childKey,
        childKey === 'value' ? '[REDACTED]' : redactTraceValue(childValue, childKey),
      ]));
    }
    return Object.fromEntries(Object.entries(value).map(([childKey, childValue]) => [childKey, redactTraceValue(childValue, childKey)]));
  }
  return value;
}

function sanitizeTraceEntry(name, body) {
  if (binaryExtensions.test(name) || body.includes(0)) return body;
  const text = body.toString('utf8');
  if (!Buffer.from(text, 'utf8').equals(body)) return body;
  const lines = text.split('\n');
  const structured = lines.map(line => {
    if (!line) return '';
    try { return JSON.stringify(redactTraceValue(JSON.parse(line))); }
    catch { return undefined; }
  });
  if (structured.every(line => line !== undefined)) return Buffer.from(structured.join('\n'), 'utf8');
  try { return Buffer.from(JSON.stringify(redactTraceValue(JSON.parse(text))), 'utf8'); }
  catch { return Buffer.from(sanitizeText(text), 'utf8'); }
}

function readEntries(path) {
  return new Promise((resolve, reject) => {
    const entries = [];
    yauzl.open(path, { lazyEntries: true, decodeStrings: true, validateEntrySizes: true }, (error, archive) => {
      if (error) return reject(error);
      archive.on('error', reject);
      archive.on('end', () => resolve(entries));
      archive.on('entry', entry => {
        if (entry.fileName.endsWith('/')) {
          archive.readEntry();
          return;
        }
        archive.openReadStream(entry, (streamError, stream) => {
          if (streamError) return reject(streamError);
          const chunks = [];
          stream.on('data', chunk => chunks.push(chunk));
          stream.on('error', reject);
          stream.on('end', () => {
            entries.push({ name: entry.fileName, body: sanitizeTraceEntry(entry.fileName, Buffer.concat(chunks)) });
            archive.readEntry();
          });
        });
      });
      archive.readEntry();
    });
  });
}

/** Repack Playwright's trace after redacting sensitive JSON fields and text resources. */
export async function sanitizePlaywrightTrace(sourcePath, targetPath) {
  // Binary payloads cannot be reliably scrubbed for arbitrary embedded credentials.
  // They are unnecessary for the retained action/DOM trace, and first-failure.png is
  // captured separately with form controls masked.
  const entries = (await readEntries(sourcePath)).filter(entry => !binaryExtensions.test(entry.name) && !entry.body.includes(0));
  const temporaryPath = `${targetPath}.redacted.tmp`;
  const archive = new yazl.ZipFile();
  for (const entry of entries) archive.addBuffer(entry.body, entry.name);
  archive.end();
  try {
    await pipeline(archive.outputStream, createWriteStream(temporaryPath, { flags: 'w' }));
    await rename(temporaryPath, targetPath);
  } catch (error) {
    await rm(temporaryPath, { force: true });
    throw error;
  }
}
