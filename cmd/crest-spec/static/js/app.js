import { eventURL, fetchJSON } from './api.js';
import {
  badge, escapeHTML as esc, formatDuration, shortID, stateClass,
  stateColor, statusBadge, timeAgo, truncate,
} from './components.js';
import { initializeRouter, updateRouteParameters } from './router.js';
import { loadProjectFeature, renderProjectDetail, renderProjectSummary } from './features/project.js';
import { loadResourcesFeature } from './features/resources.js';
import { loadPlanFeature } from './features/plan.js';
import { initializeFailureFilters, loadFailuresFeature } from './features/failures.js';
import { loadAttemptComparison } from './features/attempts.js';

let currentTab = 'session';
let sseConnected = false;
let eventSource = null;
let cachedStatus = null;
let cachedSessionResources = null;
let cachedProject = null;
let cachedContexts = [];
let cachedExecutions = [];

// ─── SSE Connection ────────────────────────────────────────────────
function connectSSE() {
  if (eventSource) {
    eventSource.close();
  }
  try {
    eventSource = new EventSource(eventURL('/api/live-status'));
    eventSource.onopen = () => {
      sseConnected = true;
      updateSSEIndicator();
    };
    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        handleSSEData(data);
      } catch {}
    };
    eventSource.onerror = () => {
      sseConnected = false;
      updateSSEIndicator();
      eventSource.close();
      eventSource = null;
      // Fall back to polling
      setTimeout(connectSSE, 10000);
    };
  } catch {
    sseConnected = false;
    updateSSEIndicator();
  }
}

function updateSSEIndicator() {
  const el = document.getElementById('sse-status');
  if (sseConnected) {
    el.className = 'sse-indicator sse-connected';
    el.textContent = 'live';
  } else {
    el.className = 'sse-indicator sse-disconnected';
    el.textContent = 'polling';
  }
}

function handleSSEData(data) {
  document.getElementById('last-update').textContent = new Date().toLocaleTimeString();

  if (data.session) {
    updateSessionSidebar(data.session);
  } else {
    clearSessionSidebar();
  }
  if (data.session_resources) {
    cachedSessionResources = data.session_resources;
    if (data.session) {
      updateResourceDonut(data.session_resources);
      updateResourceStateCounts(data.session_resources);
    }
    if (currentTab === 'session') {
      renderSessionWaves(data.session_resources, data.session);
    }
  }
  if (data.latest_apply) {
    renderApplyInfo(data.latest_apply);
  }
}

// ─── Polling fallback ──────────────────────────────────────────────
async function pollRefresh() {
  if (sseConnected) return;

  const [status, resources, project] = await Promise.all([
    fetchJSON('/api/status'),
    fetchJSON('/api/resources'),
    fetchJSON('/api/v1/project'),
  ]);

  document.getElementById('last-update').textContent = new Date().toLocaleTimeString();

  if (status) {
    cachedStatus = status;
    const pulse = document.getElementById('status-pulse');
    const hasActivity = !!status.session;
    pulse.className = hasActivity ? 'pulse' : 'pulse idle';

    if (status.session) {
      updateSessionSidebar(status.session);
    } else {
      clearSessionSidebar();
    }
    if (status.latest_apply) {
      renderApplyInfo(status.latest_apply);
    }
    if (status.resource_states) {
      updateResourceDonutFromCounts(status.resource_states);
      updateResourceStateCountsFromCounts(status.resource_states);
    }
  }

  if (project) {
    cachedProject = project;
    renderProjectSummary(project);
    if (currentTab === 'project') renderProjectDetail(project);
  }

  loadTabData(currentTab);
}

// ─── Sidebar updates ──────────────────────────────────────────────
function updateSessionSidebar(sess) {
  const pulse = document.getElementById('status-pulse');
  pulse.className = 'pulse';

  document.getElementById('session-status').innerHTML =
    statusBadge(sess.Status || sess.status);
  document.getElementById('session-id').textContent =
    shortID(sess.ID || sess.id);
  document.getElementById('session-apply-id').textContent =
    shortID(sess.ApplyID || sess.apply_id || '');

  let waves = [];
  try { waves = JSON.parse(sess.WavesJSON || sess.waves_json || '[]'); } catch {}
  const total = waves.length;
  const current = sess.CurrentWave || sess.current_wave || 0;
  document.getElementById('wave-status').textContent = (current + 1) + ' / ' + total;
}

function clearSessionSidebar() {
  const pulse = document.getElementById('status-pulse');
  pulse.className = 'pulse idle';
  document.getElementById('session-status').innerHTML = badge('idle', 'muted');
  document.getElementById('session-id').textContent = '--';
  document.getElementById('session-apply-id').textContent = '--';
  document.getElementById('wave-status').textContent = '--';
  document.getElementById('session-progress-bar').innerHTML = '';
  document.getElementById('resource-state-counts').innerHTML = '';
  document.getElementById('donut-container').innerHTML =
    '<div class="empty" style="padding:12px">No session active</div>';
}

function updateResourceStateCounts(resources) {
  const counts = {};
  for (const r of resources) {
    const st = r.State || r.state || 'pending';
    counts[st] = (counts[st] || 0) + 1;
  }
  renderStateCountsHTML(counts);
}

function updateResourceStateCountsFromCounts(counts) {
  const mapped = {};
  for (const [k, v] of Object.entries(counts)) {
    if (v > 0 && k !== 'total') mapped[k] = v;
  }
  renderStateCountsHTML(mapped);
}

function renderStateCountsHTML(counts) {
  const el = document.getElementById('resource-state-counts');
  let html = '';
  for (const [state, count] of Object.entries(counts)) {
    html += '<div class="state-count"><div class="state-dot" style="background:' +
      stateColor(state) + '"></div>' + state + ': ' + count + '</div>';
  }
  el.innerHTML = html;
}

function updateResourceDonut(resources) {
  const counts = {};
  let total = 0;
  for (const r of resources) {
    const st = r.State || r.state || 'pending';
    counts[st] = (counts[st] || 0) + 1;
    total++;
  }
  renderDonut(counts, total);
}

function updateResourceDonutFromCounts(counts) {
  const mapped = {};
  let total = counts.total || 0;
  for (const [k, v] of Object.entries(counts)) {
    if (v > 0 && k !== 'total') mapped[k] = v;
  }
  renderDonut(mapped, total);
}

function renderDonut(counts, total) {
  const el = document.getElementById('donut-container');
  if (total === 0) {
    el.innerHTML = '<div class="empty" style="padding:12px">No resources</div>';
    return;
  }

  const entries = Object.entries(counts).filter(([, v]) => v > 0);
  let gradientParts = [];
  let cumPct = 0;
  for (const [state, count] of entries) {
    const pct = (count / total) * 100;
    const color = stateColor(state);
    gradientParts.push(color + ' ' + cumPct + '% ' + (cumPct + pct) + '%');
    cumPct += pct;
  }

  const gradient = 'conic-gradient(' + gradientParts.join(', ') + ')';

  let legendHTML = '';
  for (const [state, count] of entries) {
    legendHTML += '<div class="donut-legend-item">' +
      '<div class="donut-legend-swatch" style="background:' + stateColor(state) + '"></div>' +
      '<span>' + state + ': ' + count + '</span></div>';
  }

  el.innerHTML =
    '<div class="donut" style="background:' + gradient + '">' +
      '<div class="donut-hole">' + total + '</div>' +
    '</div>' +
    '<div class="donut-legend">' + legendHTML + '</div>';

  // Update progress bar too
  const barEl = document.getElementById('session-progress-bar');
  let barHTML = '';
  for (const [state, count] of entries) {
    const pct = (count / total) * 100;
    barHTML += '<div class="progress-seg ' + stateClass(state) + '" style="width:' + pct + '%"></div>';
  }
  barEl.innerHTML = barHTML;
}

// ─── Tab data loaders ──────────────────────────────────────────────
async function loadTabData(tab) {
  switch (tab) {
    case 'project': await loadProjectTab(); break;
	case 'plan': await loadPlanFeature(); break;
    case 'session': await loadSessionTab(); break;
    case 'generations': await loadGenerationsTab(); break;
    case 'contexts': await loadContextsTab(); break;
    case 'executions': await loadExecutionsTab(); break;
    case 'verifications': await loadVerificationsTab(); break;
    case 'evaluations': await loadEvaluationsTab(); break;
	case 'failures': await loadFailuresFeature(); break;
    case 'resources': await loadResourcesTab(); break;
    case 'invariants': await loadInvariantsTab(); break;
    case 'learnings': await loadLearningsTab(); break;
  }
}

