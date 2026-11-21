import React, { useState } from 'react';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';

export default function Downloads() {
  const { showAlert } = useModal();
  const [url, setUrl] = useState('');
  const [loading, setLoading] = useState(false);
  const [playlist, setPlaylist] = useState(null);
  const [selected, setSelected] = useState(new Set());
  const [artists, setArtists] = useState({});
  const [albums, setAlbums] = useState({});
  const [queueing, setQueueing] = useState(false);
  
  const [bulkArtist, setBulkArtist] = useState('');
  const [bulkAlbum, setBulkAlbum] = useState('');

  const formatDuration = (seconds) => {
    if (!seconds) return '--:--';
    const mins = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    return `${mins}:${secs.toString().padStart(2, '0')}`;
  };

  const fetchPlaylist = async () => {
    if (!url) return;
    setLoading(true);
    try {
      const res = await apiFetch(`/info?url=${encodeURIComponent(url)}`);
      const data = await res.json();
      if (data.error) throw new Error(data.error);
      
      let items = [];
      if (data.entries) {
        items = data.entries.filter(i => i !== null);
      } else if (data.id) {
        items = [data];
      }

      const pData = {
        title: data.title || 'Unknown Video/Playlist',
        items: items
      };
      
      setPlaylist(pData);
      setSelected(new Set(items.map(i => i.id)));
      
      const artistMap = {};
      const albumMap = {};
      items.forEach(item => {
        artistMap[item.id] = item.uploader || data.uploader || 'Unknown Artist';
        albumMap[item.id] = item.album || data.title || 'Unknown Album';
      });
      setArtists(artistMap);
      setAlbums(albumMap);
    } catch (err) {
      showAlert("Error", err.message);
    }
    setLoading(false);
  };

  const applyBulkEdit = () => {
    const newArtists = { ...artists };
    const newAlbums = { ...albums };
    selected.forEach(id => {
      if (bulkArtist) newArtists[id] = bulkArtist;
      if (bulkAlbum) newAlbums[id] = bulkAlbum;
    });
    setArtists(newArtists);
    setAlbums(newAlbums);
    setBulkArtist('');
    setBulkAlbum('');
  };

  const queueDownloads = async () => {
    const items = playlist.items
      .filter(i => selected.has(i.id))
      .map(i => ({
        video_id: i.id,
        title: i.title,
        artist: artists[i.id],
        album: albums[i.id],
        thumbnail: i.thumbnail
      }));

    setQueueing(true);
    try {
      const res = await apiFetch('/queue', {
        method: 'POST',
        body: JSON.stringify(items)
      });
      const data = await res.json();
      showAlert("Success", `Successfully queued ${data.queued} downloads. You can monitor progress in the Jobs tab.`);
    } catch (e) {
      showAlert("Error", e.message);
    }
    setQueueing(false);
  };

  const toggleSelect = (id) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  };

  return (
    <div className="page-container fade-in-up">
      <div className="page-header">
        <h2 className="page-title">Downloads</h2>
        <p className="page-subtitle">Fetch music directly from YouTube</p>
      </div>
      
      <div className="card url-form">
        <input 
          className="url-input"
          placeholder="Paste YouTube Video or Playlist URL..."
          value={url}
          onChange={e => setUrl(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && fetchPlaylist()}
        />
        <button className="btn-primary" onClick={fetchPlaylist} disabled={loading}>
          {loading ? 'Analyzing...' : 'Fetch Metadata'}
        </button>
        </div>

        {playlist && (
        <div className="card fade-in-up bulk-bar">
          <div className="bulk-field">
            <label className="field-label">Bulk Artist Name</label>
            <input 
              className="field-input"
              placeholder="Common artist name..." 
              value={bulkArtist} 
              onChange={e => setBulkArtist(e.target.value)}
            />
          </div>
          <div className="bulk-field">
            <label className="field-label">Bulk Album Name</label>
            <input 
              className="field-input"
              placeholder="Common album name..." 
              value={bulkAlbum} 
              onChange={e => setBulkAlbum(e.target.value)}
            />
          </div>
          <button 
            className="btn-secondary bulk-apply"
            onClick={applyBulkEdit}
            disabled={selected.size === 0 || (!bulkArtist && !bulkAlbum)}
          >
            Apply to Selected ({selected.size})
          </button>
        </div>
        )}

        {playlist && (
        <div className="card card-flush fade-in-up">
          <div className="playlist-head">
            <div>
              <h3 className="playlist-title">{playlist.title}</h3>
              <p className="playlist-count">
                {playlist.items.length} tracks detected
              </p>
            </div>
            <button className="btn-primary" onClick={queueDownloads} disabled={queueing || selected.size === 0}>
              {queueing ? 'Queueing...' : `Download ${selected.size} Tracks`}
            </button>
          </div>

          <div className="table-scroll">
            <table className="playlist-table">
              <thead>
                <tr>
                  <th className="th-select">
                    <input 
                      type="checkbox" 
                      checked={selected.size === playlist.items.length}
                      onChange={() => {
                        if (selected.size === playlist.items.length) setSelected(new Set());
                        else setSelected(new Set(playlist.items.map(i => i.id)));
                      }}
                    />
                  </th>
                  <th>Track Name</th>
                  <th className="th-duration">Duration</th>
                  <th>Artist (Target Folder)</th>
                  <th>Album (Target Subfolder)</th>
                </tr>
              </thead>
              <tbody>
                {playlist.items.map(item => (
                  <tr key={item.id} style={{ opacity: selected.has(item.id) ? 1 : 0.5, transition: 'opacity var(--transition-fast)' }}>
                    <td className="td-center">
                      <input 
                        type="checkbox" 
                        checked={selected.has(item.id)}
                        onChange={() => toggleSelect(item.id)}
                      />
                    </td>
                    <td>
                      <span className="track-name">{item.title}</span>
                    </td>
                    <td className="td-duration">
                      {formatDuration(item.duration)}
                    </td>
                    <td>
                      <input 
                        className="cell-input"
                        value={artists[item.id] || ''}
                        onChange={e => setArtists({...artists, [item.id]: e.target.value})}
                      />
                    </td>
                    <td>
                      <input 
                        className="cell-input"
                        value={albums[item.id] || ''}
                        onChange={e => setAlbums({...albums, [item.id]: e.target.value})}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
