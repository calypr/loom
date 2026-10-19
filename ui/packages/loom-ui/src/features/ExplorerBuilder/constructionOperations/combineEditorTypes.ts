export type ConstructionCombineKind = 'KEY_JOIN' | 'APPEND' | 'MEMBERSHIP';

export interface ConstructionCombineColumn {
  readonly id: string;
  readonly name: string;
  readonly label: string;
  readonly type: string;
  readonly nullable: boolean;
  readonly repeated: boolean;
  readonly clickhouseType?: string;
  readonly joinCompatibilityKey?: string;
  readonly appendCompatibilityKey?: string;
  readonly semanticPath?: string;
  readonly cardinality?: 'required_one' | 'optional_one' | 'many';
}

export interface ConstructionCombinePublishedColumn extends ConstructionCombineColumn {
  readonly clickhouseType: string;
}

export interface ConstructionCombinePublishedRevision {
  readonly kind: 'TABLE_REVISION';
  readonly tableId: string;
  readonly revisionId: string;
  readonly outputId: string;
  readonly tableTitle: string;
  readonly outputTitle: string;
  readonly rowMeaning: string;
  readonly isCurrent: boolean;
  readonly columns: ReadonlyArray<ConstructionCombinePublishedColumn>;
}

export interface ConstructionCombineWorkspaceColumn extends ConstructionCombineColumn {
  readonly cardinality: 'required_one' | 'optional_one' | 'many';
  readonly joinCompatibilityKey?: string;
  readonly appendCompatibilityKey?: string;
}

export interface ConstructionCombineWorkspaceOutput {
  readonly kind: 'WORKSPACE_OUTPUT';
  readonly outputId: string;
  readonly title: string;
  readonly columns: ReadonlyArray<ConstructionCombineWorkspaceColumn>;
}

export type ConstructionCombineSource = ConstructionCombinePublishedRevision | ConstructionCombineWorkspaceOutput;

export type ConstructionCombineCatalog =
  | { readonly kind: 'loading' }
  | { readonly kind: 'failed'; readonly message: string }
  | {
      readonly kind: 'ready';
      readonly revisions: ReadonlyArray<ConstructionCombinePublishedRevision>;
      readonly nextCursor?: string;
    };

export type ConstructionCombineInputRef =
  | {
      kind: 'TABLE_REVISION';
      tableId: string;
      revisionId: string;
      outputId: string;
    }
  | { kind: 'WORKSPACE_OUTPUT'; outputId: string };

export interface ConstructionCombineKey {
  leftColumnId: string;
  rightColumnId: string;
}

export interface ConstructionCombineProjection {
  outputColumnId: string;
  inputIndex: number;
  inputColumnId: string;
}

export interface ConstructionCombineOutputColumn {
  id: string;
  name: string;
  label: string;
  type: string;
  nullable: boolean;
}

export type ConstructionCombineOperation =
  | {
      kind: 'KEY_JOIN';
      keys: ConstructionCombineKey[];
      projections: ConstructionCombineProjection[];
      joinType: 'INNER' | 'LEFT';
      rightMatchPolicy: 'PRESERVE_ALL';
    }
  | {
      kind: 'APPEND';
      projections: ConstructionCombineProjection[];
    }
  | {
      kind: 'MEMBERSHIP';
      keys: ConstructionCombineKey[];
      projections: ConstructionCombineProjection[];
      membershipMode: 'INCLUDE' | 'EXCLUDE';
    };

export interface ConstructionCombineStep {
  id: string;
  inputs: ConstructionCombineInputRef[];
  operation: { kind: 'COMBINE'; combine: ConstructionCombineOperation };
  outputs: ConstructionCombineOutputColumn[];
}

export interface ConstructionCombineCandidateIntent {
  candidateConstruction: {
    version: number;
    steps: ConstructionCombineStep[];
  };
  changedStepId: string;
}
