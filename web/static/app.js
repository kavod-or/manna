(() => {
  if (performance.getEntriesByType("navigation")[0]?.type === "reload" && location.hash) {
    history.replaceState(history.state, "", location.pathname + location.search);
  }

  const root = document.documentElement;
  const languageButtons = [...document.querySelectorAll("[data-language]")];
  const languageContent = [...document.querySelectorAll("[data-lang-content]")];
  const dayButtons = [...document.querySelectorAll("[data-day-button]")];
  const dayPanels = [...document.querySelectorAll("[data-day-panel]")];
  const supportedLanguages = languageButtons.map((button) => button.dataset.language);

  const infoControls = [...document.querySelectorAll('.regulatory-control')];
  function closeFoodInfo() {
    infoControls.forEach((control) => {
      const popover = control.querySelector('.regulatory-popover');
      if (popover.matches(':popover-open')) popover.hidePopover();
    });
  }
  infoControls.forEach((control) => {
    const trigger = control.querySelector('.regulatory-trigger');
    const popover = control.querySelector('.regulatory-popover');
    trigger.addEventListener('click', () => {
      if (popover.matches(':popover-open')) {
        popover.hidePopover();
        return;
      }
      const label = popover.querySelector('.regulatory-title [data-lang-content]:not([hidden])')?.textContent;
      if (label) {
        trigger.setAttribute('aria-label', label);
        popover.setAttribute('aria-label', label);
      }
      const rect = trigger.getBoundingClientRect();
      const width = Math.min(320, window.innerWidth - 24);
      popover.style.width = `${width}px`;
      popover.style.left = `${Math.max(12, Math.min(rect.left, window.innerWidth - width - 12))}px`;
      popover.showPopover();
      const height = popover.getBoundingClientRect().height;
      const below = rect.bottom + 8;
      const above = rect.top - height - 8;
      popover.style.top = `${Math.max(12, Math.min(below + height <= window.innerHeight - 12 ? below : above, window.innerHeight - height - 12))}px`;
    });
    popover.addEventListener('toggle', () => trigger.setAttribute('aria-expanded', String(popover.matches(':popover-open'))));
  });
  window.addEventListener('scroll', closeFoodInfo, {passive: true});

  let manualDay = false;
  const clock = new Intl.DateTimeFormat("en-GB", {
    timeZone: document.body.dataset.timezone,
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", hourCycle: "h23",
  });

  function conferenceNow() {
    const parts = Object.fromEntries(clock.formatToParts(new Date()).map(({type, value}) => [type, value]));
    return {date: `${parts.year}-${parts.month}-${parts.day}`, time: `${parts.hour}:${parts.minute}`};
  }

  const translations = {
    de: {
      languageLabel: "Sprache wählen",
      dayLabel: "Konferenztage",
      topicLabel: "Direkt zu",
      today: "Heute",
      fontSettings: "Schrifteinstellungen",
      fontSize: "Schriftgröße",
    },
    en: {
      languageLabel: "Choose language",
      dayLabel: "Conference days",
      topicLabel: "Jump to",
      today: "Today",
      fontSettings: "Font settings",
      fontSize: "Font size",
    },
    ru: {
      languageLabel: "Выбрать язык",
      dayLabel: "Дни конференции",
      topicLabel: "Перейти к разделу",
      today: "Сегодня",
      fontSettings: "Настройки шрифта",
      fontSize: "Размер шрифта",
    },
  };

  function readLanguage() {
    const preferredLanguages = navigator.languages?.length
      ? navigator.languages
      : [navigator.language];

    for (const language of preferredLanguages) {
      const preferred = language?.toLowerCase();
      const exact = supportedLanguages.find((supported) => supported === preferred);
      if (exact) return exact;
      const primary = preferred?.split("-")[0];
      const related = supportedLanguages.find((supported) => supported.split("-")[0] === primary);
      if (related) return related;
    }

    return supportedLanguages[0] || root.lang;
  }

  function formatDates(language) {
    const copy = translations[language.split("-")[0]] || translations.en;
    const todayKey = conferenceNow().date;
    const formatter = new Intl.DateTimeFormat(language, { weekday: "short", day: "numeric", month: "short" });

    dayButtons.forEach((button) => {
      const dateKey = button.dataset.date;
      const date = new Date(`${dateKey}T12:00:00`);
      const label = formatter.format(date).replace(/\.$/, "");
      button.querySelector("time").textContent = dateKey === todayKey ? `${copy.today} · ${label}` : label;
    });
  }

  function setLanguage(language) {
    closeFoodInfo();
    const copy = translations[language.split("-")[0]] || translations.en;
    root.lang = language;
    root.dir = ["ar", "fa", "he", "ur"].includes(language.split("-")[0]) ? "rtl" : "ltr";
    languageContent.forEach((element) => {
      element.hidden = element.dataset.langContent !== language;
    });
    languageButtons.forEach((button) => {
      button.setAttribute("aria-pressed", String(button.dataset.language === language));
    });
    document.querySelector(".language-switch").setAttribute("aria-label", copy.languageLabel);
    document.querySelector(".day-switcher").setAttribute("aria-label", copy.dayLabel);
    document.querySelectorAll(".topic-nav").forEach((nav) => nav.setAttribute("aria-label", copy.topicLabel));
    const fontCopy = {
      settings: copy.fontSettings,
      title: copy.fontSize,
    };
    document.querySelectorAll("[data-font-copy]").forEach((element) => {
      element.textContent = fontCopy[element.dataset.fontCopy];
    });
    formatDates(language);
  }

  function selectDay(index) {
    closeFoodInfo();
    dayButtons.forEach((button) => {
      button.setAttribute("aria-pressed", String(button.dataset.dayButton === index));
    });
    dayPanels.forEach((panel) => {
      const active = panel.dataset.dayPanel === index;
      panel.hidden = !active;
      panel.classList.toggle("is-active", active);
    });
  }

  languageButtons.forEach((button) => button.addEventListener("click", () => setLanguage(button.dataset.language)));
  dayButtons.forEach((button) => button.addEventListener("click", () => { manualDay = true; selectDay(button.dataset.dayButton); }));

  function restoreTopic() {
    const target = document.getElementById(location.hash.slice(1));
    const panel = target?.closest("[data-day-panel]");
    if (!panel) return;
    manualDay = true;
    selectDay(panel.dataset.dayPanel);
    target.scrollIntoView();
  }

  function updateSchedule() {
    const now = conferenceNow();
    const days = dayButtons.slice().sort((a, b) => a.dataset.date.localeCompare(b.dataset.date));
    const selected = days.find((button) => button.dataset.date >= now.date) || days.at(-1);
    if (!manualDay && selected) selectDay(selected.dataset.dayButton);
    dayPanels.forEach((panel) => {
      const date = dayButtons.find((button) => button.dataset.dayButton === panel.dataset.dayPanel).dataset.date;
      const meals = [...panel.querySelectorAll("[data-featured-from]")].sort((a, b) => a.dataset.featuredFrom.localeCompare(b.dataset.featuredFrom));
      const time = date < now.date ? "24:00" : date > now.date ? "00:00" : now.time;
      const current = meals.filter((meal) => meal.dataset.featuredFrom <= time && time < meal.dataset.featuredUntil).at(-1);
      const featured = current || meals.find((meal) => meal.dataset.featuredFrom > time) || meals.at(-1);
      meals.forEach((meal) => { meal.hidden = meal !== featured; });
    });
    formatDates(root.lang);
  }

  window.addEventListener("hashchange", restoreTopic);
  setLanguage(readLanguage());
  updateSchedule();
  restoreTopic();
  setInterval(updateSchedule, 15000);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) updateSchedule(); });
})();

