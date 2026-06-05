import React, { useState } from 'react';
import { useModal } from '../components/ModalContext';

const API = window.location.origin + '/api';

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

  const fetchPlaylist = async () => {
    if (!url) return;
    setLoading(true);
    try {
      const res = await fetch(`${API}/info?url=${encodeURIComponent(url)}`);
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
      const res = await fetch(`${API}/queue`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
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
      <div style={{ marginBottom: 32 }}>
        <h2 style={{ margin: '0 0 8px 0', fontSize: 28, letterSpacing: '-0.03em' }}>Downloads</h2>
        <p style={{ margin: 0, color: 'var(--text-muted)', fontSize: 15 }}>Fetch music directly from YouTube</p>
      </div>
      
      <div className="card" style={{ marginBottom: 24, display: 'flex', gap: 12 }}>
        <input 
          style={{ flex: 1 }}
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
        <div className="card fade-in-up" style={{ marginBottom: 24, display: 'flex', gap: 12, alignItems: 'flex-end', backgroundColor: 'var(--bg-sidebar)', padding: '16px 24px' }}>
          <div style={{ flex: 1 }}>
            <label style={{ display: 'block', fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 8, textTransform: 'uppercase' }}>Bulk Artist Name</label>
            <input 
              placeholder="Common artist name..." 
              value={bulkArtist} 
              onChange={e => setBulkArtist(e.target.value)}
              style={{ width: '100%', height: 38 }}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label style={{ display: 'block', fontSize: 11, fontWeight: 600, color: 'var(--text-muted)', marginBottom: 8, textTransform: 'uppercase' }}>Bulk Album Name</label>
            <input 
              placeholder="Common album name..." 
              value={bulkAlbum} 
              onChange={e => setBulkAlbum(e.target.value)}
              style={{ width: '100%', height: 38 }}
            />
          </div>
          <button 
            className="btn-secondary" 
            style={{ height: 38, whiteSpace: 'nowrap' }}
            onClick={applyBulkEdit}
            disabled={selected.size === 0 || (!bulkArtist && !bulkAlbum)}
          >
            Apply to Selected ({selected.size})
          </button>
        </div>
        )}

        {playlist && (
        <div className="card fade-in-up" style={{ padding: 0 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '24px 24px 16px', borderBottom: '1px solid var(--border)' }}>
            <div>
              <h3 style={{ margin: 0, fontSize: 18 }}>{playlist.title}</h3>
              <p style={{ color: 'var(--text-muted)', margin: '4px 0 0 0', fontSize: 14 }}>
                {playlist.items.length} tracks detected
              </p>
            </div>
            <button className="btn-primary" onClick={queueDownloads} disabled={queueing || selected.size === 0}>
              {queueing ? 'Queueing...' : `Download ${selected.size} Tracks`}
            </button>
          </div>

          <table>
            <thead>
              <tr>
                <th style={{ width: 40, textAlign: 'center' }}>
                  <input 
                    type="checkbox" 
                    checked={selected.size === playlist.items.length}
                    onChange={() => {
                      if (selected.size === playlist.items.length) setSelected(new Set());
                      else setSelected(new Set(playlist.items.map(i => i.id)));
                    }}
                  />
                </th>
                <th>Track</th>
                <th>Artist (Target Folder)</th>
                <th>Album (Target Subfolder)</th>
              </tr>
            </thead>
            <tbody>
              {playlist.items.map(item => (
                <tr key={item.id} style={{ opacity: selected.has(item.id) ? 1 : 0.5, transition: 'opacity var(--transition-fast)' }}>
                  <td style={{ textAlign: 'center' }}>
                    <input 
                      type="checkbox" 
                      checked={selected.has(item.id)}
                      onChange={() => toggleSelect(item.id)}
                    />
                  </td>
                  <td>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                      {item.thumbnail ? (
                        <img src={item.thumbnail} alt="" style={{ width: 40, height: 40, borderRadius: 6, objectFit: 'cover' }} />
                      ) : (
                        <div style={{ width: 40, height: 40, borderRadius: 6, backgroundColor: 'var(--bg-hover)' }}></div>
                      )}
                      <span style={{ fontSize: 14, fontWeight: 500 }}>{item.title}</span>
                    </div>
                  </td>
                  <td>
                    <input 
                      value={artists[item.id] || ''}
                      onChange={e => setArtists({...artists, [item.id]: e.target.value})}
                      style={{ width: '100%' }}
                    />
                  </td>
                  <td>
                    <input 
                      value={albums[item.id] || ''}
                      onChange={e => setAlbums({...albums, [item.id]: e.target.value})}
                      style={{ width: '100%' }}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
