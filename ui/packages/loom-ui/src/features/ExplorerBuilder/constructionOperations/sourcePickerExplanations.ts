
export type SourcePickerChoice = {
  readonly choiceId: string;
  readonly fieldPath: string;
  readonly label: string;
  readonly isPopulated: boolean;
};

export type SourcePickerEmptyClass = 'available' | 'all-bound' | 'none-populated' | 'none-listed';

export type SourcePickerEmptiness = {
  readonly emptyClass: SourcePickerEmptyClass;
  readonly unpopulated: ReadonlyArray<SourcePickerChoice>;
  readonly boundCount: number;
};

export const classifySourcePickerEmptiness = (
  choices: ReadonlyArray<SourcePickerChoice>,
  boundChoiceIds: ReadonlySet<string>,
): SourcePickerEmptiness => {
  const bound = choices.filter((choice) => boundChoiceIds.has(choice.choiceId));
  const boundCount = bound.length;
  const unbound = choices.filter((choice) => !boundChoiceIds.has(choice.choiceId));
  if (unbound.some((choice) => choice.isPopulated)) {
    return { emptyClass: 'available', unpopulated: [], boundCount };
  }
  if (choices.length === 0) {
    return { emptyClass: 'none-listed', unpopulated: [], boundCount };
  }
  if (unbound.length === 0 || bound.some((choice) => choice.isPopulated)) {
    return { emptyClass: 'all-bound', unpopulated: [], boundCount };
  }
  return { emptyClass: 'none-populated', unpopulated: unbound, boundCount };
};

export const sourcePickerBoundSet = <T extends { readonly choiceId: string }>(args: {
  readonly bindings: ReadonlyArray<T>;
  readonly isOwn: (binding: T) => boolean;
  readonly ownVisible: boolean;
}): Set<string> => {
  const others = args.bindings.filter((binding) => !args.isOwn(binding));
  const ids = others.length === 0 && args.ownVisible
    ? []
    : args.bindings.map((binding) => binding.choiceId);
  return new Set(ids);
};