// ─── Evaluation dashboard ─────────────────────────────────────────
async function loadEvaluationsTab() {
  const [summary, runs, datasets, configurations, cases, promotions] = await Promise.all([
    fetchJSON('/api/v1/evaluations/summary'),
    fetchJSON('/api/v1/evaluations/runs?limit=100'),
    fetchJSON('/api/v1/evaluations/datasets?limit=100'),
    fetchJSON('/api/v1/evaluations/configurations?limit=100'),
    fetchJSON('/api/v1/evaluations/cases?limit=100'),
    fetchJSON('/api/v1/evaluations/promotions?limit=100'),
  ]);
  renderEvaluationSummary(summary || {});
  renderEvaluationRuns(runs || []);
  renderEvaluationDatasets(datasets || [], configurations || []);
  renderEvaluationCases(cases || []);
  renderEvaluationPromotions(promotions || []);
  const runID = new URL(window.location.href).searchParams.get('run');
  const runIndex = (runs || []).findIndex(run => run.id === runID);
  if (runIndex >= 0) await toggleEvaluationRun(runID, runIndex, true);
}

function renderEvaluationSummary(summary) {
  const el = document.getElementById('evaluation-summary');
  const stats = [
    ['Cases', summary.cases || 0], ['Datasets', summary.datasets || 0],
    ['Configurations', summary.configurations || 0], ['Runs', summary.runs || 0],
    ['Running', summary.running || 0], ['Completed', summary.completed || 0],
    ['Candidate wins', summary.candidate_wins || 0], ['Inconclusive', summary.inconclusive || 0],
    ['Actionable promotions', summary.actionable_promotions || 0],
  ];
  el.classList.remove('empty');
  el.innerHTML = '<h2>Empirical improvement status</h2><div class="stat-row">' + stats.map(stat =>
    '<div><div class="stat-label">' + esc(stat[0]) + '</div><div class="stat-value">' + stat[1] + '</div></div>'
  ).join('') + '</div>';
}

function renderEvaluationRuns(runs) {
  const el = document.getElementById('evaluation-runs');
  if (!runs.length) {
    el.innerHTML = '<div class="empty">No evaluation runs yet. Curate cases, seal a dataset, then compare immutable configurations through MCP.</div>';
    return;
  }
  let html = '<table><thead><tr><th>Run</th><th>Dataset</th><th>Status</th><th>Conclusion</th><th>Variants</th><th>Assignments</th><th>Created</th></tr></thead><tbody>';
  runs.forEach((run, index) => {
    const variants = run.variants || [];
    const assignments = run.assignments || [];
    const terminal = assignments.filter(a => a.status === 'submitted' || a.status === 'cancelled').length;
    html += '<tr class="clickable" data-action="toggle-evaluation-run" data-record-id="' + esc(run.id) + '" data-index="' + index + '">' +
      '<td><div class="resource-id">' + esc(run.name || run.id) + '</div><div class="mono">' + esc(shortID(run.id)) + '</div></td>' +
      '<td class="mono">' + esc(shortID(run.dataset_id || '')) + '</td><td>' + statusBadge(run.status || '') + '</td>' +
      '<td>' + statusBadge(run.conclusion || 'pending') + (run.winning_variant ? '<div class="mono">' + esc(run.winning_variant) + '</div>' : '') + '</td>' +
      '<td>' + variants.map(v => badge(v.name + (v.baseline ? ' baseline' : ''), v.baseline ? 'muted' : 'blue')).join(' ') + '</td>' +
      '<td class="mono">' + terminal + ' / ' + assignments.length + '</td><td style="font-size:12px;color:var(--text-muted)">' + timeAgo(run.created_at) + '</td></tr>' +
      '<tr id="evaluation-run-' + index + '" style="display:none"><td colspan="7"></td></tr>';
  });
  el.innerHTML = html + '</tbody></table>';
}

async function toggleEvaluationRun(runID, index, restoring = false) {
  const row = document.getElementById('evaluation-run-' + index);
  if (row.style.display !== 'none') {
    row.style.display = 'none';
    if (!restoring) updateRouteParameters({ run: null });
    return;
  }
  if (!restoring) updateRouteParameters({ view: 'evaluations', run: runID });
  row.style.display = '';
  const td = row.querySelector('td');
  td.innerHTML = '<div class="drilldown"><div class="empty">Loading deterministic comparison...</div></div>';
  const run = await fetchJSON('/api/v1/evaluations/runs/' + encodeURIComponent(runID));
  if (!run) { td.innerHTML = '<div class="error-msg">Could not load run</div>'; return; }
  const comparisons = run.comparisons || [];
  const assignments = run.assignments || [];
  const observations = run.observations || [];
  let html = '<div class="drilldown"><div style="font-size:12px;color:var(--text-muted);margin-bottom:10px">' + esc(run.conclusion_reason || '') + '</div>' +
    '<h3>Metric comparisons</h3><table><thead><tr><th>Split</th><th>Variant</th><th>Metric</th><th>Baseline</th><th>Candidate</th><th>Change</th><th>Conclusion</th></tr></thead><tbody>';
  comparisons.forEach(c => {
    html += '<tr><td>' + badge(c.split || '', 'muted') + '</td><td class="mono">' + esc(c.candidate_variant || '') + '</td><td>' + esc(c.metric_name || '') + '</td>' +
      '<td class="mono">' + formatMetric(c.baseline_value) + ' (' + (c.baseline_sample_count || 0) + ')</td>' +
      '<td class="mono">' + formatMetric(c.candidate_value) + ' (' + (c.candidate_sample_count || 0) + ')</td>' +
      '<td class="mono">' + formatMetric(c.absolute_change) + '</td><td>' + statusBadge(c.conclusion || '') + (c.regression ? ' ' + badge('regression', 'red') : '') + '</td></tr>';
  });
  html += '</tbody></table><h3 style="margin-top:14px">Assignment provenance</h3><table><thead><tr><th>Case</th><th>Variant</th><th>Split</th><th>Status</th><th>Attempt</th></tr></thead><tbody>';
  assignments.forEach(a => {
    html += '<tr><td><button type="button" class="collapsible-toggle" data-action="show-evaluation-case" data-record-id="' + esc(a.case_id) + '">' + esc(shortID(a.case_id)) + '</button></td>' +
      '<td class="mono">' + esc(a.variant_name || '') + '</td><td>' + badge(a.split || '', 'muted') + '</td><td>' + statusBadge(a.status || '') + '</td>' +
      '<td class="mono">' + esc(a.attempt_id ? shortID(a.attempt_id) : 'not linked') + '</td></tr>';
  });
  html += '</tbody></table><h3 style="margin-top:14px">Raw per-case observations</h3><table><thead><tr><th>Case</th><th>Variant</th><th>Split</th><th>Metric</th><th>Value</th><th>Source</th></tr></thead><tbody>';
  observations.forEach(o => {
    html += '<tr><td class="mono">' + esc(shortID(o.case_id || '')) + '</td><td class="mono">' + esc(o.variant_name || '') + '</td><td>' + badge(o.split || '', 'muted') + '</td>' +
      '<td>' + esc(o.name || '') + '</td><td class="mono">' + (o.missing_reason ? esc('missing: ' + o.missing_reason) : formatMetric(o.value)) + '</td>' +
      '<td><span class="mono">' + esc(o.source_type || '') + '</span><div class="mono">' + esc(shortID(o.source_id || '')) + '</div></td></tr>';
  });
  td.innerHTML = html + '</tbody></table><div class="related-record"></div></div>';
}

function renderEvaluationDatasets(datasets, configurations) {
  const el = document.getElementById('evaluation-datasets');
  let html = '<table><thead><tr><th>Dataset</th><th>Status</th><th>Cases</th><th>Identity</th></tr></thead><tbody>';
  datasets.forEach(d => {
    const splits = {};
    (d.cases || []).forEach(c => splits[c.split] = (splits[c.split] || 0) + 1);
    html += '<tr><td>' + esc(d.name || '') + '</td><td>' + statusBadge(d.status || '') + '</td><td>' +
      Object.entries(splits).map(entry => badge(entry[0] + ' ' + entry[1], 'muted')).join(' ') + '</td><td class="mono">' + esc(shortID(d.identity_hash || 'draft')) + '</td></tr>';
  });
  html += '</tbody></table><h3 style="margin-top:14px">Configurations</h3><table><thead><tr><th>Name</th><th>Planner</th><th>Context</th><th>Role policy</th><th>Host / model</th><th>Identity</th></tr></thead><tbody>';
  configurations.forEach(c => {
    html += '<tr><td>' + esc(c.name || '') + '</td><td class="mono">' + esc(c.planner_version || '') + '</td>' +
      '<td><span class="mono">' + esc(c.context_selector_version || '') + '</span><div style="font-size:11px;color:var(--text-muted)">' + (c.context_budget_tokens || 0) + ' tokens</div></td>' +
      '<td class="mono">' + esc(c.role_policy_version || '') + '</td><td>' + esc((c.host_name || '') + ' / ' + (c.model || '')) + '</td><td class="mono">' + esc(shortID(c.identity_hash || '')) + '</td></tr>';
  });
  el.innerHTML = html + '</tbody></table>';
}

