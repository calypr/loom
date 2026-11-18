import assert from 'node:assert/strict';
import test from 'node:test';
import { resolve } from 'node:path';
import { readFixture, withFreshUIExplorer, createResourceTableInUI, api, assertRowsMatch } from './app.mjs';

test('the Patient→Observation network follows fixture references through related rows and reload', { timeout: 180000 }, async () => {
  const patients = await readFixture('Patient');
  const observations = await readFixture('Observation');
  const patientsById = new Map(patients.map(patient => [patient.id, patient]));
  const expected = observations.flatMap(observation => {
    const patientId = observation.subject?.reference?.split('/').at(-1);
    return patientId && patientsById.has(patientId) ? [[patientId, observation.id]] : [];
  });
  assert.equal(expected.length, 3, 'All three subject-linked Observation records have an independent Patient oracle');

  await withFreshUIExplorer(
    'record-network',
    (scenario, page) => createResourceTableInUI(scenario, page, 'Patient'),
    async ({ scenario, page }) => {
      const connectionSelector = 'button[aria-label="Add Observation via subject_Patient"]';
      const nextConnectionSelector = 'button[aria-label="Next available connections"]:not(:disabled)';
      for (let pageIndex = 0; pageIndex < 20; pageIndex += 1) {
        if (await page.evaluate(`Boolean(document.querySelector(${JSON.stringify(connectionSelector)}))`)) break;
        const previousPage = await page.evaluate(`document.querySelector('[data-testid="traversal-discovery-layer"]')?.innerText ?? ''`);
        const next = await page.evaluate(`Boolean(document.querySelector(${JSON.stringify(nextConnectionSelector)}))`);
        assert(next, `The network UI offers Patient→Observation within its available-connection pages; current=${JSON.stringify(previousPage)}`);
        await page.click(nextConnectionSelector);
        await page.until(() => page.evaluate(`(document.querySelector('[data-testid="traversal-discovery-layer"]')?.innerText ?? '') !== ${JSON.stringify(previousPage)}`), 'available connection page advances');
      }
      await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(connectionSelector)}))`), 'fixture Patient→Observation graph connection');
      await page.evaluate(`(() => {window.__e2eNetworkEvents=[];for(const type of ['pointerdown','mousedown','pointerup','mouseup','click'])document.addEventListener(type,event=>window.__e2eNetworkEvents.push({type,target:event.target?.tagName,label:event.target?.closest?.('button')?.getAttribute('aria-label'),x:event.clientX,y:event.clientY,defaultPrevented:event.defaultPrevented}),true)})()`);
      await page.clickLabel('Add Observation via subject_Patient');
      try {
        await page.until(async () => {
          const builder = await api(`${scenario.base}/builder`);
          return builder.workspace.documents[0].route.children?.some(child =>
            child.resourceType === 'Observation' && child.relationship === 'subject_Patient',
          );
        }, 'Patient → Observation relationship saved through the network UI');
      } catch (error) {
        const builder = await api(`${scenario.base}/builder`);
        const diagnostics = await page.evaluate(`({events:window.__e2eNetworkEvents,buttons:Array.from(document.querySelectorAll('button'),button=>({label:button.getAttribute('aria-label'),disabled:button.disabled,text:button.innerText.trim()})).filter(button=>button.label?.includes('Observation')||button.text.includes('Observation')),alerts:Array.from(document.querySelectorAll('[role="alert"]'),item=>item.innerText),requests:performance.getEntriesByType('resource').map(item=>item.name).filter(name=>name.includes('/api/')).slice(-20),text:document.body.innerText.slice(-900)})`);
        throw new Error(`${error.message}; click=${JSON.stringify(page.lastClick)}; route=${JSON.stringify(builder.workspace.documents[0].route)}; UI=${JSON.stringify(diagnostics)}`);
      }
      const nativeClickEvents = await page.evaluate('window.__e2eNetworkEvents');
      assert(nativeClickEvents.some(event => event.type === 'click' && event.label === 'Add Observation via subject_Patient'), `The native click reaches the Patient→Observation control: ${JSON.stringify(nativeClickEvents)}`);
      assert(page.lastClick?.geometry.hitPath.some(node => node.label === 'Add Observation via subject_Patient'), `The dispatched CSS point hits the Patient→Observation button: ${JSON.stringify(page.lastClick)}`);
      await page.reload();
      await page.until(() => page.evaluate(`Boolean([...document.querySelectorAll('button')].find(button => (button.getAttribute('aria-label') || '').startsWith('Patient → subject_Patient → Observation')))`), 'saved Patient → Observation graph path');
      const beforeExpansion = await api(`${scenario.base}/builder`);

      const configureExpansion = async () => {
        await page.openRowSettings();
        await page.click('[data-testid="construction-action-related-rows"]');
        const editor = '[data-testid="construction-related-expand-editor"]';
        await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(`${editor} select[aria-label="Related record type"]`)}))`), 'related-record row editor');
        await page.selectOptionMatching(`${editor} select[aria-label="Related record type"]`, 'Observation');
        await page.until(() => page.evaluate(`Boolean([...document.querySelectorAll(${JSON.stringify(`${editor} input[type="radio"]`)} )].find(input => (input.getAttribute('aria-label') || '').includes('Patient <-[subject]- Observation')))`), 'Patient <- subject -> Observation relationship choice');
        const routeChoice = await page.evaluate(`(() => {const root=document.querySelector(${JSON.stringify(editor)});const input=[...(root?.querySelectorAll('input[type="radio"]')??[])].find(item=>(item.getAttribute('aria-label')||'').includes('Patient <-[subject]- Observation'));if(!input)return null;input.setAttribute('data-e2e-related-route','selected');return '[data-e2e-related-route="selected"]'})()`);
        assert(routeChoice, 'The related-row UI exposes the exact Patient-to-Observation path');
        await page.click(routeChoice);
        try {
          await page.until(() => page.evaluate(`document.querySelector('[data-testid="construction-proposal-panel"]')?.dataset.proposalStatus === 'ready'`), 'related Observation rows proposal');
        } catch (error) {
          const diagnostics = await page.evaluate(`JSON.stringify({proposal:(()=>{const item=document.querySelector('[data-testid="construction-proposal-panel"]');return item?{status:item.dataset.proposalStatus,receipt:item.dataset.proposalId,digest:item.dataset.proposalDraftDigest,outputId:item.dataset.proposalOutputId,previewStatus:item.dataset.proposalPreviewStatus,text:item.innerText}:null})(),editor:document.querySelector('[data-testid="construction-related-expand-editor"]')?.innerText,alerts:Array.from(document.querySelectorAll('[role="alert"]'),item=>item.innerText),statuses:Array.from(document.querySelectorAll('[role="status"]'),item=>item.innerText.trim()).filter(Boolean),table:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText,requests:performance.getEntriesByType('resource').map(item=>item.name).filter(name=>name.includes('/authoring/v2/construction-proposals')||name.includes('/authoring/v2/preview')||name.includes('/authoring/v2/construction-capabilities'))})`);
          throw new Error(`${error.message}; related proposal diagnostics=${diagnostics}; recent CDP=${JSON.stringify(page.devtoolsEvents.slice(-25))}`);
        }

        const rows = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid="construction-proposal-preview-row"]'),row=>Array.from(row.querySelectorAll('td'),cell=>cell.innerText.trim()))`);
        assert.equal(rows.length, expected.length, 'Proposal rows equal the number of literal Observations with a Patient reference');
        for (const [patientId, observationId] of expected) {
          assert(rows.some(row => row.includes(patientId) && row.includes(observationId)), `Proposal contains fixture link ${patientId} → ${observationId}`);
        }
      };

      await configureExpansion();
      await page.pressEscape();
      await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'related-row proposal canceled');
      assert.equal((await api(`${scenario.base}/builder`)).draftDigest, beforeExpansion.draftDigest, 'Cancel leaves the saved Patient→Observation route unchanged');

      await configureExpansion();
      await page.click('[data-testid="construction-apply-proposal"]');
      await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'related Observation rows applied');
      const applied = await api(`${scenario.base}/builder`);
      assert.notEqual(applied.draftDigest, beforeExpansion.draftDigest, 'Apply saves the related-row expansion');
      const expansion = applied.workspace.documents[0].construction.steps.at(-1);
      assert.equal(expansion.operation.kind, 'RELATED_EXPAND');
      assert.equal(expansion.operation.relatedExpand.targetResourceType, 'Observation');
      assert.equal(expansion.operation.relatedExpand.route[0].relationship, 'subject_Patient');

      const verifyRows = async label => {
        const rows = await page.until(async () => {
          const current = await page.tableRows();
          return current.length ? current : false;
        }, label);
        assert.equal(rows.length, expected.length);
        for (const [patientId, observationId] of expected) {
          assert(rows.some(row => row.includes(patientId) && row.includes(observationId)), `Saved rows contain fixture link ${patientId} → ${observationId}`);
        }
      };
      await verifyRows('expanded Patient→Observation rows');
      await page.reload();
      assert.equal((await api(`${scenario.base}/builder`)).draftDigest, applied.draftDigest, 'Reload preserves the exact related-row construction');
      await verifyRows('reloaded Patient→Observation rows');
    },
  );
});

