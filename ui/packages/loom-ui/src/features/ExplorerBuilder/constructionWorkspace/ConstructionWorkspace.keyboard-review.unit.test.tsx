// @vitest-environment jsdom
import React from 'react';
import {createPortal} from 'react-dom';
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
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Related records'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Graph node'}));
 await userEvent.tab();
 expect(panel.contains(document.activeElement), `Focus escaped to ${document.activeElement?.getAttribute("aria-label")}`).toBe(true);
});
it('review: immediate Escape and nested popup ownership preserve editor draft',()=>{
 render(view(<><input aria-label="Draft field" defaultValue="draft"/><button onKeyDown={event=>event.preventDefault()}>Handled nested Escape</button>{createPortal(<div role="dialog" aria-label="Nested editor dialog"><input aria-label="Nested dialog field"/></div>,document.body)}</>));
 const expand=screen.getByRole('complementary',{name:'Recipe editor'});expand.focus();fireEvent.keyDown(expand,{key:'Enter'});
 const panel=screen.getByRole('complementary',{name:'Recipe editor'});
 fireEvent.keyDown(expand,{key:'Escape'});expect(panel).not.toHaveAttribute('data-expanded');expect(document.activeElement).toBe(expand);
 fireEvent.keyDown(expand,{key:'Enter'});const field=screen.getByRole('textbox',{name:'Draft field'});fireEvent.change(field,{target:{value:'changed'}});
 fireEvent.keyDown(screen.getByRole('textbox',{name:'Nested dialog field'}),{key:'Escape'});expect(panel).toHaveAttribute('data-expanded','true');
 fireEvent.keyDown(screen.getByRole('button',{name:'Handled nested Escape'}),{key:'Escape'});expect(panel).toHaveAttribute('data-expanded','true');
 field.focus();fireEvent.keyDown(field,{key:'Escape'});expect(panel).not.toHaveAttribute('data-expanded');expect(document.activeElement).toBe(expand);expect(screen.getByRole('textbox',{name:'Draft field'})).toBe(field);expect(field instanceof HTMLInputElement && field.value).toBe('changed');
});
it('review: Tab and Shift Tab include the visible summary of a closed details disclosure',async()=>{
 render(view(<><input aria-label="First editor field"/><details><summary>Advanced editor settings</summary><input aria-label="Collapsed editor field"/></details></>));
 const expand=screen.getByRole('complementary',{name:'Recipe editor'});expand.focus();fireEvent.keyDown(expand,{key:'Enter'});
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Recipe editor'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('textbox',{name:'First editor field'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByText('Advanced editor settings'));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Recipe editor'}));
 await userEvent.tab({shift:true});expect(document.activeElement).toBe(screen.getByText('Advanced editor settings'));
});
it('review: basic graph Tab and Shift Tab wrap within expanded panel',async()=>{
 render(view());const expand=screen.getByRole('region',{name:'Related records'});expand.focus();fireEvent.keyDown(expand,{key:'Enter'});
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Related records'}));
 await userEvent.tab({shift:true});expect(document.activeElement).toBe(screen.getByRole('button',{name:'Graph node'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Related records'}));
});
it('review: Tab skips controls disabled by an ancestor fieldset',async()=>{
 render(view(<><fieldset disabled><input aria-label="Disabled draft field"/></fieldset><button>Enabled editor action</button></>));
 const expand=screen.getByRole('complementary',{name:'Recipe editor'});expand.focus();fireEvent.keyDown(expand,{key:'Enter'});
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Recipe editor'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Enabled editor action'}));
});
it('review: closed outer details excludes a nested closed summary',async()=>{
 render(view(<details><summary>Outer disclosure</summary><details><summary>Inner disclosure</summary><input aria-label="Nested hidden draft"/></details></details>));
 const expand=screen.getByRole('complementary',{name:'Recipe editor'});expand.focus();fireEvent.keyDown(expand,{key:'Enter'});
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Recipe editor'}));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByText('Outer disclosure'));
 await userEvent.tab();expect(document.activeElement).toBe(screen.getByRole('button',{name:'Close expanded Recipe editor'}));
});
