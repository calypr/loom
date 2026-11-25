import type {
  ProjectViewBuilderCatalog,
  ProjectViewBuilderConfigRevision,
  ProjectViewBuilderFrame,
  ProjectViewConfig,
} from '../../projectViewBuilder';

const safeIdPart = (value: string): string => {
  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[._-]+|[._-]+$/g, '');
  return normalized || 'view';
};

const uniqueProjectConfigId = (
  title: string,
  existing: ReadonlyArray<string>,
): string => {
  const base = safeIdPart(title);
  const used = new Set(existing);
  if (!used.has(base)) return base;
  let suffix = 2;
  while (used.has(`${base}-${suffix}`)) suffix += 1;
  return `${base}-${suffix}`;
};

export const projectViewFrameRef = (
  recipeName: string,
  outputName: string,
): string => `${recipeName}/${outputName}`;

const outputNameForFrameRef = (
  recipeName: string,
  frameRef: string,
): string | undefined => {
  const prefix = `${recipeName}/`;
  if (!frameRef.startsWith(prefix)) return undefined;
  const outputName = frameRef.slice(prefix.length);
  return outputName || undefined;
};

const viewIdFor = (configId: string, outputName: string, index: number) =>
  `${safeIdPart(configId)}-${safeIdPart(outputName)}-${index + 1}`;

export const projectViewFromFrame = (
  configId: string,
  frame: ProjectViewBuilderFrame,
  index: number,
) => ({
  id: viewIdFor(configId, frame.id, index),
  title: frame.title || frame.id,
  output: frame.id,
  table: {
    columns: frame.columns.map((column) => ({
      column: column.name,
      label: column.label,
      visible: true,
    })),
  },
  filters: [],
  charts: [],
});

export const updateProjectView = (
  config: ProjectViewConfig,
  viewId: string,
  update: (
    view: ProjectViewConfig['views'][number],
  ) => ProjectViewConfig['views'][number],
): ProjectViewConfig => ({
  ...config,
  views: config.views.map((view) => (view.id === viewId ? update(view) : view)),
});

export const projectConfigFromCatalog = (
  catalog: ProjectViewBuilderCatalog,
  title: string,
  existingIds: ReadonlyArray<string>,
): ProjectViewConfig => {
  const id = uniqueProjectConfigId(title, existingIds);
  return {
    id,
    title: title.trim(),
    recipeRef: { name: catalog.recipeName },
    views: catalog.frames.map((frame, index) =>
      projectViewFromFrame(id, frame, index),
    ),
    sharedFilters: {},
    fileActions: { extensions: {}, actions: {} },
  };
};

export const duplicateProjectConfig = (
  source: ProjectViewConfig,
  title: string,
  existingIds: ReadonlyArray<string>,
): ProjectViewConfig => {
  const id = uniqueProjectConfigId(title, existingIds);
  return {
    ...source,
    id,
    title: title.trim(),
    views: source.views.map((view, index) => ({
      ...view,
      id: viewIdFor(id, view.output, index),
      table: {
        ...view.table,
        columns: view.table.columns.map((column) => ({ ...column })),
      },
      ...(view.filters
        ? { filters: view.filters.map((filter) => ({ ...filter })) }
        : {}),
      ...(view.charts
        ? { charts: view.charts.map((chart) => ({ ...chart })) }
        : {}),
    })),
  };
};

export const viewForFrameReference = (
  config: ProjectViewConfig,
  frameRef: string,
): ProjectViewBuilderConfigRevision['config']['views'][number] | undefined => {
  const outputName = outputNameForFrameRef(config.recipeRef.name, frameRef);
  return outputName
    ? config.views.find((view) => view.output === outputName)
    : undefined;
};

export const canUseViewColumnPresentation = (column: {
  readonly filterable: boolean;
  readonly chartable: boolean;
}): { readonly filterable: boolean; readonly chartable: boolean } => ({
  filterable: column.filterable,
  chartable: column.chartable,
});

export const moveArrayItem = <T>(
  values: ReadonlyArray<T>,
  index: number,
  offset: -1 | 1,
): T[] => {
  const nextIndex = index + offset;
  if (index < 0 || nextIndex < 0 || nextIndex >= values.length) {
    return [...values];
  }
  const next = [...values];
  const [selected] = next.splice(index, 1);
  next.splice(nextIndex, 0, selected);
  return next;
};
