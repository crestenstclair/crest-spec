export function escapeHTML(value) {
  if (value === undefined || value === null || value === '') return '';
  const node = document.createElement('div');
  node.textContent = String(value);
  return node.innerHTML;
}

export function badge(text, color) {
  return '<span class="badge badge-' + color + '">' + escapeHTML(text) + '</span>';
}

export function statusBadge(status) {
  const map = {
    running: 'yellow', active: 'yellow', pending: 'muted', declared: 'muted',
    completed: 'green', complete: 'green', committed: 'green', passed: 'green', accepted: 'green',
    failed: 'red', errored: 'red', rejected: 'red', regressed: 'red',
    cancelled: 'orange', skipped: 'orange', timed_out: 'orange', stale: 'orange',
    blocked: 'purple', dispatched: 'blue', executing: 'blue', integrated: 'blue',
    candidate_submitted: 'yellow', validating: 'yellow', partially_implemented: 'yellow',
    create: 'green', modify: 'yellow', destroy: 'red', drift: 'orange',
  };
  return badge(status || '--', map[status] || 'muted');
}

export function stateColor(state) {
  const map = {
    committed: 'var(--green)', rejected: 'var(--red)', errored: 'var(--orange)',
    pending: 'var(--text-muted)', dispatched: 'var(--accent)',
    skipped: 'var(--text-muted)', blocked: 'var(--purple)',
  };
  return map[state] || 'var(--text-muted)';
}

export function stateClass(state) { return state || 'pending'; }

export function timeAgo(timestamp) {
  if (!timestamp) return '--';
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return '--';
  const seconds = Math.floor((Date.now() - date.getTime()) / 1000);
  if (seconds < 0) return 'just now';
  if (seconds < 60) return seconds + 's ago';
  if (seconds < 3600) return Math.floor(seconds / 60) + 'm ago';
  if (seconds < 86400) return Math.floor(seconds / 3600) + 'h ago';
  return date.toLocaleDateString();
}

export function shortID(id) { return id ? String(id).substring(0, 8) : '--'; }

export function truncate(value, length) {
  if (!value) return '';
  return value.length <= length ? escapeHTML(value) : escapeHTML(value.substring(0, length)) + '...';
}

export function formatDuration(milliseconds) {
  if (!milliseconds && milliseconds !== 0) return '--';
  if (milliseconds < 1000) return milliseconds + 'ms';
  if (milliseconds < 60000) return (milliseconds / 1000).toFixed(1) + 's';
  return (milliseconds / 60000).toFixed(1) + 'm';
}

export function announce(message) {
  const region = document.getElementById('dashboard-status');
  if (region) region.textContent = message;
}

export function recordStateNotice(state) {
  if (!state) return '';
  const labels = [];
  if (state.legacy) labels.push('legacy');
  if (state.stale) labels.push('stale');
  if (state.redacted) labels.push('redacted');
  if (!labels.length) return '';
  return '<div class="notice record-state">' +
    labels.map(label => statusBadge(label)).join(' ') +
    (state.reason ? ' <span>' + escapeHTML(state.reason) + '</span>' : '') +
    '</div>';
}
