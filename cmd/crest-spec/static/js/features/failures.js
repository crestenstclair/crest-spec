import { requestJSON } from '../api.js';
import { announce, badge, escapeHTML as esc, recordStateNotice, statusBadge, timeAgo } from '../components.js';
import { updateRouteParameters } from '../router.js';

const categories = [
  'missing_project_intent', 'missing_context', 'incorrect_context_selection', 'stale_context', 'context_truncation',
  'ambiguous_resource_contract', 'architectural_inconsistency', 'implementation_error', 'integration_error',
  'invalid_validation', 'behavioral_theater', 'tool_failure', 'host_failure', 'model_failure', 'unsupported_project_pattern',
];

async function showFailure(id) {
  const element = document.getElementById('failure-detail');
  element.innerHTML = '<div class="loading-state">Loading failure evidence…</div>';
  try {
    const data = await requestJSON('/api/v1/failures/' + encodeURIComponent(id));
    const failure = data.failure;
    element.innerHTML = '<div class="detail-panel"><div class="view-heading"><div><h2>' + esc(failure.category) + '</h2><p class="subtle">' + esc(failure.resource_id) + ' · attempt ' + esc(failure.attempt_id) + '</p></div>' + statusBadge(data.status?.state) + '</div>' +
      '<div class="relationship-grid"><section><h2>Root-cause origin</h2><p>' + esc(failure.origin) + ' · confidence ' + esc(failure.confidence) + '</p></section>' +
      '<section><h2>Corrective action</h2><p>' + esc(failure.corrective_action) + (failure.next_role ? ' → ' + esc(failure.next_role) : '') + '</p></section>' +
      '<section><h2>Affected goals</h2><p>' + esc((failure.goal_ids || []).join(', ') || 'None linked') + '</p></section>' +
      '<section><h2>Resolution</h2><p>' + esc(failure.resolution || 'Unresolved') + '</p></section></div>' +
      recordStateNotice(failure.record_state) +
      '<h2>Evidence · ' + esc(failure.evidence_source) + ':' + esc(failure.evidence_reference) + '</h2><pre>' + esc(data.evidence || 'No captured evidence') + '</pre>' +
      (data.evidence_truncated ? '<div class="notice">Inline evidence is truncated. Original size: ' + esc(data.evidence_bytes) + ' bytes.</div>' : '') +
      '<p class="subtle">Evidence hash ' + esc(failure.evidence_hash || '—') + '</p></div>';
    announce('Loaded failure ' + id);
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Could not load failure: ' + esc(error.message) + '</div>';
  }
}

export async function loadFailuresFeature() {
  const element = document.getElementById('failure-view');
  const status = document.getElementById('failure-status').value;
  const category = document.getElementById('failure-category').value;
  const query = new URLSearchParams({ limit: '100' });
  if (status) query.set('status', status);
  if (category) query.set('kind', category);
  updateRouteParameters({ failure_status: status, failure_category: category }, { replace: true });
  element.setAttribute('aria-busy', 'true');
  try {
    const page = await requestJSON('/api/v1/failures?' + query.toString());
    element.classList.remove('empty');
    element.innerHTML = page.items?.length ? '<table><thead><tr><th>Category</th><th>Resource</th><th>Goals</th><th>Origin</th><th>Action</th><th>Resolution</th><th>Time</th></tr></thead><tbody>' + page.items.map(failure =>
      '<tr><td><button type="button" class="relationship-button" data-failure-id="' + esc(failure.id) + '">' + esc(failure.category) + '</button></td><td class="resource-id">' + esc(failure.resource_id) + '</td><td>' + esc((failure.goal_ids || []).join(', ') || '—') + '</td><td>' + badge(failure.origin, 'muted') + '</td><td>' + esc(failure.corrective_action) + '</td><td>' + statusBadge(failure.resolution ? 'resolved' : 'unresolved') + '</td><td>' + timeAgo(failure.created_at) + '</td></tr>').join('') + '</tbody></table><section id="failure-detail" tabindex="-1" aria-live="polite"></section>' : '<div class="empty">No failure classification matches these filters.</div><section id="failure-detail" tabindex="-1"></section>';
    element.querySelectorAll('[data-failure-id]').forEach(button => button.addEventListener('click', async () => {
      updateRouteParameters({ view: 'failures', failure: button.dataset.failureId });
      await showFailure(button.dataset.failureId);
      document.getElementById('failure-detail')?.focus();
    }));
    const id = new URL(window.location.href).searchParams.get('failure');
    if (id) await showFailure(id);
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Could not load failures: ' + esc(error.message) + '</div>';
  } finally {
    element.removeAttribute('aria-busy');
  }
}

export function initializeFailureFilters() {
  const category = document.getElementById('failure-category');
  category.innerHTML += categories.map(value => '<option value="' + value + '">' + value.replaceAll('_', ' ') + '</option>').join('');
  const parameters = new URL(window.location.href).searchParams;
  document.getElementById('failure-status').value = parameters.get('failure_status') || '';
  category.value = parameters.get('failure_category') || '';
  document.getElementById('filter-failures').addEventListener('click', loadFailuresFeature);
}
