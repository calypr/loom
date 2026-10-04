export function isApplicationBrowserRequest(requestUrl, initiator, applicationOrigins) {
  const origins = new Set(applicationOrigins.map(url => new URL(url).origin));
  const owns = url => {
    try { return origins.has(new URL(url).origin); } catch { return false; }
  };
  if (owns(requestUrl) || owns(initiator?.url)) return true;
  for (let stack = initiator?.stack; stack; stack = stack.parent) {
    if (stack.callFrames?.some(frame => owns(frame.url))) return true;
  }
  return false;
}
