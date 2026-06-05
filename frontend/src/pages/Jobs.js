import React, { useState, useEffect } from 'react';
import { useModal } from '../components/ModalContext';

const API = window.location.origin + '/api';

export default function Jobs() {
  const { showAlert } = useModal();
  const [jobs, setJobs] = useState([]);

  const fetchJobs = async () => {
    try {
      const res = await fetch(`${API}/jobs`);
      const data = await res.json();
      setJobs(data || []);
    } catch (err) {
      console.error(err);
    }
  };

  useEffect(() => {
    fetchJobs();
    const interval = setInterval(fetchJobs, 2000);
    return () => clearInterval(interval);
  }, []);

  const clearJobs = async () => {
    try {
      await fetch(`${API}/jobs/clear`, { method: 'POST' });
      fetchJobs();
    } catch (err) {
      showAlert("Error", err.message);
    }
  };

  return (
    <div className="page-container fade-in-up">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', marginBottom: 32 }}>
        <div>
          <h2 style={{ margin: '0 0 8px 0', fontSize: 28, letterSpacing: '-0.03em' }}>Background Jobs</h2>
          <p style={{ margin: 0, color: 'var(--text-muted)', fontSize: 15 }}>Monitor downloads and library scans</p>
        </div>
        <button className="btn-secondary" onClick={clearJobs}>
          Clear Completed
        </button>
      </div>

      <div className="card" style={{ padding: 0 }}>
        {jobs.length === 0 ? (
          <div className="empty-state">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg>
            <h3>No Active Jobs</h3>
            <p>Your job queue is currently empty.</p>
          </div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Type</th>
                <th>Status</th>
                <th>Details</th>
                <th>Progress</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map(job => {
                let detailsStr = job.details;
                try {
                  const parsed = JSON.parse(job.details);
                  detailsStr = parsed.title || parsed.path || job.details;
                } catch (e) {}

                return (
                  <tr key={job.id}>
                    <td>
                      <span style={{ textTransform: 'capitalize', fontWeight: 500, color: 'var(--text-main)' }}>
                        {job.type}
                      </span>
                    </td>
                    <td>
                      <span className={`badge ${job.status}`}>
                        {job.status}
                      </span>
                    </td>
                    <td style={{ maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {detailsStr}
                      {job.error && (
                        <div style={{ color: 'var(--danger)', fontSize: 12, marginTop: 4 }}>
                          {job.error}
                        </div>
                      )}
                    </td>
                    <td style={{ fontWeight: 500 }}>{job.progress || '0%'}</td>
                    <td style={{ color: 'var(--text-muted)', fontSize: 13 }}>
                      {new Date(job.created_at).toLocaleString()}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