function renderEvaluationCases(cases) {
  const el = document.getElementById('evaluation-cases');
  if (!cases.length) { el.innerHTML = '<div class="empty">No reproducible cases captured yet.</div>'; return; }
  let html = '<table><thead><tr><th>Case</th><th>Provenance</th><th>Goal / capability</th><th>Resource</th><th>Repository</th><th>Created</th></tr></thead><tbody>';
  cases.forEach((c, index) => {
    html += '<tr class="clickable" data-action="toggle-evaluation-case" data-record-id="' + esc(c.id) + '" data-index="' + index + '"><td class="mono">' + esc(shortID(c.id)) + '</td><td>' + badge(c.provenance || '', 'purple') + '</td>' +
      '<td><div class="resource-id">' + esc(c.goal_id || 'project') + '</div><div class="mono">' + esc(c.capability_id || '') + '</div></td><td class="resource-id">' + esc(c.resource_id || '') + '</td>' +
      '<td class="mono">' + esc(shortID(c.repository_hash || '')) + '</td><td style="font-size:12px;color:var(--text-muted)">' + timeAgo(c.created_at) + '</td></tr>' +
      '<tr id="evaluation-case-' + index + '" style="display:none"><td colspan="6"></td></tr>';
  });
  el.innerHTML = html + '</tbody></table>';
}

async function toggleEvaluationCase(caseID, index) {
  const row = document.getElementById('evaluation-case-' + index);
  if (row.style.display !== 'none') { row.style.display = 'none'; return; }
  row.style.display = '';
  await renderEvaluationCaseInto(caseID, row.querySelector('td'));
}

async function showEvaluationCase(caseID, button) {
  const container = button.closest('.drilldown').querySelector('.related-record');
  await renderEvaluationCaseInto(caseID, container);
}

async function renderEvaluationCaseInto(caseID, container) {
  container.innerHTML = '<div class="drilldown"><div class="empty">Loading immutable case...</div></div>';
  const c = await fetchJSON('/api/v1/evaluations/cases/' + encodeURIComponent(caseID));
  if (!c) { container.innerHTML = '<div class="error-msg">Could not load case</div>'; return; }
  const links = [];
  if (c.context_manifest_id) links.push('<button type="button" class="collapsible-toggle" data-action="inspect-evaluation-record" data-record-url="' + esc('/api/v1/contexts/' + encodeURIComponent(c.context_manifest_id)) + '">Context ' + esc(shortID(c.context_manifest_id)) + '</button>');
  if (c.execution_id) links.push('<button type="button" class="collapsible-toggle" data-action="inspect-evaluation-record" data-record-url="' + esc('/api/v1/executions/' + encodeURIComponent(c.execution_id)) + '">Execution ' + esc(shortID(c.execution_id)) + '</button>');
  container.innerHTML = '<div class="drilldown"><div class="state-counts">' + links.join(' ') + '</div>' +
    '<h3>Case payload</h3><pre>' + esc(JSON.stringify(c.payload || {}, null, 2)) + '</pre>' +
    '<h3 style="margin-top:12px">Expected outcome</h3><pre>' + esc(JSON.stringify(c.expected_outcome || {}, null, 2)) + '</pre><div class="related-record-detail"></div></div>';
}

async function inspectEvaluationRecord(url, button) {
  const container = button.closest('.drilldown').querySelector('.related-record-detail');
  const record = await fetchJSON(url);
  container.innerHTML = record ? '<pre style="margin-top:12px">' + esc(JSON.stringify(record, null, 2)) + '</pre>' : '<div class="error-msg">Could not load linked record</div>';
}

function renderEvaluationPromotions(promotions) {
  const el = document.getElementById('evaluation-promotions');
  if (!promotions.length) { el.innerHTML = '<div class="empty">No reusable improvement has been proposed.</div>'; return; }
  let html = '<table><thead><tr><th>Change</th><th>Configuration</th><th>Run / variant</th><th>Status</th><th>Eligibility</th><th>Rollback</th><th>Decisions</th></tr></thead><tbody>';
  promotions.forEach((p, index) => {
    html += '<tr class="clickable" data-action="toggle-evaluation-promotion" data-record-id="' + esc(p.id) + '" data-index="' + index + '"><td>' + badge(p.change_kind || '', 'purple') + '<div class="mono">' + esc(p.target_identity || '') + '</div></td>' +
      '<td>' + esc(p.configuration_name || '') + '<div class="mono">' + esc(shortID(p.configuration_identity_hash || '')) + '</div></td>' +
      '<td class="mono">' + esc(shortID(p.run_id || '')) + ' / ' + esc(p.variant_name || '') + '</td><td>' + statusBadge(p.status || '') + '</td>' +
      '<td style="font-size:12px">' + esc(p.eligibility_reason || '') + '</td><td class="mono">' + esc(shortID(p.rollback_identity || '')) + '</td><td>' + (p.decisions || []).length + '</td></tr>' +
      '<tr id="evaluation-promotion-' + index + '" style="display:none"><td colspan="7"></td></tr>';
  });
  el.innerHTML = html + '</tbody></table>';
}

async function toggleEvaluationPromotion(id, index) {
  const row = document.getElementById('evaluation-promotion-' + index);
  if (row.style.display !== 'none') { row.style.display = 'none'; return; }
  row.style.display = '';
  const p = await fetchJSON('/api/v1/evaluations/promotions/' + encodeURIComponent(id));
  const decisions = p ? (p.decisions || []) : [];
  row.querySelector('td').innerHTML = p ? '<div class="drilldown"><h3>Exact proposed change</h3><pre>' + esc(JSON.stringify(p.change || {}, null, 2)) + '</pre>' +
    '<h3 style="margin-top:12px">Immutable human decisions</h3><pre>' + esc(JSON.stringify(decisions, null, 2)) + '</pre></div>' : '<div class="error-msg">Could not load proposal</div>';
}

function formatMetric(value) {
  return typeof value === 'number' ? value.toFixed(4) : 'missing';
}

// ─── Project overview and goal explorer ───────────────────────────
async function loadProjectTab() {
	cachedProject = await loadProjectFeature();
}

// ─── Tab: Learnings ────────────────────────────────────────────────
async function loadLearningsTab() {
  const el = document.getElementById('learnings-table');
  const learnings = await fetchJSON('/api/learnings');
  if (!learnings) {
    el.innerHTML = '<div class="error-msg">Error loading learnings</div>';
    return;
  }
  if (learnings.length === 0) {
    el.innerHTML = '<div class="empty">No active learnings recorded yet.</div>';
    return;
  }

  let html = '<table><thead><tr>' +
    '<th>Scope</th><th>Text</th><th>Confidence</th><th>Applied</th><th>Status</th>' +
  '</tr></thead><tbody>';

  for (const l of learnings) {
    const lang = l.scope_lang || '';
    const kind = l.scope_kind || '';
    const scope = (lang || kind) ? esc([lang, kind].filter(Boolean).join('/')) : '<span style="color:var(--text-muted)">any</span>';
    const conf = (typeof l.confidence === 'number') ? l.confidence.toFixed(2) : '--';

    html += '<tr>' +
      '<td style="font-size:12px">' + scope + '</td>' +
      '<td style="font-size:12px">' + esc(l.text || '') + '</td>' +
      '<td class="mono">' + conf + '</td>' +
      '<td class="mono">' + (l.times_applied || 0) + '</td>' +
      '<td>' + badge(l.status || '', 'blue') + '</td>' +
    '</tr>';
  }

  html += '</tbody></table>';
  el.innerHTML = html;
  const contextID = new URL(window.location.href).searchParams.get('context');
  const contextIndex = contexts.findIndex(manifest => (manifest.ID || manifest.id) === contextID);
  if (contextIndex >= 0) await toggleContextDrilldown(contextID, contextIndex, true);
}

