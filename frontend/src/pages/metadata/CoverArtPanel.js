import React, { useEffect, useRef, useState } from 'react';
import { apiFetch } from '../../api';
import { errorFromResponse, waitForJob } from './useEditorState';

const MAX_COVER_BYTES = 10 * 1024 * 1024;
const COVER_TYPES = ['image/jpeg', 'image/png'];

// CoverArtPanel shows the album cover (GET /library/albums/{id}/cover) with
// a fallback placeholder and offers upload (file picker + drag-and-drop),
// extract-from-file and use-from-lookup.
export default function CoverArtPanel({
  albumId,
  coverKey,
  lookupCoverUrl,
  onDone,
  onError,
}) {
  const [busy, setBusy] = useState(null); // 'upload' | 'extract' | 'url'
  const [dragOver, setDragOver] = useState(false);
  const [broken, setBroken] = useState(false);
  const fileRef = useRef(null);

  // A new cover (coverKey bump) should replace the placeholder after an
  // earlier 404, so retry loading the image whenever the key changes.
  useEffect(() => {
    setBroken(false);
  }, [coverKey]);

  const validate = (file) => {
    if (!COVER_TYPES.includes(file.type)) {
      return 'Only JPEG or PNG images are allowed';
    }
    if (file.size > MAX_COVER_BYTES) {
      return 'Image must be 10 MB or smaller';
    }
    return null;
  };

  const upload = async (file) => {
    const problem = validate(file);
    if (problem) {
      onError(problem);
      return;
    }
    setBusy('upload');
    try {
      const form = new FormData();
      form.append('file', file);
      const res = await apiFetch(`/library/albums/${albumId}/cover`, {
        method: 'POST',
        body: form,
      });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      const { job_id } = await res.json();
      await waitForJob(job_id);
      onDone();
    } catch (e) {
      onError(e.message);
    } finally {
      setBusy(null);
    }
  };

  const extract = async () => {
    setBusy('extract');
    try {
      const res = await apiFetch(`/library/albums/${albumId}/cover/extract`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      onDone();
    } catch (e) {
      onError(e.message);
    } finally {
      setBusy(null);
    }
  };

  const fromLookup = async () => {
    if (!lookupCoverUrl) return;
    setBusy('url');
    try {
      const res = await apiFetch(`/library/albums/${albumId}/cover/from-url`, {
        method: 'POST',
        body: JSON.stringify({ url: lookupCoverUrl }),
      });
      if (!res.ok) throw new Error(await errorFromResponse(res));
      const { job_id } = await res.json();
      await waitForJob(job_id);
      onDone();
    } catch (e) {
      onError(e.message);
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="bulk-section cover-section">
      <h3 className="bulk-heading">Cover Art</h3>
      <div
        className={`cover-drop ${dragOver ? 'drag-over' : ''}`}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragOver(false);
          const file = e.dataTransfer.files && e.dataTransfer.files[0];
          if (file) upload(file);
        }}
      >
        {broken ? (
          <div className="cover-placeholder" aria-label="No cover art">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round"><path d="M9 18V5l12-2v13"></path><circle cx="6" cy="18" r="3"></circle><circle cx="18" cy="16" r="3"></circle></svg>
            <span>No cover yet</span>
          </div>
        ) : (
          <img
            className="cover-img"
            src={`/api/library/albums/${albumId}/cover?t=${coverKey}`}
            alt="Album cover"
            onError={() => setBroken(true)}
          />
        )}
      </div>
      <input
        ref={fileRef}
        type="file"
        accept="image/png,image/jpeg"
        hidden
        onChange={(e) => {
          const file = e.target.files && e.target.files[0];
          if (file) upload(file);
          e.target.value = '';
        }}
      />
      <div className="cover-actions">
        <button
          type="button"
          className="btn-secondary btn-sm"
          onClick={() => fileRef.current && fileRef.current.click()}
          disabled={busy !== null}
        >
          {busy === 'upload' ? 'Uploading…' : 'Upload image'}
        </button>
        <button
          type="button"
          className="btn-secondary btn-sm"
          onClick={extract}
          disabled={busy !== null}
        >
          {busy === 'extract' ? 'Extracting…' : 'Extract from files'}
        </button>
        <button
          type="button"
          className="btn-secondary btn-sm"
          onClick={fromLookup}
          disabled={busy !== null || !lookupCoverUrl}
          title={
            lookupCoverUrl
              ? 'Re-download the cover from the selected MusicBrainz release'
              : 'Apply a release in the MusicBrainz lookup with "Set cover art from release" checked first'
          }
        >
          {busy === 'url' ? 'Downloading…' : 'Use from lookup'}
        </button>
      </div>
      <p className="cover-hint">Drop a JPEG or PNG here, or use the buttons. Max 10 MB.</p>
    </section>
  );
}
