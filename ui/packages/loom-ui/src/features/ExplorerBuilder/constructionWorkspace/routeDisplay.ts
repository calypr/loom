import type { ConstructionRouteStep } from '../../../types';

export const relationshipLabel = (edge: ConstructionRouteStep): string => {
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
