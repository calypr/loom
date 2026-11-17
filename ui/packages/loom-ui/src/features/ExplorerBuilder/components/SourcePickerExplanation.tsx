
import type { SourcePickerEmptiness } from '../constructionOperations/sourcePickerExplanations';

const sourcePickerExplanationText = (emptiness: SourcePickerEmptiness): string => {
  switch (emptiness.emptyClass) {
    case 'all-bound':
      return 'Every eligible dataset field that contains values is already used by another source selection here. Remove one to choose a different field.';
    case 'none-populated':
      return 'No eligible dataset field contains values in this dataset yet.';
    case 'none-listed':
      return 'Loom has not listed any dataset fields for this step.';
    case 'available':
      return '';
  }
};

export const SourcePickerExplanation = (props: {
  readonly emptiness: SourcePickerEmptiness;
}) => {
  if (props.emptiness.emptyClass === 'available') return null;
  const text = sourcePickerExplanationText(props.emptiness);
  if (text === '') return null;
  return (
    <div className="grid gap-1">
      <p role="status" data-testid="source-picker-explanation" className="text-xs text-slate-600">
        {text}
      </p>
      {props.emptiness.emptyClass === 'none-populated' ? (
        <ul data-testid="source-picker-empty-list" className="list-disc pl-5 text-xs text-slate-600">
          {props.emptiness.unpopulated.map((choice) => (
            <li
              key={choice.choiceId}
              data-testid="source-picker-empty-list-item"
              title={choice.fieldPath}
            >
              {choice.label} ({choice.fieldPath}) — has no values in this dataset yet
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
};
