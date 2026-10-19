import React from 'react';
import type { ConstructionRouteStep } from '../../../types';
import { relationshipField } from './routeDisplay';

const stepKey = (edge: ConstructionRouteStep) => JSON.stringify([
  edge.fromResourceType, edge.toResourceType, edge.relationship, edge.storageDirection, edge.matchMode,
]);

export const TraversalPath = ({ route, referenceRoute }: {
  readonly route: ReadonlyArray<ConstructionRouteStep>;
  readonly referenceRoute?: ReadonlyArray<ConstructionRouteStep>;
}) => {
  const referenceKeys = referenceRoute?.map(stepKey);
  let referenceIndex = 0;
  return (
    <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1 text-xs" data-testid="construction-traversal-path">
      <span className="font-medium text-slate-600">{route[0]?.fromResourceType ?? 'Same record'}</span>
      {route.map((edge, index) => {
        const matchIndex = referenceKeys?.indexOf(stepKey(edge), referenceIndex) ?? -1;
        const changed = referenceKeys !== undefined && matchIndex < 0;
        if (matchIndex >= 0) referenceIndex = matchIndex + 1;
        const fieldOwner = edge.storageDirection === 'INBOUND' ? edge.toResourceType : edge.fromResourceType;
        return (
          <span key={index} className="inline-flex items-center gap-2" data-testid="construction-traversal-branch" data-different={changed}>
            <span aria-hidden="true" className="text-slate-300">›</span>
            <span className={`inline-flex items-center gap-1.5 rounded px-1.5 py-1 ${changed ? 'bg-amber-100 text-amber-950 ring-1 ring-amber-300' : 'text-slate-600'}`}
              title={`${fieldOwner}.${relationshipField(edge)} · ${edge.storageDirection === 'INBOUND' ? 'reference on destination' : 'reference on source'} · ${edge.matchMode}`}>
              <code className="text-[10px]">[{relationshipField(edge)}]</code>
              <span className="font-medium">{edge.toResourceType}</span>
            </span>
          </span>
        );
      })}
    </span>
  );
};
