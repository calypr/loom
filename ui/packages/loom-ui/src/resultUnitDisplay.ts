import type { ResultUnit } from './types';

export const resultUnitTitle = (unit: ResultUnit): string =>
  `Unit ${unit.code}; system ${unit.system}`;
