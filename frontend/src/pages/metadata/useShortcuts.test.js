import { renderHook, act } from '@testing-library/react';
import useShortcuts from './useShortcuts';

function makeHandlers() {
  return {
    onSave: jest.fn(),
    onUndo: jest.fn(),
    onRedo: jest.fn(),
    onSelectAll: jest.fn(),
    onFillDown: jest.fn(),
    onArrowLeft: jest.fn(),
    onArrowRight: jest.fn(),
    onArrowUp: jest.fn(),
    onArrowDown: jest.fn(),
    onEnter: jest.fn(),
    onTab: jest.fn(),
    onEscape: jest.fn(),
    onFind: jest.fn(),
    onLookup: jest.fn(),
    onHistory: jest.fn(),
    onHelp: jest.fn(),
  };
}

// Dispatches a keydown to the active element (or the window) so the
// window-level listener in the hook sees it with the right e.target.
function press(init) {
  const ev = new KeyboardEvent('keydown', {
    bubbles: true,
    cancelable: true,
    ...init,
  });
  const target = document.activeElement || window;
  act(() => {
    target.dispatchEvent(ev);
  });
  return ev;
}

const CASES = [
  ['onSave', { key: 's', ctrlKey: true }],
  ['onSave', { key: 'S', metaKey: true }],
  ['onUndo', { key: 'z', ctrlKey: true }],
  ['onRedo', { key: 'z', ctrlKey: true, shiftKey: true }],
  ['onRedo', { key: 'y', ctrlKey: true }],
  ['onSelectAll', { key: 'a', ctrlKey: true }],
  ['onFillDown', { key: 'd', ctrlKey: true }],
  ['onArrowLeft', { key: 'ArrowLeft' }],
  ['onArrowRight', { key: 'ArrowRight' }],
  ['onArrowUp', { key: 'ArrowUp' }],
  ['onArrowDown', { key: 'ArrowDown' }],
  ['onEnter', { key: 'Enter' }],
  ['onEscape', { key: 'Escape' }],
  ['onFind', { key: '/' }],
  ['onLookup', { key: 'l' }],
  ['onLookup', { key: 'L' }],
  ['onHistory', { key: 'h' }],
  ['onHistory', { key: 'H' }],
  ['onHelp', { key: '?' }],
];

describe('useShortcuts', () => {
  afterEach(() => {
    document.body.innerHTML = '';
  });

  test.each(CASES)('%s fires for %j', (handler, init) => {
    const handlers = makeHandlers();
    renderHook(() => useShortcuts(handlers));
    press(init);
    expect(handlers[handler]).toHaveBeenCalledTimes(1);
  });

  test('Tab passes the shift flag through', () => {
    const handlers = makeHandlers();
    renderHook(() => useShortcuts(handlers));
    press({ key: 'Tab', shiftKey: true });
    expect(handlers.onTab).toHaveBeenCalledWith(true);
    press({ key: 'Tab' });
    expect(handlers.onTab).toHaveBeenCalledWith(false);
  });

  test('Ctrl+S and Ctrl+Z prevent the browser default', () => {
    const handlers = makeHandlers();
    renderHook(() => useShortcuts(handlers));
    expect(press({ key: 's', ctrlKey: true }).defaultPrevented).toBe(true);
    expect(press({ key: 'z', ctrlKey: true }).defaultPrevented).toBe(true);
    expect(press({ key: '/' }).defaultPrevented).toBe(true);
  });

  test('single keys are ignored while typing in an input', () => {
    const handlers = makeHandlers();
    renderHook(() => useShortcuts(handlers));
    const input = document.createElement('input');
    document.body.appendChild(input);
    input.focus();

    press({ key: 'l' });
    press({ key: 'ArrowLeft' });
    press({ key: '?' });
    press({ key: 'Escape' });
    press({ key: 'a', ctrlKey: true });

    expect(handlers.onLookup).not.toHaveBeenCalled();
    expect(handlers.onArrowLeft).not.toHaveBeenCalled();
    expect(handlers.onHelp).not.toHaveBeenCalled();
    expect(handlers.onEscape).not.toHaveBeenCalled();
    expect(handlers.onSelectAll).not.toHaveBeenCalled();
  });

  test('save/undo/redo still work while typing in an input', () => {
    const handlers = makeHandlers();
    renderHook(() => useShortcuts(handlers));
    const input = document.createElement('input');
    document.body.appendChild(input);
    input.focus();

    press({ key: 's', ctrlKey: true });
    press({ key: 'z', ctrlKey: true });
    press({ key: 'z', ctrlKey: true, shiftKey: true });
    press({ key: 'y', ctrlKey: true });

    expect(handlers.onSave).toHaveBeenCalledTimes(1);
    expect(handlers.onUndo).toHaveBeenCalledTimes(1);
    expect(handlers.onRedo).toHaveBeenCalledTimes(2);
  });

  test('nothing fires while disabled (a modal is open)', () => {
    const handlers = makeHandlers();
    renderHook(() => useShortcuts(handlers, { disabled: true }));
    press({ key: 'l' });
    press({ key: 's', ctrlKey: true });
    press({ key: 'z', ctrlKey: true });
    expect(handlers.onLookup).not.toHaveBeenCalled();
    expect(handlers.onSave).not.toHaveBeenCalled();
    expect(handlers.onUndo).not.toHaveBeenCalled();
  });

  test('unmodified keys do not trigger modified handlers', () => {
    const handlers = makeHandlers();
    renderHook(() => useShortcuts(handlers));
    press({ key: 's' });
    press({ key: 'z' });
    press({ key: 'd' });
    expect(handlers.onSave).not.toHaveBeenCalled();
    expect(handlers.onUndo).not.toHaveBeenCalled();
    expect(handlers.onFillDown).not.toHaveBeenCalled();
  });
});
