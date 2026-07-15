import { fetchJSON } from '../api.js';
import { badge, escapeHTML as esc, shortID, statusBadge, timeAgo } from '../components.js';
import { updateRouteParameters } from '../router.js';

export async function loadEvaluationsFeature() {
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

export async function handleEvaluationAction(event, trigger) {
  const index = Number.parseInt(trigger.dataset.index || '', 10);
  switch (trigger.dataset.action) {
    case 'toggle-evaluation-run':
      event.preventDefault();
      await toggleEvaluationRun(trigger.dataset.recordId, index);
      return true;
    case 'show-evaluation-case':
      event.preventDefault();
      await showEvaluationCase(trigger.dataset.recordId, trigger);
      return true;
    case 'toggle-evaluation-case':
      event.preventDefault();
      await toggleEvaluationCase(trigger.dataset.recordId, index);
      return true;
    case 'inspect-evaluation-record':
      event.preventDefault();
      await inspectEvaluationRecord(trigger.dataset.recordUrl, trigger);
      return true;
    case 'toggle-evaluation-promotion':
      event.preventDefault();
      await toggleEvaluationPromotion(trigger.dataset.recordId, index);
      return true;
    default:
      return false;
  }
}
