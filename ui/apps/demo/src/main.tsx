import React from 'react';
import { createRoot } from 'react-dom/client';
import '@mantine/core/styles.css';
import '@calypr/loom-ui/styles.css';
import './styles.css';

const params = new URLSearchParams(window.location.search);
const project = params.get('project') ?? import.meta.env.VITE_LOOM_PROJECT ?? 'NCPI_ACCEPTANCE';
const explorerId = params.get('explorer') ?? import.meta.env.VITE_LOOM_EXPLORER ?? 'default';
const mode = params.get('mode') ?? import.meta.env.VITE_LOOM_MODE ?? 'builder';
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
  const [currentMode, setMode] = React.useState(mode === 'viewer' ? 'viewer' : 'builder');
  return (
    <div className="demo-shell">
      <header className="demo-header">
        <div><span className="demo-mark">LOOM</span><strong>FHIR Explorer Studio</strong></div>
        <div className="demo-controls">
          <span>{project} / {explorerId}</span>
          <button type="button" className={currentMode === 'builder' ? 'active' : ''} onClick={() => setMode('builder')}>Builder</button>
          <button type="button" className={currentMode === 'viewer' ? 'active' : ''} onClick={() => setMode('viewer')}>Viewer</button>
        </div>
      </header>
      <div className="demo-content">
        <React.Suspense fallback={<div className="p-6 text-sm text-slate-500">Loading Explorer…</div>}>
          {currentMode === 'builder'
            ? <LoomExplorerBuilder project={project} explorerId={explorerId} />
            : <LoomExplorerViewer project={project} explorerId={explorerId} />}
        </React.Suspense>
      </div>
    </div>
  );
};

const root = document.getElementById('root');
if (!root) throw new Error('Loom demo root element is missing.');
createRoot(root).render(<App />);
