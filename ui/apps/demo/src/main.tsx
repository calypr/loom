import React from 'react';
import { createRoot } from 'react-dom/client';
import '@mantine/core/styles.css';
import '@calypr/loom-ui/styles.css';
import './styles.css';

const params = new URLSearchParams(window.location.search);
const project = params.get('project') ?? import.meta.env.VITE_LOOM_PROJECT ?? 'NCPI_ACCEPTANCE';
const explorerId = params.get('explorer') ?? import.meta.env.VITE_LOOM_EXPLORER ?? 'default';
const selectionRevisionId = params.get('selection') ?? undefined;
const mode = params.get('mode') ?? import.meta.env.VITE_LOOM_MODE ?? 'builder';
const initialFeatureFocus = params.get('focusOutput') && params.get('focusColumn')
  ? { outputId: params.get('focusOutput')!, column: params.get('focusColumn')!, label: params.get('focusLabel') ?? undefined }
  : undefined;
const baseUrl = import.meta.env.VITE_LOOM_BASE_URL ?? '/';

const LoomExplorerBuilder = React.lazy(() =>
  import('@calypr/loom-ui/builder').then(({ LoomExplorerBuilder: Builder, createLoomClient }) => ({
    default: (props: React.ComponentProps<typeof Builder>) => {
      const client = React.useMemo(() => createLoomClient({ baseUrl }), []);
      return <Builder {...props} client={client} />;
    },
  })),
);

const LoomExplorerViewer = React.lazy(() =>
  import('@calypr/loom-ui/viewer').then(({ LoomExplorerViewer: Viewer, createLoomClient }) => ({
    default: (props: React.ComponentProps<typeof Viewer>) => {
      const client = React.useMemo(() => createLoomClient({ baseUrl }), []);
      return <Viewer {...props} client={client} />;
    },
  })),
);

const App = () => {
  const [selectedExplorerId, setSelectedExplorerId] = React.useState(explorerId);
  const [currentMode, setMode] = React.useState(mode === 'viewer' ? 'viewer' : 'builder');
  const [featureFocus, setFeatureFocus] = React.useState(initialFeatureFocus);
  const setCurrentMode = React.useCallback((nextMode: 'builder' | 'viewer') => {
    setMode(nextMode);
    const nextURL = new URL(window.location.href);
    nextURL.searchParams.set('project', project);
    nextURL.searchParams.set('explorer', selectedExplorerId);
    nextURL.searchParams.set('mode', nextMode);
    window.history.replaceState({}, '', nextURL);
  }, [selectedExplorerId]);
  const onExplorerChange = React.useCallback((nextExplorerId: string) => {
    setSelectedExplorerId(nextExplorerId);
    const nextURL = new URL(window.location.href);
    nextURL.searchParams.set('project', project);
    nextURL.searchParams.set('explorer', nextExplorerId);
    window.history.replaceState({}, '', nextURL);
  }, []);
  const repairFeature = React.useCallback((focus: { outputId: string; column: string; label: string }) => {
    setFeatureFocus(focus);
    setMode('builder');
    const nextURL = new URL(window.location.href);
    nextURL.searchParams.set('project', project);
    nextURL.searchParams.set('explorer', selectedExplorerId);
    nextURL.searchParams.set('mode', 'builder');
    nextURL.searchParams.set('focusOutput', focus.outputId);
    nextURL.searchParams.set('focusColumn', focus.column);
    nextURL.searchParams.set('focusLabel', focus.label);
    window.history.replaceState({}, '', nextURL);
  }, [selectedExplorerId]);
  return (
    <div className="demo-shell">
      <header className="demo-header">
        <div><span className="demo-mark">LOOM</span><strong>FHIR Explorer Studio</strong></div>
        <div className="demo-controls">
          <span>{project} / {selectedExplorerId}</span>
          <button type="button" className={currentMode === 'builder' ? 'active' : ''} onClick={() => setCurrentMode('builder')}>Builder</button>
          <button type="button" className={currentMode === 'viewer' ? 'active' : ''} onClick={() => setCurrentMode('viewer')}>Viewer</button>
        </div>
      </header>
      <div className="demo-content">
        <React.Suspense fallback={<div className="p-6 text-sm text-slate-500">Loading Explorer…</div>}>
          {currentMode === 'builder'
            ? <LoomExplorerBuilder project={project} explorerId={selectedExplorerId} selectionRevisionId={selectionRevisionId} onExplorerChange={onExplorerChange} featureFocus={featureFocus} />
            : <LoomExplorerViewer project={project} explorerId={selectedExplorerId} onRepairFeature={repairFeature} />}
        </React.Suspense>
      </div>
    </div>
  );
};

const root = document.getElementById('root');
if (!root) throw new Error('Loom demo root element is missing.');
createRoot(root).render(<App />);
