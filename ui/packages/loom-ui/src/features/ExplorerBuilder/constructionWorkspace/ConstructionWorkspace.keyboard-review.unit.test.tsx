// @vitest-environment jsdom
import React from 'react';
import {cleanup,fireEvent,render,screen} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {afterEach,expect,it,vi} from 'vitest';
import {ConstructionWorkspace} from './ConstructionWorkspace';
afterEach(cleanup);
const view=(editor:React.ReactNode=<input aria-label="Draft field" defaultValue="draft"/>)=><ConstructionWorkspace tables={[]} onSelectTable={vi.fn()} onNewTable={vi.fn()} onDuplicateTable={vi.fn()} onDeleteTable={vi.fn()} onRenameTable={vi.fn()} onSelectFamily={vi.fn()} network={<button>Graph node</button>} editor={editor} preview={<button>Preview row</button>} previewStatus="ready" draftVersion={1} draftDigest="draft"/>;
it('review: keyboard Tab stays in the visible expanded graph panel',async()=>{
 render(view());
 const expand=screen.getByRole('region',{name:'Related records'});expand.focus();fireEvent.keyDown(expand,{key:'Enter'});
 const panel=screen.getByRole('region',{name:'Related records'});
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Back from expanded Related records'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Graph node'}));
 await userEvent.tab();
 expect(panel.contains(document.activeElement), `Focus escaped to ${document.activeElement?.getAttribute("aria-label")}`).toBe(true);
});
it('review: basic graph Tab and Shift Tab wrap within expanded panel',async()=>{
 render(view());const expand=screen.getByRole('region',{name:'Related records'});expand.focus();fireEvent.keyDown(expand,{key:'Enter'});
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Back from expanded Related records'}));
 await userEvent.tab({shift:true});expect(document.activeElement).toBe(screen.getByRole('button',{name:'Graph node'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Back from expanded Related records'}));
});
