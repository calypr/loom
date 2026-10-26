import { cdaCurrentDraftMembershipWorkflow } from './cda-current-draft-membership-workflow.mjs';

export const cdaPublishedMembershipWorkflow = ({ page, cda }) =>
  cdaCurrentDraftMembershipWorkflow({ page, cda, sourceMode: 'published' });
