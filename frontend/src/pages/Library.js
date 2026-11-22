import React, { useState, useEffect } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';

export default function Library() {
  const { artistId } = useParams();
  const navigate = useNavigate();
  const { showAlert } = useModal();
  const [artists, setArtists] = useState([]);
  const [albums, setAlbums] = useState([]);
  const [scanning, setScanning] = useState(false);

  useEffect(() => {
    fetchArtists();
  }, []);

  const fetchArtists = async () => {
    try {
      const res = await apiFetch('/library/artists');
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
      apiFetch(`/library/artists/${artistId}/albums`)
        .then(r => r.json())
        .then(data => setAlbums(data || []))
        .catch(err => console.error("Error loading albums", err));
    }
  }, [artistId]);

  const triggerScan = async () => {
    setScanning(true);
    try {
      const res = await apiFetch('/library/scan', { method: 'POST' });
      if (res.ok) {
        showAlert('Library scan started', 'The scan runs in the background. Follow its progress in the Jobs tab.');
      } else {
        throw new Error("Failed to start scan");
      }
    } catch (e) {
      showAlert('Error', e.message);
    }
    setScanning(false);
  };

  return (
    <div className="library-layout">
      {/* Sidebar: Artist List */}
      <div className="artist-list">
        <div className="artist-list-head">
          <h3 className="artist-list-title">
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
        <div className="artist-list-scroll">
          {artists.map(artist => (
            <Link
              key={artist.id}
              to={`/library/${artist.id}`}
              className={`artist-link ${artistId === artist.id ? 'active' : ''}`}
            >
              {artist.name}
            </Link>
          ))}
        </div>
      </div>

      {/* Main: Album Grid */}
      <div className="album-pane">
        <h2 className="album-pane-title">Albums</h2>
        {albums.length === 0 ? (
          <div className="card album-empty">
            <p>No albums found for this artist.</p>
          </div>
        ) : (
          <div className="album-grid">
            {albums.map(album => (
              <Link 
                key={album.id} 
                to={`/library/${artistId}/${album.id}`}
                className="card album-link"
                onMouseEnter={e => {
                  e.currentTarget.style.transform = 'translateY(-4px)';
                  e.currentTarget.style.borderColor = 'var(--accent)';
                }}
                onMouseLeave={e => {
                  e.currentTarget.style.transform = 'none';
                  e.currentTarget.style.borderColor = 'var(--border)';
                }}
              >
                <div className="album-cover">
                  {album.cover_art ? (
                    <img 
                      src={window.location.origin + album.cover_art} 
                      alt=""
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
                <div className="album-title">
                  {album.title}
                </div>
                <div className="album-artist">
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
