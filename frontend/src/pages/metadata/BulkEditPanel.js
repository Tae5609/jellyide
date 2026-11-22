import React, { useState } from 'react';
import { FIELDS, INT_MAX, fieldDef } from './useEditorState';

// BulkEditPanel sets one field to one value on all selected rows as
// pending edits (nothing is written to disk until the changes are applied).
export default function BulkEditPanel({ selectedCount, onApply, onError }) {
  const [field, setField] = useState('title');
  const [value, setValue] = useState('');

  const apply = () => {
    const def = fieldDef(field);
    const raw = value.trim();
    if (def.kind === 'int') {
      if (raw !== '' && (!/^\d+$/.test(raw) || Number(raw) > INT_MAX[field])) {
        onError(`${def.label} must be a whole number between 0 and ${INT_MAX[field]}`);
        return;
      }
      onApply(field, raw === '' ? null : Number(raw));
    } else {
      if (field === 'title' && raw === '') {
        onError('Title must not be empty');
        return;
      }
      // Stage the trimmed value: the backend stores text verbatim, so a
      // padded draft would land in tags and history with its spaces.
      onApply(field, raw === '' ? null : raw);
    }
  };

  return (
    <section className="bulk-section">
      <h3 className="bulk-heading">Bulk Edit</h3>
      <p className="bulk-hint">
        {selectedCount > 0
          ? `Apply to ${selectedCount} selected track${selectedCount === 1 ? '' : 's'}`
          : 'Select tracks in the grid to use bulk edit'}
      </p>
      <div className="bulk-edit-form">
        <select value={field} onChange={(e) => setField(e.target.value)} aria-label="Field">
          {FIELDS.map((f) => (
            <option key={f.key} value={f.key}>
              {f.label}
            </option>
          ))}
        </select>
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="Value (empty clears the tag)"
          aria-label="Value"
        />
        <button
          type="button"
          className="btn-secondary"
          onClick={apply}
          disabled={selectedCount === 0}
        >
          Apply to selection
        </button>
      </div>
    </section>
  );
}
