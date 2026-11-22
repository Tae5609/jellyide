import React, { useState } from 'react';
import { PREVIEW_FIELDS } from './useEditorState';

// FindReplacePanel runs the existing replace / prefix / suffix / regex
// operations on any text field. Operations are previewed server-side
// (POST /metadata/preview) and the result is staged as pending edits.
export default function FindReplacePanel({
  trackIds,
  analysis,
  onPreview,
  onNotice,
  onError,
}) {
  const [field, setField] = useState('title');
  const [action, setAction] = useState('replace');
  const [target, setTarget] = useState('');
  const [value, setValue] = useState('');
  const [busy, setBusy] = useState(false);

  const needsTarget = action === 'replace' || action === 'regex';

  const run = async (overrides = {}) => {
    const req = {
      action: overrides.action || action,
      target: overrides.target !== undefined ? overrides.target : target,
      value: overrides.value !== undefined ? overrides.value : value,
      field: overrides.field || field,
    };
    setBusy(true);
    try {
      const n = await onPreview(req);
      onNotice(`${n} change${n === 1 ? '' : 's'} staged as pending edits`);
    } catch (e) {
      onError(e.message);
    } finally {
      setBusy(false);
    }
  };

  const runSuggestion = (text) => {
    setAction('replace');
    setTarget(text);
    setValue('');
    run({ action: 'replace', target: text, value: '', field: 'title' });
  };

  return (
    <section className="bulk-section">
      <h3 className="bulk-heading">Find &amp; Replace</h3>
      <div className="bulk-edit-form">
        <select value={field} onChange={(e) => setField(e.target.value)} aria-label="Field">
          {PREVIEW_FIELDS.map((f) => (
            <option key={f.key} value={f.key}>
              {f.label}
            </option>
          ))}
        </select>
        <select value={action} onChange={(e) => setAction(e.target.value)} aria-label="Operation">
          <option value="replace">Replace Text</option>
          <option value="prefix">Add Prefix</option>
          <option value="suffix">Add Suffix</option>
          <option value="regex">Regex Replace</option>
        </select>
        {needsTarget && (
          <input
            id="find-replace-target"
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            placeholder="Target string"
          />
        )}
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="New value"
        />
        <button
          type="button"
          className="btn-secondary"
          onClick={() => run()}
          disabled={busy || trackIds.length === 0}
        >
          {busy ? 'Running…' : 'Run Transformation'}
        </button>
      </div>

      <div className="cleanup-section">
        <h3 className="cleanup-heading">Cleanup Suggestions</h3>

        {!analysis ? (
          <p className="bulk-analyzing">Analyzing album patterns...</p>
        ) : (
          <div className="suggestion-list">
            {analysis.suggestions?.map((p, i) => (
              <div className="suggestion" key={i}>
                <div className="suggestion-text">"{p.text}"</div>
                <div className="suggestion-count">Found in {p.count} tracks</div>
                <button
                  type="button"
                  className="btn-secondary suggestion-btn"
                  onClick={() => runSuggestion(p.text)}
                  disabled={busy}
                >
                  Remove this pattern
                </button>
              </div>
            ))}

            {(!analysis.suggestions?.length) && (
              <p className="suggestion-empty">No significant patterns detected.</p>
            )}
          </div>
        )}
      </div>
    </section>
  );
}
