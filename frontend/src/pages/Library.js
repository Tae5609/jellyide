import React, { useState, useEffect, useRef } from 'react';
import { useParams, useNavigate, Link } from 'react-router-dom';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';

const SCAN_POLL_MS = 2000;
const SCAN_TERMINAL = ['completed', 'failed'];

export default function Library() {
  const { artistId } = useParams();
  const navigate = useNavigate();
  const { showAlert } = useModal();
  const [artists, setArtists] = useState([]);
  const [albums, setAlbums] = useState([]);
  const [scanning, setScanning] = useState(false);
  const scanPollRef = useRef(null);
  const scanBaselineRef = useRef(null);

  useEffect(() => {
    fetchArtists();
    return stopScanWatch;
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

  const stopScanWatch = () => {
    if (scanPollRef.current) {
      clearInterval(scanPollRef.current);
      scanPollRef.current = null;
    }
    scanBaselineRef.current = null;
  };

  // Newest scan job wins on created_at; ties break on id so the result
  // is a total order that does not depend on the array order of the
  // /jobs response, which is not guaranteed stable across polls.
  const newestScanJob = (scanJobs) =>
    scanJobs.reduce((a, b) => {
      const ta = new Date(a.created_at).getTime();
      const tb = new Date(b.created_at).getTime();
      return ta > tb || (ta === tb && a.id >= b.id) ? a : b;
    });

  // True once the scan started by triggerScan reaches a terminal state.
  // The baseline (captured right after the scan starts) records the job
  // statuses at that moment, so older completed scans still sitting in
  // the jobs table are not mistaken for the new scan finishing. Failed
  // scans end the watch too: a scan that dies partway may already have
  // written artists to the library, so they refetch in both cases.
  const scanSettled = async () => {
    let jobs;
    try {
      const res = await apiFetch('/jobs');
      jobs = (await res.json()) || [];
    } catch (e) {
      return false; // transient network error: keep watching
    }
    const scanJobs = jobs.filter(j => j.type === 'scan');
    if (scanJobs.length === 0) return false;
    if (scanBaselineRef.current === null) {
      scanBaselineRef.current = new Map(scanJobs.map(j => [j.id, j.status]));
      const newest = newestScanJob(scanJobs);
      return SCAN_TERMINAL.includes(newest.status);
    }
    return scanJobs.some(
      j =>
        SCAN_TERMINAL.includes(j.status) &&
        scanBaselineRef.current.get(j.id) !== j.status
    );
  };

  const finishScanWatch = () => {
    stopScanWatch();
    setScanning(false);
    fetchArtists();
  };

  const watchScanCompletion = async () => {
    stopScanWatch();
    if (await scanSettled()) {
      finishScanWatch();
      return;
    }
    scanPollRef.current = setInterval(async () => {
      if (await scanSettled()) finishScanWatch();
    }, SCAN_POLL_MS);
  };

  const triggerScan = async () => {
    setScanning(true);
    try {
      const res = await apiFetch('/library/scan', { method: 'POST' });
      if (!res.ok) throw new Error("Failed to start scan");
      showAlert('Library scan started', 'The scan runs in the background. Follow its progress in the Jobs tab.');
      // Keep the button busy until the scan job reaches a terminal
      // state, then refetch so newly scanned artists appear.
      watchScanCompletion();
    } catch (e) {
      setScanning(false);
      showAlert('Error', e.message);
    }
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
