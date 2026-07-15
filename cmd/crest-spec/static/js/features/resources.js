import { requestJSON } from '../api.js';
import { announce, badge, escapeHTML as esc, recordStateNotice, statusBadge, timeAgo } from '../components.js';
import { updateRouteParameters } from '../router.js';

function list(values, key, empty = 'None') {
  if (!values || values.length === 0) return '<span class="empty-inline">' + esc(empty) + '</span>';
  return '<ul class="compact-list">' + values.map(value => '<li>' + esc(key ? value[key] : value) + '</li>').join('') + '</ul>';
}

function bindResourceLinks(container) {
  container.querySelectorAll('[data-resource-id]').forEach(button => {
    button.addEventListener('click', async () => {
      const resourceID = button.dataset.resourceId;
      updateRouteParameters({ view: 'resources', resource: resourceID });
      await renderResource(resourceID);
      document.getElementById('resource-detail')?.focus();
    });
  });
}

function relationship(values) {
  if (!values || values.length === 0) return '<span class="empty-inline">None</span>';
  return '<ul class="compact-list">' + values.map(value => '<li><button type="button" class="relationship-button" data-resource-id="' + esc(value.resource_id) + '">' + esc(value.resource_id) + '</button> <span class="subtle">' + esc(value.kind) + '</span></li>').join('') + '</ul>';
}

async function renderResource(resourceID) {
  const element = document.getElementById('resource-detail');
  if (!element) return;
  element.innerHTML = '<div class="loading-state">Loading resource ' + esc(resourceID) + '…</div>';
  try {
    const data = await requestJSON('/api/v1/resources/' + encodeURIComponent(resourceID));
    const resource = data.resource;
    element.innerHTML = '<div class="detail-panel"><div class="view-heading"><div><h2>' + esc(resource.id) + '</h2><p>' + esc(resource.kind) + ' in ' + esc(resource.context_name || 'the project boundary') + '</p></div>' + statusBadge(data.status?.state) + '</div>' +
      '<p class="subtle">' + esc(data.status?.reason) + '</p>' +
      recordStateNotice(resource.record_state) +
      '<div class="relationship-grid"><section><h2>Goals</h2>' + list(resource.goals) + '</section><section><h2>Capabilities</h2>' + list(resource.capabilities) + '</section>' +
      '<section><h2>Dependencies</h2>' + relationship(data.dependencies) + '</section><section><h2>Consumers</h2>' + relationship(data.consumers) + '</section></div>' +
      '<h2>Generated files</h2>' + list(data.files, 'path', 'No generated files') +
      '<h2>Context and execution attempts</h2>' + ((data.attempts || []).length ? '<table><thead><tr><th>Attempt</th><th>Role</th><th>Status</th><th>Context</th><th>Execution</th></tr></thead><tbody>' + data.attempts.map(attempt =>
        '<tr><td class="mono">' + esc(attempt.id) + '</td><td>' + esc(attempt.role) + '</td><td>' + statusBadge(attempt.status) + '</td><td class="mono">' + esc(attempt.context_manifest_id || '—') + '</td><td class="mono">' + esc(attempt.execution_id || '—') + '</td></tr>').join('') + '</tbody></table>' : '<div class="empty">No generation attempts recorded.</div>') +
      '<h2>Validation history</h2>' + ((data.validations || []).length ? '<table><thead><tr><th>Definition</th><th>Classification</th><th>Currency</th><th>Source tree</th></tr></thead><tbody>' + data.validations.map(run =>
        '<tr><td class="resource-id">' + esc(run.definition_id) + '</td><td>' + statusBadge(run.classification) + '</td><td>' + statusBadge(run.currency) + '</td><td class="mono">' + esc(run.source_tree_hash) + '</td></tr>').join('') + '</tbody></table>' : '<div class="empty">No validation runs target this resource.</div>') +
      '</div>';
    bindResourceLinks(element);
    announce('Loaded resource ' + resourceID);
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Could not load resource: ' + esc(error.message) + '</div>';
  }
}

export async function loadResourcesFeature() {
  const element = document.getElementById('resources-table');
  element.setAttribute('aria-busy', 'true');
  try {
    const page = await requestJSON('/api/v1/resources?limit=100');
    if (!page.items?.length) {
      element.innerHTML = '<div class="empty">No settled resources yet. Declared resources appear after their generated state is accepted.</div><section id="resource-detail" tabindex="-1"></section>';
      return;
    }
    element.innerHTML = '<table><thead><tr><th>Resource</th><th>Kind</th><th>Goals</th><th>Capabilities</th><th>Context</th><th>Model</th><th>Settled</th></tr></thead><tbody>' + page.items.map(resource =>
      '<tr><td><button type="button" class="relationship-button" data-resource-id="' + esc(resource.id) + '">' + esc(resource.id) + '</button></td><td>' + badge(resource.kind, 'blue') + '</td>' +
      '<td>' + list(resource.goals) + '</td><td>' + list(resource.capabilities) + '</td><td>' + esc(resource.context_name || '—') + recordStateNotice(resource.record_state) + '</td><td>' + esc(resource.model || '—') + '</td><td>' + timeAgo(resource.settled_at) + '</td></tr>').join('') +
      '</tbody></table>' + (page.page?.has_more ? '<div class="notice">Showing the first ' + page.page.limit + ' resources. Refine through the API filters for a stable cursor.</div>' : '') +
      '<section id="resource-detail" tabindex="-1" aria-live="polite"></section>';
    bindResourceLinks(element);
    const resourceID = new URL(window.location.href).searchParams.get('resource');
    if (resourceID) await renderResource(resourceID);
  } catch (error) {
    element.innerHTML = '<div class="error-msg">Error loading resources: ' + esc(error.message) + '</div>';
  } finally {
    element.removeAttribute('aria-busy');
  }
}