// ─── Tab: Session (Wave Progress) ──────────────────────────────────
async function loadSessionTab() {
  const status = cachedStatus || await fetchJSON('/api/status');
  cachedStatus = status;
  if (!status || !status.session) {
    document.getElementById('session-waves').innerHTML =
      '<div class="empty">No active session</div>';
    return;
  }
  const sess = status.session;
  const resources = await fetchJSON('/api/session-resources/' + encodeURIComponent(sess.ID));
  if (!resources) {
    document.getElementById('session-waves').innerHTML =
      '<div class="error-msg">Error loading session resources</div>';
    return;
  }
  cachedSessionResources = resources;
  renderSessionWaves(resources, sess);
}

function renderSessionWaves(resources, sess) {
  const el = document.getElementById('session-waves');
  if (!resources || resources.length === 0) {
    el.innerHTML = '<div class="empty">No resources in session</div>';
    return;
  }

  // Group by wave
  const byWave = {};
  for (const r of resources) {
    const w = r.WaveIndex != null ? r.WaveIndex : (r.wave_index != null ? r.wave_index : 0);
    if (!byWave[w]) byWave[w] = [];
    byWave[w].push(r);
  }

  const waveKeys = Object.keys(byWave).sort((a, b) => a - b);
  let currentWave = 0;
  if (sess) {
    currentWave = sess.CurrentWave != null ? sess.CurrentWave : (sess.current_wave || 0);
  }

  let html = '';
  for (const w of waveKeys) {
    const waveResources = byWave[w];
    const counts = {};
    let total = waveResources.length;
    for (const r of waveResources) {
      const st = r.State || r.state || 'pending';
      counts[st] = (counts[st] || 0) + 1;
    }
    const isCurrent = parseInt(w) === currentWave;

    // Progress segments
    let progressHTML = '';
    for (const [state, count] of Object.entries(counts)) {
      const pct = (count / total) * 100;
      progressHTML += '<div class="progress-seg ' + stateClass(state) + '" style="width:' + pct + '%"></div>';
    }

    // State summary
    let stateHTML = '';
    for (const [state, count] of Object.entries(counts)) {
      stateHTML += '<span style="color:' + stateColor(state) + ';margin-right:12px">' +
        state + ': ' + count + '</span>';
    }

    const waveID = 'wave-' + w;
    html += '<div class="card wave-card">' +
      '<div class="wave-card-header" data-action="toggle-wave" data-target-id="' + esc(waveID) + '">' +
        '<div>' +
          '<span style="font-weight:600;color:var(--accent)">Wave ' + (parseInt(w) + 1) + '</span>' +
          (isCurrent ? ' ' + badge('current', 'yellow') : '') +
          ' <span style="font-size:12px;color:var(--text-muted)">' + total + ' resources</span>' +
        '</div>' +
        '<div style="font-size:12px">' + stateHTML + '</div>' +
      '</div>' +
      '<div class="progress-bar">' + progressHTML + '</div>' +
      '<div class="wave-card-body' + (isCurrent ? ' open' : '') + '" id="' + waveID + '">' +
        '<table><thead><tr>' +
          '<th>Resource</th><th>State</th><th>Attempts</th><th>Last Error</th><th>Updated</th>' +
        '</tr></thead><tbody>';

    for (const r of waveResources) {
      const rid = r.ResourceID || r.resource_id || '';
      const st = r.State || r.state || 'pending';
      const phase = r.Phase || r.phase || '';
      const attempts = r.Attempts || r.attempts || 0;
      const maxRetries = r.MaxRetries || r.max_retries || 0;
      const lastErr = r.LastError || r.last_error || '';
      const updatedAt = r.UpdatedAt || r.updated_at || '';
      const elapsedMs = r.ElapsedMS || r.elapsed_ms || 0;

      const phaseDisplay = phase && st === 'dispatched'
        ? '<span style="color:var(--purple);font-size:11px;margin-left:4px">' + esc(phase) + '</span>'
        : '';
      const elapsedDisplay = elapsedMs > 0
        ? '<span style="color:var(--text-muted);font-size:11px;margin-left:4px">' + formatDuration(elapsedMs) + '</span>'
        : '';

      const errDisplay = lastErr
        ? '<span class="collapsible" style="max-height:40px;display:block;font-size:11px;color:var(--text-muted)">' +
            esc(lastErr) + '</span>' +
          (lastErr.length > 80 ? '<span class="collapsible-toggle" data-action="toggle-collapsible">Show more</span>' : '')
        : '<span style="color:var(--text-muted)">--</span>';

      html += '<tr>' +
        '<td class="resource-id">' + esc(rid) + '</td>' +
        '<td>' + statusBadge(st) + phaseDisplay + elapsedDisplay + '</td>' +
        '<td class="mono">' + attempts + '/' + maxRetries + '</td>' +
        '<td>' + errDisplay + '</td>' +
        '<td style="color:var(--text-muted);font-size:12px">' + timeAgo(updatedAt) + '</td>' +
      '</tr>';
    }

    html += '</tbody></table></div></div>';
  }

  el.innerHTML = html;
}

function toggleWave(id) {
  const el = document.getElementById(id);
  if (el) el.classList.toggle('open');
}

function toggleCollapsible(btn) {
  const sib = btn.previousElementSibling;
  if (sib) {
    sib.classList.toggle('expanded');
    btn.textContent = sib.classList.contains('expanded') ? 'Show less' : 'Show more';
  }
}

// ─── Tab: Generations ──────────────────────────────────────────────
async function loadGenerationsTab() {
  const el = document.getElementById('generations-table');
  const gens = await fetchJSON('/api/generations-recent?limit=100');
  if (!gens) {
    el.innerHTML = '<div class="error-msg">Error loading generations</div>';
    return;
  }
  if (gens.length === 0) {
    el.innerHTML = '<div class="empty">No generations yet</div>';
    return;
  }
  renderGenerationsTable(gens);
}

function renderGenerationsTable(gens) {
  const el = document.getElementById('generations-table');
  let html = '<table><thead><tr>' +
    '<th>Resource</th><th>Attempt</th><th>Model</th><th>Outcome</th>' +
    '<th>Duration</th><th>Tokens</th><th>Cost</th><th>Time</th>' +
  '</tr></thead><tbody>';

  for (let i = 0; i < gens.length; i++) {
    const g = gens[i];
    const rid = g.resource_id || g.ResourceID || '';
    const attempt = g.retry_count != null ? g.retry_count : (g.RetryCount || 0);
    const model = g.model || g.Model || '--';
    const outcome = g.outcome || g.Outcome || 'pending';
    const duration = g.duration_ms || g.DurationMS || 0;
    const inTok = g.input_tokens || g.InputTokens || 0;
    const outTok = g.output_tokens || g.OutputTokens || 0;
    const cost = g.cost_usd || g.CostUSD || 0;
    const ts = g.created_at || g.CreatedAt || '';
    const gID = g.id || g.ID || i;

    html += '<tr class="clickable" data-action="toggle-generation" data-record-id="' + esc(String(gID)) + '" data-resource-id="' + esc(rid) + '" data-index="' + i + '">' +
      '<td class="resource-id">' + esc(rid) + '</td>' +
      '<td class="mono">#' + (attempt + 1) + '</td>' +
      '<td style="font-size:12px;color:var(--text-muted)">' + esc(model) + '</td>' +
      '<td>' + statusBadge(outcome) + '</td>' +
      '<td class="mono">' + formatDuration(duration) + '</td>' +
      '<td class="mono" style="font-size:11px">' + inTok + ' / ' + outTok + '</td>' +
      '<td class="mono" style="font-size:11px">$' + (cost ? cost.toFixed(4) : '0') + '</td>' +
      '<td style="color:var(--text-muted);font-size:12px">' + timeAgo(ts) + '</td>' +
    '</tr>' +
    '<tr id="gen-drilldown-' + i + '" style="display:none"><td colspan="8"></td></tr>';
  }

  html += '</tbody></table>';
  el.innerHTML = html;
}