test('DocumentReference rows follow the deep Specimen-to-Patient path and preserve the unmatched ResearchStudy row', { timeout: 240000 }, async () => {
  const fixtureDirectory = resolve(import.meta.dirname, '../../testdata/traversal-first');
  const documents = await readFixture('DocumentReference', fixtureDirectory);
  const specimens = await readFixture('Specimen', fixtureDirectory);
  const patients = await readFixture('Patient', fixtureDirectory);
  const specimenById = new Map(specimens.map(specimen => [specimen.id, specimen]));
  const patientById = new Map(patients.map(patient => [patient.id, patient]));
  const expected = documents.map(document => {
    const [subjectType, subjectId] = String(document.subject?.reference ?? '').split('/');
    const specimen = subjectType === 'Specimen' ? specimenById.get(subjectId) : undefined;
    const [patientType, patientId] = String(specimen?.subject?.reference ?? '').split('/');
    return [document.id, patientType === 'Patient' && patientById.has(patientId) ? patientId : null];
  });
  assert.deepEqual(expected, [['document-one', 'p-one'], ['document-study', null]],
    'The oracle follows literal DocumentReference.subject → Specimen.subject → Patient references; ResearchStudy is deliberately unmatched.');

  await withFreshUIExplorer(
    'deep-document-patient',
    (scenario, page) => createResourceTableInUI(scenario, page, 'DocumentReference'),
    async ({ scenario, page }) => {
      const connectionPage = '[data-testid="traversal-discovery-layer"]';
      const nextConnection = 'button[aria-label="Next available connections"]:not(:disabled)';
      const addConnection = async (label, parentOccurrenceId, routeMatches) => {
        if (parentOccurrenceId) {
          const occurrenceSelector = `[data-testid="authored-traversal-editor"] button[data-occurrence-selector="true"][data-occurrence-id=${JSON.stringify(parentOccurrenceId)}]`;
          await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(occurrenceSelector)}))`), `authored ${label} parent occurrence`);
          await page.click(occurrenceSelector);
        }
        const buttonSelector = `button[aria-label=${JSON.stringify(label)}]`;
        for (let index = 0; index < 20; index += 1) {
          if (await page.evaluate(`Boolean(document.querySelector(${JSON.stringify(buttonSelector)}))`)) break;
          const previous = await page.evaluate(`document.querySelector(${JSON.stringify(connectionPage)})?.innerText ?? ''`);
          assert(await page.evaluate(`Boolean(document.querySelector(${JSON.stringify(nextConnection)}))`),
            `The graph UI offers ${label}; current=${JSON.stringify(previous)}`);
          await page.click(nextConnection);
          await page.until(() => page.evaluate(`(document.querySelector(${JSON.stringify(connectionPage)})?.innerText ?? '') !== ${JSON.stringify(previous)}`), 'available connection page advances');
        }
        await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(buttonSelector)}))`), `graph connection ${label}`);
        await page.clickLabel(label);
        return page.until(async () => {
          const state = await api(`${scenario.base}/builder`);
          return routeMatches(state.workspace.documents[0].route) ? state : false;
        }, `saved graph connection ${label}`);
      };

      const firstHop = await addConnection('Add Specimen via subject_Specimen', undefined, route =>
        route.children?.some(child => child.resourceType === 'Specimen' && child.relationship === 'subject_Specimen'));
      const specimenOccurrence = firstHop.workspace.documents[0].route.children.find(child =>
        child.resourceType === 'Specimen' && child.relationship === 'subject_Specimen');
      assert(specimenOccurrence?.occurrenceId, 'The saved Specimen hop has an occurrence identity');
      const authored = await addConnection('Add Patient via subject_Patient', specimenOccurrence.occurrenceId, route =>
        route.children?.find(child => child.resourceType === 'Specimen')?.children?.some(child =>
          child.resourceType === 'Patient' && child.relationship === 'subject_Patient'));
      const routeDocument = authored.workspace.documents[0];
      const savedSpecimen = routeDocument.route.children.find(child => child.resourceType === 'Specimen');
      const savedPatient = savedSpecimen?.children?.find(child => child.resourceType === 'Patient');
      assert(savedPatient?.occurrenceId, 'The saved route contains the Patient child of the Specimen occurrence');
      assert.deepEqual([
        [savedSpecimen.relationship, savedSpecimen.resourceType],
        [savedPatient.relationship, savedPatient.resourceType],
      ], [['subject_Specimen', 'Specimen'], ['subject_Patient', 'Patient']]);

      const beforeExpansion = authored;
      const configureExpansion = async () => {
        const rootScopeSelector = `[data-testid="authored-traversal-editor"] button[data-occurrence-selector="true"][data-occurrence-id=${JSON.stringify(routeDocument.route.occurrenceId)}]`;
        await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(rootScopeSelector)}))`), 'DocumentReference root scope selector');
        await page.click(rootScopeSelector);
        await page.openRowSettings();
        await page.click('[data-testid="construction-action-related-rows"]');
        const editor = '[data-testid="construction-related-expand-editor"]';
        await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(`${editor} select[aria-label="Related record type"]`)}))`), 'deep RelatedExpand editor');
        await page.selectOptionMatching(`${editor} select[aria-label="Related record type"]`, 'Patient');
        const deepPathLabel = 'DocumentReference -[subject]-> Specimen -[subject]-> Patient';
        const routeSelector = `${editor} input[type="radio"][aria-label=${JSON.stringify(deepPathLabel)}]`;
        await page.until(() => page.evaluate(`Boolean(document.querySelector(${JSON.stringify(routeSelector)}))`), 'DocumentReference → Specimen → Patient related path');
        const visibleRoute = await page.evaluate(`(() => {const route=document.querySelector(${JSON.stringify(routeSelector)});if(!route)return null;const details=route.closest('details');if(details&&!details.open){details.querySelector('summary')?.click();}route.setAttribute('data-e2e-deep-route','selected');return '[data-e2e-deep-route="selected"]'})()`);
        assert(visibleRoute, 'The RelatedExpand editor exposes the exact two-hop path');
        await page.click(visibleRoute);
        await page.select('[aria-label="If a current row has no matches"]', 'PRESERVE_PARENT');
        await page.until(() => page.evaluate('document.querySelector("[data-testid=construction-proposal-panel]")?.dataset.proposalStatus === "ready"'), 'deep related-row proposal');
        const rows = await page.evaluate(`Array.from(document.querySelectorAll('[data-testid="construction-proposal-preview-row"]'),row=>Array.from(row.querySelectorAll('td'),cell=>cell.innerText.trim()))`);
        assert.equal(rows.length, expected.length, 'Deep expansion retains each DocumentReference source row');
        assert(rows.every(row => row.length === 2), `Preview consists of DocumentReference ID and related Patient ID: ${JSON.stringify(rows)}`);
        assertRowsMatch(rows.map(row => [row[0], row[1] === '—' || row[1] === '' ? null : row[1]]), expected,
          'deep DocumentReference→Specimen→Patient proposal rows');
      };

      await configureExpansion();
      await page.pressEscape();
      await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'deep expansion canceled');
      assert.equal((await api(`${scenario.base}/builder`)).draftDigest, beforeExpansion.draftDigest, 'Cancel preserves both saved graph hops');

      await configureExpansion();
      await page.click('[data-testid="construction-apply-proposal"]');
      await page.until(() => page.evaluate('!document.querySelector("[data-testid=construction-proposal-panel]")'), 'deep expansion applied');
      const applied = await api(`${scenario.base}/builder`);
      const document = applied.workspace.documents[0];
      const expansion = document.construction.steps.at(-1);
      assert.equal(expansion.operation.kind, 'RELATED_EXPAND');
      assert.equal(expansion.operation.relatedExpand.targetResourceType, 'Patient');
      assert.equal(expansion.operation.relatedExpand.emptyPolicy, 'PRESERVE_PARENT');
      assert.deepEqual(expansion.operation.relatedExpand.route.map(edge => [edge.fromResourceType, edge.relationship, edge.toResourceType]), [
        ['DocumentReference', 'subject_Specimen', 'Specimen'],
        ['Specimen', 'subject_Patient', 'Patient'],
      ]);
      const rootColumn = document.columns.find(column => column.source?.field?.path === 'id');
      const relatedColumn = document.construction.steps.flatMap(step => step.outputs ?? [])
        .find(column => column.id === expansion.operation.relatedExpand.relatedRecordColumnId);
      const resultingColumns = [
        ...document.columns,
        ...document.construction.steps.flatMap(step => step.outputs ?? [])
          .filter(output => !document.columns.some(column => column.columnId === output.id)),
      ];
      const rootColumnIndex = rootColumn ? resultingColumns.findIndex(column => column.columnId === rootColumn.columnId) : -1;
      const relatedColumnIndex = relatedColumn ? resultingColumns.findIndex(column => column.id === relatedColumn.id) : -1;
      assert(rootColumn && relatedColumn && rootColumnIndex >= 0 && relatedColumnIndex >= 0,
        'Saved row schema exposes the DocumentReference and Patient identity columns');
      const expectedRows = expected.map(([documentId, patientId]) => [documentId, patientId ?? '—']);
      const assertSavedRows = async label => {
        const canonical = rows => rows.map(row => JSON.stringify(row)).sort();
        let lastRows = [];
        try {
          lastRows = await page.until(async () => {
            const current = await page.tableRows();
            if (!current.length) return false;
            lastRows = current;
            const actual = current.map(row => [row[rootColumnIndex], row[relatedColumnIndex]]);
            return canonical(actual).join('\n') === canonical(expectedRows).join('\n') ? current : false;
          }, `${label} match the exact related-row oracle`, 20000);
        } catch (error) {
          const diagnostics = await page.evaluate(`JSON.stringify({table:document.querySelector('[data-testid="preview-table-scroll"]')?.innerText,rows:Array.from(document.querySelectorAll('[data-testid="preview-table-scroll"] [role="row"]'),row=>Array.from(row.querySelectorAll('[role="cell"]'),cell=>cell.innerText.trim())),statuses:Array.from(document.querySelectorAll('[role="status"]'),item=>item.innerText.trim()).filter(Boolean),proposal:(()=>{const item=document.querySelector('[data-testid="construction-proposal-panel"]');return item?{status:item.dataset.proposalStatus,digest:item.dataset.proposalDraftDigest,receipt:item.dataset.proposalId}:null})(),requests:performance.getEntriesByType('resource').map(item=>item.name).filter(name=>name.includes('/authoring/v2/preview')||name.includes('/construction-proposal'))})`);
          throw new Error(`${error.message}; lastRows=${JSON.stringify(lastRows)}; visible diagnostics=${diagnostics}; recent CDP=${JSON.stringify(page.devtoolsEvents.slice(-25))}`);
        }
        const actual = lastRows.map(row => [row[rootColumnIndex], row[relatedColumnIndex]]);
        assert.equal(actual.length, expected.length);
        assertRowsMatch(actual, expectedRows, label);
      };
      await assertSavedRows('applied deep-path rows');
      await page.reload();
      assert.equal((await api(`${scenario.base}/builder`)).draftDigest, applied.draftDigest, 'Reload preserves the deep graph and RelatedExpand construction');
      await assertSavedRows('reloaded deep-path rows');
    },
    { fixtureDirectory },
  );
});
