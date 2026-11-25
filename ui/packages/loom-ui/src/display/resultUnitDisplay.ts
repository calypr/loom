import type { ResultUnit } from '../contracts/types';

export const resultUnitTitle = (unit: ResultUnit): string =>
  `Unit ${unit.code}; system ${unit.system}`;