async function toggleGenDrilldown(genID, resourceID, idx) {
  const row = document.getElementById('gen-drilldown-' + idx);
  if (!row) return;

  if (row.style.display !== 'none') {
    row.style.display = 'none';
    return;
  }

  const td = row.querySelector('td');
  td.innerHTML = '<div class="drilldown"><div class="empty">Loading...</div></div>';
  row.style.display = '';

  // Fetch full generation details for this resource
  const gens = await fetchJSON('/api/generations/' + encodeURIComponent(resourceID));
  if (!gens || gens.length === 0) {
    td.innerHTML = '<div class="drilldown"><div class="error-msg">Could not load details</div></div>';
    return;
  }

  // Find matching generation
  const gen = gens.find(g => (g.ID || g.id) === genID) || gens[0];

  let html = '<div class="drilldown">';

  // Rejection reason
  const rejection = gen.RejectionReason || gen.rejection_reason || '';
  if (rejection) {
    html += '<h3>Rejection Reason</h3><p style="color:var(--red);font-size:13px;margin-bottom:12px">' + esc(rejection) + '</p>';
  }

  // Prompt
  const prompt = gen.PromptText || gen.prompt_text || '';
  if (prompt) {
    const promptID = 'prompt-' + idx;
    html += '<h3>Prompt</h3>' +
      '<div class="collapsible" id="' + promptID + '" style="max-height:100px">' +
        '<pre>' + esc(prompt) + '</pre>' +
      '</div>' +
      '<span class="collapsible-toggle" data-action="toggle-collapsible">Show more</span>';
  }

  // Output
  const output = gen.OutputText || gen.output_text || '';
  if (output) {
    const outputID = 'output-' + idx;
    html += '<h3 style="margin-top:12px">Output</h3>' +
      '<div class="collapsible" id="' + outputID + '" style="max-height:100px">' +
        '<pre>' + esc(output) + '</pre>' +
      '</div>' +
      '<span class="collapsible-toggle" data-action="toggle-collapsible">Show more</span>';
  }

  // Meta
  html += '<div style="margin-top:12px;font-size:12px;color:var(--text-muted)">' +
    'Model: ' + esc(gen.Model || gen.model || '') +
    ' | Duration: ' + formatDuration(gen.DurationMS || gen.duration_ms || 0) +
    ' | Tokens: ' + (gen.InputTokens || gen.input_tokens || 0) + ' in / ' + (gen.OutputTokens || gen.output_tokens || 0) + ' out' +
    ' | Cost: $' + ((gen.CostUSD || gen.cost_usd || 0).toFixed(4)) +
  '</div>';

  html += '</div>';
  td.innerHTML = html;
}

// ─── Tab: Context Inspector ───────────────────────────────────────
async function loadContextsTab() {
  const el = document.getElementById('contexts-table');
  const contexts = await fetchJSON('/api/v1/contexts?limit=100');
  if (!contexts) {
    el.innerHTML = '<div class="error-msg">Error loading context attempts</div>';
    return;
  }
  cachedContexts = contexts;
  const restoreComparison = populateContextCompare(contexts);
	if (restoreComparison) await compareSelectedContexts();
  if (contexts.length === 0) {
    el.innerHTML = '<div class="empty">No context attempts yet. Calling spec/context creates the first immutable manifest.</div>';
    return;
  }

  let html = '<table><thead><tr><th>Resource</th><th>Retry</th><th>Role</th><th>Allocation</th><th>Status</th><th>Context Hash</th><th>Time</th></tr></thead><tbody>';
  contexts.forEach((manifest, index) => {
    const attempt = manifest.Attempt || manifest.attempt || {};
    const id = manifest.ID || manifest.id || '';
    const resource = attempt.ResourceID || attempt.resource_id || '';
    const retry = attempt.RetryNumber || attempt.retry_number || 1;
    const role = attempt.Role || attempt.role || '';
    const estimated = manifest.EstimatedTokens || manifest.estimated_tokens || 0;
    const budget = manifest.BudgetTokens || manifest.budget_tokens || 0;
    const blocked = manifest.Blocked || manifest.blocked;
    const status = blocked ? 'context_blocked' : (attempt.Status || attempt.status || 'context_prepared');
    const hash = manifest.ContextHash || manifest.context_hash || '';
    const created = manifest.CreatedAt || manifest.created_at || '';
    html += '<tr class="clickable" data-action="toggle-context" data-record-id="' + esc(id) + '" data-index="' + index + '">' +
      '<td class="resource-id">' + esc(resource) + '</td>' +
      '<td class="mono">#' + retry + '</td>' +
      '<td style="font-size:12px">' + esc(role) + '</td>' +
      '<td class="mono">' + estimated + ' / ' + budget + '</td>' +
      '<td>' + statusBadge(status) + '</td>' +
      '<td class="mono" title="' + esc(hash) + '">' + esc(shortID(hash)) + '</td>' +
      '<td style="color:var(--text-muted);font-size:12px">' + timeAgo(created) + '</td>' +
    '</tr><tr id="context-drilldown-' + index + '" style="display:none"><td colspan="7"></td></tr>';
  });
  html += '</tbody></table>';
  el.innerHTML = html;
}

function populateContextCompare(contexts) {
  const left = document.getElementById('context-compare-left');
  const right = document.getElementById('context-compare-right');
  const options = contexts.map(manifest => {
    const attempt = manifest.Attempt || manifest.attempt || {};
    const id = manifest.ID || manifest.id || '';
    const label = (attempt.ResourceID || attempt.resource_id || '') + ' #' + (attempt.RetryNumber || attempt.retry_number || 1) + ' · ' + shortID(id);
    return '<option value="' + esc(id) + '">' + esc(label) + '</option>';
  }).join('');
  left.innerHTML = options;
  right.innerHTML = options;
  if (contexts.length > 1) {
    left.selectedIndex = 1;
    right.selectedIndex = 0;
  }
	const parameters = new URL(window.location.href).searchParams;
	const leftID = parameters.get('context_left');
	const rightID = parameters.get('context_right');
	if (leftID && contexts.some(item => (item.ID || item.id) === leftID)) left.value = leftID;
	if (rightID && contexts.some(item => (item.ID || item.id) === rightID)) right.value = rightID;
	return Boolean(leftID && rightID && left.value === leftID && right.value === rightID && leftID !== rightID);
}

async function toggleContextDrilldown(manifestID, index, restoring = false) {
  const row = document.getElementById('context-drilldown-' + index);
  if (!row) return;
  if (row.style.display !== 'none') {
    row.style.display = 'none';
    if (!restoring) updateRouteParameters({ context: null });
    return;
  }
  if (!restoring) updateRouteParameters({ view: 'contexts', context: manifestID });
  row.style.display = '';
  const td = row.querySelector('td');
  td.innerHTML = '<div class="drilldown"><div class="empty">Reconstructing immutable context...</div></div>';
  const manifest = await fetchJSON('/api/v1/contexts/' + encodeURIComponent(manifestID));
  if (!manifest) {
    td.innerHTML = '<div class="drilldown"><div class="error-msg">Could not load context manifest</div></div>';
    return;
  }
  const attempt = manifest.Attempt || manifest.attempt || {};
  const sections = manifest.Sections || manifest.sections || [];
  const templateHashes = manifest.TemplateHashes || manifest.template_hashes || {};
  let html = '<div class="drilldown">' +
    '<div class="stat-row" style="margin-bottom:14px">' +
      '<div><div class="stat-label">Attempt</div><div class="mono">' + esc(attempt.ID || attempt.id || '') + '</div></div>' +
      '<div><div class="stat-label">Parent</div><div class="mono">' + esc(attempt.ParentAttemptID || attempt.parent_attempt_id || 'first attempt') + '</div></div>' +
      '<div><div class="stat-label">Selector</div><div class="mono">' + esc(manifest.SelectorVersion || manifest.selector_version || '') + '</div></div>' +
    '</div>' +
    '<h3>Template hashes</h3><pre>' + esc(JSON.stringify(templateHashes, null, 2)) + '</pre>' +
    '<h3 style="margin-top:12px">Selection decisions</h3>' +
    '<table><thead><tr><th>Section</th><th>Source</th><th>Decision</th><th>Tokens</th><th>Reason</th></tr></thead><tbody>';
  sections.forEach((section, sectionIndex) => {
    const decision = section.Decision || section.decision || '';
    const title = section.Title || section.title || section.Kind || section.kind || '';
    const source = (section.SourceKind || section.source_kind || '') + ':' + (section.SourceID || section.source_id || '');
    const tokens = section.EstimatedTokens || section.estimated_tokens || 0;
    const reason = section.Reason || section.reason || '';
    const content = section.Content || section.content || '';
    const contentID = 'context-content-' + index + '-' + sectionIndex;
    html += '<tr><td><strong>' + esc(title) + '</strong>' + (content ? '<br><span class="collapsible-toggle" data-action="toggle-context-content" data-target-id="' + esc(contentID) + '">show snapshot</span>' : '') + '</td>' +
      '<td class="mono">' + esc(source) + '</td><td>' + statusBadge(decision) + '</td><td class="mono">' + tokens + '</td><td style="font-size:12px;color:var(--text-muted)">' + esc(reason) + '</td></tr>';
    if (content) {
      html += '<tr id="' + contentID + '" style="display:none"><td colspan="5"><pre>' + esc(content) + '</pre></td></tr>';
    }
  });
  html += '</tbody></table>' +
    '<h3 style="margin-top:12px">Rendered system prompt</h3><pre>' + esc(manifest.SystemPrompt || manifest.system_prompt || '') + '</pre>' +
    '<h3 style="margin-top:12px">Rendered task prompt</h3><pre>' + esc(manifest.RenderedPrompt || manifest.rendered_prompt || '') + '</pre>' +
    '</div>';
  td.innerHTML = html;
}

