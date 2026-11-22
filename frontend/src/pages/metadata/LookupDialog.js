import React, { useCallback, useEffect, useState } from 'react';
import { apiFetch } from '../../api';
import { errorFromResponse } from './useEditorState';

// Rows matched with less than this confidence are highlighted.
const LOW_CONFIDENCE = 0.6;

function textOf(v) {
  return v === null || v === undefined ? '' : String(v);
}

// LookupDialog searches MusicBrainz (defaulting to the album artist and
// title), shows the candidate list, and — after a candidate is picked — the
// current-vs-proposed mapping table. "Apply" stages the included rows and
// fields as pending edits (nothing is written yet) and optionally passes the
// release cover URL to the parent.
export default function LookupDialog({ albumId, album, onClose, onApply, onError }) {
  const defaultQuery = `${album.artist || ''} ${album.title || ''}`.trim();
  const [query, setQuery] = useState(defaultQuery);
  const [results, setResults] = useState(null);
  const [searching, setSearching] = useState(false);
  const [release, setRelease] = useState(null);
  const [loadingRelease, setLoadingRelease] = useState(false);
  const [include, setInclude] = useState({}); // trackId -> {field: bool}
  const [setCover, setSetCover] = useState(false);

  const search = useCallback(
    async (q) => {
      setSearching(true);
      setRelease(null);
      try {
        const res = await apiFetch(
          `/metadata/lookup/search?album_id=${encodeURIComponent(albumId)}&q=${encodeURIComponent(q)}`
        );
        if (!res.ok) throw new Error(await errorFromResponse(res));
        setResults(await res.json());
      } catch (e) {
        setResults({ error: e.message });
      } finally {
        setSearching(false);
      }
    },
    [albumId]
  );

  useEffect(() => {
    search(defaultQuery);
  }, []);

  const openRelease = async (mbid) => {
    setLoadingRelease(true);
    setResults(null);
    try {
      const res = await apiFetch(
        `/metadata/lookup/release/${mbid}?album_id=${encodeURIComponent(albumId)}`
      );
      if (!res.ok) throw new Error(await errorFromResponse(res));
      const data = await res.json();
      setRelease(data);
      const inc = {};
      (data.mapping || []).forEach((m) => {
        const row = {};
        Object.keys(m.proposed || {}).forEach((f) => {
          row[f] = true;
        });
        inc[m.track_id] = row;
      });
      setInclude(inc);
      setSetCover(false);
    } catch (e) {
      onError(e.message);
    } finally {
      setLoadingRelease(false);
    }
  };

  const toggleField = (trackId, field) => {
    setInclude((prev) => {
      const row = { ...(prev[trackId] || {}) };
      row[field] = !row[field];
      return { ...prev, [trackId]: row };
    });
  };

  const toggleRow = (trackId) => {
    setInclude((prev) => {
      const row = prev[trackId] || {};
      const on = !Object.values(row).every(Boolean);
      const next = {};
      Object.keys(row).forEach((f) => {
        next[f] = on;
      });
      return { ...prev, [trackId]: next };
    });
  };

  const stagedEntries = () => {
    const out = [];
    ((release && release.mapping) || []).forEach((m) => {
      const row = include[m.track_id] || {};
      Object.keys(m.proposed || {}).forEach((f) => {
        if (row[f]) out.push({ trackId: m.track_id, field: f, value: m.proposed[f] });
      });
    });
    return out;
  };

  const apply = () => {
    const entries = stagedEntries();
    onApply(entries, setCover && release.cover_url ? release.cover_url : null);
  };

  const entryCount = stagedEntries().length;

  return (
    <div className="overlay" role="dialog" aria-label="MusicBrainz lookup">
      <div className="dialog lookup-dialog fade-in-up">
        <div className="dialog-head">
          <h3 className="dialog-title">MusicBrainz Lookup</h3>
          <button type="button" className="icon-btn" onClick={onClose} aria-label="Close lookup">
            ✕
          </button>
        </div>

        {!release && (
          <div className="dialog-body">
            <div className="dialog-search-row">
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && search(query)}
                placeholder="Artist and album title"
                aria-label="Search query"
              />
              <button
                type="button"
                className="btn-primary btn-sm"
                onClick={() => search(query)}
                disabled={searching}
              >
                {searching ? 'Searching…' : 'Search'}
              </button>
            </div>
            {results && results.error && <p className="lookup-error">{results.error}</p>}
            {searching && <p className="lookup-status">Searching MusicBrainz…</p>}
            {Array.isArray(results) && (
              <div className="lookup-results">
                {results.map((c) => (
                  <button
                    type="button"
                    className="lookup-result"
                    key={c.mbid}
                    onClick={() => openRelease(c.mbid)}
                  >
                    <span className="lookup-result-title">{c.title}</span>
                    <span className="lookup-result-sub">
                      {[c.artist, c.date, c.country].filter(Boolean).join(' · ')}
                    </span>
                    <span className="lookup-result-meta">
                      {c.track_count} tracks · score {c.score}
                    </span>
                  </button>
                ))}
                {results.length === 0 && (
                  <p className="lookup-status">No releases found.</p>
                )}
              </div>
            )}
          </div>
        )}

        {release && (
          <div className="dialog-body">
            <div className="lookup-release-head">
              <button type="button" className="btn-secondary btn-sm" onClick={() => setRelease(null)}>
                ← Back to results
              </button>
              <div className="lookup-release-title">
                {textOf(release.release && release.release.title)}
                <span className="lookup-release-artist">
                  {(release.release && release.release.artist) || ''}
                </span>
              </div>
            </div>

            <div className="lookup-mapping">
              {(release.mapping || []).map((m) => {
                const row = include[m.track_id] || {};
                const fieldKeys = Object.keys(m.proposed || {});
                const rowAll = fieldKeys.length > 0 && fieldKeys.every((f) => row[f]);
                const low = m.confidence < LOW_CONFIDENCE;
                return (
                  <div
                    key={m.track_id}
                    className={`lookup-row ${low ? 'low-confidence' : ''}`}
                  >
                    <label className="lookup-row-include">
                      <input
                        type="checkbox"
                        checked={rowAll}
                        disabled={fieldKeys.length === 0}
                        onChange={() => toggleRow(m.track_id)}
                      />
                      <span className="lookup-current">{textOf(m.current && m.current.title) || '—'}</span>
                      <span className={`lookup-confidence ${low ? 'low' : ''}`} title={low ? 'Low match confidence' : 'Match confidence'}>
                        {Math.round(m.confidence * 100)}%
                      </span>
                    </label>
                    <div className="lookup-fields">
                      {fieldKeys.map((f) => (
                        <label key={f} className="lookup-field">
                          <input
                            type="checkbox"
                            checked={Boolean(row[f])}
                            onChange={() => toggleField(m.track_id, f)}
                          />
                          <span className="lookup-field-name">{f}</span>
                          <span className="lookup-field-old">{textOf(m.current && m.current[f]) || '—'}</span>
                          <span className="lookup-arrow">→</span>
                          <span className="lookup-field-new">{textOf(m.proposed[f])}</span>
                        </label>
                      ))}
                      {fieldKeys.length === 0 && (
                        <span className="lookup-field-none">No proposed changes</span>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>

            <label className="lookup-cover-option">
              <input
                type="checkbox"
                checked={setCover}
                disabled={!release.cover_url}
                onChange={(e) => setSetCover(e.target.checked)}
              />
              Set cover art from release ({release.cover_url ? 'available' : 'none'})
            </label>

            <div className="dialog-actions">
              <button type="button" className="btn-secondary" onClick={onClose}>
                Cancel
              </button>
              <button
                type="button"
                className="btn-primary"
                onClick={apply}
                disabled={entryCount === 0 || loadingRelease}
              >
                Stage {entryCount} field{entryCount === 1 ? '' : 's'} as pending edits
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
