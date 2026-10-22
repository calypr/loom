export const shouldShowBuilderLoadError = (explorersError: boolean, hasBuilderData: boolean): boolean =>
  explorersError || !hasBuilderData;