function toggleContextContent(id) {
  const row = document.getElementById(id);
  if (row) row.style.display = row.style.display === 'none' ? '' : 'none';
}

async function compareSelectedContexts() {
  const left = document.getElementById('context-compare-left').value;
  const right = document.getElementById('context-compare-right').value;
	updateRouteParameters({ context_left: left, context_right: right }, { replace: true });
  const el = document.getElementById('context-comparison');
  if (!left || !right || left === right) {
    el.innerHTML = '<div class="error-msg">Select two different context attempts.</div>';
    return;
  }
  el.innerHTML = '<div class="empty">Comparing context decisions...</div>';
  const comparison = await fetchJSON('/api/v1/context-comparison?left=' + encodeURIComponent(left) + '&right=' + encodeURIComponent(right));
  if (!comparison) {
    el.innerHTML = '<div class="error-msg">Could not compare contexts</div>';
    return;
  }
  const added = comparison.AddedSections || comparison.added_sections || [];
  const removed = comparison.RemovedSections || comparison.removed_sections || [];
  const changed = comparison.ChangedSections || comparison.changed_sections || [];
  const unchanged = comparison.UnchangedSections || comparison.unchanged_sections || 0;
  let html = '<div class="drilldown" style="margin:0 0 16px 0">' +
    '<h3>Structural comparison</h3><div class="state-counts">' +
      '<span>' + badge(added.length + ' added', added.length ? 'green' : 'muted') + '</span>' +
      '<span>' + badge(removed.length + ' removed', removed.length ? 'red' : 'muted') + '</span>' +
      '<span>' + badge(changed.length + ' changed', changed.length ? 'yellow' : 'muted') + '</span>' +
      '<span>' + badge(unchanged + ' unchanged', 'muted') + '</span>' +
    '</div><div style="font-size:12px;color:var(--text-muted);margin-top:10px">' +
      'Budget changed: ' + !!(comparison.BudgetChanged || comparison.budget_changed) +
      ' · Selector changed: ' + !!(comparison.SelectorChanged || comparison.selector_changed) +
      ' · Same context: ' + !!(comparison.SameContext || comparison.same_context) +
    '</div>';
  const changes = added.concat(removed).concat(changed);
  if (changes.length) {
    html += '<table style="margin-top:10px"><thead><tr><th>Source</th><th>Changes</th><th>Before</th><th>After</th></tr></thead><tbody>';
    changes.forEach(change => {
      const before = change.Before || change.before;
      const after = change.After || change.after;
      html += '<tr><td class="mono">' + esc(change.SourceKey || change.source_key || '') + '</td>' +
        '<td>' + esc((change.Changes || change.changes || []).join(', ')) + '</td>' +
        '<td>' + esc(before ? ((before.Decision || before.decision || '') + ' ' + shortID(before.ContentHash || before.content_hash || before.OriginalHash || before.original_hash || '')) : '—') + '</td>' +
        '<td>' + esc(after ? ((after.Decision || after.decision || '') + ' ' + shortID(after.ContentHash || after.content_hash || after.OriginalHash || after.original_hash || '')) : '—') + '</td></tr>';
    });
    html += '</tbody></table>';
  }
  html += '</div>';
  el.innerHTML = html;
}

// ─── Tab: Execution Inspector ─────────────────────────────────────
async function loadExecutionsTab() {
	await loadAttemptComparison();
  const el = document.getElementById('executions-table');
  const executions = await fetchJSON('/api/v1/executions?limit=100');
  if (!executions) {
    el.innerHTML = '<div class="error-msg">Error loading execution manifests</div>';
    return;
  }
  cachedExecutions = executions;
  const restoreComparison = populateExecutionCompare(executions);
	if (restoreComparison) await compareSelectedExecutions();
  if (executions.length === 0) {
    el.innerHTML = '<div class="empty">No execution manifests yet. A host must call spec/execution_start after spec/context.</div>';
    return;
  }
  let html = '<table><thead><tr><th>Resource</th><th>Retry</th><th>Role</th><th>Host / Model</th><th>Status</th><th>Duration</th><th>Execution</th><th>Time</th></tr></thead><tbody>';
  executions.forEach((manifest, index) => {
    const id = manifest.ID || manifest.id || '';
    const resource = manifest.ResourceID || manifest.resource_id || '';
    const retry = manifest.RetryNumber || manifest.retry_number || 1;
    const role = manifest.Role || manifest.role || '';
    const host = manifest.HostName || manifest.host_name || '';
    const model = manifest.Model || manifest.model || '';
    const status = manifest.Status || manifest.status || '';
    const duration = manifest.DurationMS || manifest.duration_ms || 0;
    const created = manifest.CreatedAt || manifest.created_at || '';
    html += '<tr class="clickable" data-action="toggle-execution" data-record-id="' + esc(id) + '" data-index="' + index + '">' +
      '<td class="resource-id">' + esc(resource) + '</td>' +
      '<td class="mono">#' + retry + '</td>' +
      '<td>' + esc(role) + '</td>' +
      '<td><strong>' + esc(host) + '</strong><br><span style="font-size:11px;color:var(--text-muted)">' + esc(model) + '</span></td>' +
      '<td>' + statusBadge(status) + '</td>' +
      '<td class="mono">' + formatDuration(duration) + '</td>' +
      '<td class="mono" title="' + esc(id) + '">' + esc(shortID(id)) + '</td>' +
      '<td style="color:var(--text-muted);font-size:12px">' + timeAgo(created) + '</td>' +
    '</tr><tr id="execution-drilldown-' + index + '" style="display:none"><td colspan="8"></td></tr>';
  });
  el.innerHTML = html + '</tbody></table>';
  const executionID = new URL(window.location.href).searchParams.get('execution');
  const executionIndex = executions.findIndex(manifest => (manifest.ID || manifest.id) === executionID);
  if (executionIndex >= 0) await toggleExecutionDrilldown(executionID, executionIndex, true);
}

function populateExecutionCompare(executions) {
  const left = document.getElementById('execution-compare-left');
  const right = document.getElementById('execution-compare-right');
  const options = executions.map(manifest => {
    const id = manifest.ID || manifest.id || '';
    const label = (manifest.ResourceID || manifest.resource_id || '') + ' #' +
      (manifest.RetryNumber || manifest.retry_number || 1) + ' · ' +
      (manifest.HostName || manifest.host_name || '') + '/' + (manifest.Model || manifest.model || '');
    return '<option value="' + esc(id) + '">' + esc(label) + '</option>';
  }).join('');
  left.innerHTML = options;
  right.innerHTML = options;
  if (executions.length > 1) { left.selectedIndex = 1; right.selectedIndex = 0; }
	const parameters = new URL(window.location.href).searchParams;
	const leftID = parameters.get('execution_left');
	const rightID = parameters.get('execution_right');
	if (leftID && executions.some(item => (item.ID || item.id) === leftID)) left.value = leftID;
	if (rightID && executions.some(item => (item.ID || item.id) === rightID)) right.value = rightID;
	return Boolean(leftID && rightID && left.value === leftID && right.value === rightID && leftID !== rightID);
}

