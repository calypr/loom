import type { DraftTable } from '../authoring/model';
import { authoredStages } from './authoredTraversal';
import { columnPolicy, operationExpression } from './rowRecipeProjection';
const table: DraftTable = { outputId:'subjects',tabId:'subjects',title:'Subjects',document:{kind:'ExplorerBuilderDocument',output:{id:'subjects',title:'Subjects'},rootResourceType:'ResearchSubject',route:{occurrenceId:'base',resourceType:'ResearchSubject'},rows:{kind:'RECORDS',records:{}},columns:[{columnId:'gender_id',column:'subject_gender',label:'Gender',occurrenceId:'base',source:{kind:'field',field:{path:'gender',projectionMode:'VALUE'}}}],construction:{version:1,steps:[{id:'g',inputs:[{kind:'SOURCE_PROJECTION'}],operation:{kind:'GROUP',group:{constructionId:'g',keys:[{inputColumnId:'gender_id',outputColumnId:'g_gender'}]}},outputs:[{id:'g_gender',name:'gender',label:'Gender'}]}]}}};
it('preserves a stable source column ID in the Group label',()=>{expect(authoredStages(table)[0].title).toBe('Group by Gender');});
it('reports the same Group missing policy in both editor projections',()=>{const step=table.document.construction!.steps[0];const row=operationExpression(step,new Map([['gender_id','Gender']]));expect(row.clauses[0]).toBe('Missing keys: keep a missing-key group');expect(authoredStages(table)[0].missingPolicy).toBe('GROUP');});

it('discloses existing first-related selection even with authoritative exact network binding',()=>{const column={...table.document.columns[0],source:{kind:'field' as const,field:{path:'gender',projectionMode:'ALL' as const,relatedSelection:{kind:'first-by-resource-key' as const,acknowledged:true}}}};const network={owner:'DOCUMENT' as const,outputId:'subjects',sourceRows:table.document.rows,occurrences:[],operations:[],bindings:[{columnId:'gender_id',occurrenceId:'base',status:'EXACT' as const,valueMode:'ALL'}]};expect(columnPolicy(column,network).label).toContain('first related record');});

it('describes a calculation without repeating hundreds of unchanged passthrough columns', () => {
  const outputs = Array.from({ length: 300 }, (_, index) => ({ id: `source_${index}`, name: `source_${index}`, label: `Unchanged source ${index}` }));
  const step: import('../../../types').ConstructionStep = {
    id: 'derive_one', inputs: [{ kind: 'SOURCE_PROJECTION' }],
    operation: { kind: 'DERIVE', derive: { constructionId: 'derive_one', missingInputPolicy: 'ERROR', outputColumnId: 'new', operation: 'ADD', left: { kind: 'COLUMN', columnId: 'source_0' }, right: { kind: 'LITERAL', literal: { kind: 'INTEGER', integer: 2 } } } },
    outputs: [...outputs, { id: 'new', name: 'new', label: 'Calculated value' }],
  };
  const expression = operationExpression(step, new Map(step.outputs.map((column) => [column.id, column.label])));
  expect(expression.title).toBe('Calculate: Calculated value');
  expect(expression.clauses.join(' ')).toContain('Calculated value = Unchanged source 0 + 2');
  expect(expression.clauses.join(' ')).not.toContain('Unchanged source 299');
  expect(step.outputs).toHaveLength(301);
});
