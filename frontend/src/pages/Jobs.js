import React, { useState, useEffect } from 'react';
import { useModal } from '../components/ModalContext';
import { apiFetch } from '../api';

// Errors longer than this are shown truncated with a "details" expander.
const ERROR_TRUNCATE_AT = 80;

function truncateError(message) {
  if (message.length <= ERROR_TRUNCATE_AT) {
    return { text: message, truncated: false };
  }
  return { text: `${message.slice(0, ERROR_TRUNCATE_AT)}…`, truncated: true };
}

// formatRetryIn renders the time left until next_retry_at (e.g. "30s",
// "2m", "1h 5m"); null when the deadline is not in the future.
function formatRetryIn(nextRetryAt) {
  const ms = new Date(nextRetryAt).getTime() - Date.now();
  if (Number.isNaN(ms) || ms <= 0) return null;
  const secs = Math.ceil(ms / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  return `${hours}h ${mins % 60}m`;
}

function isFailedDownload(job) {
  return job.type === 'download' && job.status === 'failed';
}

// isRetryScheduled is true only while the badge has a countdown to show:
// the job is queued and next_retry_at is still in the future. A queued job
// whose deadline has already passed (waiting on the concurrency limit)
// shows no badge rather than "retrying in null".
function isRetryScheduled(job) {
  return job.status === 'queued' && formatRetryIn(job.next_retry_at) !== null;
}

export default function Jobs() {
  const { showAlert } = useModal();
  const [jobs, setJobs] = useState([]);
  const [retryingIds, setRetryingIds] = useState(() => new Set());
  const [retryingAll, setRetryingAll] = useState(false);
  const [expandedErrors, setExpandedErrors] = useState(() => new Set());

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

  // readError extracts {"error": ...} from a failed response body; null
  // when the body is not JSON or has no error field.
  const readError = async (res) => {
    try {
      const body = await res.json();
      if (body && body.error) return body.error;
    } catch (err) {
      // non-JSON error body: fall through to the status-code message
    }
    return null;
  };

  const clearJobs = async () => {
    try {
      await apiFetch('/jobs/clear', { method: 'POST' });
      fetchJobs();
    } catch (err) {
      showAlert('Error', err.message);
    }
  };

  const retryJob = async (id) => {
    setRetryingIds((prev) => new Set(prev).add(id));
    try {
      const res = await apiFetch(`/jobs/${id}/retry`, { method: 'POST' });
      if (!res.ok) {
        const bodyError = await readError(res);
        throw new Error(bodyError || `retry failed (HTTP ${res.status})`);
      }
      await fetchJobs();
    } catch (err) {
      showAlert('Retry failed', err.message);
    } finally {
      setRetryingIds((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    }
  };

  const retryAllFailed = async () => {
    setRetryingAll(true);
    try {
      const res = await apiFetch('/jobs/retry-failed', { method: 'POST' });
      if (!res.ok) {
        const bodyError = await readError(res);
        throw new Error(bodyError || `retry failed (HTTP ${res.status})`);
      }
      await fetchJobs();
    } catch (err) {
      showAlert('Retry failed', err.message);
    } finally {
      setRetryingAll(false);
    }
  };

  const toggleError = (id) => {
    setExpandedErrors((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const formatDetails = (job) => {
    try {
      const parsed = JSON.parse(job.details);
      return parsed.title || parsed.path || job.details;
    } catch (e) {
      return job.details;
    }
  };

  const failedDownloadCount = jobs.filter(isFailedDownload).length;

  const renderBadges = (job) => (
    <span className="job-badges">
      <span className={`badge ${job.status}`}>{job.status}</span>
      {isRetryScheduled(job) && (
        <span className="badge retry-scheduled">
          {`retrying in ${formatRetryIn(job.next_retry_at)}`}
        </span>
      )}
    </span>
  );

  const renderError = (job) => {
    if (!job.error) return null;
    const expanded = expandedErrors.has(job.id);
    const { text, truncated } = truncateError(job.error);
    return (
      <div className="job-error">
        <span className={expanded ? 'job-error-full' : undefined}>
          {expanded ? job.error : text}
        </span>
        {truncated && (
          <button type="button" className="job-error-toggle" onClick={() => toggleError(job.id)}>
            {expanded ? 'less' : 'details'}
          </button>
        )}
      </div>
    );
  };

  const renderRetryButton = (job) => {
    if (!isFailedDownload(job)) return null;
    const busy = retryingIds.has(job.id);
    return (
      <button
        type="button"
        className="btn-secondary job-retry-btn"
        onClick={() => retryJob(job.id)}
        disabled={busy}
      >
        {busy && <span className="spinner" aria-hidden="true" />}
        {busy ? 'Retrying…' : 'Retry'}
      </button>
    );
  };

  return (
    <div className="page-container fade-in-up">
      <div className="page-header-row">
        <div>
          <h2 className="page-title">Background Jobs</h2>
          <p className="page-subtitle">Monitor downloads and library scans</p>
        </div>
        <div className="page-header-actions">
          {failedDownloadCount > 0 && (
            <button
              type="button"
              className="btn-secondary"
              onClick={retryAllFailed}
              disabled={retryingAll}
            >
              {retryingAll && <span className="spinner" aria-hidden="true" />}
              {retryingAll ? 'Retrying…' : `Retry all failed (${failedDownloadCount})`}
            </button>
          )}
          <button className="btn-secondary" onClick={clearJobs}>
            Clear Completed
          </button>
        </div>
      </div>

      <div className="card card-flush">
        {jobs.length === 0 ? (
          <div className="empty-state">
            <svg
              width="48"
              height="48"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <circle cx="12" cy="12" r="10"></circle>
              <polyline points="12 6 12 12 16 14"></polyline>
            </svg>
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
                  {jobs.map((job) => (
                    <tr key={job.id}>
                      <td>
                        <span className="job-type">{job.type}</span>
                      </td>
                      <td className="job-status-cell">
                        {renderBadges(job)}
                        {renderRetryButton(job)}
                      </td>
                      <td className="job-details-cell">
                        <span className="job-details-main">
                          {formatDetails(job)}
                          {job.attempts > 0 && (
                            <span className="job-attempts"> attempt {job.attempts}</span>
                          )}
                        </span>
                        {renderError(job)}
                      </td>
                      <td className="job-progress">{job.progress || '0%'}</td>
                      <td className="job-time">{new Date(job.created_at).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <div className="job-cards">
              {jobs.map((job) => (
                <div className="job-card" key={job.id}>
                  <div className="job-card-top">
                    <span className="job-type">{job.type}</span>
                    {renderBadges(job)}
                  </div>
                  <div className="job-details">
                    {formatDetails(job)}
                    {job.attempts > 0 && (
                      <span className="job-attempts"> attempt {job.attempts}</span>
                    )}
                    {renderError(job)}
                  </div>
                  {isFailedDownload(job) && (
                    <div className="job-card-actions">{renderRetryButton(job)}</div>
                  )}
                  <div className="job-card-bottom">
                    <span className="job-progress">{job.progress || '0%'}</span>
                    <span className="job-time">{new Date(job.created_at).toLocaleString()}</span>
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