async function toggleExecutionDrilldown(executionID, index, restoring = false) {
  const row = document.getElementById('execution-drilldown-' + index);
  if (!row) return;
  if (row.style.display !== 'none') {
    row.style.display = 'none';
    if (!restoring) updateRouteParameters({ execution: null });
    return;
  }
  if (!restoring) updateRouteParameters({ view: 'executions', execution: executionID });
  row.style.display = '';
  const td = row.querySelector('td');
  td.innerHTML = '<div class="drilldown"><div class="empty">Loading execution chain...</div></div>';
  const trace = await fetchJSON('/api/v1/executions/' + encodeURIComponent(executionID));
  if (!trace) {
    td.innerHTML = '<div class="drilldown"><div class="error-msg">Could not load execution trace</div></div>';
    return;
  }
  const manifest = trace.execution || trace.Execution || {};
  const context = trace.context || trace.Context || {};
  const candidate = trace.candidate || trace.Candidate;
  const failures = trace.failures || trace.Failures || [];
  const handoffs = trace.handoffs || trace.Handoffs || [];
  const generation = trace.generation || trace.Generation;
  const tools = manifest.Tools || manifest.tools || [];
  const events = manifest.Events || manifest.events || [];
  const contextID = manifest.ContextManifestID || manifest.context_manifest_id || '';
  const attemptID = manifest.AttemptID || manifest.attempt_id || '';
  let html = '<div class="drilldown">' +
    '<div class="stat-row" style="margin-bottom:14px">' +
      '<div><div class="stat-label">Attempt</div><div class="mono">' + esc(attemptID) + '</div></div>' +
      '<div><div class="stat-label">Context</div><div class="mono">' + esc(contextID) + '</div></div>' +
      '<div><div class="stat-label">Protocol / policy</div><div class="mono">' + esc((manifest.ProtocolVersion || '') + ' · ' + (manifest.RolePolicyVersion || '')) + '</div></div>' +
    '</div>' +
    '<h3>Host configuration</h3><pre>' + esc(JSON.stringify({
      host: manifest.HostName, host_version: manifest.HostVersion, provider: manifest.Provider,
      model: manifest.Model, host_session_id: manifest.HostSessionID,
      inference: manifest.InferenceConfig, agent: manifest.AgentConfig,
      template_hashes: manifest.TemplateHashes, context_hash: manifest.ContextHash,
      goals: manifest.GoalIDs, capabilities: manifest.CapabilityIDs,
      goal_progress: manifest.GoalProgress, host_commit_ref: manifest.HostCommitRef
    }, null, 2)) + '</pre>' +
    '<h3 style="margin-top:12px">Tools and permissions</h3><pre>' + esc(JSON.stringify(tools, null, 2)) + '</pre>' +
    '<h3 style="margin-top:12px">System instructions snapshot</h3><pre>' + esc(manifest.SystemInstructions || '') + '</pre>';
  if (candidate) {
    const files = candidate.Files || candidate.files || [];
    html += '<h3 style="margin-top:12px">Candidate · ' + esc(candidate.Status || candidate.status || '') + '</h3>' +
      '<div class="mono" style="font-size:11px;color:var(--text-muted)">' + esc(candidate.CandidateHash || candidate.candidate_hash || '') + '</div>' +
      '<table><thead><tr><th>Path</th><th>Intent</th><th>Bytes</th><th>Hash</th></tr></thead><tbody>' +
      files.map(file => '<tr><td class="resource-id">' + esc(file.Path || file.path || '') + '</td><td>' +
        esc(file.WriteIntent || file.write_intent || '') + '</td><td class="mono">' + (file.ByteSize || file.byte_size || 0) +
        '</td><td class="mono">' + esc(shortID(file.ContentHash || file.content_hash || '')) + '</td></tr>').join('') +
      '</tbody></table>';
  }
  if (failures.length) {
    html += '<h3 style="margin-top:12px">Failure classification</h3>' + failures.map(failure =>
      '<div class="error-msg"><strong>' + esc(failure.Category || failure.category || '') + '</strong> · ' +
      esc(failure.CorrectiveAction || failure.corrective_action || '') +
      ((failure.NextRole || failure.next_role) ? ' → ' + esc(failure.NextRole || failure.next_role) : '') +
      '<pre>' + esc(failure.Evidence || failure.evidence || '') + '</pre></div>').join('');
  }
  if (handoffs.length) {
    html += '<h3 style="margin-top:12px">Role handoffs</h3><pre>' + esc(JSON.stringify(handoffs, null, 2)) + '</pre>';
  }
  html += '<h3 style="margin-top:12px">State events</h3><pre>' + esc(JSON.stringify(events, null, 2)) + '</pre>';
  if (generation) {
    html += '<h3 style="margin-top:12px">Legacy generation projection</h3><pre>' + esc(JSON.stringify(generation, null, 2)) + '</pre>';
  }
  html += '<div style="margin-top:12px;font-size:11px;color:var(--text-muted)">Chain: goals ' +
    esc((manifest.GoalIDs || []).join(', ') || '—') + ' → plan operation ' + esc(manifest.PlanOperationID || '') +
    ' → resource ' + esc(manifest.ResourceID || '') + ' → attempt ' +
    esc(attemptID) + ' → context ' + esc(contextID) + ' → execution ' + esc(executionID) +
    (candidate ? ' → candidate ' + esc(candidate.ID || candidate.id || '') : '') + '</div></div>';
  td.innerHTML = html;
}

async function compareSelectedExecutions() {
  const left = document.getElementById('execution-compare-left').value;
  const right = document.getElementById('execution-compare-right').value;
	updateRouteParameters({ execution_left: left, execution_right: right }, { replace: true });
  const el = document.getElementById('execution-comparison');
  if (!left || !right || left === right) {
    el.innerHTML = '<div class="error-msg">Select two different executions.</div>';
    return;
  }
  const comparison = await fetchJSON('/api/v1/execution-comparison?left=' + encodeURIComponent(left) + '&right=' + encodeURIComponent(right));
  if (!comparison) {
    el.innerHTML = '<div class="error-msg">Could not compare executions</div>';
    return;
  }
  const changes = comparison.changes || comparison.Changes || {};
  const changed = Object.keys(changes).filter(key => changes[key]);
  el.innerHTML = '<div class="drilldown" style="margin:0 0 16px 0"><h3>Execution comparison</h3>' +
    (changed.length ? changed.map(key => badge(key + ' changed', 'yellow')).join(' ') : badge('governed inputs and outcome match', 'green')) +
    '</div>';
}

// ─── Tab: Validation and Evidence Explorer ───────────────────────
async function loadVerificationsTab() {
  const definitionsEl = document.getElementById('verification-definitions');
  const runsEl = document.getElementById('verifications-table');
  const [definitions, runs] = await Promise.all([
    fetchJSON('/api/v1/verifications/definitions'),
    fetchJSON('/api/v1/verifications?limit=100'),
  ]);
  if (!definitions || !runs) {
    definitionsEl.innerHTML = '<div class="error-msg">Error loading verification state</div>';
    runsEl.innerHTML = '';
    return;
  }
  definitionsEl.innerHTML = '<h2>Current definitions</h2>' + (definitions.length
    ? '<div class="state-counts" style="margin-bottom:18px">' + definitions.map(definition => {
        const id = definition.definition_id || definition.DefinitionID || '';
        const scope = definition.scope || definition.Scope || '';
        const type = definition.definition_type || definition.DefinitionType || '';
        return '<span title="' + esc(id) + '">' + badge(type + ' · ' + scope, type === 'witness' ? 'purple' : 'blue') +
          ' <span class="mono">' + esc(id) + '</span></span>';
      }).join('') + '</div>'
    : '<div class="empty">No named definitions have been reconciled.</div>');
  if (!runs.length) {
    runsEl.innerHTML = '<div class="empty">No validation or witness runs yet.</div>';
    return;
  }
  let html = '<h2>Run history</h2><table><thead><tr><th>Definition</th><th>Scope</th><th>Classification</th><th>Targets</th><th>Source tree</th><th>Duration</th><th>Time</th></tr></thead><tbody>';
  runs.forEach((run, index) => {
    const definition = run.definition || run.Definition || {};
    const id = run.id || run.ID || '';
    const definitionID = run.definition_id || run.DefinitionID || '';
    const scope = definition.scope || definition.Scope || '';
    const classification = run.classification || run.Classification || '';
    const targets = run.targets || run.Targets || [];
    const tree = run.source_tree_hash || run.SourceTreeHash || '';
    html += '<tr class="clickable" data-action="toggle-verification" data-record-id="' + esc(id) + '" data-index="' + index + '">' +
      '<td class="resource-id">' + esc(definitionID) + '</td><td>' + badge(scope, 'blue') + '</td>' +
      '<td>' + statusBadge(classification) + '</td><td><div class="files-list">' + targets.map(target =>
        '<span>' + esc((target.Kind || target.kind || '') + ':' + (target.ID || target.id || '')) + '</span>').join('') + '</div></td>' +
      '<td class="mono" title="' + esc(tree) + '">' + esc(shortID(tree)) + '</td>' +
      '<td class="mono">' + formatDuration(run.duration_ms || run.DurationMS || 0) + '</td>' +
      '<td style="color:var(--text-muted);font-size:12px">' + timeAgo(run.created_at || run.CreatedAt || '') + '</td></tr>' +
      '<tr id="verification-drilldown-' + index + '" style="display:none"><td colspan="7"></td></tr>';
  });
  runsEl.innerHTML = html + '</tbody></table>';
  const verificationID = new URL(window.location.href).searchParams.get('verification');
  const verificationIndex = runs.findIndex(run => (run.id || run.ID) === verificationID);
  if (verificationIndex >= 0) await toggleVerificationDrilldown(verificationID, verificationIndex, true);
}

