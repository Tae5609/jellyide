import React, { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';

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
  const [album, setAlbum] = useState(null);
  const [tracks, setTracks] = useState([]);
  const [analysis, setAnalysis] = useState(null);
  
  // Album Edit state
  const [isEditingAlbum, setIsEditingAlbum] = useState(false);
  const [newAlbumTitle, setNewAlbumTitle] = useState('');

  // Bulk Edit state
  const [action, setAction] = useState('replace'); 
  const [target, setTarget] = useState('');
  const [value, setValue] = useState('');
  
  // modifications: Map of trackId -> newTitle
  const [modifications, setModifications] = useState({});
  const [applying, setApplying] = useState(false);
  const [editingTrackId, setEditingTrackId] = useState(null);

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

  const handleRenameAlbum = async () => {
    try {
      const res = await fetch(`${API}/library/albums/${albumId}/rename`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: newAlbumTitle })
      });
      if (res.ok) {
        setAlbum({ ...album, title: newAlbumTitle });
        setIsEditingAlbum(false);
      }
    } catch (e) {
      alert("Failed to rename album");
    }
  };

  const handleDeleteAlbum = async () => {
    if (!window.confirm("Are you sure you want to delete this album and all its tracks from the database? This will NOT delete files from disk.")) {
      return;
    }
    try {
      const res = await fetch(`${API}/library/albums/${albumId}`, {
        method: 'DELETE'
      });
      if (res.ok) {
        navigate(`/library/${artistId}`);
      }
    } catch (e) {
      alert("Failed to delete album");
    }
  };

  const handleDeleteTrack = async (trackId) => {
    if (!window.confirm("Delete this track from the database? (File will stay on disk)")) {
      return;
    }
    try {
      const res = await fetch(`${API}/library/tracks/${trackId}`, {
        method: 'DELETE'
      });
      if (res.ok) {
        setTracks(tracks.filter(t => t.id !== trackId));
        const next = { ...modifications };
        delete next[trackId];
        setModifications(next);
      }
    } catch (e) {
      alert("Failed to delete track");
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
          if (currentTarget) {
             nextTitle = currentTitle.split(currentTarget).join(currentValue);
          }
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
        newMods[track.id] = nextTitle.trim();
      } else {
        delete newMods[track.id];
      }
    });
    setModifications(newMods);
  };

  const applyChanges = async () => {
    const previewList = Object.entries(modifications).map(([trackId, newTitle]) => {
      const track = tracks.find(t => t.id === trackId);
      return {
        track_id: trackId,
        old_title: track ? track.title : '',
        new_title: newTitle
      };
    });

    if (previewList.length === 0) return;

    setApplying(true);
    try {
      const res = await fetch(`${API}/metadata/apply`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(previewList)
      });
      if (!res.ok) throw new Error('Failed to apply changes');
      
      alert("Changes applied successfully!");
      refreshData();
    } catch (e) {
      alert(e.message);
    }
    setApplying(false);
  };

  const handleManualEdit = (trackId, val) => {
    const track = tracks.find(t => t.id === trackId);
    if (!track) return;
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
    <div style={{ display: 'flex', height: '100%', flexDirection: 'column' }}>
      {/* Header */}
      <div style={{ padding: '20px 32px', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', gap: 16 }}>
        <button className="btn-secondary" onClick={() => navigate(`/library/${artistId}`)}>← Back</button>
        <div style={{ flex: 1 }}>
          {isEditingAlbum ? (
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <input 
                autoFocus
                value={newAlbumTitle} 
                onChange={e => setNewAlbumTitle(e.target.value)} 
                onKeyDown={e => e.key === 'Enter' && handleRenameAlbum()}
                style={{ fontSize: 18, fontWeight: 600, padding: '4px 8px' }}
              />
              <button className="btn-primary" onClick={handleRenameAlbum} style={{ padding: '6px 12px' }}>Save</button>
              <button className="btn-secondary" onClick={() => setIsEditingAlbum(false)} style={{ padding: '6px 12px' }}>Cancel</button>
            </div>
          ) : (
            <h2 
              style={{ margin: 0, fontSize: 20, cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 8 }}
              onClick={() => setIsEditingAlbum(true)}
              title="Click to rename album"
            >
              {album?.title}
              <span style={{ fontSize: 12, opacity: 0.5 }}>✎</span>
            </h2>
          )}
          <p style={{ margin: '4px 0 0 0', color: 'var(--text-muted)', fontSize: 13 }}>{album?.artist} · {tracks.length} tracks</p>
        </div>
        <div style={{ display: 'flex', gap: 12 }}>
          <button className="btn-secondary" style={{ color: 'var(--danger)', borderColor: 'rgba(239,68,68,0.2)' }} onClick={handleDeleteAlbum}>
            Delete Album
          </button>
          <button 
            className="btn-primary" 
            onClick={applyChanges} 
            disabled={Object.keys(modifications).length === 0 || applying}
          >
            {applying ? 'Applying...' : `Apply Changes (${Object.keys(modifications).length})`}
          </button>
        </div>
      </div>

      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
        {/* Track List */}
        <div style={{ flex: 1, padding: 32, overflowY: 'auto' }}>
          <div className="card" style={{ padding: 0 }}>
            <table>
              <thead>
                <tr>
                  <th style={{ width: 40, textAlign: 'center' }}>#</th>
                  <th style={{ width: '45%' }}>Title</th>
                  <th>New Title</th>
                  <th style={{ width: 50, textAlign: 'center' }}></th>
                </tr>
              </thead>
              <tbody>
                {tracks.map((track, idx) => {
                  const mod = modifications[track.id];
                  const hasMod = mod !== undefined;
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
                            style={{ width: '100%' }}
                            value={hasMod ? mod : track.title}
                            onChange={(e) => handleManualEdit(track.id, e.target.value)}
                            onBlur={() => setEditingTrackId(null)}
                            onKeyDown={(e) => e.key === 'Enter' && setEditingTrackId(null)}
                          />
                        ) : (
                          <div style={{ 
                            padding: '4px 8px', 
                            color: hasMod ? 'var(--accent)' : 'var(--text-muted)',
                            fontWeight: hasMod ? 600 : 400,
                            cursor: 'text'
                          }}>
                            {hasMod ? mod : "(click to edit)"}
                          </div>
                        )}
                      </td>
                      <td style={{ textAlign: 'center' }}>
                        <button 
                          onClick={() => handleDeleteTrack(track.id)} 
                          style={{ color: 'var(--danger)', opacity: 0.6, background: 'none', border: 'none', cursor: 'pointer' }}
                          title="Delete track from DB"
                        >
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

        {/* Bulk Actions Panel */}
        <div style={{ width: 340, backgroundColor: 'var(--bg-sidebar)', borderLeft: '1px solid var(--border)', padding: 24, overflowY: 'auto' }}>
          <h3 style={{ fontSize: 13, textTransform: 'uppercase', color: 'var(--text-muted)', marginBottom: 20 }}>Bulk Edit</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            <select value={action} onChange={e => setAction(e.target.value)}>
              <option value="replace">Replace Text</option>
              <option value="prefix">Add Prefix</option>
              <option value="suffix">Add Suffix</option>
              <option value="regex">Regex Replace</option>
            </select>
            {(action === 'replace' || action === 'regex') && (
              <input value={target} onChange={e => setTarget(e.target.value)} placeholder="Target string" />
            )}
            <input value={value} onChange={e => setValue(e.target.value)} placeholder="New value" />
            <button className="btn-secondary" onClick={() => handleBulkPreview()}>Run Transformation</button>
          </div>

          <div style={{ marginTop: 40 }}>
            <h3 style={{ fontSize: 12, textTransform: 'uppercase', color: 'var(--text-muted)', marginBottom: 16 }}>Cleanup Suggestions</h3>
            
            {!analysis ? (
              <p style={{ fontSize: 13, color: 'var(--text-muted)' }}>Analyzing album patterns...</p>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                {analysis.suggestions?.map((p, i) => (
                  <div key={i} style={{ backgroundColor: 'var(--bg-hover)', padding: '12px 16px', borderRadius: 8, border: '1px solid var(--border)' }}>
                    <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 4, color: '#fff' }}>"{p.text}"</div>
                    <div style={{ fontSize: 12, color: 'var(--text-muted)', marginBottom: 12 }}>Found in {p.count} tracks</div>
                    <button 
                      className="btn-secondary" 
                      style={{ width: '100%', fontSize: 11, padding: '6px', backgroundColor: 'rgba(255,255,255,0.05)' }}
                      onClick={() => {
                        setAction('replace');
                        setTarget(p.text);
                        setValue('');
                        handleBulkPreview('replace', p.text, '');
                      }}
                    >
                      Remove this pattern
                    </button>
                  </div>
                ))}
                
                {(!analysis.suggestions?.length) && (
                  <p style={{ fontSize: 12, color: 'var(--text-muted)', textAlign: 'center', padding: '20px 0' }}>No significant patterns detected.</p>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
