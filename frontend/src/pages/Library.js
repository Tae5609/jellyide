import React, { useState, useEffect } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';

const API = window.location.origin + '/api';

export default function Library() {
  const { artistId } = useParams();
  const navigate = useNavigate();
  const [artists, setArtists] = useState([]);
  const [albums, setAlbums] = useState([]);
  const [scanning, setScanning] = useState(false);

  useEffect(() => {
    fetchArtists();
  }, []);

  const fetchArtists = async () => {
    try {
      const res = await fetch(`${API}/library/artists`);
      const data = await res.json();
      const list = data || [];
      setArtists(list);
      // If no artist selected, select the first one automatically
      if (list.length > 0 && !artistId) {
        navigate(`/library/${list[0].id}`, { replace: true });
      }
    } catch (e) {
      console.error("Failed to load artists", e);
    }
  };

  useEffect(() => {
    if (artistId) {
      fetch(`${API}/library/artists/${artistId}/albums`)
        .then(r => r.json())
        .then(data => setAlbums(data || []))
        .catch(err => console.error("Error loading albums", err));
    }
  }, [artistId]);

  const triggerScan = async () => {
    setScanning(true);
    try {
      const res = await fetch(`${API}/library/scan`, { method: 'POST' });
      if (res.ok) {
        alert('Library scan started!');
      } else {
        throw new Error("Failed to start scan");
      }
    } catch (e) {
      alert(e.message);
    }
    setScanning(false);
  };

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* Sidebar: Artist List */}
      <div style={{ width: 260, borderRight: '1px solid var(--border)', display: 'flex', flexDirection: 'column', backgroundColor: 'var(--bg-sidebar)' }}>
        <div style={{ padding: '24px 16px 12px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <h3 style={{ margin: 0, fontSize: 12, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.1em' }}>
            Artists
          </h3>
          <button 
            className="btn-secondary" 
            style={{ padding: '4px 8px', fontSize: 10, fontWeight: 700 }}
            onClick={triggerScan}
            disabled={scanning}
          >
            {scanning ? '...' : 'SCAN'}
          </button>
        </div>
        <div style={{ flex: 1, overflowY: 'auto', padding: '0 8px' }}>
          {artists.map(artist => (
            <Link
              key={artist.id}
              to={`/library/${artist.id}`}
              style={{
                display: 'block',
                textDecoration: 'none',
                padding: '10px 12px',
                borderRadius: 8,
                marginBottom: 2,
                backgroundColor: artistId === artist.id ? 'var(--bg-hover)' : 'transparent',
                color: artistId === artist.id ? 'var(--text-main)' : 'var(--text-muted)',
                fontWeight: artistId === artist.id ? 600 : 400,
                fontSize: 14,
                transition: 'all 0.2s'
              }}
            >
              {artist.name}
            </Link>
          ))}
        </div>
      </div>

      {/* Main: Album Grid */}
      <div style={{ flex: 1, padding: 32, overflowY: 'auto' }}>
        <h2 style={{ marginBottom: 24, fontSize: 24 }}>Albums</h2>
        {albums.length === 0 ? (
          <div className="card" style={{ textAlign: 'center', padding: '60px 0' }}>
            <p style={{ color: 'var(--text-muted)', margin: 0 }}>No albums found for this artist.</p>
          </div>
        ) : (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(180px, 1fr))', gap: 32 }}>
            {albums.map(album => (
              <Link 
                key={album.id} 
                to={`/library/${artistId}/${album.id}`}
                className="card" 
                style={{ 
                  padding: 16, 
                  textDecoration: 'none', 
                  color: 'inherit',
                  display: 'flex',
                  flexDirection: 'column',
                  transition: 'transform 0.2s, border-color 0.2s'
                }}
                onMouseEnter={e => {
                  e.currentTarget.style.transform = 'translateY(-4px)';
                  e.currentTarget.style.borderColor = 'var(--accent)';
                }}
                onMouseLeave={e => {
                  e.currentTarget.style.transform = 'none';
                  e.currentTarget.style.borderColor = 'var(--border)';
                }}
              >
                <div style={{ 
                  aspectRatio: '1', 
                  backgroundColor: 'var(--bg-hover)', 
                  borderRadius: 6, 
                  marginBottom: 16, 
                  display: 'flex', 
                  alignItems: 'center', 
                  justifyContent: 'center', 
                  overflow: 'hidden',
                  boxShadow: '0 4px 12px rgba(0,0,0,0.2)'
                }}>
                  {album.cover_art ? (
                    <img 
                      src={window.location.origin + album.cover_art} 
                      alt="" 
                      style={{ width: '100%', height: '100%', objectFit: 'cover' }}
                      onError={(e) => {
                        e.target.onerror = null;
                        e.target.style.display = 'none';
                        e.target.parentNode.innerHTML = '<span style="font-size: 40px">🎵</span>';
                      }}
                    />
                  ) : (
                    <span style={{ fontSize: 40 }}>🎵</span>
                  )}
                </div>
                <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 4, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                  {album.title}
                </div>
                <div style={{ color: 'var(--text-muted)', fontSize: 13 }}>
                  {album.artist}
                </div>
              </Link>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
