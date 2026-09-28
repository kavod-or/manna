(() => {
  const elements = {
    eventSelect: document.getElementById('event-select'),
    addEvent: document.getElementById('add-event-button'),
    deleteEvent: document.getElementById('delete-event-button'),
    form: document.getElementById('menu-form'),
    tabs: document.getElementById('editor-tabs'),
    status: document.getElementById('editor-status'),
    validate: document.getElementById('validate-button'),
    publish: document.getElementById('publish-button'),
    reload: document.getElementById('reload-button'),
    publicLink: document.getElementById('public-menu-link'),
    previewPanel: document.getElementById('preview-panel'),
    preview: document.getElementById('menu-preview'),
    previewLanguage: document.getElementById('preview-language'),
    previewDay: document.getElementById('preview-day'),
  };
  if (!elements.form) return;

  const state = {path: '', revision: '', manifestRevision: '', canManageEvents: false, config: null, saved: '', yaml: '', savedYAML: '', busy: false, tab: 'event', loadSequence: 0, previewLanguage: '', previewDay: 0, previewLogo: null, publishedLogo: ''};
  const tabs = [['event', 'Event'], ['permanent', 'Permanent menu'], ['schedule', 'Schedule'], ['regulatory', 'Declarations'], ['yaml', 'YAML']];
  let previewTimer = 0;
  const eventSlug = (eventPath) => encodeURIComponent(eventPath.replace(/^\//, ''));
  const clone = (value) => JSON.parse(JSON.stringify(value));
  const snapshot = () => state.config ? JSON.stringify(state.config) : '';
  const isYAML = () => state.tab === 'yaml';
  const isDirty = () => Boolean(state.path) && (
    (state.config && snapshot() !== state.saved) || state.yaml !== state.savedYAML
  );

  function draftPayload() {
    return isYAML()
      ? {yaml: state.yaml}
      : {config: state.config, source_yaml: state.yaml};
  }
  const pathKey = (path) => encodeURIComponent(JSON.stringify(path));
  const readPath = (value) => JSON.parse(decodeURIComponent(value));
  const esc = (value = '') => String(value).replace(/[&<>"']/g, (character) => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[character]));
  const title = (value) => String(value || '').replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());

  function get(path) { return path.reduce((value, key) => value?.[key], state.config); }
  function set(path, value) {
    const key = path.at(-1);
    const parent = get(path.slice(0, -1));
    if (value === undefined) delete parent[key]; else parent[key] = value;
  }

  function setStatus(message, kind = 'neutral') {
    elements.status.textContent = message;
    elements.status.className = `editor-status is-${kind}`;
  }

  function updateControls() {
    const loaded = Boolean(state.path && state.revision && (state.config || isYAML()));
    elements.eventSelect.disabled = state.busy || elements.eventSelect.options.length === 0;
    elements.addEvent.disabled = state.busy || !state.canManageEvents || !state.manifestRevision;
    elements.deleteEvent.disabled = state.busy || !loaded || !state.canManageEvents || !state.manifestRevision;
    elements.validate.disabled = state.busy || !loaded;
    elements.publish.disabled = state.busy || !loaded || !isDirty();
    elements.reload.disabled = state.busy || !loaded;
    for (const input of elements.form.querySelectorAll?.('input, textarea, select, button') || []) input.disabled = state.busy || !loaded;
  }

  function changed(options = {}) {
    updateControls();
    renderPreview(!options.immediatePreview);
    if (isDirty()) setStatus('You have unpublished changes.', 'warning');
  }

  function setBusy(busy) { state.busy = busy; updateControls(); }
  function updatePublicLink() {
    elements.publicLink.href = state.path || '#';
    elements.publicLink.classList[state.path ? 'remove' : 'add']('is-disabled');
    elements.publicLink.setAttribute('aria-disabled', String(!state.path));
  }

  async function requestJSON(url, options = {}) {
    const response = await fetch(url, options);
    let body = {};
    try { body = await response.json(); } catch { /* handled below */ }
    if (!response.ok) {
      const error = new Error(body.error || `Request failed with status ${response.status}`);
      error.status = response.status;
      throw error;
    }
    return body;
  }

  function normalize(config) {
    config.tags ||= {};
    config.regulatory ||= {};
    config.permanent ||= {};
    for (const group of ['coffee', 'drinks', 'snacks']) config.permanent[group] ||= [];
    config.days ||= [];
    config.conference ||= {};
    config.conference.languages ||= ['de', 'en'];
    const normalizeItem = (item) => { item.name ||= {}; item.description ||= {}; item.variants ||= []; item.tags ||= []; };
    for (const group of ['coffee', 'drinks', 'snacks']) config.permanent[group].forEach(normalizeItem);
    for (const day of config.days) {
      day.services ||= [];
      day.food_trucks ||= [];
      for (const service of day.services) {
        service.title ||= {}; service.subtitle ||= {}; service.items ||= [];
        service.items.forEach(normalizeItem);
      }
      for (const truck of day.food_trucks) {
        truck.name ||= {}; truck.description ||= {}; truck.location ||= {}; truck.payment ||= {};
        truck.times ||= [];
        if (!truck.times.length && (truck.from || truck.until)) {
          truck.times.push({from: truck.from || '', until: truck.until || ''});
          delete truck.from; delete truck.until;
        }
        truck.items ||= []; truck.items.forEach(normalizeItem);
      }
    }
    for (const declaration of Object.values(config.regulatory)) {
      declaration.allergens ||= []; declaration.additives ||= []; declaration.notices ||= [];
    }
    return config;
  }

  function applyConfig(config) {
    state.config = normalize(clone(config));
    const logo = state.config.conference.logo || '';
    if (state.previewLogo?.filename !== logo) state.previewLogo = null;
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

  function clearEvent() {
    state.path = ''; state.revision = ''; state.config = null; state.saved = '';
    state.yaml = ''; state.savedYAML = ''; state.previewLogo = null; state.publishedLogo = '';
    elements.form.replaceChildren();
    elements.tabs.hidden = true;
    elements.previewPanel.hidden = true;
    updatePublicLink();
  }

  async function loadEvents(preferredPath = '') {
    setBusy(true);
    setStatus('Loading events…');
    try {
      const body = await requestJSON('/admin/api/events');
      state.manifestRevision = body.revision || '';
      state.canManageEvents = Boolean(body.can_manage);
      writeOptions(body);
      if (!body.events?.length) {
        clearEvent();
        setStatus('No events exist yet. Add one to create its menu and public route.', 'warning');
        return;
      }
      const selected = body.events.some((event) => event.path === preferredPath) ? preferredPath : body.events[0].path;
      elements.eventSelect.value = selected;
      await loadEvent(selected, false);
    } catch (error) { setStatus(error.message || 'Could not load events.', 'error'); }
    finally { setBusy(false); }
  }

  async function addEvent() {
    if (state.busy || !state.canManageEvents) return;
    if (isDirty() && !window.confirm('Discard your unpublished menu changes and add another event?')) return;
    const entered = window.prompt('Enter the new public event path. Use lowercase letters, numbers, and hyphens, for example /team-day.');
    if (entered == null) return;
    const eventPath = entered.trim();
    if (!/^\/[a-z0-9]+(?:-[a-z0-9]+)*$/.test(eventPath)) {
      setStatus('Use a path such as /team-day with lowercase letters, numbers, and single hyphens.', 'error');
      return;
    }
    if (!window.confirm(`Create ${eventPath} with a blank menu and publish its public route?`)) return;
    setBusy(true); setStatus(`Creating ${eventPath}…`);
    try {
      const body = await requestJSON('/admin/api/events', {
        method: 'POST', headers: {'Content-Type': 'application/json', 'X-Manna-Admin': '1'},
        body: JSON.stringify({path: eventPath, revision: state.manifestRevision}),
      });
      state.manifestRevision = body.manifest_revision;
      await loadEvents(eventPath);
      setStatus(`${eventPath} was created with a blank menu. Review and publish your event details.`, 'success');
    } catch (error) {
      setStatus(error.status === 409 ? 'The event list changed or that path already exists. Reload and try again.' : (error.message || 'Could not create the event.'), error.status === 409 ? 'warning' : 'error');
    } finally { setBusy(false); }
  }

  async function deleteEvent() {
    if (state.busy || !state.path || !state.canManageEvents) return;
    if (isDirty() && !window.confirm('Discard your unpublished changes before removing this event?')) return;
    const eventPath = state.path;
    const confirmation = window.prompt(`Permanently remove ${eventPath}, its menu file, and its uploaded logo? Type the full event path to confirm.`);
    if (confirmation !== eventPath) {
      if (confirmation != null) setStatus(`Removal cancelled. The confirmation must exactly match ${eventPath}.`, 'warning');
      return;
    }
    if (!window.confirm(`This permanently deletes ${eventPath}. Continue?`)) return;
    setBusy(true); setStatus(`Removing ${eventPath}…`);
    try {
      const body = await requestJSON(`/admin/api/events/${eventSlug(eventPath)}`, {
        method: 'DELETE', headers: {'Content-Type': 'application/json', 'X-Manna-Admin': '1'},
        body: JSON.stringify({revision: state.manifestRevision, confirmation}),
      });
      state.manifestRevision = body.revision;
      clearEvent();
      await loadEvents();
      setStatus(`${eventPath} and its event-owned files were permanently removed.`, 'success');
    } catch (error) {
      setStatus(error.status === 409 ? 'The event list changed. Reload before removing this event.' : (error.message || 'Could not remove the event.'), error.status === 409 ? 'warning' : 'error');
    } finally { setBusy(false); }
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
      state.yaml = body.yaml || '';
      state.savedYAML = state.yaml;
      state.previewLogo = null;
      state.previewDay = 0;
      if (body.config) {
        applyConfig(body.config);
        state.saved = snapshot();
        state.publishedLogo = state.config.conference.logo || '';
        state.previewLanguage = state.config.conference.languages[0] || 'en';
      } else {
        state.config = null;
        state.saved = '';
        state.publishedLogo = '';
        state.previewLanguage = 'en';
        state.tab = 'yaml';
      }
      elements.eventSelect.value = body.path;
      updatePublicLink();
      render();
      setStatus(body.config ? `${body.path} loaded.` : `${body.path} contains invalid YAML. Repair it here, then check or publish it.`, body.config ? 'success' : 'warning');
      return true;
    } catch (error) {
      if (sequence === state.loadSequence) setStatus(error.message || 'Could not load the event.', 'error');
      return false;
    } finally { if (sequence === state.loadSequence) setBusy(false); }
  }

  function input(label, path, options = {}) {
    const value = get(path);
    const type = options.type || 'text';
    const required = options.required ? ' required' : '';
    const help = options.help ? `<small>${esc(options.help)}</small>` : '';
    if (type === 'checkbox') return `<label class="check-field"><input type="checkbox" data-path="${pathKey(path)}" data-type="boolean"${value ? ' checked' : ''}> <span>${esc(label)}</span></label>`;
    if (type === 'textarea') return `<label class="form-field span-2"><span>${esc(label)}</span><textarea data-path="${pathKey(path)}" rows="2"${required}>${esc(value || '')}</textarea>${help}</label>`;
    if (type === 'select') return `<label class="form-field"><span>${esc(label)}</span><select data-path="${pathKey(path)}">${options.values.map((entry) => `<option value="${esc(entry.value)}"${entry.value === value ? ' selected' : ''}>${esc(entry.label)}</option>`).join('')}</select>${help}</label>`;
    const dataType = type === 'price' ? ' data-type="price"' : type === 'list' ? ' data-type="list"' : '';
    const shown = type === 'price' ? (value == null ? '' : (value / 100).toFixed(2)) : type === 'list' ? (value || []).join(', ') : (value ?? '');
    const htmlType = type === 'price' ? 'number' : type;
    const priceAttrs = type === 'price' ? ' min="0" step="0.01" inputmode="decimal"' : '';
    return `<label class="form-field"><span>${esc(label)}</span><input type="${htmlType}" value="${esc(shown)}" data-path="${pathKey(path)}"${dataType}${priceAttrs}${required}>${help}</label>`;
  }

  function localized(label, path, textarea = false) {
    return `<div class="localized grid-2">${state.config.conference.languages.map((language) => input(`${label} · ${language.toUpperCase()}`, [...path, language], {type: textarea ? 'textarea' : 'text'})).join('')}</div>`;
  }

  function removeButton(path, label) { return `<button class="icon-button danger" type="button" data-action="remove" data-path="${pathKey(path)}" aria-label="Remove ${esc(label)}">Remove</button>`; }
  function addButton(path, template, label) { return `<button class="button add" type="button" data-action="add" data-path="${pathKey(path)}" data-template="${template}">+ ${esc(label)}</button>`; }
  function itemTemplate() { return {id: '', sold_out: false, name: {}, description: {}, variants: [], tags: []}; }
  function templates(name) {
    return {
      item: itemTemplate(), variant: {}, day: {date: '', food_trucks: [], services: []},
      service: {id: '', title: {}, subtitle: {}, from: '', until: '', sold_out: false, items: []},
      truck: {id: '', name: {}, description: {}, location: {}, payment: {}, times: [], items: []},
      time: {from: '', until: ''}, declaration: {},
    }[name];
  }

  function formatPrice(value) {
    const symbol = {dollar: '$', schekel: '₪'}[state.config.conference.currency] || '€';
    return `${symbol}${(value / 100).toFixed(2)}`;
  }

  function itemPriceSummary(item, includeSizes) {
    const prices = [];
    if (item.price != null) prices.push(formatPrice(item.price));
    if (includeSizes) {
      if (item.price_small != null) prices.push(`Small ${formatPrice(item.price_small)}`);
      if (item.price_normal != null) prices.push(`Normal ${formatPrice(item.price_normal)}`);
      if (item.price_large != null) prices.push(`Large ${formatPrice(item.price_large)}`);
    }
    return prices.join(' · ');
  }

  function renderItems(path) {
    const items = get(path) || [];
    const includeSizedPrices = path[0] === 'permanent';
    return `<div class="stack">${items.map((item, index) => {
      const current = [...path, index];
      const name = item.name?.[state.config.conference.languages[0]] || item.id || `Item ${index + 1}`;
      const priceSummary = itemPriceSummary(item, includeSizedPrices);
      return `<details class="nested-card" data-detail="${pathKey(current)}"><summary><span>${esc(name)}</span><span class="summary-meta">${item.sold_out ? `Sold out${priceSummary ? ' · ' : ''}` : ''}${esc(priceSummary)}</span></summary>
        <div class="nested-content"><div class="row-actions">${removeButton(current, 'item')}</div>
        <div class="grid-2">${input('ID', [...current, 'id'], {required: true})}${input('Sold out', [...current, 'sold_out'], {type: 'checkbox'})}</div>
        <h4>Name</h4>${localized('Name', [...current, 'name'])}<h4>Description</h4>${localized('Description', [...current, 'description'], true)}
        <h4>Price</h4><div class="grid-4">${['price', 'price_small', 'price_normal', 'price_large'].map((key) => input(title(key), [...current, key], {type: 'price'})).join('')}</div>
        ${input('Tags', [...current, 'tags'], {type: 'list', help: 'Comma-separated tag IDs'})}
        <h4>Variants</h4>${renderLocalizedList([...current, 'variants'], 'variant')}
        </div></details>`;
    }).join('')}${addButton(path, 'item', 'Add item')}</div>`;
  }

  function renderLocalizedList(path, singular) {
    const values = get(path) || [];
    return `<div class="compact-list">${values.map((_, index) => `<div class="compact-row"><div class="grow">${localized(title(singular), [...path, index])}</div>${removeButton([...path, index], singular)}</div>`).join('')}${addButton(path, 'declaration', `Add ${singular}`)}</div>`;
  }

  function renderEvent() {
    const base = ['conference'];
    return `<section class="form-section"><div class="section-heading"><div><p class="eyebrow">Event</p><h2>Event details</h2></div></div>
      <div class="grid-2">${input('Languages', [...base, 'languages'], {type: 'list', required: true, help: 'Comma-separated language codes'})}${input('Timezone', [...base, 'timezone'], {required: true})}${input('Currency', [...base, 'currency'], {type: 'select', values: [{value: '', label: 'Euro (default)'}, {value: 'euro', label: 'Euro'}, {value: 'dollar', label: 'Dollar'}, {value: 'schekel', label: 'Shekel'}]})}${renderLogoEditor([...base, 'logo'])}${input('Hide all prices', [...base, 'hide_prices'], {type: 'checkbox'})}${input('Serious mode', [...base, 'serious_mode'], {type: 'checkbox'})}${input('Easter egg mode', [...base, 'easter_egg_mode'], {type: 'select', values: [{value: '', label: 'Girly vibes (default)'}, {value: 'girly_vibes', label: 'Girly vibes'}, {value: 'mazel_tov', label: 'Mazel Tov'}]})}</div>
      <h3>Name</h3>${localized('Name', [...base, 'name'])}<h3>Location</h3>${localized('Location', [...base, 'location'])}<h3>Payment note</h3>${localized('Payment', [...base, 'payment'], true)}</section>
      <section class="form-section"><div class="section-heading"><div><p class="eyebrow">Labels</p><h2>Tags</h2></div>${addButton(['tags'], 'tag', 'Add tag')}</div>${renderMapEntries(['tags'], 'tag')}</section>`;
  }

  function renderLogoEditor(path) {
    return `<div class="logo-editor">${input('Logo filename', path)}<label class="button upload-button"><span>Upload PNG or JPG</span><input type="file" accept=".png,.jpg,.jpeg,image/png,image/jpeg" data-logo-upload="true"></label><small>PNG or JPEG, maximum 2 MB. Uploading sets the filename; publish to activate it.</small></div>`;
  }

  function renderMapEntries(path, kind) {
    const entries = Object.entries(get(path) || {});
    if (!entries.length) return '<p class="empty-state">No entries yet.</p>';
    return `<div class="stack">${entries.map(([key]) => `<div class="nested-card open-card"><div class="row-actions">${removeButton([...path, key], kind)}</div><div class="grid-2"><label class="form-field"><span>${title(kind)} ID</span><input value="${esc(key)}" data-action="rename" data-parent="${pathKey(path)}" data-old="${esc(key)}" required></label></div>${kind === 'tag' ? localized('Label', [...path, key]) : renderDeclaration([...path, key])}</div>`).join('')}</div>`;
  }

  function renderPermanent() {
    return `<section class="form-section"><p class="eyebrow">Always available</p><h2>Permanent menu</h2><p class="section-copy">Coffee, drinks, and snacks shown independently of the daily schedule.</p>${['coffee', 'drinks', 'snacks'].map((group) => `<div class="collection"><h3>${title(group)}</h3>${renderItems(['permanent', group])}</div>`).join('')}</section>`;
  }

  function renderSchedule() {
    return `<section class="form-section"><div class="section-heading"><div><p class="eyebrow">Schedule</p><h2>Days</h2></div>${addButton(['days'], 'day', 'Add day')}</div><div class="stack">${state.config.days.map((day, index) => renderDay(index, day)).join('')}</div></section>`;
  }

  function dayLabel(date, index) {
    const prefix = `Day ${index + 1}`;
    if (!/^\d{4}-\d{2}-\d{2}$/.test(date || '')) return date ? `${prefix} · ${date}` : prefix;
    const parsed = new Date(`${date}T00:00:00Z`);
    if (Number.isNaN(parsed.getTime())) return `${prefix} · ${date}`;
    const options = {weekday: 'long', year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC'};
    let formatter;
    try { formatter = new Intl.DateTimeFormat(state.config.conference.languages[0], options); }
    catch { formatter = new Intl.DateTimeFormat(undefined, options); }
    const formatted = formatter.format(parsed);
    return `${prefix} · ${formatted}`;
  }

  function renderDay(index, day) {
    const path = ['days', index];
    return `<details class="day-card" data-detail="${pathKey(path)}"><summary><span>${esc(dayLabel(day.date, index))}</span><span>${day.services.length} services · ${day.food_trucks.length} food trucks</span></summary><div class="day-content"><div class="row-actions">${removeButton(path, 'day')}</div>${input(`Day ${index + 1} date`, [...path, 'date'], {type: 'date', required: true})}
      <div class="collection"><div class="section-heading"><h3>Services</h3>${addButton([...path, 'services'], 'service', 'Add service')}</div>${(day.services || []).map((service, serviceIndex) => renderService([...path, 'services', serviceIndex], service)).join('')}</div>
      <div class="collection"><div class="section-heading"><h3>Food trucks</h3>${addButton([...path, 'food_trucks'], 'truck', 'Add food truck')}</div>${(day.food_trucks || []).map((truck, truckIndex) => renderTruck([...path, 'food_trucks', truckIndex], truck)).join('')}</div></div></details>`;
  }

  function renderService(path, service) {
    return `<details class="nested-card" data-detail="${pathKey(path)}"><summary><span>${esc(service.title?.[state.config.conference.languages[0]] || service.id || 'New service')}</span><span>${esc(service.from || '')}–${esc(service.until || '')}</span></summary><div class="nested-content"><div class="row-actions">${removeButton(path, 'service')}</div><div class="grid-4">${input('ID', [...path, 'id'], {required: true})}${input('From', [...path, 'from'], {type: 'time', required: true})}${input('Until', [...path, 'until'], {type: 'time', required: true})}${input('Sold out', [...path, 'sold_out'], {type: 'checkbox'})}</div><h4>Title</h4>${localized('Title', [...path, 'title'])}<h4>Subtitle</h4>${localized('Subtitle', [...path, 'subtitle'], true)}<h4>Items</h4>${renderItems([...path, 'items'])}</div></details>`;
  }

  function renderTruck(path, truck) {
    return `<details class="nested-card" data-detail="${pathKey(path)}"><summary><span>${esc(truck.name?.[state.config.conference.languages[0]] || truck.id || 'New food truck')}</span><span>${truck.items.length} items</span></summary><div class="nested-content"><div class="row-actions">${removeButton(path, 'food truck')}</div>${input('ID', [...path, 'id'], {required: true})}<h4>Name</h4>${localized('Name', [...path, 'name'])}<h4>Description</h4>${localized('Description', [...path, 'description'], true)}<h4>Location</h4>${localized('Location', [...path, 'location'])}<h4>Payment</h4>${localized('Payment', [...path, 'payment'], true)}<h4>Serving times</h4>${renderTimes([...path, 'times'])}<h4>Items</h4>${renderItems([...path, 'items'])}</div></details>`;
  }

  function renderTimes(path) {
    return `<div class="compact-list">${(get(path) || []).map((_, index) => `<div class="compact-row grid-time">${input('From', [...path, index, 'from'], {type: 'time', required: true})}${input('Until', [...path, index, 'until'], {type: 'time', required: true})}${removeButton([...path, index], 'time')}</div>`).join('')}${addButton(path, 'time', 'Add time window')}</div>`;
  }

  function renderDeclaration(path) { return ['allergens', 'additives', 'notices'].map((group) => `<div class="collection"><h4>${title(group)}</h4>${renderLocalizedList([...path, group], group.slice(0, -1))}</div>`).join(''); }
  function renderRegulatory() {
    return `<section class="form-section"><div class="section-heading"><div><p class="eyebrow">Guest information</p><h2>Allergens & declarations</h2><p class="section-copy">Use the exact item ID that the declaration applies to.</p></div>${addButton(['regulatory'], 'regulatory', 'Add declaration')}</div>${renderMapEntries(['regulatory'], 'regulatory')}</section>`;
  }

  const previewCopy = {
    de: {meals: 'Essen', refreshments: 'Getränke & Snacks', trucks: 'Food Trucks', more: 'Zusätzlich vor Ort', allDay: 'Durchgehend', coffee: 'Kaffee', drinks: 'Getränke', snacks: 'Snacks', program: 'Programm', fullDay: 'Der ganze Tag', location: 'Standort', soldOut: 'Ausverkauft', small: 'Klein', normal: 'Normal', large: 'Groß', enjoy: 'Guten Appetit!'},
    en: {meals: 'Meals', refreshments: 'Drinks & Snacks', trucks: 'Food Trucks', more: 'More to enjoy', allDay: 'All day', coffee: 'Coffee', drinks: 'Drinks', snacks: 'Snacks', program: 'Schedule', fullDay: 'The full day', location: 'Location', soldOut: 'Sold out', small: 'Small', normal: 'Regular', large: 'Large', enjoy: 'Enjoy your meal!'},
    ru: {meals: 'Еда', refreshments: 'Напитки и снеки', trucks: 'Фудтраки — уличная еда', more: 'Дополнительные угощения', allDay: 'Весь день', coffee: 'Кофе', drinks: 'Напитки', snacks: 'Снеки', program: 'Программа', fullDay: 'Меню на весь день', location: 'Локация', soldOut: 'Распродано', small: 'Маленький', normal: 'Обычный', large: 'Большой', enjoy: 'Приятного аппетита!'},
  };

  function previewText(value, language = state.previewLanguage) {
    if (!value) return '';
    return value[language] || value[language.split('-')[0]] || value.en || value.de || Object.values(value).find(Boolean) || '';
  }

  function previewPrice(value) {
    if (value == null || state.config.conference.hide_prices) return '';
    const currency = {dollar: 'USD', schekel: 'ILS'}[state.config.conference.currency] || 'EUR';
    try { return new Intl.NumberFormat(state.previewLanguage, {style: 'currency', currency}).format(value / 100); }
    catch { return `${(value / 100).toFixed(2)} ${currency}`; }
  }

  function previewAvailability(item) {
    const copy = previewCopy[state.previewLanguage.split('-')[0]] || previewCopy.en;
    if (item.sold_out) return `<span class="sold-out">${esc(copy.soldOut)}</span>`;
    if (state.config.conference.hide_prices) return '';
    const sized = [['price_small', copy.small], ['price_normal', copy.normal], ['price_large', copy.large]].filter(([key]) => item[key] != null);
    if (sized.length) return `<span class="size-prices">${sized.map(([key, label]) => `<span class="size-price">${esc(label)} <span class="price">${esc(previewPrice(item[key]))}</span></span>`).join('')}</span>`;
    return item.price == null ? '' : `<span class="price">${esc(previewPrice(item.price))}</span>`;
  }

  function previewVariants(item) {
    const values = (item.variants || []).map((variant) => previewText(variant)).filter(Boolean);
    return values.length ? `<span class="item-variants">${values.map(esc).join(' · ')}</span>` : '';
  }

  function previewTags(item) {
    const tags = (item.tags || []).map((id) => previewText(state.config.tags[id]) || id);
    return tags.length ? `<div class="tags">${tags.map((tag) => `<span><i aria-hidden="true"></i><b>${esc(tag)}</b></span>`).join('')}</div>` : '';
  }

  function previewDish(item) {
    return `<div class="focus-dish"><div><h3>${esc(previewText(item.name) || item.id || 'Untitled item')}</h3>${previewAvailability(item)}${previewVariants(item)}${previewText(item.description) ? `<p>${esc(previewText(item.description))}</p>` : ''}</div>${previewTags(item)}</div>`;
  }

  function previewService(service) {
    const copy = previewCopy[state.previewLanguage.split('-')[0]] || previewCopy.en;
    return `<article class="focus-card"><div class="focus-topline"><time>${esc(service.from || '')}–${esc(service.until || '')}</time></div><h2>${esc(previewText(service.title) || service.id || 'Untitled service')}</h2>${service.sold_out ? `<span class="sold-out">${esc(copy.soldOut)}</span>` : ''}<p class="focus-subtitle">${esc(previewText(service.subtitle))}</p><div class="focus-dishes">${(service.items || []).map(previewDish).join('')}</div></article>`;
  }

  function previewPermanent() {
    const permanent = state.config.permanent || {};
    const copy = previewCopy[state.previewLanguage.split('-')[0]] || previewCopy.en;
    const groups = [['coffee', copy.coffee], ['drinks', copy.drinks], ['snacks', copy.snacks]].filter(([key]) => permanent[key]?.length);
    if (!groups.length) return '';
    return `<aside class="always-card"><div class="card-heading"><div><p class="eyebrow">${esc(copy.allDay)}</p><h2>${esc(copy.refreshments)}</h2></div></div>${groups.map(([key, label]) => {
      const table = `<table class="price-table"><tbody>${permanent[key].map((item) => `<tr><th scope="row">${esc(previewText(item.name) || item.id)}${previewVariants(item)}</th><td>${previewAvailability(item)}</td></tr>`).join('')}</tbody></table>`;
      return `<div class="always-group"><h3>${esc(label)}</h3><div class="menu-table-compact">${table}</div><div class="menu-tables-wide">${table}</div></div>`;
    }).join('')}</aside>`;
  }

  function previewTrucks(day) {
    const copy = previewCopy[state.previewLanguage.split('-')[0]] || previewCopy.en;
    if (!day.food_trucks?.length) return '';
    return `<section class="food-trucks"><div class="section-heading"><p class="eyebrow">${esc(copy.more)}</p><h2>${esc(copy.trucks)}</h2></div><div class="food-truck-grid">${day.food_trucks.map((truck) => {
      const times = (truck.times || []).length ? truck.times : [{from: truck.from || '', until: truck.until || ''}];
      return `<article class="food-truck-card"><p class="food-truck-time">${times.map((time) => `${esc(time.from)}–${esc(time.until)}`).join(' · ')}</p><h3>${esc(previewText(truck.name) || truck.id)}</h3><p>${esc(previewText(truck.description))}</p><p class="food-truck-location">${esc(copy.location)}: ${esc(previewText(truck.location))}</p>${previewText(truck.payment) ? `<p class="payment-notice">${esc(previewText(truck.payment))}</p>` : ''}<ul class="food-truck-items">${(truck.items || []).map((item) => `<li><strong>${esc(previewText(item.name) || item.id)}</strong>${previewAvailability(item)}${previewVariants(item)}${previewText(item.description) ? `<small>${esc(previewText(item.description))}</small>` : ''}</li>`).join('')}</ul></article>`;
    }).join('')}</div></section>`;
  }

  function previewSchedule(day) {
    const copy = previewCopy[state.previewLanguage.split('-')[0]] || previewCopy.en;
    return `<section class="schedule"><div class="section-heading"><p class="eyebrow">${esc(copy.program)}</p><h2>${esc(copy.fullDay)}</h2></div><div class="timeline">${(day.services || []).map((service) => `<article class="timeline-item"><div class="timeline-time"><time>${esc(service.from || '')}</time><span>– ${esc(service.until || '')}</span></div><div class="timeline-content"><h3>${esc(previewText(service.title) || service.id)}</h3>${service.sold_out ? `<span class="sold-out">${esc(copy.soldOut)}</span>` : ''}<p>${esc(previewText(service.subtitle))}</p><div class="timeline-dishes">${(service.items || []).map((item) => `<div><strong>${esc(previewText(item.name) || item.id)}</strong>${previewAvailability(item)}${previewVariants(item)}${previewText(item.description) ? `<small>${esc(previewText(item.description))}</small>` : ''}</div>`).join('')}</div></div></article>`).join('')}</div></section>`;
  }

  function previewMarkup() {
    const config = state.config;
    const language = state.previewLanguage;
    const copy = previewCopy[language.split('-')[0]] || previewCopy.en;
    const day = config.days[state.previewDay] || config.days[0] || {services: [], food_trucks: []};
    const service = day.services?.[0];
    const draftLogo = config.conference.logo || '';
    const uploadedLogo = state.previewLogo?.filename === draftLogo ? state.previewLogo.dataURL : '';
    const logo = uploadedLogo || (draftLogo && draftLogo === state.publishedLogo ? `/branding${state.path}` : (!draftLogo ? '/static/logo.png' : ''));
    const logoMarkup = logo ? `<img class="brand-logo" src="${esc(logo)}" alt="${esc(previewText(config.conference.name))}">` : '<span>Logo preview unavailable until this menu is published.</span>';
    const direction = ['ar', 'fa', 'he', 'ur'].includes(language.split('-')[0]) ? 'rtl' : 'ltr';
    const body = `<div class="site-shell"><header class="site-header"><span class="brand">${logoMarkup}</span><div class="header-tools"><div class="location">${esc(previewText(config.conference.location))}</div><div class="language-switch">${config.conference.languages.map((entry) => `<button type="button" aria-pressed="${entry === language}">${esc(entry.toUpperCase())}</button>`).join('<span></span>')}</div></div></header><section class="intro"><p class="eyebrow">${esc(previewText(config.conference.name))}</p>${previewText(config.conference.payment) ? `<p class="payment-notice">${esc(previewText(config.conference.payment))}</p>` : ''}</section><nav class="day-switcher">${config.days.map((entry, index) => `<button type="button" aria-pressed="${index === state.previewDay}"><time>${esc(entry.date || `Day ${index + 1}`)}</time></button>`).join('')}</nav><main class="day-panel is-active"><nav class="topic-nav"><a>${esc(copy.meals)}</a>${day.food_trucks?.length ? `<a>${esc(copy.trucks)}</a>` : ''}<a>${esc(copy.refreshments)}</a><a>${esc(copy.program)}</a></nav><section class="overview-grid"><div class="featured-meals">${service ? previewService(service) : '<article class="focus-card"><h2>Add a service to preview it</h2></article>'}</div><div class="side-column">${previewTrucks(day)}${previewPermanent()}</div></section>${previewSchedule(day)}</main><footer>${esc(copy.enjoy)}</footer></div>`;
    return {language, direction, body, document: `<!doctype html><html lang="${esc(language)}" dir="${direction}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/static/styles.css"></head><body>${body}</body></html>`};
  }

  function writePreviewDocument() {
    previewTimer = 0;
    if (!state.config || !elements.preview) return;
    const markup = previewMarkup();
    const previewDocument = elements.preview.contentDocument;
    if (elements.preview.srcdoc && previewDocument?.body && previewDocument.documentElement) {
      const scrollTop = previewDocument.scrollingElement?.scrollTop || 0;
      previewDocument.documentElement.lang = markup.language;
      previewDocument.documentElement.dir = markup.direction;
      previewDocument.body.innerHTML = markup.body;
      if (previewDocument.scrollingElement) previewDocument.scrollingElement.scrollTop = scrollTop;
      return;
    }
    elements.preview.srcdoc = markup.document;
  }

  function flushPreview() {
    if (previewTimer) clearTimeout(previewTimer);
    writePreviewDocument();
  }

  function renderPreview(deferDocument = false) {
    if (!state.config || !elements.preview) {
      if (elements.previewPanel) elements.previewPanel.hidden = true;
      return;
    }
    const languages = state.config.conference.languages || [];
    if (!languages.includes(state.previewLanguage)) state.previewLanguage = languages[0] || 'en';
    if (state.previewDay >= state.config.days.length) state.previewDay = Math.max(0, state.config.days.length - 1);
    elements.previewPanel.hidden = false;
    elements.previewLanguage.innerHTML = languages.map((language) => `<option value="${esc(language)}"${language === state.previewLanguage ? ' selected' : ''}>${esc(language.toUpperCase())}</option>`).join('');
    elements.previewDay.innerHTML = state.config.days.map((day, index) => `<option value="${index}"${index === state.previewDay ? ' selected' : ''}>${esc(dayLabel(day.date, index))}</option>`).join('');
    if (previewTimer) clearTimeout(previewTimer);
    if (deferDocument) previewTimer = setTimeout(writePreviewDocument, 180);
    else writePreviewDocument();
  }

  function renderYAML() {
    return `<section class="form-section yaml-section"><div class="section-heading"><div><p class="eyebrow">Advanced</p><h2>Direct YAML editor</h2><p class="section-copy">Paste a complete menu here to import it, or copy the current YAML to export it.</p></div><button class="button secondary" type="button" data-action="copy-yaml">Copy YAML</button></div><label class="yaml-field"><span class="visually-hidden">Event menu YAML</span><textarea id="yaml-editor" data-yaml="true" spellcheck="false" autocapitalize="off" autocomplete="off">${esc(state.yaml)}</textarea></label><p class="yaml-help"><kbd>Ctrl</kbd>/<kbd>⌘</kbd> + <kbd>S</kbd> checks the YAML without publishing.</p></section>`;
  }

  function render() {
    if (!state.config && !isYAML()) return;
    const openDetails = new Set(Array.from(elements.form.querySelectorAll?.('details[open][data-detail]') || [], (detail) => detail.dataset.detail));
    const scrollY = window.scrollY;
    elements.tabs.hidden = false;
    elements.tabs.innerHTML = tabs.map(([key, label]) => `<button type="button" data-tab="${key}" class="tab${state.tab === key ? ' active' : ''}">${label}</button>`).join('');
    elements.form.innerHTML = ({event: renderEvent, permanent: renderPermanent, schedule: renderSchedule, regulatory: renderRegulatory, yaml: renderYAML}[state.tab])();
    for (const detail of elements.form.querySelectorAll?.('details[data-detail]') || []) {
      if (openDetails.has(detail.dataset.detail)) detail.open = true;
    }
    if (Number.isFinite(scrollY)) window.scrollTo?.({top: scrollY, behavior: 'auto'});
    updateControls();
    renderPreview();
  }

  function addAt(path, templateName) {
    const parent = get(path);
    if (Array.isArray(parent)) parent.push(clone(templates(templateName)));
    else {
      const prefix = templateName === 'tag' ? 'new_tag' : 'item_id';
      let key = prefix;
      for (let suffix = 2; parent[key]; suffix++) key = `${prefix}_${suffix}`;
      parent[key] = templateName === 'tag' ? {} : {allergens: [], additives: [], notices: []};
    }
    render(); changed();
  }

  function removeAt(path) {
    if (!window.confirm('Remove this entry?')) return;
    const parent = get(path.slice(0, -1));
    const key = path.at(-1);
    if (Array.isArray(parent)) parent.splice(key, 1); else delete parent[key];
    render(); changed();
  }

  function updateField(target, rerenderLanguages = false) {
    if (target.dataset.yaml) {
      state.yaml = target.value;
      changed();
      return;
    }
    if (target.dataset.action === 'rename') {
      if (target.value === target.dataset.old) return;
      const parent = get(readPath(target.dataset.parent));
      if (!target.value || Object.hasOwn(parent, target.value)) {
        setStatus('IDs must be non-empty and unique.', 'error');
        target.value = target.dataset.old;
        return;
      }
      parent[target.value] = parent[target.dataset.old];
      delete parent[target.dataset.old];
      render(); changed();
      return;
    }
    if (!target.dataset.path) return;
    const path = readPath(target.dataset.path);
    let value = target.value;
    if (target.dataset.type === 'boolean') value = target.checked;
    if (target.dataset.type === 'list') value = value.split(',').map((entry) => entry.trim()).filter(Boolean);
    if (target.dataset.type === 'price') value = value === '' ? undefined : Math.round(Number(value) * 100);
    set(path, value);
    if (path.join('.') === 'conference.logo' && state.previewLogo?.filename !== value) state.previewLogo = null;
    if (rerenderLanguages && path.join('.') === 'conference.languages') render();
    changed({immediatePreview: path.join('.') === 'conference.logo'});
  }

  async function validateMenu() {
    if ((!state.config && !isYAML()) || state.busy) return;
    setBusy(true); setStatus('Checking menu…');
    try {
      const body = await requestJSON(`/admin/api/events/${eventSlug(state.path)}/validate`, {method: 'POST', headers: {'Content-Type': 'application/json', 'X-Manna-Admin': '1'}, body: JSON.stringify(draftPayload())});
      if (isYAML()) {
        if (!body.config) throw new Error('The YAML is valid but could not be opened in the form editor.');
        applyConfig(body.config);
        state.previewLanguage ||= state.config.conference.languages[0] || 'en';
        renderPreview();
      } else {
        state.yaml = body.yaml;
      }
      setStatus('Everything looks good. Nothing has been published yet.', 'success');
    } catch (error) { setStatus(error.message || 'The menu has errors.', 'error'); }
    finally { setBusy(false); }
  }

  async function publishMenu() {
    if ((!state.config && !isYAML()) || state.busy || !isDirty()) return;
    if (!window.confirm(`Publish your changes to ${state.path}?`)) return;
    setBusy(true); setStatus('Checking and publishing menu…');
    try {
      const payload = {...draftPayload(), revision: state.revision};
      const body = await requestJSON(`/admin/api/events/${eventSlug(state.path)}`, {method: 'PUT', headers: {'Content-Type': 'application/json', 'X-Manna-Admin': '1'}, body: JSON.stringify(payload)});
      state.revision = body.revision;
      const publishedFromYAML = isYAML();
      if (body.config) applyConfig(body.config);
      if (typeof body.yaml === 'string') state.yaml = body.yaml;
      state.saved = body.config || !publishedFromYAML ? snapshot() : null;
      state.savedYAML = typeof body.yaml === 'string' || publishedFromYAML ? state.yaml : null;
      if (body.config || !publishedFromYAML) state.publishedLogo = state.config?.conference.logo || '';
      state.previewLogo = null;
      renderPreview();
      setStatus(`${state.path} was published successfully.`, 'success');
    } catch (error) {
      setStatus(error.status === 409 ? 'The live menu changed after you loaded it. Reload before publishing again.' : (error.message || 'Could not publish the menu.'), error.status === 409 ? 'warning' : 'error');
    } finally { setBusy(false); }
  }

  async function switchTab(nextTab) {
    if (!nextTab || nextTab === state.tab || state.busy) return;
    const crossingEditorModes = (state.tab === 'yaml') !== (nextTab === 'yaml');
    if (!crossingEditorModes) {
      state.tab = nextTab;
      render();
      return;
    }
    const sourceWasDirty = isDirty();
    setBusy(true);
    setStatus(isYAML() ? 'Reading YAML into the form editor…' : 'Generating YAML from the form editor…');
    try {
      const body = await requestJSON(`/admin/api/events/${eventSlug(state.path)}/validate`, {
        method: 'POST', headers: {'Content-Type': 'application/json', 'X-Manna-Admin': '1'},
        body: JSON.stringify(draftPayload()),
      });
      if (!body.config) throw new Error('The YAML is valid but could not be opened in the form editor.');
      state.yaml = body.yaml;
      applyConfig(body.config);
      if (!sourceWasDirty) {
        state.savedYAML = state.yaml;
        state.saved = snapshot();
        state.publishedLogo = state.config.conference.logo || '';
      }      state.tab = nextTab;
      render();
      setStatus(sourceWasDirty ? 'Your unpublished changes were synchronized.' : `${state.path} loaded.`, sourceWasDirty ? 'warning' : 'success');
    } catch (error) {
      setStatus(error.message || 'Could not switch editors because the menu has errors.', 'error');
    } finally { setBusy(false); }
  }

  async function copyYAML() {
    try {
      await navigator.clipboard.writeText(state.yaml);
      setStatus('YAML copied to the clipboard.', 'success');
    } catch {
      const editor = document.getElementById('yaml-editor');
      editor?.focus();
      editor?.select();
      setStatus('Select and copy the highlighted YAML.', 'warning');
    }
  }

  async function uploadLogo(target) {
    const file = target.files?.[0];
    if (!file) return;
    const typeAllowed = file.type === 'image/png' || file.type === 'image/jpeg';
    const extensionAllowed = !file.type && /\.(?:png|jpe?g)$/i.test(file.name || '');
    if (!typeAllowed && !extensionAllowed) {
      target.value = '';
      setStatus('Choose a PNG or JPEG image.', 'error');
      return;
    }
    if (file.size > 2 * 1024 * 1024) {
      target.value = '';
      setStatus('The logo is larger than 2 MB.', 'error');
      return;
    }
    setBusy(true);
    setStatus('Uploading logo…');
    try {
      const previewLogo = await new Promise((resolve) => {
        if (typeof FileReader === 'undefined') return resolve('');
        const reader = new FileReader();
        reader.addEventListener('load', () => resolve(typeof reader.result === 'string' ? reader.result : ''));
        reader.addEventListener('error', () => resolve(''));
        reader.readAsDataURL(file);
      });
      const form = new FormData();
      form.append('logo', file);
      const body = await requestJSON(`/admin/api/events/${eventSlug(state.path)}/logo`, {
        method: 'POST', headers: {'X-Manna-Admin': '1'}, body: form,
      });
      state.config.conference.logo = body.logo;
      state.previewLogo = {filename: body.logo, dataURL: previewLogo};
      render();
      changed();
      setStatus('Logo uploaded. Publish the menu to activate it.', 'warning');
    } catch (error) {
      setStatus(error.message || 'Could not upload the logo.', 'error');
    } finally { setBusy(false); }
  }

  elements.eventSelect.addEventListener('change', () => loadEvent(elements.eventSelect.value));
  elements.addEvent.addEventListener('click', addEvent);
  elements.deleteEvent.addEventListener('click', deleteEvent);
  elements.reload.addEventListener('click', () => loadEvent(state.path));
  elements.validate.addEventListener('click', validateMenu);
  elements.publish.addEventListener('click', publishMenu);
  elements.tabs.addEventListener('click', (event) => { if (event.target.dataset.tab) switchTab(event.target.dataset.tab); });
  elements.form.addEventListener('input', (event) => { if (event.target.dataset.type !== 'price' && event.target.dataset.action !== 'rename') updateField(event.target); });
  elements.form.addEventListener('change', (event) => {
    if (event.target.dataset.logoUpload) uploadLogo(event.target);
    else updateField(event.target, true);
  });
  elements.form.addEventListener('click', (event) => {
    const button = event.target.closest?.('button[data-action]');
    if (!button) return;
    if (button.dataset.action === 'copy-yaml') { copyYAML(); return; }
    const path = readPath(button.dataset.path);
    if (button.dataset.action === 'add') addAt(path, button.dataset.template);
    if (button.dataset.action === 'remove') removeAt(path);
  });
  elements.form.addEventListener('keydown', (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
      event.preventDefault();
      validateMenu();
    }
  });
  elements.previewLanguage?.addEventListener('change', () => { state.previewLanguage = elements.previewLanguage.value; renderPreview(); });
  elements.previewDay?.addEventListener('change', () => { state.previewDay = Number(elements.previewDay.value) || 0; renderPreview(); });
  window.addEventListener('beforeunload', (event) => { if (isDirty()) { event.preventDefault(); event.returnValue = ''; } });

  updatePublicLink();
  const ready = loadEvents();
  if (globalThis.__mannaAdminTest) Object.assign(globalThis.__mannaAdminTest, {ready, state, isDirty, loadEvent, validateMenu, publishMenu, switchTab, uploadLogo, addEvent, deleteEvent, addAt, removeAt, updateField, render, flushPreview});
})();
