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

export const relationshipField = (edge: Pick<ConstructionRouteStep, 'fromResourceType' | 'toResourceType' | 'relationship'>): string =>
  [edge.fromResourceType, edge.toResourceType].reduce((name, resource) =>
    name.endsWith(`_${resource}`) || name.endsWith(`-${resource}`)
      ? name.slice(0, -resource.length - 1) : name, edge.relationship);

export const routePath = (route: ReadonlyArray<ConstructionRouteStep>): string => {
  const first = route[0];
  if (!first) return 'Same record';
  return first.fromResourceType + route.map((edge) => edge.storageDirection === 'INBOUND'
    ? ` <-[${relationshipField(edge)}]- ${edge.toResourceType}`
    : ` -[${relationshipField(edge)}]-> ${edge.toResourceType}`).join('');
};
