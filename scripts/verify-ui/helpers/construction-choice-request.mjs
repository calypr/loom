const proposalPath = '/authoring/v2/construction-choice-proposals';
const bindingFields = [
  'commandId', 'snapshotToken', 'expectedDraftVersion', 'expectedDraftDigest', 'outputId',
];
const choiceFields = ['choiceId', 'form', 'rowValuePolicy', 'route'];

/** Copy only the relevant raw fields from an owned choice-proposal POST. */
export const captureConstructionChoiceProposalRequest = (request, requestBody) => {
  if (request?.method?.() !== 'POST') return null;
  let url;
  try { url = new URL(request.url()); } catch { return null; }
  if (!url.pathname.endsWith(proposalPath)) return null;

  let body;
  try {
    const wireBody = requestBody === undefined ? request.postDataJSON() : requestBody;
    const input = wireBody?.variables?.input ?? wireBody;
    body = input && typeof input === 'object' && !Array.isArray(input) ? input : null;
  } catch {
    return null;
  }
  if (!body) return null;

  const captured = Object.fromEntries(bindingFields
    .filter(field => Object.hasOwn(body, field))
    .map(field => [field, body[field]]));
  if (Array.isArray(body.constructionChoices)) {
    captured.constructionChoices = body.constructionChoices.slice(0, 100).map(selection =>
      Object.fromEntries(choiceFields
        .filter(field => Object.hasOwn(selection ?? {}, field))
        .map(field => [field, selection[field]])));
  }
  return captured;
};
