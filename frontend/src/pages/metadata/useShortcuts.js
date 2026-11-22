import { useEffect, useRef } from 'react';

// useShortcuts wires the metadata editor keyboard shortcuts onto window.
//
// Handlers (all optional): onSave, onUndo, onRedo, onSelectAll, onFillDown,
// onArrowLeft/Right/Up/Down, onEnter, onTab(shift), onEscape, onFind,
// onLookup, onHistory, onHelp.
//
// Rules:
//  - desktop only (the grid is a card list on phones, where the shortcuts
//    are not shown)
//  - disabled entirely while a modal/overlay is open
//  - Ctrl/⌘+S and Ctrl/⌘+Z / Ctrl/⌘+Shift+Z / Ctrl/⌘+Y still work while
//    typing in an input
//  - every other shortcut is ignored while an input/textarea/select is
//    focused (typing)
export default function useShortcuts(handlers, { disabled = false } = {}) {
  const ref = useRef({ handlers, disabled });
  ref.current = { handlers, disabled };

  useEffect(() => {
    const isDesktop = () =>
      typeof window.matchMedia === 'function'
        ? window.matchMedia('(min-width: 769px)').matches
        : true;

    const isTyping = (t) =>
      Boolean(
        t &&
          (t.tagName === 'INPUT' ||
            t.tagName === 'TEXTAREA' ||
            t.tagName === 'SELECT' ||
            t.isContentEditable)
      );

    const onKeyDown = (e) => {
      const { handlers: h, disabled: off } = ref.current;
      if (off || !isDesktop()) return;
      const mod = e.ctrlKey || e.metaKey;
      const key = e.key;

      // Save/undo/redo work everywhere, including inside inputs.
      if (mod && !e.shiftKey && (key === 's' || key === 'S')) {
        e.preventDefault();
        if (h.onSave) h.onSave();
        return;
      }
      if (mod && (key === 'z' || key === 'Z')) {
        e.preventDefault();
        if (e.shiftKey) {
          if (h.onRedo) h.onRedo();
        } else if (h.onUndo) {
          h.onUndo();
        }
        return;
      }
      if (mod && (key === 'y' || key === 'Y')) {
        e.preventDefault();
        if (h.onRedo) h.onRedo();
        return;
      }

      // Everything below is ignored while typing.
      if (isTyping(e.target)) return;

      if (mod && (key === 'a' || key === 'A')) {
        e.preventDefault();
        if (h.onSelectAll) h.onSelectAll();
        return;
      }
      if (mod && (key === 'd' || key === 'D')) {
        e.preventDefault();
        if (h.onFillDown) h.onFillDown();
        return;
      }

      switch (key) {
        case 'ArrowLeft':
          e.preventDefault();
          if (h.onArrowLeft) h.onArrowLeft();
          break;
        case 'ArrowRight':
          e.preventDefault();
          if (h.onArrowRight) h.onArrowRight();
          break;
        case 'ArrowUp':
          e.preventDefault();
          if (h.onArrowUp) h.onArrowUp();
          break;
        case 'ArrowDown':
          e.preventDefault();
          if (h.onArrowDown) h.onArrowDown();
          break;
        case 'Enter':
          if (h.onEnter) h.onEnter();
          break;
        case 'Tab':
          e.preventDefault();
          if (h.onTab) h.onTab(e.shiftKey);
          break;
        case 'Escape':
          if (h.onEscape) h.onEscape();
          break;
        case '/':
          e.preventDefault();
          if (h.onFind) h.onFind();
          break;
        case '?':
          if (h.onHelp) h.onHelp();
          break;
        case 'l':
        case 'L':
          if (h.onLookup) h.onLookup();
          break;
        case 'h':
        case 'H':
          if (h.onHistory) h.onHistory();
          break;
        default:
          break;
      }
    };

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);
}
