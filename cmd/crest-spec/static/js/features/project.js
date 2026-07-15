import { requestJSON } from '../api.js';
import { announce, badge, escapeHTML as esc, statusBadge, timeAgo } from '../components.js';
import { updateRouteParameters } from '../router.js';

let cachedOverview = null;

function list(values, empty = 'None') {
  if (!values || values.length === 0) return '<span class="empty-inline">' + esc(empty) + '</span>';
  return '<ul class="compact-list">' + values.map(value => '<li>' + esc(value) + '</li>').join('') + '</ul>';
}

function explanation(status = {}) {
  return '<div class="status-explanation">' +
    '<div>' + statusBadge(status.state || 'declared') + ' <span>' + esc(status.reason || '') + '</span></div>' +
    (status.missing?.length ? '<div><strong>Missing</strong>' + list(status.missing) + '</div>' : '') +
    (status.blockers?.length ? '<div><strong>Blocked by</strong>' + list(status.blockers) + '</div>' : '') +
    (status.regressions?.length ? '<div><strong>Regressions</strong>' + list(status.regressions) + '</div>' : '') +
    (status.recommended_next?.length ? '<div><strong>Recommended next</strong>' + list(status.recommended_next) + '</div>' : '') +
  '</div>';
}

export function renderProjectSummary(data) {
  const project = data?.project;
  if (!project) return;
  document.getElementById('project-summary').innerHTML =
    '<div style="margin-bottom:10px">' + statusBadge(data.status?.state || project.completion_status) + '</div>' +
    '<div style="font-size:13px">' + esc(project.mission) + '</div>' +
    '<div style="font-size:12px;color:var(--text-muted);margin-top:8px">' + esc(data.status?.reason || project.completion_reason) + '</div>';
}

function bindGoalLinks(container) {
  container.querySelectorAll('[data-goal-id]').forEach(button => {
    button.addEventListener('click', async () => {
      const goalID = button.dataset.goalId;
      updateRouteParameters({ view: 'project', goal: goalID });
      await renderGoal(goalID);
      document.getElementById('goal-detail')?.focus();
    });
  });
}

export function renderProjectDetail(data) {
  const element = document.getElementById('project-detail');
  const project = data.project || {};
  const required = new Set(project.required_goals || []);
  const goals = project.goals || [];
  const actors = project.actors || [];
  let html = '<div class="view-heading"><div><h2>Project overview</h2><p class="mission">' + esc(project.mission) + '</p></div>' +
    '<div>' + statusBadge(data.status?.state || project.completion_status) + '</div></div>' +
    explanation(data.status) +
    '<div class="relationship-grid"><section><h2>Actors</h2>' + list(actors.map(actor => actor.id + ' — ' + actor.description)) + '</section>' +
    '<section><h2>Explicit non-goals</h2>' + list((project.non_goals || []).map(item => item.id + ' — ' + item.description)) + '</section></div>' +
    '<h2>Goals</h2><table><thead><tr><th>Goal</th><th>Priority</th><th>Status</th><th>Capabilities</th><th>Contributing resources</th><th>Why</th></tr></thead><tbody>';
  goals.forEach(goal => {
    html += '<tr><td><button type="button" class="relationship-button" data-goal-id="' + esc(goal.id) + '">' + esc(goal.id) + '</button>' +
      '<div style="font-size:12px">' + esc(goal.description) + '</div></td>' +
      '<td>' + badge(required.has(goal.id) ? 'required' : (goal.priority || 'optional'), required.has(goal.id) ? 'blue' : 'muted') + '</td>' +
      '<td>' + statusBadge(goal.status) + '</td><td>' + list(goal.capabilities, 'Unlinked') + '</td>' +
      '<td>' + list(goal.resources, 'Missing') + '</td><td class="subtle">' + esc(goal.status_reason) + '</td></tr>';
  });
  html += '</tbody></table><section id="goal-detail" tabindex="-1" aria-live="polite"></section>';
  element.innerHTML = html;
  bindGoalLinks(element);
}

async function renderGoal(goalID) {
  const element = document.getElementById('goal-detail');
  if (!element) return;
  element.innerHTML = '<div class="loading-state">Loading goal ' + esc(goalID) + '…</div>';
  try {
    const data = await requestJSON('/api/v1/goals/' + encodeURIComponent(goalID));
    const goal = data.goal;
    const evidence = data.evidence || [];
    const acceptance = data.acceptance || [];
    element.innerHTML = '<div class="detail-panel"><div class="view-heading"><div><h2>Goal ' + esc(goal.id) + '</h2><p>' + esc(goal.description) + '</p></div>' + statusBadge(goal.status) + '</div>' +
      explanation(data.status) +
      '<div class="relationship-grid"><section><h2>Dependencies</h2>' + list(goal.depends_on) + '</section>' +
      '<section><h2>Resources</h2>' + list(goal.resources, 'No contributing resource') + '</section></div>' +
      '<h2>Requirements</h2>' + list((data.requirements || []).map(item => item.id + ' [' + item.kind + '] — ' + item.description)) +
      '<h2>Acceptance scenarios</h2>' + (acceptance.length ? acceptance.map(scenario => '<article class="nested-card"><strong>' + esc(scenario.id) + '</strong><p>' + esc(scenario.description) + '</p>' +
        list((scenario.steps || []).map(step => step.action + ' → ' + step.observes)) + '</article>').join('') : '<div class="empty">No acceptance scenario declared.</div>') +
      '<h2>Completion evidence</h2>' + (evidence.length ? '<table><thead><tr><th>Evidence</th><th>Kind</th><th>Currency</th><th>Classification</th><th>Source tree</th></tr></thead><tbody>' + evidence.map(item =>
        '<tr><td class="resource-id">' + esc(item.id) + '</td><td>' + esc(item.kind) + '</td><td>' + statusBadge(item.currency) + '</td><td>' + statusBadge(item.classification || 'missing') + '</td><td class="mono">' + esc(item.source_tree_hash || '—') + '</td></tr>').join('') + '</tbody></table>' : '<div class="empty">No evidence requirement declared.</div>') +
      '<h2>Status history</h2>' + ((data.history || []).length ? list(data.history.map(item => timeAgo(item.created_at) + ': ' + item.from_status + ' → ' + item.to_status + ' — ' + item.reason)) : '<div class="empty">No status transitions recorded.</div>') +
      '</div>';
    announce('Loaded goal ' + goalID);
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Could not load goal: ' + esc(error.message) + '</div>';
  }
}

export async function loadProjectFeature({ force = false } = {}) {
  const detail = document.getElementById('project-detail');
  if (detail) detail.setAttribute('aria-busy', 'true');
  try {
    if (!cachedOverview || force) cachedOverview = await requestJSON('/api/v1/project');
    renderProjectSummary(cachedOverview);
    if (detail) renderProjectDetail(cachedOverview);
    const goalID = new URL(window.location.href).searchParams.get('goal');
    if (goalID && detail) await renderGoal(goalID);
  } catch (error) {
    if (detail) detail.innerHTML = '<div class="error-msg">Error loading project intent: ' + esc(error.message) + '</div>';
  } finally {
    if (detail) detail.removeAttribute('aria-busy');
  }
  return cachedOverview;
}
