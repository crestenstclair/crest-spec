import { requestJSON } from '../api.js';
import { announce, badge, escapeHTML as esc, statusBadge } from '../components.js';
import { updateRouteParameters } from '../router.js';

let loaded = false;

function attemptOf(manifest) { return manifest.Attempt || manifest.attempt || {}; }

async function compareAttempts() {
  const left = document.getElementById('attempt-compare-left').value;
  const right = document.getElementById('attempt-compare-right').value;
  updateRouteParameters({ view: 'executions', attempt_left: left, attempt_right: right }, { replace: true });
  const element = document.getElementById('attempt-comparison');
  if (!left || !right || left === right) {
    element.innerHTML = '<div class="error-msg">Select two different attempts.</div>';
    return;
  }
  element.innerHTML = '<div class="loading-state">Comparing governed attempt lifecycle…</div>';
  try {
    const data = await requestJSON('/api/v1/attempt-comparison?left=' + encodeURIComponent(left) + '&right=' + encodeURIComponent(right));
    element.innerHTML = '<div class="detail-panel"><h2>Attempt comparison</h2><div class="state-counts">' + data.summary.map(item => badge(item, item.includes('match') ? 'green' : 'yellow')).join('') + '</div>' +
      '<table><thead><tr><th>Area</th><th>Changed</th><th>Before</th><th>After</th><th>Comparison rule</th></tr></thead><tbody>' + data.changes.map(change =>
        '<tr><td>' + esc(change.area) + '</td><td>' + statusBadge(change.changed ? 'changed' : 'same') + '</td><td class="mono comparison-value">' + esc(change.before) + '</td><td class="mono comparison-value">' + esc(change.after) + '</td><td class="subtle">' + esc(change.reason) + '</td></tr>').join('') + '</tbody></table>' +
      '<div class="relationship-grid"><section><h2>Left outcome</h2>' + statusBadge(data.left.status?.state) + '<p class="subtle">' + esc(data.left.status?.reason) + '</p></section><section><h2>Right outcome</h2>' + statusBadge(data.right.status?.state) + '<p class="subtle">' + esc(data.right.status?.reason) + '</p></section></div></div>';
    announce('Compared attempts ' + left + ' and ' + right);
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Could not compare attempts: ' + esc(error.message) + '</div>';
  }
}

export async function loadAttemptComparison() {
  if (loaded) return;
  const element = document.getElementById('attempt-comparison');
  try {
    const manifests = await requestJSON('/api/v1/contexts?limit=100');
    const left = document.getElementById('attempt-compare-left');
    const right = document.getElementById('attempt-compare-right');
    const options = manifests.map(manifest => {
      const attempt = attemptOf(manifest);
      return '<option value="' + esc(attempt.ID || attempt.id) + '">' + esc((attempt.ResourceID || attempt.resource_id) + ' #' + (attempt.RetryNumber || attempt.retry_number || 1) + ' · ' + (attempt.Role || attempt.role)) + '</option>';
    }).join('');
    left.innerHTML = options;
    right.innerHTML = options;
    if (manifests.length > 1) { left.selectedIndex = 1; right.selectedIndex = 0; }
    const parameters = new URL(window.location.href).searchParams;
    if (parameters.get('attempt_left')) left.value = parameters.get('attempt_left');
    if (parameters.get('attempt_right')) right.value = parameters.get('attempt_right');
    document.getElementById('compare-attempts').addEventListener('click', compareAttempts);
    loaded = true;
    if (left.value && right.value && parameters.get('attempt_left') && parameters.get('attempt_right')) await compareAttempts();
    else if (manifests.length < 2) element.innerHTML = '<div class="empty">At least two context attempts are required for comparison.</div>';
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Could not list attempts: ' + esc(error.message) + '</div>';
  }
}
