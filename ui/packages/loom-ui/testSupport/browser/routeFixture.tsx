import React, { useMemo, useState } from 'react';
import { LoomProvider } from '../../src/react';
import type { LoomClient } from '../../src/api';
import type { Construction, ConstructionCapabilitiesResponse, ConstructionProposalResponse, ExplorerBuilderCatalog, ExplorerBuilderPreviewResult } from '../../src/types';
import { RelatedExpandEditor } from '../../src/features/ExplorerBuilder/constructionOperations/RelatedExpandEditor';
import { useConstructionLifecycle } from '../../src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle';
import { ConstructionProposalPanel } from '../../src/features/ExplorerBuilder/constructionWorkspace/ConstructionProposalPanel';
import { PreviewTable } from '../../src/features/ExplorerBuilder/components/PreviewTable';

const route = (id: string) => [{ edgeId: id, fromNodeId: 'patient', toNodeId: 'observation', fromResourceType: 'Patient', toResourceType: 'Observation', relationship: id, storageDirection: 'OUTBOUND' as const, matchMode: 'OPTIONAL' as const }];
const columns = [{ id: 'patient', name: 'patient_id', label: 'Patient ID', type: 'string' }, { id: 'observation', name: 'observation_id', label: 'Observation ID', type: 'string' }];
const initial: Construction = { version: 1, steps: [{ id: 'saved-related', inputs: [{ kind: 'SOURCE_PROJECTION' }], outputs: columns, operation: { kind: 'RELATED_EXPAND', relatedExpand: { anchorColumnId: '_key', targetResourceType: 'Observation', targetNodeId: 'observation', choiceId: 'route-A', route: route('route-A'), relatedRecordColumnId: 'observation', emptyPolicy: 'EXCLUDE', contributorRule: { policy: 'ALL_MATCHES' } } } }] };
const catalog: ExplorerBuilderCatalog = { snapshotToken: 'browser-route-snapshot', generation: 'browser-route-generation', routePolicy: {}, nodes: [ { nodeId: 'patient', resourceType: 'Patient', rowRootEligible: true, populated: true, documentCount: 2 }, { nodeId: 'observation', resourceType: 'Observation', rowRootEligible: false, populated: true, documentCount: 2 } ], edges: ['route-A','route-B'].map(edgeId => ({ edgeId, fromNodeId: 'patient', toNodeId: 'observation', label: edgeId })) };
const routeId = (construction: Construction) => { const operation = construction.steps[0]?.operation; return operation?.kind === 'RELATED_EXPAND' ? operation.relatedExpand.choiceId : ''; };
const previewFor = (construction: Construction, receiptId: string): ExplorerBuilderPreviewResult => ({ apiVersion: 'loom.calypr.org/explorer-authoring/v2', kind: 'ExplorerBuilderPreview', receiptId, outputId: 'patients', columns: columns.map(c => ({ column: c.name, label: c.label, logicalType: 'string', filterable: true, chartable: false })), rows: routeId(construction) === 'route-A' ? [{ patient_id:'p1', observation_id:'o1', __loom_row_id:'p1/o1' }, {patient_id:'p2',observation_id:'o2',__loom_row_id:'p2/o2'}] : [{patient_id:'p1',observation_id:'o2',__loom_row_id:'p1/o2'}], rowCount: routeId(construction) === 'route-A' ? 2 : 1, diagnostics: [], rowLineageCapability: {status:'UNAVAILABLE',reasonCode:'CONTROLLED_ROUTE_FIXTURE'} });
export function RouteFixture() {
  const [saved,setSaved] = useState<{construction:Construction;version:number;writes:number}>(()=>JSON.parse(sessionStorage.getItem('route-saved') || 'null') || {construction:initial,version:1,writes:0});
  const [epoch,setEpoch] = useState(0);
  const [hold,setHold] = useState(false);
  const [pending,setPending] = useState<(() => void) | undefined>();
  const [released,setReleased] = useState(0);
  const capabilities: ConstructionCapabilitiesResponse = {snapshotToken:catalog.snapshotToken,draftVersion:saved.version,draftDigest:`digest-${saved.version}`,outputId:'patients',stageId:'source_projection',groupFieldChoices:[],groupRowValueChoices:[],workspaceInputs:[],baseConstruction:saved.construction,stages:[],selectedStage:{id:'source_projection',inputStageId:'',rowIdentityColumn:'_key',columns:[columns[0]!],capabilities:[{kind:'RELATED_EXPAND',supported:true}],relatedExpandAnchors:[{anchorColumnId:'_key',kind:'root',nodeId:'patient',resourceType:'Patient',label:'Original Patient record'}]}};
  const client = useMemo(()=>({
    getConstructionCapabilities: async () => capabilities,
    searchRelatedExpandChoices: async () => ({snapshotToken:catalog.snapshotToken,draftVersion:saved.version,draftDigest:`digest-${saved.version}`,outputId:'patients',stageId:'source_projection',complete:true,truncated:false,choices:['route-A','route-B'].map(choiceId=>({choiceId,anchorColumnId:'_key',kind:'root',nodeId:'patient',resourceType:'Patient',label:'Original Patient record',targetNodeId:'observation',targetResourceType:'Observation',route:route(choiceId)}))}),
    searchRelatedExpandContributors: async (args: {routeChoiceId:string}) => ({snapshotToken:catalog.snapshotToken,draftVersion:saved.version,draftDigest:`digest-${saved.version}`,outputId:'patients',stageId:'source_projection',routeChoiceId:args.routeChoiceId,complete:true,truncated:false,choices:[]}),
    proposeConstruction: async (args: {candidateConstruction:Construction}) => {
      const response: ConstructionProposalResponse = {proposalId:crypto.randomUUID(),outputId:'patients',snapshotToken:catalog.snapshotToken,draftVersion:saved.version,draftDigest:`digest-${saved.version}`,baseDocumentDigest:'base',candidateWorkspaceDigest:'candidate',changedStepId:'saved-related',candidateConstruction:args.candidateConstruction,dependencyImpact:{affectedStepIds:[]},stages:[],previewStatus:'READY',previewDurationMs:1};
      const complete = {...response,preview:previewFor(args.candidateConstruction,response.proposalId!)};
      if(hold) {setHold(false); return new Promise<ConstructionProposalResponse>(resolve=>setPending(()=>()=>{setReleased(n=>n+1);resolve(complete);}));}
      return complete;
    },
    preview: async () => {throw new Error('Unexpected fallback preview');},
    discoverConstructionCategories: async () => {throw new Error('Unexpected category request');},
  }),[saved,hold]);
  const lifecycle = useConstructionLifecycle({client,capabilitiesRequest:{project:'fixture/project',explorerId:'browser-routes',snapshotToken:catalog.snapshotToken,expectedDraftVersion:saved.version,expectedDraftDigest:`digest-${saved.version}`,outputId:'patients',stageId:'source_projection'}});
  const cancel=()=>{lifecycle.cancel();setEpoch(n=>n+1);};
  const apply=()=>{const attempt=lifecycle.beginApply(); if(!attempt || lifecycle.proposal.status!=='ready') return; const next={construction:lifecycle.proposal.response.candidateConstruction,version:saved.version+1,writes:saved.writes+1}; if(!attempt.finish(true)) return; sessionStorage.setItem('route-saved',JSON.stringify(next));setSaved(next);setEpoch(n=>n+1);};
  const shown=lifecycle.proposal.status==='ready'||lifecycle.proposal.status==='applying'?lifecycle.proposal.preview:previewFor(saved.construction,'saved');
  return <LoomProvider client={client as unknown as LoomClient}>
    <h2>Saved related route browser checkpoint</h2>
    <output data-testid="route-saved">{JSON.stringify({route:routeId(saved.construction),version:saved.version,writes:saved.writes})}</output>
    <output data-testid="route-proposal">{lifecycle.proposal.status}</output>
    <button onClick={()=>setHold(true)} data-testid="route-hold">Hold next response</button>
    <button disabled={!pending} onClick={()=>{pending?.();setPending(undefined);}} data-testid="route-release">Release old response</button>
    <output data-testid="route-released">{released}</output>
    <RelatedExpandEditor key={`${saved.version}/${epoch}`} project="fixture/project" explorerId="browser-routes" snapshotToken={catalog.snapshotToken} outputId="patients" catalog={catalog} construction={saved.construction} capabilities={capabilities} step={saved.construction.steps[0] as Parameters<typeof RelatedExpandEditor>[0]['step']} disabled={false} onCandidateChange={lifecycle.onCandidateChange}/>
    <ConstructionProposalPanel state={lifecycle.proposal} canApply={lifecycle.canApply} baseConstruction={saved.construction} onApply={apply} onCancel={cancel} onRetry={lifecycle.retry}/>
    <section data-testid="route-preview"><PreviewTable preview={shown} limit={25} onLimitChange={()=>undefined} onColumnChange={()=>undefined} onColumnsChange={()=>undefined}/></section>
  </LoomProvider>;
}
