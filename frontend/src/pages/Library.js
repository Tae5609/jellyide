import React, { useState, useEffect, useMemo } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useModal } from '../components/ModalContext';

const API = window.location.origin + '/api';

const TrashIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M3 6h18"></path>
    <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"></path>
    <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"></path>
  </svg>
);

export default function Library() {
  const { artistId: urlArtistId } = useParams();
  const { showAlert, showConfirm } = useModal();
  const [artists, setArtists] = useState([]);
  const [search, setSearch] = useState('');
  const [selectedArtist, setSelectedArtist] = useState(null);
  const [albums, setAlbums] = useState([]);
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    fetch(`${API}/library/artists`)
      .then(r => r.json())
      .then(data => {
        setArtists(data || []);
        // If no artist selected yet but we have data, pick one
        if (!urlArtistId && data && data.length > 0) {
          navigate(`/library/${data[0].id}`, { replace: true });
        }
      });
  }, [urlArtistId, navigate]);

  useEffect(() => {
    if (urlArtistId) {
      setSelectedArtist(parseInt(urlArtistId));
    }
  }, [urlArtistId]);

  useEffect(() => {
    if (selectedArtist) {
      refreshAlbums();
    } else {
      setAlbums([]);
    }
  }, [selectedArtist]);

  const refreshAlbums = async () => {
    setLoading(true);
    try {
      const r = await fetch(`${API}/library/artists/${selectedArtist}/albums`);
      const data = await r.json();
      setAlbums(data || []);
    } catch (e) {}
    setLoading(false);
  };

  const triggerScan = async () => {
    try {
      await fetch(`${API}/library/scan`, { method: 'POST' });
      showAlert("Scan Triggered", "Check the 'Jobs' tab for progress.");
    } catch (e) {
      showAlert("Error", e.message);
    }
  };

  const deleteAlbum = async (e, albumId) => {
    e.stopPropagation();
    const confirmed = await showConfirm("Delete Album", "Are you sure you want to permanently delete this entire album and all its tracks from disk?");
    if (!confirmed) return;
    
    try {
      const res = await fetch(`${API}/library/albums/${albumId}`, { method: 'DELETE' });
      if (res.ok) {
        setAlbums(albums.filter(a => a.id !== albumId));
      } else {
        throw new Error("Failed to delete album");
      }
    } catch (e) {
      showAlert("Error", e.message);
    }
  };

  const openMetadata = (artistId, albumId) => {
    navigate(`/library/${artistId}/${albumId}`);
  };

  const filteredArtists = useMemo(() => {
    return artists.filter(a => a.name.toLowerCase().includes(search.toLowerCase()));
  }, [artists, search]);

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* Library Sidebar */}
      <div style={{ width: 280, borderRight: '1px solid var(--border)', display: 'flex', flexDirection: 'column', backgroundColor: 'var(--bg-sidebar)', zIndex: 5 }}>
        <div style={{ padding: '24px 16px 12px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', position: 'sticky', top: 0, backgroundColor: 'var(--bg-sidebar)' }}>
          <h3 style={{ margin: 0, fontSize: 12, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
            Artists
          </h3>
          <button onClick={triggerScan} title="Scan for new files" style={{ color: 'var(--text-muted)', padding: '4px 8px', borderRadius: '4px' }}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg>
          </button>
        </div>
        
        <div style={{ padding: '0 12px 12px', position: 'sticky', top: 56, backgroundColor: 'var(--bg-sidebar)', borderBottom: '1px solid var(--border)' }}>
          <input 
            className="search-input" 
            placeholder="Search artists..." 
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>

        <div style={{ flex: 1, overflowY: 'auto', padding: '12px 8px' }}>
          {filteredArtists.length === 0 ? (
            <div className="empty-state" style={{ padding: '32px 12px' }}>
              <p>No artists found.</p>
            </div>
          ) : (
            filteredArtists.map(artist => (
              <button
                key={artist.id}
                onClick={() => navigate(`/library/${artist.id}`)}
                style={{
                  width: '100%',
                  textAlign: 'left',
                  padding: '10px 12px',
                  borderRadius: 'var(--radius-sm)',
                  marginBottom: 4,
                  backgroundColor: selectedArtist === artist.id ? 'var(--bg-active)' : 'transparent',
                  color: selectedArtist === artist.id ? 'var(--text-main)' : 'var(--text-muted)',
                  fontWeight: selectedArtist === artist.id ? 500 : 400,
                  fontSize: 14,
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center'
                }}
              >
                <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{artist.name}</span>
              </button>
            ))
          )}
        </div>
      </div>

      {/* Library Main Content - Grid View */}
      <div style={{ flex: 1, padding: '40px 48px', overflowY: 'auto' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', marginBottom: 32 }}>
          <div>
            <h2 style={{ margin: '0 0 8px 0', fontSize: 28, letterSpacing: '-0.03em' }}>Albums</h2>
            <p style={{ margin: 0, color: 'var(--text-muted)', fontSize: 15 }}>Browse and manage your music collection</p>
          </div>
          <span style={{ color: 'var(--text-muted)', fontSize: 14 }}>{albums.length} albums</span>
        </div>

        {loading ? (
          <p style={{ color: 'var(--text-muted)' }}>Loading albums...</p>
        ) : albums.length === 0 ? (
          <div className="empty-state">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>
            <h3 style={{ marginTop: 16 }}>No Albums Found</h3>
            <p>We couldn't find any albums for this artist in the database. Ensure the files exist and trigger a scan.</p>
          </div>
        ) : (
          <div className="fade-in-up" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 24 }}>
            {albums.map(album => (
              <div 
                key={album.id} 
                className="album-card"
                onClick={() => openMetadata(album.artist_id, album.id)}
              >
                <div style={{ position: 'relative', aspectRatio: '1', marginBottom: 16 }}>
                  <img 
                    src={`${API}/library/albums/${album.id}/cover`} 
                    alt="" 
                    style={{ width: '100%', height: '100%', objectFit: 'cover', borderRadius: 'var(--radius-sm)', position: 'absolute', top: 0, left: 0, zIndex: 2 }} 
                    onError={(e) => { e.target.style.display = 'none'; }} 
                  />
                  <div className="album-cover-placeholder" style={{ margin: 0, width: '100%', height: '100%', position: 'absolute', top: 0, left: 0, zIndex: 1 }}>
                    <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M9 18V5l12-2v13"></path><circle cx="6" cy="18" r="3"></circle><circle cx="18" cy="16" r="3"></circle></svg>
                  </div>
                </div>
                
                <div style={{ flex: 1 }}>
                  <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 4, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', color: 'var(--text-main)' }}>
                    {album.title}
                  </div>
                  <div style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 16 }}>
                    {album.artist}
                  </div>
                </div>

                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', borderTop: '1px solid var(--border)', paddingTop: 12, marginTop: 'auto' }}>
                  <span style={{ fontSize: 12, color: 'var(--text-muted)', fontWeight: 500 }}>{album.track_count || 0} TRACKS</span>
                  <button 
                    onClick={(e) => deleteAlbum(e, album.id)}
                    style={{ color: 'var(--danger)', opacity: 0.5, padding: 4 }}
                    title="Delete Album"
                    onMouseEnter={(e) => e.currentTarget.style.opacity = 1}
                    onMouseLeave={(e) => e.currentTarget.style.opacity = 0.5}
                  >
                    <TrashIcon />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
