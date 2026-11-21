import React, { useState, useEffect } from 'react';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';

export default function Jobs() {
  const { showAlert } = useModal();
  const [jobs, setJobs] = useState([]);

  const fetchJobs = async () => {
    try {
      const res = await apiFetch('/jobs');
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
      await apiFetch('/jobs/clear', { method: 'POST' });
      fetchJobs();
    } catch (err) {
      showAlert("Error", err.message);
    }
  };

  const formatDetails = (job) => {
    try {
      const parsed = JSON.parse(job.details);
      return parsed.title || parsed.path || job.details;
    } catch (e) {
      return job.details;
    }
  };

  return (
    <div className="page-container fade-in-up">
      <div className="page-header-row">
        <div>
          <h2 className="page-title">Background Jobs</h2>
          <p className="page-subtitle">Monitor downloads and library scans</p>
        </div>
        <button className="btn-secondary" onClick={clearJobs}>
          Clear Completed
        </button>
      </div>

      <div className="card card-flush">
        {jobs.length === 0 ? (
          <div className="empty-state">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg>
            <h3>No Active Jobs</h3>
            <p>Your job queue is currently empty.</p>
          </div>
        ) : (
          <>
            <div className="table-scroll jobs-table-wrap">
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
                  {jobs.map(job => (
                    <tr key={job.id}>
                      <td>
                        <span className="job-type">
                          {job.type}
                        </span>
                      </td>
                      <td>
                        <span className={`badge ${job.status}`}>
                          {job.status}
                        </span>
                      </td>
                      <td className="job-details-cell">
                        {formatDetails(job)}
                        {job.error && (
                          <div className="job-error">
                            {job.error}
                          </div>
                        )}
                      </td>
                      <td className="job-progress">{job.progress || '0%'}</td>
                      <td className="job-time">
                        {new Date(job.created_at).toLocaleString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="job-cards">
              {jobs.map(job => (
                <div className="job-card" key={job.id}>
                  <div className="job-card-top">
                    <span className="job-type">
                      {job.type}
                    </span>
                    <span className={`badge ${job.status}`}>
                      {job.status}
                    </span>
                  </div>
                  <div className="job-details">
                    {formatDetails(job)}
                    {job.error && (
                      <div className="job-error">
                        {job.error}
                      </div>
                    )}
                  </div>
                  <div className="job-card-bottom">
                    <span className="job-progress">{job.progress || '0%'}</span>
                    <span className="job-time">
                      {new Date(job.created_at).toLocaleString()}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
