import React from 'react';
import { createRoot } from 'react-dom/client';
import { LoomExplorerBuilder, LoomExplorerViewer, createLoomClient } from '@calypr/loom-ui';
import '@calypr/loom-ui/styles.css';
import './styles.css';

const params = new URLSearchParams(window.location.search);
const project = params.get('project') ?? import.meta.env.VITE_LOOM_PROJECT ?? 'NCPI_ACCEPTANCE';
const explorerId = params.get('explorer') ?? import.meta.env.VITE_LOOM_EXPLORER ?? 'default';
const mode = params.get('mode') ?? import.meta.env.VITE_LOOM_MODE ?? 'builder';
const client = createLoomClient({
  baseUrl: import.meta.env.VITE_LOOM_BASE_URL ?? '/',
});

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
        {currentMode === 'builder'
          ? <LoomExplorerBuilder client={client} project={project} explorerId={explorerId} />
          : <LoomExplorerViewer client={client} project={project} explorerId={explorerId} />}
      </div>
    </div>
  );
};

const root = document.getElementById('root');
if (!root) throw new Error('Loom demo root element is missing.');
createRoot(root).render(<App />);
