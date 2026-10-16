import React from 'react';
import type { ExplorerBuilderCandidate } from '../../../types';

export type ObservedCodeCandidate = NonNullable<
  ExplorerBuilderCandidate['conceptCandidates']
>[number];

export const ObservedCodeEvidence = ({
  concepts,
  compact = false,
}: {
  readonly concepts: ReadonlyArray<ObservedCodeCandidate>;
  readonly compact?: boolean;
}) => {
  if (concepts.length === 0) return null;

  return (
    <section
      aria-label="Observed code evidence"
      className={`rounded border border-slate-200 bg-white p-2 text-[11px] text-slate-600 ${compact ? 'max-h-16 overflow-y-auto' : ''}`}
    >
      <h4 className="font-semibold text-slate-800">
        {concepts.length} observed {concepts.length === 1 ? 'code profile' : 'code profiles'}
      </h4>
      <div className="mt-1 space-y-2">
        {concepts.map((concept, index) => (
          <article
            key={`${concept.sourceResourceType}:${concept.sourcePath ?? ''}:${concept.system ?? ''}:${concept.code ?? ''}:${index}`}
            className="border-t border-slate-100 pt-1 first:border-t-0 first:pt-0"
          >
            <p className="break-all font-semibold text-slate-800">
              {[concept.display, concept.system, concept.code].filter(Boolean).join(' · ') || 'Observed code without display metadata'}
            </p>
            <p>
              Scope: {concept.sourceResourceType}{concept.owningScope ? ` · ${concept.owningScope}` : ''} · {concept.completeness.toLowerCase()} evidence
            </p>
            <p>
              Source: {concept.sourcePath ?? 'path not provided'}{concept.valueSelector ? ` · value ${concept.valueSelector}` : ''} · status {concept.status.toLowerCase()}
            </p>
            <p>
              {concept.population.toLocaleString()} observed source {concept.population === 1 ? 'occurrence' : 'occurrences'}; denominator and current-table coverage are not provided.
            </p>
            {concept.observedUnits?.length ? (
              <p className="break-all">
                Observed units: {concept.observedUnits.join(', ')}{concept.observedUnitsTruncated ? ' · additional units exist' : ''}
              </p>
            ) : null}
            {concept.examples?.length ? (
              <p className="break-all">
                Observed examples: {concept.examples.join(', ')}{concept.examplesTruncated ? ' · additional examples exist' : ''}
              </p>
            ) : <p>Observed examples are not available for this code.</p>}
          </article>
        ))}
      </div>
    </section>
  );
};
