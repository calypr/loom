import type { ConstructionRouteStep } from '../../../types';

export const relationshipLabel = (edge: Pick<ConstructionRouteStep, 'fromResourceType' | 'toResourceType' | 'relationship'>): string => {
  const relationship = [edge.fromResourceType, edge.toResourceType].reduce((name, resourceType) => {
    if (name.endsWith(`_${resourceType}`) || name.endsWith(`-${resourceType}`)) {
      return name.slice(0, -resourceType.length - 1);
    }
    return name;
  }, edge.relationship);
  return relationship
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .replace(/^./, (first) => first.toUpperCase());
};

export const routeMeaning = (route: ReadonlyArray<ConstructionRouteStep>): string => route.map((edge, index) => {
  const source = index === 0 ? `this ${edge.fromResourceType}` : `the matched ${edge.fromResourceType}`;
  const relationship = relationshipLabel(edge);
  return edge.storageDirection === 'INBOUND'
    ? `Find ${edge.toResourceType} records whose ${relationship} points to ${source}.`
    : `Find ${edge.toResourceType} records pointed to by ${source} through ${relationship}.`;
}).join(' Then ');
