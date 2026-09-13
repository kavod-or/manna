(() => {
  const elements = {
    eventSelect: document.getElementById('event-select'),
    editor: document.getElementById('yaml-editor'),
    status: document.getElementById('editor-status'),
    position: document.getElementById('editor-position'),
    validate: document.getElementById('validate-button'),
    publish: document.getElementById('publish-button'),
    reload: document.getElementById('reload-button'),
    publicLink: document.getElementById('public-menu-link'),
  };
  if (!elements.editor) return;

  const state = {
    path: '',
    revision: '',
    savedYAML: '',
    busy: false,
    loadSequence: 0,
  };

  const isDirty = () => Boolean(state.path) && elements.editor.value !== state.savedYAML;
  const eventSlug = (eventPath) => encodeURIComponent(eventPath.replace(/^\//, ''));

  function setStatus(message, kind = 'neutral') {
    elements.status.textContent = message;
    elements.status.className = `editor-status is-${kind}`;
  }

  function updateControls() {
    const loaded = Boolean(state.path && state.revision);
    elements.eventSelect.disabled = state.busy || elements.eventSelect.options.length === 0;
    elements.editor.disabled = state.busy || !loaded;
    elements.validate.disabled = state.busy || !loaded;
    elements.publish.disabled = state.busy || !loaded || !isDirty();
    elements.reload.disabled = state.busy || !loaded;
  }

  function setBusy(busy) {
    state.busy = busy;
    updateControls();
  }

  function updatePosition() {
    const beforeCursor = elements.editor.value.slice(0, elements.editor.selectionStart || 0);
    const lines = beforeCursor.split('\n');
    elements.position.textContent = `Line ${lines.length}, column ${lines.at(-1).length + 1}`;
  }

  function updatePublicLink() {
    if (!state.path) {
      elements.publicLink.href = '#';
      elements.publicLink.classList.add('is-disabled');
      elements.publicLink.setAttribute('aria-disabled', 'true');
      return;
    }
    elements.publicLink.href = state.path;
    elements.publicLink.classList.remove('is-disabled');
    elements.publicLink.setAttribute('aria-disabled', 'false');
  }

  async function requestJSON(url, options = {}) {
    const response = await fetch(url, options);
    let body = {};
    try {
      body = await response.json();
    } catch {
      // Unexpected non-JSON responses receive a generic message below.
    }
    if (!response.ok) {
      const error = new Error(body.error || `Request failed with status ${response.status}`);
      error.status = response.status;
      throw error;
    }
    return body;
  }

  function writeOptions(body) {
    elements.eventSelect.replaceChildren();
    for (const event of body.events || []) {
      const option = document.createElement('option');
      option.value = event.path;
      option.textContent = event.path;
      elements.eventSelect.append(option);
    }
  }

  async function loadEvents() {
    setBusy(true);
    setStatus('Loading events…');
    try {
      const body = await requestJSON('/admin/api/events');
      writeOptions(body);
      if (!body.events?.length) {
        setStatus('No existing events are available.', 'warning');
        return;
      }
      elements.eventSelect.value = body.events[0].path;
      await loadEvent(body.events[0].path, false);
    } catch (error) {
      setStatus(error.message || 'Could not load events.', 'error');
    } finally {
      setBusy(false);
    }
  }

  async function loadEvent(eventPath, confirmDiscard = true) {
    if (!eventPath) return false;
    if (confirmDiscard && isDirty() && !window.confirm('Discard your unpublished changes and load another menu?')) {
      elements.eventSelect.value = state.path;
      return false;
    }

    const sequence = ++state.loadSequence;
    setBusy(true);
    setStatus(`Loading ${eventPath}…`);
    try {
      const body = await requestJSON(`/admin/api/events/${eventSlug(eventPath)}`);
      if (sequence !== state.loadSequence) return false;
      state.path = body.path;
      state.revision = body.revision;
      state.savedYAML = body.yaml;
      elements.editor.value = body.yaml;
      elements.eventSelect.value = body.path;
      updatePublicLink();
      updatePosition();
      setStatus(`${body.path} loaded.`, 'success');
      return true;
    } catch (error) {
      if (sequence === state.loadSequence) {
        elements.eventSelect.value = state.path;
        setStatus(error.message || 'Could not load the event.', 'error');
      }
      return false;
    } finally {
      if (sequence === state.loadSequence) setBusy(false);
    }
  }

  async function validateMenu() {
    if (!state.path || state.busy) return;
    setBusy(true);
    setStatus('Validating YAML…');
    try {
      await requestJSON(`/admin/api/events/${eventSlug(state.path)}/validate`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json', 'X-Mana-Admin': '1'},
        body: JSON.stringify({yaml: elements.editor.value}),
      });
      setStatus('YAML is valid. Nothing has been published yet.', 'success');
    } catch (error) {
      setStatus(error.message || 'Could not validate the YAML.', 'error');
    } finally {
      setBusy(false);
    }
  }

  async function publishMenu() {
    if (!state.path || state.busy || !isDirty()) return;
    if (!window.confirm(`Publish your changes to ${state.path}?`)) return;
    setBusy(true);
    setStatus('Validating and publishing YAML…');
    try {
      const body = await requestJSON(`/admin/api/events/${eventSlug(state.path)}`, {
        method: 'PUT',
        headers: {'Content-Type': 'application/json', 'X-Mana-Admin': '1'},
        body: JSON.stringify({yaml: elements.editor.value, revision: state.revision}),
      });
      state.revision = body.revision;
      state.savedYAML = elements.editor.value;
      setStatus(`${state.path} was published successfully.`, 'success');
    } catch (error) {
      if (error.status === 409) {
        setStatus('The live menu changed after you loaded it. Reload before publishing again.', 'warning');
      } else {
        setStatus(error.message || 'Could not publish the menu.', 'error');
      }
    } finally {
      setBusy(false);
    }
  }

  elements.eventSelect.addEventListener('change', () => loadEvent(elements.eventSelect.value));
  elements.reload.addEventListener('click', () => loadEvent(state.path));
  elements.validate.addEventListener('click', validateMenu);
  elements.publish.addEventListener('click', publishMenu);
  elements.editor.addEventListener('input', () => {
    updateControls();
    updatePosition();
    if (isDirty()) setStatus('You have unpublished changes.', 'warning');
  });
  elements.editor.addEventListener('click', updatePosition);
  elements.editor.addEventListener('keyup', updatePosition);
  elements.editor.addEventListener('keydown', (event) => {
    if (event.key === 'Tab') {
      event.preventDefault();
      const start = elements.editor.selectionStart;
      const end = elements.editor.selectionEnd;
      elements.editor.setRangeText('  ', start, end, 'end');
      updateControls();
      updatePosition();
      setStatus('You have unpublished changes.', 'warning');
      return;
    }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
      event.preventDefault();
      validateMenu();
    }
  });
  window.addEventListener('beforeunload', (event) => {
    if (!isDirty()) return;
    event.preventDefault();
    event.returnValue = '';
  });

  updatePublicLink();
  updatePosition();
  const ready = loadEvents();
  if (globalThis.__manaAdminTest) {
    Object.assign(globalThis.__manaAdminTest, {ready, state, isDirty, loadEvent, validateMenu, publishMenu});
  }
})();