// Keep the visitor's readability preference on this device.
(() => {
  const root = document.documentElement;
  const [slider] = document.querySelectorAll("[data-font-size-slider]");
  if (!slider) return;
  const value = document.querySelector("[data-font-size-value]");
  if (!value) return;

  const storageKey = "manna-font-size";
  const minimum = Number(slider.min);
  const maximum = Number(slider.max);
  let selected = minimum;
  try {
    const stored = Number(window.localStorage?.getItem(storageKey));
    if (Number.isFinite(stored)) selected = Math.min(maximum, Math.max(minimum, stored));
  } catch {
    // Storage can be unavailable in privacy modes; the setting still works for this visit.
  }

  function setSize(size, persist = true) {
    selected = Math.min(maximum, Math.max(minimum, Number(size) || minimum));
    slider.value = String(selected);
    value.textContent = `${selected}%`;
    root.style.setProperty("--font-size-adjustment", `${selected - minimum}%`);
    if (persist) {
      try { window.localStorage?.setItem(storageKey, selected); } catch {}
    }
  }

  setSize(selected, false);
  slider.addEventListener("input", () => setSize(slider.value));
})();

// Keep only the trigger here; download the animation after the fifth click.
(() => {
  const trigger = document.querySelector('.app-version');
  if (!trigger) return;
  let clicks = 0;
  let loading = false;
  trigger.addEventListener('click', async () => {
    if (loading || ++clicks < 5) return;
    clicks = 0;
    loading = true;
    try {
      const {startManna} = await import('/static/manna.js');
      startManna();
    } catch {
      // A failed optional download must not affect the menu. Five clicks retry.
    } finally {
      loading = false;
    }
  });
})();

