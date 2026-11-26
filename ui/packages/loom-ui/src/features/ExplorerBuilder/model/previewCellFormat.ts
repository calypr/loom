import { displayValue } from '../../../display/valueDisplay';

export const formatPreviewCell = (value: unknown, depth = 0): string =>
  displayValue(value, { unknownRecord: 'parts', maxDepth: 3 }, depth);
