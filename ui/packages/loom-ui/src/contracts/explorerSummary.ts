export interface ExplorerSummary {
  readonly project: string;
  readonly explorerId: string;
  readonly title: string;
  readonly management: string;
  readonly activeRevisionId?: string;
  readonly updatedAt: string;
}
