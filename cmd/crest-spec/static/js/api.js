const API_ROOT = '';

export class APIRequestError extends Error {
  constructor(status, code, message, details = {}) {
    super(message);
    this.name = 'APIRequestError';
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

export async function requestJSON(path, options = {}) {
  const response = await fetch(API_ROOT + path, {
    headers: { Accept: 'application/json', ...(options.headers || {}) },
    ...options,
  });
  if (!response.ok) {
    let body = {};
    try { body = await response.json(); } catch {}
    const apiError = body.error || {};
    throw new APIRequestError(
      response.status,
      apiError.code || 'http_error',
      apiError.message || String(body.error || response.statusText),
      apiError.details || {},
    );
  }
  return response.json();
}

// Transitional adapter for legacy views. New feature modules use requestJSON
// and render explicit error state; preserved views continue to treat failure as
// an absent response until they are migrated.
export async function fetchJSON(path) {
  try { return await requestJSON(path); } catch { return null; }
}

export function eventURL(path) {
  return API_ROOT + path;
}
