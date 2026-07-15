const VIEW_PARAMETER = 'view';

export function initializeRouter({ defaultView = 'session', onNavigate }) {
  const tabs = Array.from(document.querySelectorAll('[role="tab"][data-tab]'));
  const allowed = new Set(tabs.map(tab => tab.dataset.tab));

  const selectedFromURL = () => {
    const requested = new URL(window.location.href).searchParams.get(VIEW_PARAMETER);
    return allowed.has(requested) ? requested : defaultView;
  };

  const activate = (view, { push = false, focus = false } = {}) => {
    if (!allowed.has(view)) view = defaultView;
    tabs.forEach(tab => {
      const selected = tab.dataset.tab === view;
      tab.classList.toggle('active', selected);
      tab.setAttribute('aria-selected', String(selected));
      tab.tabIndex = selected ? 0 : -1;
      if (focus && selected) tab.focus();
    });
    document.querySelectorAll('[role="tabpanel"]').forEach(panel => {
      const selected = panel.id === 'tab-' + view;
      panel.classList.toggle('active', selected);
      panel.hidden = !selected;
    });
    if (push) {
      const url = new URL(window.location.href);
      url.searchParams.set(VIEW_PARAMETER, view);
      window.history.pushState({ view }, '', url);
    }
    onNavigate(view);
  };

  tabs.forEach((tab, index) => {
    tab.addEventListener('click', () => activate(tab.dataset.tab, { push: true }));
    tab.addEventListener('keydown', event => {
      let next = index;
      if (event.key === 'ArrowRight') next = (index + 1) % tabs.length;
      else if (event.key === 'ArrowLeft') next = (index - 1 + tabs.length) % tabs.length;
      else if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = tabs.length - 1;
      else return;
      event.preventDefault();
      activate(tabs[next].dataset.tab, { push: true, focus: true });
    });
  });
  window.addEventListener('popstate', () => activate(selectedFromURL()));
  activate(selectedFromURL());

  return { navigate: (view, options) => activate(view, { push: true, ...options }) };
}

export function updateRouteParameters(values, { replace = false } = {}) {
  const url = new URL(window.location.href);
  Object.entries(values).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') url.searchParams.delete(key);
    else url.searchParams.set(key, value);
  });
  window.history[replace ? 'replaceState' : 'pushState']({}, '', url);
}