async function toggleVerificationDrilldown(runID, index, restoring = false) {
  const row = document.getElementById('verification-drilldown-' + index);
  if (!row) return;
  if (row.style.display !== 'none') {
    row.style.display = 'none';
    if (!restoring) updateRouteParameters({ verification: null });
    return;
  }
  if (!restoring) updateRouteParameters({ view: 'verifications', verification: runID });
  row.style.display = '';
  const td = row.querySelector('td');
  td.innerHTML = '<div class="drilldown"><div class="empty">Loading captured evidence...</div></div>';
  const run = await fetchJSON('/api/v1/verifications/' + encodeURIComponent(runID));
  if (!run) {
    td.innerHTML = '<div class="drilldown"><div class="error-msg">Could not load verification run</div></div>';
    return;
  }
  const definition = run.definition || run.Definition || {};
  const executions = run.executions || run.Executions || [];
  const predicates = run.predicate_results || run.PredicateResults || [];
  const evidence = run.evidence || run.Evidence || [];
  let html = '<div class="drilldown"><h3>Definition snapshot</h3><pre>' + esc(definition.definition_json || definition.DefinitionJSON || '') + '</pre>' +
    '<div style="font-size:11px;color:var(--text-muted);margin:8px 0">definition hash ' +
    esc(definition.definition_hash || definition.DefinitionHash || '') + ' · source tree ' +
    esc(run.source_tree_hash || run.SourceTreeHash || '') + '</div>';
  executions.forEach(execution => {
    const role = execution.role || execution.Role || '';
    const observation = execution.observation || execution.Observation;
    html += '<h3 style="margin-top:12px">' + esc(role) + ' command</h3><pre>' + esc(execution.command_json || execution.CommandJSON || '') + '</pre>' +
      '<div class="state-counts"><span>' + badge('exit ' + (execution.exit_code ?? execution.ExitCode), (execution.exit_code ?? execution.ExitCode) === 0 ? 'green' : 'red') + '</span>' +
      '<span class="mono">executable ' + esc(shortID(execution.executable_hash || execution.ExecutableHash || '')) + '</span></div>' +
      '<h3 style="margin-top:10px">stdout' + ((execution.stdout_truncated || execution.StdoutTruncated) ? ' (truncated)' : '') + '</h3><pre>' + esc(execution.stdout || execution.Stdout || '') + '</pre>' +
      '<h3 style="margin-top:10px">stderr' + ((execution.stderr_truncated || execution.StderrTruncated) ? ' (truncated)' : '') + '</h3><pre>' + esc(execution.stderr || execution.Stderr || '') + '</pre>' +
      (observation ? '<h3 style="margin-top:10px">Parsed observation</h3><pre>' + esc(observation.observation_json || observation.ObservationJSON || '') + '</pre>' : '');
  });
  html += '<h3 style="margin-top:12px">Predicate results</h3><pre>' + esc(JSON.stringify(predicates, null, 2)) + '</pre>' +
    '<h3 style="margin-top:12px">Evidence currency</h3><pre>' + esc(JSON.stringify(evidence, null, 2)) + '</pre>' +
    '<div style="margin-top:12px;font-size:11px;color:var(--text-muted)">Chain: definition → targets → command executions → parsed observations → predicate results → evidence record</div></div>';
  td.innerHTML = html;
}

// ─── Tab: Resources ────────────────────────────────────────────────
async function loadResourcesTab() { await loadResourcesFeature(); }

// ─── Tab: Invariants ───────────────────────────────────────────────
async function loadInvariantsTab() {
  const el = document.getElementById('invariants-table');
  const status = cachedStatus || await fetchJSON('/api/status');
  cachedStatus = status;

  if (!status || !status.latest_apply) {
    el.innerHTML = '<div class="empty">No applies found</div>';
    return;
  }

  const applyID = status.latest_apply.ID || status.latest_apply.id;
  const checks = await fetchJSON('/api/invariant-checks/' + encodeURIComponent(applyID));
  if (!checks) {
    el.innerHTML = '<div class="error-msg">Error loading invariant checks</div>';
    return;
  }
  if (checks.length === 0) {
    el.innerHTML = '<div class="empty">No invariant checks recorded</div>';
    return;
  }

  // Group by resource
  const byResource = {};
  for (const c of checks) {
    const rid = c.ResourceID || c.resource_id || 'unknown';
    if (!byResource[rid]) byResource[rid] = [];
    byResource[rid].push(c);
  }

  let html = '<table><thead><tr>' +
    '<th>Resource</th><th>Invariant</th><th>Result</th><th>Details</th><th>Time</th>' +
  '</tr></thead><tbody>';

  for (const [rid, rChecks] of Object.entries(byResource)) {
    let first = true;
    for (const c of rChecks) {
      const passed = c.Passed || c.passed;
      const checkType = c.CheckType || c.check_type || c.Invariant || c.invariant || '';
      const output = c.Output || c.output || c.Details || c.details || '';
      const ts = c.CreatedAt || c.created_at || '';

      html += '<tr>' +
        '<td class="resource-id">' + (first ? esc(rid) : '') + '</td>' +
        '<td style="font-size:12px">' + esc(checkType) + '</td>' +
        '<td>' + (passed ? badge('passed', 'green') : badge('failed', 'red')) + '</td>' +
        '<td style="font-size:12px;color:var(--text-muted)">' + truncate(output, 200) + '</td>' +
        '<td style="color:var(--text-muted);font-size:12px">' + timeAgo(ts) + '</td>' +
      '</tr>';
      first = false;
    }
  }

  html += '</tbody></table>';
  el.innerHTML = html;
}

// ─── Sidebar: Apply Info ───────────────────────────────────────────
function renderApplyInfo(apply) {
  const el = document.getElementById('apply-info');
  el.innerHTML =
    '<div class="stat">' +
      '<div class="stat-label">ID</div>' +
      '<div class="mono">' + esc(shortID(apply.ID || apply.id)) + '</div>' +
    '</div>' +
    '<div class="stat">' +
      '<div class="stat-label">Status</div>' +
      '<div>' + statusBadge(apply.Status || apply.status) + '</div>' +
    '</div>' +
    '<div class="stat">' +
      '<div class="stat-label">Started</div>' +
      '<div style="font-size:12px;color:var(--text-muted)">' + timeAgo(apply.StartedAt || apply.started_at) + '</div>' +
    '</div>';
}

async function handleDashboardAction(event) {
  const trigger = event.target.closest?.('[data-action]');
  if (!trigger) return;

  const index = Number.parseInt(trigger.dataset.index || '', 10);
  event.preventDefault();

  switch (trigger.dataset.action) {
    case 'toggle-evaluation-run':
      await toggleEvaluationRun(trigger.dataset.recordId, index);
      break;
    case 'show-evaluation-case':
      await showEvaluationCase(trigger.dataset.recordId, trigger);
      break;
    case 'toggle-evaluation-case':
      await toggleEvaluationCase(trigger.dataset.recordId, index);
      break;
    case 'inspect-evaluation-record':
      await inspectEvaluationRecord(trigger.dataset.recordUrl, trigger);
      break;
    case 'toggle-evaluation-promotion':
      await toggleEvaluationPromotion(trigger.dataset.recordId, index);
      break;
    case 'toggle-wave':
      toggleWave(trigger.dataset.targetId);
      break;
    case 'toggle-collapsible':
      toggleCollapsible(trigger);
      break;
    case 'toggle-generation':
      await toggleGenDrilldown(trigger.dataset.recordId, trigger.dataset.resourceId, index);
      break;
    case 'toggle-context':
      await toggleContextDrilldown(trigger.dataset.recordId, index);
      break;
    case 'toggle-context-content':
      toggleContextContent(trigger.dataset.targetId);
      break;
    case 'toggle-execution':
      await toggleExecutionDrilldown(trigger.dataset.recordId, index);
      break;
    case 'toggle-verification':
      await toggleVerificationDrilldown(trigger.dataset.recordId, index);
      break;
  }
}

// ─── Initialize ────────────────────────────────────────────────────
async function init() {
	initializeRouter({
	  defaultView: 'session',
	  onNavigate: view => {
	    currentTab = view;
	    loadTabData(view);
	  },
	});
	document.getElementById('compare-contexts').addEventListener('click', compareSelectedContexts);
	document.getElementById('compare-executions').addEventListener('click', compareSelectedExecutions);
	document.addEventListener('click', handleDashboardAction);
	initializeFailureFilters();
  // Initial full load
  await pollRefresh();

  // Connect SSE for live updates
  connectSSE();

  // Polling fallback every 3 seconds if SSE is down
  setInterval(() => {
    if (!sseConnected) pollRefresh();
  }, 3000);
}

init();
