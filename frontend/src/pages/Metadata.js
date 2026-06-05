import React, { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useModal } from '../components/ModalContext';

const API = window.location.origin + '/api';

const TrashIcon = () => (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M3 6h18"></path>
    <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"></path>
    <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"></path>
    <line x1="10" y1="11" x2="10" y2="17"></line>
    <line x1="14" y1="11" x2="14" y2="17"></line>
  </svg>
);

export default function Metadata() {
  const { artistId, albumId } = useParams();
  const navigate = useNavigate();
  const { showAlert, showConfirm } = useModal();
  const [album, setAlbum] = useState(null);
  const [tracks, setTracks] = useState([]);
  const [analysis, setAnalysis] = useState(null);
  
  // Bulk Edit state
  const [action, setAction] = useState('replace'); 
  const [target, setTarget] = useState('');
  const [value, setValue] = useState('');
  
  // Title modifications state: Map of trackId -> newTitle
  const [modifications, setModifications] = useState({});
  const [applying, setApplying] = useState(false);
  const [editingAlbum, setEditingAlbum] = useState(false);
  const [newAlbumTitle, setNewAlbumTitle] = useState('');

  // Inline editing state
  const [editingTrackId, setEditingTrackId] = useState(null);
  const [showAllSuggestions, setShowAllSuggestions] = useState(false);

  useEffect(() => {
    if (!albumId) return;
    refreshData();
  }, [albumId]);

  const refreshData = async () => {
    try {
      const aRes = await fetch(`${API}/library/albums/${albumId}`);
      const aData = await aRes.json();
      setAlbum(aData);
      setNewAlbumTitle(aData.title);

      const tRes = await fetch(`${API}/library/albums/${albumId}/tracks`);
      const tData = await tRes.json();
      setTracks(tData || []);

      const anRes = await fetch(`${API}/metadata/analyze/${albumId}`);
      const anData = await anRes.json();
      setAnalysis(anData);
      
      setModifications({});
    } catch (e) {
      console.error("Error refreshing data:", e);
    }
  };

  const handleBulkPreview = (overrideAction, overrideTarget, overrideValue) => {
    if (tracks.length === 0) return;
    
    const currentAction = overrideAction || action;
    const currentTarget = overrideTarget !== undefined ? overrideTarget : target;
    const currentValue = overrideValue !== undefined ? overrideValue : value;

    const newMods = { ...modifications };
    tracks.forEach(track => {
      const currentTitle = newMods[track.id] !== undefined ? newMods[track.id] : track.title;
      let nextTitle = currentTitle;

      switch (currentAction) {
        case 'replace':
          if (currentTarget) nextTitle = currentTitle.replaceAll(currentTarget, currentValue);
          break;
        case 'prefix':
          nextTitle = currentValue + currentTitle;
          break;
        case 'suffix':
          nextTitle = currentTitle + currentValue;
          break;
        case 'regex':
          try {
            const re = new RegExp(currentTarget, 'g');
            nextTitle = currentTitle.replace(re, currentValue);
          } catch (e) {}
          break;
        default:
          break;
      }

      if (nextTitle !== track.title) {
        newMods[track.id] = nextTitle;
      } else {
        delete newMods[track.id];
      }
    });
    setModifications(newMods);
  };

  const applyChanges = async () => {
    const previewList = Object.entries(modifications).map(([trackId, newTitle]) => {
      const track = tracks.find(t => t.id === parseInt(trackId));
      return {
        track_id: parseInt(trackId),
        old_title: track ? track.title : '',
        new_title: newTitle
      };
    });

    if (previewList.length === 0) {
      showAlert("Notice", "No changes to apply.");
      return;
    }

    setApplying(true);
    try {
      const res = await fetch(`${API}/metadata/apply`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(previewList)
      });
      if (!res.ok) throw new Error('Failed to apply changes');
      
      showAlert("Success", "Changes applied and files renamed successfully!");
      refreshData();
    } catch (e) {
      showAlert("Error", e.message);
    }
    setApplying(false);
  };

  const deleteTrack = async (trackId) => {
    const confirmed = await showConfirm("Delete Track", "Are you sure you want to permanently delete this track from disk?");
    if (!confirmed) return;
    try {
      const res = await fetch(`${API}/library/tracks/${trackId}`, { method: 'DELETE' });
      if (res.ok) {
        setTracks(tracks.filter(t => t.id !== trackId));
        const next = { ...modifications };
        delete next[trackId];
        setModifications(next);
      }
    } catch (e) {
      showAlert("Error", e.message);
    }
  };

  const handleManualEdit = (trackId, val) => {
    const track = tracks.find(t => t.id === trackId);
    if (val === track.title) {
      const next = { ...modifications };
      delete next[trackId];
      setModifications(next);
    } else {
      setModifications({ ...modifications, [trackId]: val });
    }
  };

  if (!albumId) return null;

  return (
    <div className="fade-in-up" style={{ display: 'flex', height: '100%', flexDirection: 'column' }}>
      <div style={{ padding: '20px 32px', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', gap: 16 }}>
        <button className="btn-secondary" onClick={() => navigate(`/library/${artistId || album?.artist_id}`)}>← Back</button>
        <div style={{ flex: 1 }}>
          {editingAlbum ? (
            <div style={{ display: 'flex', gap: 8 }}>
              <input value={newAlbumTitle} onChange={e => setNewAlbumTitle(e.target.value)} style={{ fontSize: 18, width: 300 }} />
              <button className="btn-primary" onClick={async () => {
                await fetch(`${API}/library/albums/${albumId}/rename`, {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ title: newAlbumTitle })
                });
                setAlbum({ ...album, title: newAlbumTitle });
                setEditingAlbum(false);
              }}>Save</button>
              <button className="btn-secondary" onClick={() => setEditingAlbum(false)}>Cancel</button>
            </div>
          ) : (
            <h2 style={{ margin: 0, fontSize: 18, display: 'flex', alignItems: 'center', gap: 10 }}>
              {album?.title}
              <button onClick={() => setEditingAlbum(true)} style={{ color: 'var(--text-muted)', fontSize: 12, background: 'none', border: 'none' }}>✎</button>
            </h2>
          )}
          <p style={{ margin: 0, color: 'var(--text-muted)', fontSize: 13 }}>{album?.artist} · {tracks.length} tracks</p>
        </div>
        <button className="btn-primary" onClick={applyChanges} disabled={Object.keys(modifications).length === 0 || applying}>
          {applying ? 'Applying...' : `Apply Changes (${Object.keys(modifications).length})`}
        </button>
      </div>

      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
        <div style={{ flex: 1, padding: 32, overflowY: 'auto' }}>
          <div className="card" style={{ padding: 0 }}>
            <table>
              <thead>
                <tr>
                  <th style={{ width: 40, textAlign: 'center' }}>#</th>
                  <th style={{ width: '45%' }}>OLD TITLE</th>
                  <th>NEW TITLE</th>
                  <th style={{ width: 50 }}></th>
                </tr>
              </thead>
              <tbody>
                {tracks.map((track, idx) => {
                  const mod = modifications[track.id];
                  const hasMod = mod !== undefined;
                  const displayTitle = hasMod ? mod : "";

                  return (
                    <tr key={track.id}>
                      <td style={{ textAlign: 'center', color: 'var(--text-muted)' }}>{idx + 1}</td>
                      <td style={{ textDecoration: hasMod ? 'line-through' : 'none', color: hasMod ? 'var(--text-muted)' : 'inherit' }}>
                        {track.title}
                      </td>
                      <td onClick={() => setEditingTrackId(track.id)}>
                        {editingTrackId === track.id ? (
                          <input
                            autoFocus
                            style={{ width: '100%', padding: '4px 8px' }}
                            value={displayTitle || track.title}
                            onChange={(e) => handleManualEdit(track.id, e.target.value)}
                            onBlur={() => setEditingTrackId(null)}
                            onKeyDown={(e) => e.key === 'Enter' && setEditingTrackId(null)}
                          />
                        ) : (
                          <div style={{ 
                            padding: '4px 8px', 
                            borderRadius: 4,
                            cursor: 'text',
                            color: hasMod ? 'var(--accent)' : 'var(--text-muted)',
                            fontWeight: hasMod ? 600 : 400,
                            minHeight: 20
                          }}>
                            {hasMod ? mod : "(click to edit)"}
                          </div>
                        )}
                      </td>
                      <td style={{ textAlign: 'center' }}>
                        <button onClick={() => deleteTrack(track.id)} style={{ color: 'var(--danger)', opacity: 0.6, background: 'none', border: 'none' }}>
                          <TrashIcon />
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>

        <div style={{ width: 320, backgroundColor: 'var(--bg-sidebar)', borderLeft: '1px solid var(--border)', padding: 24, overflowY: 'auto' }}>
          <h3 style={{ fontSize: 14, textTransform: 'uppercase', color: 'var(--text-muted)', marginBottom: 20 }}>Bulk Edit</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <select value={action} onChange={e => setAction(e.target.value)} style={{ width: '100%' }}>
              <option value="replace">Replace Text</option>
              <option value="prefix">Add Prefix</option>
              <option value="suffix">Add Suffix</option>
              <option value="regex">Regex Replace</option>
            </select>
            {(action === 'replace' || action === 'regex') && (
              <input value={target} onChange={e => setTarget(e.target.value)} placeholder="Target pattern" />
            )}
            <input value={value} onChange={e => setValue(e.target.value)} placeholder="Replacement value" />
            <button className="btn-secondary" onClick={() => handleBulkPreview()}>Run Transformation</button>
          </div>

          <div style={{ marginTop: 40 }}>
            <h3 style={{ fontSize: 12, textTransform: 'uppercase', color: 'var(--text-muted)', marginBottom: 16 }}>⭐ Suggested Cleanup</h3>
            
            {!analysis ? (
              <p style={{ fontSize: 13, color: 'var(--text-muted)' }}>Loading analysis...</p>
            ) : analysis.suggestions && analysis.suggestions.length > 0 ? (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                {analysis.suggestions.slice(0, showAllSuggestions ? analysis.suggestions.length : 5).map((p, i) => (
                  <div key={i} style={{ backgroundColor: 'var(--bg-hover)', padding: 12, borderRadius: 6 }}>
                    <div style={{ color: 'var(--text-main)', fontWeight: 600, fontSize: 14, marginBottom: 4 }}>"{p.text}"</div>
                    <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 12 }}>Found in {p.count} tracks</div>
                    <button 
                      className="btn-secondary" 
                      style={{ width: '100%', fontSize: 12, padding: '6px 12px' }}
                      onClick={() => {
                        setAction('replace');
                        setTarget(p.text);
                        setValue('');
                        handleBulkPreview('replace', p.text, '');
                      }}
                    >
                      [ Remove ]
                    </button>
                  </div>
                ))}
                {!showAllSuggestions && analysis.suggestions.length > 5 && (
                  <button 
                    className="btn-secondary" 
                    onClick={() => setShowAllSuggestions(true)}
                    style={{ width: '100%' }}
                  >
                    Show More
                  </button>
                )}
              </div>
            ) : (
              <p style={{ fontSize: 13, color: 'var(--text-muted)' }}>No strong patterns found in this album.</p>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
