import { requestJSON } from '../api.js';
import { announce, badge, escapeHTML as esc, statusBadge } from '../components.js';
import { updateRouteParameters } from '../router.js';

function list(values, empty = 'None') {
  if (!values?.length) return '<span class="empty-inline">' + esc(empty) + '</span>';
  return '<ul class="compact-list">' + values.map(value => '<li>' + esc(value) + '</li>').join('') + '</ul>';
}

function explanation(status = {}) {
  return '<div class="status-explanation"><div>' + statusBadge(status.state) + ' ' + esc(status.reason) + '</div>' +
    (status.missing?.length ? '<div><strong>Missing</strong>' + list(status.missing) + '</div>' : '') +
    (status.blockers?.length ? '<div><strong>Blockers</strong>' + list(status.blockers) + '</div>' : '') +
    (status.recommended_next?.length ? '<div><strong>Recommended next</strong>' + list(status.recommended_next) + '</div>' : '') + '</div>';
}

async function showImpact(resourceID, container) {
  container.innerHTML = '<div class="loading-state">Tracing resource impact…</div>';
  try {
    const impact = await requestJSON('/api/v1/resources/' + encodeURIComponent(resourceID) + '/impact');
    container.innerHTML = '<div class="detail-panel"><h2>Impact from ' + esc(resourceID) + '</h2><div class="relationship-grid">' +
      '<section><h2>Affected resources</h2>' + list(impact.affected_resources) + '</section>' +
      '<section><h2>Capabilities</h2>' + list(impact.capabilities) + '</section>' +
      '<section><h2>Goals</h2>' + list(impact.goals) + '</section>' +
      '<section><h2>Evidence to re-run</h2>' + list(impact.evidence) + '</section></div>' +
      (impact.regressed_goals?.length ? '<div class="error-msg">Completed goals requiring re-verification: ' + esc(impact.regressed_goals.join(', ')) + '</div>' : '') +
      '<h2>Why this impact exists</h2>' + list(impact.explanation) + '</div>';
  } catch (error) {
    container.innerHTML = '<div class="error-msg">Could not trace impact: ' + esc(error.message) + '</div>';
  }
}

function bindPlanActions(element, plan) {
  element.querySelectorAll('[data-operation-id]').forEach(button => {
    button.addEventListener('click', () => {
      const operationID = button.dataset.operationId;
      updateRouteParameters({ view: 'plan', operation: operationID });
      const row = document.getElementById('operation-' + CSS.escape(operationID));
      row.hidden = !row.hidden;
      if (!row.hidden) row.focus();
    });
  });
  element.querySelectorAll('[data-impact-resource]').forEach(button => {
    button.addEventListener('click', () => showImpact(button.dataset.impactResource, button.closest('.operation-detail').querySelector('.operation-impact')));
  });
  const operationID = new URL(window.location.href).searchParams.get('operation');
  if (operationID && plan.operations.some(operation => operation.id === operationID)) {
    const row = document.getElementById('operation-' + CSS.escape(operationID));
    if (row) row.hidden = false;
  }
}

export async function loadPlanFeature() {
  const element = document.getElementById('plan-view');
  element.setAttribute('aria-busy', 'true');
  try {
    const plan = await requestJSON('/api/v1/plan');
    const slices = plan.slices || [];
    const operations = plan.operations || [];
    element.classList.remove('empty');
    element.innerHTML = '<div class="view-heading"><div><h2>Current goal-directed plan</h2><p class="subtle">Project ' + esc(plan.project_name) + ' · specification ' + esc(plan.spec_hash) + '</p></div>' + statusBadge(plan.status?.state) + '</div>' +
      (plan.record_state?.stale ? '<div class="error-msg">Stale active session: ' + esc(plan.record_state.reason) + '</div>' : '') + explanation(plan.status) +
      '<h2>Capability slices</h2>' + (slices.length ? '<div class="slice-grid">' + slices.map(slice => '<article class="nested-card"><div class="view-heading"><strong>' + esc(slice.capability) + '</strong>' + statusBadge(slice.status) + '</div>' +
        '<p class="subtle">' + esc(slice.current_gap) + '</p><h2>Goals</h2>' + list(slice.goals) + '<h2>Expected behavior</h2>' + list(slice.expected_behavior) +
        '<h2>Completion evidence</h2>' + list(slice.expected_evidence) + '</article>').join('') + '</div>' : '<div class="empty">No incomplete capability slice remains.</div>') +
      '<h2>Execution waves</h2>' + (plan.waves?.length ? '<div class="state-counts">' + plan.waves.map(wave => '<span>' + badge('wave ' + wave.index + ' · ' + wave.state, wave.state === 'complete' ? 'green' : wave.state === 'active' ? 'blue' : 'muted') + '</span>').join('') + '</div>' : '<div class="empty">No waves remain.</div>') +
      '<h2 style="margin-top:20px">Operations</h2>' + (operations.length ? '<table><thead><tr><th>Operation</th><th>Resource</th><th>Change</th><th>Wave</th><th>Role</th><th>Status</th><th>Why</th></tr></thead><tbody>' + operations.map(operation =>
        '<tr><td><button type="button" class="relationship-button" data-operation-id="' + esc(operation.id) + '">' + esc(operation.id) + '</button></td><td><a class="relationship-button" href="?view=resources&amp;resource=' + encodeURIComponent(operation.resource_id) + '">' + esc(operation.resource_id) + '</a></td><td>' + badge(operation.kind, operation.kind === 'destroy' ? 'red' : operation.kind === 'create' ? 'green' : 'yellow') + ' ' + badge(operation.category, 'muted') + '</td><td>' + operation.wave_index + '</td><td>' + esc(operation.recommended_role) + '</td><td>' + statusBadge(operation.execution_status) + '</td><td class="subtle">' + esc(operation.reason) + '</td></tr>' +
        '<tr id="operation-' + esc(operation.id) + '" class="operation-detail" tabindex="-1" hidden><td colspan="7"><div class="detail-panel"><div class="relationship-grid"><section><h2>Goals</h2>' + list(operation.goals) + '</section><section><h2>Capabilities</h2>' + list(operation.capabilities) + '</section><section><h2>Expected behavior</h2>' + list(operation.expected_behavior) + '</section><section><h2>Expected evidence</h2>' + list(operation.expected_evidence) + '</section></div><button type="button" class="primary-button" data-impact-resource="' + esc(operation.resource_id) + '">Trace change impact</button><div class="operation-impact"></div></div></td></tr>').join('') + '</tbody></table>' : '<div class="empty">The accepted project state matches the current declarations.</div>');
    bindPlanActions(element, plan);
    announce('Loaded current plan with ' + operations.length + ' operations');
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Could not load the current plan: ' + esc(error.message) + '</div>';
  } finally {
    element.removeAttribute('aria-busy');
  }
}