// A deliberate logo hold reveals the event's configured easter egg.
(() => {
  const mode = document.body.dataset.easterEggMode;
  if (mode === 'none') return;
  const logo = document.querySelector('.brand');
  if (!logo) return;

  const toggle = mode === 'mazel_tov' ? mazelTovToggle() : girlyVibesToggle();
  let timer;
  let held = false;
  let origin;
  const cancel = () => { clearTimeout(timer); timer = null; };

  logo.addEventListener('pointerdown', (event) => {
    if (event.button !== 0) return;
    cancel(); held = false;
    origin = {x: event.clientX, y: event.clientY};
    timer = setTimeout(() => { held = true; toggle(); }, 3000);
  });
  logo.addEventListener('pointermove', (event) => {
    if (origin && Math.hypot(event.clientX - origin.x, event.clientY - origin.y) > 12) cancel();
  });
  ['pointerup', 'pointercancel', 'pointerleave', 'blur', 'dragstart'].forEach((type) => logo.addEventListener(type, cancel));
  logo.addEventListener('keydown', (event) => {
    if (event.key !== ' ' || event.repeat) return;
    event.preventDefault(); cancel(); held = false;
    timer = setTimeout(() => { held = true; toggle(); }, 3000);
  });
  logo.addEventListener('keyup', cancel);
  logo.addEventListener('click', (event) => {
    if (held) { event.preventDefault(); held = false; }
  });
  logo.addEventListener('contextmenu', (event) => { if (timer || held) event.preventDefault(); });

  function girlyVibesToggle() {
    let theme;
    let heartStop;
    let heartClicks = 0;
    let heartLoading = false;
    let loading = false;
    return () => {
      if (loading) return;
      if (theme) {
        document.documentElement.classList.toggle('girly-vibes');
        heartClicks = 0;
        if (heartStop) {
          heartStop();
          heartStop = null;
        }
        return;
      }
      loading = true;
      const link = document.createElement('link');
      link.rel = 'stylesheet';
      link.href = '/static/girly.css';
      link.onload = () => {
        loading = false;
        theme = link;
        const message = document.createElement('button');
        message.type = 'button';
        message.className = 'girly-message';
        message.textContent = 'For the girly vibes';
        message.addEventListener('click', async () => {
          if (!document.documentElement.classList.contains('girly-vibes') || heartLoading || ++heartClicks < 5) return;
          heartClicks = 0;
          heartLoading = true;
          try {
            const {startHearts} = await import('/static/hearts.js');
            if (document.documentElement.classList.contains('girly-vibes')) heartStop = startHearts();
          } catch {
            // The optional effect can be retried without interrupting the menu.
          } finally {
            heartLoading = false;
          }
        });
        document.querySelector('.site-header').after(message);
        document.documentElement.classList.add('girly-vibes');
      };
      link.onerror = () => { loading = false; link.remove(); };
      document.head.append(link);
    };
  }

  function mazelTovToggle() {
    let loading = false;
    let stop;
    let stylesheetPromise;
    return async () => {
      if (stop) {
        const stopEffect = stop;
        stop = null;
        stopEffect();
        return;
      }
      if (loading) return;
      loading = true;
      try {
        if (!stylesheetPromise) {
          const link = document.createElement('link');
          link.rel = 'stylesheet';
          link.href = '/static/mazel-tov.css';
          stylesheetPromise = new Promise((resolve, reject) => {
            link.onload = resolve;
            link.onerror = () => {
              link.remove();
              stylesheetPromise = null;
              reject();
            };
            document.head.append(link);
          });
        }
        const [, module] = await Promise.all([
          stylesheetPromise,
          import('/static/mazel-tov.js'),
        ]);
        stop = module.startMazelTov(() => { stop = null; });
      } catch {
        // A failed optional download can be retried with another hold.
      } finally {
        loading = false;
      }
    };
  }
})();
