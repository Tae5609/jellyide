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
//  - Tab is intercepted only while a grid cell is focused (the
//    `cellFocused` option); otherwise native tab order to the page
//    controls stays intact (WCAG 2.1.2)
//  - Enter is skipped while an interactive element (button, link, tab
//    stop) holds DOM focus: Enter already activates it natively, and the
//    shortcut must not double-fire on top of that
export default function useShortcuts(
  handlers,
  { disabled = false, cellFocused = false } = {}
) {
  const ref = useRef({ handlers, disabled, cellFocused });
  ref.current = { handlers, disabled, cellFocused };

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

    // Enter activates a focused interactive element natively (buttons,
    // links, tab stops). Used to keep onEnter from firing on top of that
    // native activation. Inputs/textareas/selects never reach the Enter
    // case (the isTyping guard above returns first).
    const isInteractive = (t) =>
      Boolean(
        t &&
          t.tagName &&
          (t.tagName === 'BUTTON' ||
            t.tagName === 'A' ||
            t.tagName === 'INPUT' ||
            t.tagName === 'TEXTAREA' ||
            t.tagName === 'SELECT' ||
            t.isContentEditable ||
            t.tabIndex >= 0)
      );

    const onKeyDown = (e) => {
      const { handlers: h, disabled: off, cellFocused } = ref.current;
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
          // A focused button/link/tab stop activates natively on Enter;
          // skip the shortcut so it does not double-fire (e.g. the grid's
          // "Auto-number" button also opening the cell editor).
          if (h.onEnter && !isInteractive(document.activeElement)) h.onEnter();
          break;
        case 'Tab':
          if (!cellFocused) break;
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
