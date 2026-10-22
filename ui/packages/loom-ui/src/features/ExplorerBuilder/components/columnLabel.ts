export function normalizedColumnLabel(label: string, fallback: string): string {
  const next = label.trim();
  return next || fallback;
}
