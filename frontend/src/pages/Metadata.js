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

  const pendingCount = Object.keys(modifications).length;

  const renderTrackEditor = (track, mod, hasMod) => {
    if (editingTrackId === track.id) {
      return (
        <input
          className="track-new-input"
          autoFocus
          value={hasMod ? mod : track.title}
          onChange={(e) => handleManualEdit(track.id, e.target.value)}
          onBlur={() => setEditingTrackId(null)}
          onKeyDown={(e) => e.key === 'Enter' && setEditingTrackId(null)}
        />
      );
    }
    return (
      <div
        className={`track-new ${hasMod ? 'modified' : ''}`}
        onClick={() => setEditingTrackId(track.id)}
      >
        {hasMod ? mod : "(click to edit)"}
      </div>
    );
  };

  return (
    <div className="metadata-layout">
      {/* Header */}
      <div className="metadata-header">
        <button className="btn-secondary" onClick={() => navigate(`/library/${artistId}`)}>← Back</button>
        <div className="metadata-header-main">
          {isEditingAlbum ? (
            <div className="album-edit-row">
              <input 
                className="album-title-input"
                autoFocus
                value={newAlbumTitle} 
                onChange={e => setNewAlbumTitle(e.target.value)} 
                onKeyDown={e => e.key === 'Enter' && handleRenameAlbum()}
              />
              <button className="btn-primary btn-sm" onClick={handleRenameAlbum}>Save</button>
              <button className="btn-secondary btn-sm" onClick={() => setIsEditingAlbum(false)}>Cancel</button>
            </div>
          ) : (
            <h2 
              className="album-title-edit"
              onClick={() => setIsEditingAlbum(true)}
              title="Click to rename album"
            >
              {album?.title}
              <span style={{ fontSize: 12, opacity: 0.5 }}>✎</span>
            </h2>
          )}
          <p className="metadata-sub">{album?.artist} · {tracks.length} tracks</p>
        </div>
        <div className="metadata-actions">
          <button className="btn-secondary" style={{ color: 'var(--danger)', borderColor: 'rgba(239,68,68,0.2)' }} onClick={handleDeleteAlbum}>
            Delete Album
          </button>
          <button 
            className="btn-primary" 
            onClick={applyChanges} 
            disabled={pendingCount === 0 || applying}
          >
            {applying ? 'Applying...' : `Apply Changes (${pendingCount})`}
          </button>
        </div>
      </div>

      <div className="metadata-body">
        {/* Track List */}
        <div className="metadata-tracks">
          <div className="card card-flush">
            <div className="table-scroll metadata-table-wrap">
              <table className="metadata-table">
                <thead>
                  <tr>
                    <th className="th-track-idx">#</th>
                    <th className="th-track-title">Title</th>
                    <th>New Title</th>
                    <th className="th-track-actions"></th>
                  </tr>
                </thead>
                <tbody>
                  {tracks.map((track, idx) => {
                    const mod = modifications[track.id];
                    const hasMod = mod !== undefined;
                    return (
                      <tr key={track.id}>
                        <td className="td-center-muted">{idx + 1}</td>
                        <td style={{ textDecoration: hasMod ? 'line-through' : 'none', color: hasMod ? 'var(--text-muted)' : 'inherit' }}>
                          {track.title}
                        </td>
                        <td onClick={() => setEditingTrackId(track.id)}>
                          {renderTrackEditor(track, mod, hasMod)}
                        </td>
                        <td className="td-center">
                          <button 
                            className="icon-btn"
                            onClick={() => handleDeleteTrack(track.id)} 
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
            <div className="track-cards">
              {tracks.map((track, idx) => {
                const mod = modifications[track.id];
                const hasMod = mod !== undefined;
                return (
                  <div className="track-card" key={track.id}>
                    <div className="track-card-head">
                      <span className="track-idx">{idx + 1}</span>
                      <button
                        className="icon-btn"
                        onClick={() => handleDeleteTrack(track.id)}
                        title="Delete track from DB"
                      >
                        <TrashIcon />
                      </button>
                    </div>
                    <div className={`track-old ${hasMod ? 'modified' : ''}`}>
                      {track.title}
                    </div>
                    <div onClick={() => setEditingTrackId(track.id)}>
                      {renderTrackEditor(track, mod, hasMod)}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        </div>

        {/* Bulk Actions Panel */}
        <div className="bulk-panel">
          <h3 className="bulk-heading">Bulk Edit</h3>
          <div className="bulk-edit-form">
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
                      className="btn-secondary suggestion-btn"
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
                  <p className="suggestion-empty">No significant patterns detected.</p>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Sticky action bar (phones only) keeps Apply reachable */}
      <div className="metadata-actionbar">
        <button
          className="btn-primary"
          onClick={applyChanges}
          disabled={pendingCount === 0 || applying}
        >
          {applying ? 'Applying...' : `Apply Changes (${pendingCount})`}
        </button>
      </div>
    </div>
  );
}
