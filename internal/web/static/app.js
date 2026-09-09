// shortDur mirrors Go's shortDur: "5s", "3m", "1h12m".
function shortDur(s) {
  if (s < 60) return s + 's';
  if (s < 3600) return Math.floor(s / 60) + 'm';
  return Math.floor(s / 3600) + 'h' + String(Math.floor((s % 3600) / 60)).padStart(2, '0') + 'm';
}

// Agent badges carry data-since; tick their elapsed time every second so
// "working 0s" does not sit frozen until the next server render. The
// label word comes from data-label (working/idle) and the stalled prefix
// from the class, so the text is rebuilt, not appended to.
setInterval(function () {
  var now = Math.floor(Date.now() / 1000);
  document.querySelectorAll('.state[data-since]').forEach(function (el) {
    var since = parseInt(el.dataset.since, 10);
    if (!since) return;
    var word = el.dataset.label === 'waiting' ? 'idle' : el.dataset.label;
    if (el.classList.contains('stalled')) word = 'stalled · ' + word;
    el.textContent = word + ' ' + shortDur(Math.max(0, now - since));
  });
}, 1000);

// erbrus UI script: SSE-driven refresh for the channel view.
// Contract: on "message"/"run" events whose channel_id matches the open
// channel, refetch the corresponding partial and swap innerHTML.
// In-page confirmation dialog for forms carrying data-confirm, on EVERY
// page (the project card's × for an empty archived channel lives outside
// the channel page — it once submitted with no dialog because this sat in
// the channel-only block). Delegated on document (capture) so it survives
// the innerHTML swaps of #messages and #runs. A confirmed form re-submits
// with a one-shot flag.
(function () {
  var overlay = document.createElement('div');
  overlay.className = 'modal-overlay';
  overlay.innerHTML = '<div class="modal"><div class="modal-msg"></div>' +
    '<div class="modal-foot"><button type="button" class="btn" data-act="cancel">Cancel</button>' +
    '<button type="button" class="btn danger" data-act="ok">Delete</button></div></div>';
  document.body.appendChild(overlay);
  var modalMsg = overlay.querySelector('.modal-msg');
  var pendingForm = null;

  function closeModal() { overlay.classList.remove('open'); pendingForm = null; }
  overlay.addEventListener('click', function (e) {
    if (e.target === overlay || e.target.dataset.act === 'cancel') closeModal();
    if (e.target.dataset.act === 'ok' && pendingForm) {
      var f = pendingForm;
      closeModal();
      f.dataset.confirmed = '1';
      if (f.requestSubmit) f.requestSubmit(); else f.submit();
    }
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && overlay.classList.contains('open')) closeModal();
  });

  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f.dataset || !f.dataset.confirm) return;
    if (f.dataset.confirmed === '1') { delete f.dataset.confirmed; return; }
    e.preventDefault();
    // Batch delete with nothing selected: no dialog, no request — nothing.
    if (f.id === 'batchdel' && !document.querySelector('.selbox:checked')) return;
    pendingForm = f;
    modalMsg.textContent = f.dataset.confirm;
    overlay.classList.add('open');
  }, true);
})();

// Collapsible rail sections (<details data-rail="…">). The open state is
// remembered per section in localStorage; the template's own `open`
// attribute is the default for a first visit (agents open, presets closed).
(function () {
  document.querySelectorAll('details[data-rail]').forEach(function (d) {
    var key = 'erbrus.rail.' + d.dataset.rail;
    try {
      var saved = localStorage.getItem(key);
      if (saved === '1') d.open = true; else if (saved === '0') d.open = false;
    } catch (e) { /* storage blocked: keep the default */ }
    d.addEventListener('toggle', function () {
      try { localStorage.setItem(key, d.open ? '1' : '0'); } catch (e) { /* ignore */ }
    });
  });
})();

(function () {
  var messages = document.getElementById('messages');
  if (!messages) return; // not on a channel page
  var channelID = messages.dataset.channel;
  var runs = document.getElementById('runs');

  function refresh(el, path) {
    fetch(path)
      .then(function (r) { if (!r.ok) throw new Error('refresh failed'); return r.text(); })
      .then(function (html) {
        // Checkbox selections live inside the swapped innerHTML — carry
        // them across the refresh or every new message would clear them.
        var checked = {};
        el.querySelectorAll('.selbox:checked').forEach(function (cb) { checked[cb.value] = true; });
        el.innerHTML = html;
        el.querySelectorAll('.selbox').forEach(function (cb) { if (checked[cb.value]) cb.checked = true; });
        if (el === messages) el.scrollTop = el.scrollHeight;
      })
      .catch(function () { /* transient; next event retries */ });
  }

  // System notes toggle: remembered per browser, applied as a class on
  // #messages so it survives the innerHTML swaps of the message list.
  var showSys = document.getElementById('show-system');
  if (showSys) {
    var key = 'erbrus.showSystem';
    var on = false;
    try { on = localStorage.getItem(key) === '1'; } catch (e) { /* storage blocked */ }
    showSys.checked = on;
    messages.classList.toggle('show-system', on);
    showSys.addEventListener('change', function () {
      messages.classList.toggle('show-system', showSys.checked);
      try { localStorage.setItem(key, showSys.checked ? '1' : '0'); } catch (e) { /* ignore */ }
      messages.scrollTop = messages.scrollHeight;
    });
  }

  // Desktop notifications: needs-input / stalled state changes and new
  // reports, for any channel, while this tab is open. Permission must be
  // requested from a user gesture, so it happens in the checkbox handler.
  var notifyBox = document.getElementById('notify');
  var notifyOn = false;
  var lastNotified = {}; // run id -> "state" or "state!" (stalled)
  function channelName(id) {
    var a = document.querySelector('a.chan[href="/ui/channels/' + id + '"]');
    return a ? a.textContent.replace(/\d+$/, '').trim() : '#' + id;
  }
  function notify(title, body, url, tag) {
    if (!notifyOn || typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
    try {
      var n = new Notification(title, { body: body, tag: tag });
      n.onclick = function () { window.focus(); if (url) window.open(url, tag); n.close(); };
    } catch (err) { /* notifications unavailable */ }
  }
  if (notifyBox) {
    try { notifyOn = localStorage.getItem('erbrus.notify') === '1'; } catch (e) { /* storage blocked */ }
    if (typeof Notification === 'undefined') {
      notifyBox.disabled = true;
      notifyBox.parentNode.title = 'this browser has no Notification API';
    } else if (notifyOn && Notification.permission !== 'granted') {
      notifyOn = false;
    }
    notifyBox.checked = notifyOn;
    notifyBox.addEventListener('change', function () {
      if (!notifyBox.checked) {
        notifyOn = false;
        try { localStorage.setItem('erbrus.notify', '0'); } catch (e) { /* ignore */ }
        return;
      }
      Notification.requestPermission().then(function (perm) {
        notifyOn = perm === 'granted';
        notifyBox.checked = notifyOn;
        try { localStorage.setItem('erbrus.notify', notifyOn ? '1' : '0'); } catch (e) { /* ignore */ }
        if (!notifyOn) notifyBox.parentNode.title = 'notifications are blocked for this site in the browser';
      });
    });
  }
  function onRunEvent(d) {
    if (!d.state && !d.stalled) return;
    var key = (d.state || '') + (d.stalled ? '!' : '');
    if (lastNotified[d.id] === key) return;
    lastNotified[d.id] = key;
    var ch = channelName(d.channel_id);
    if (d.state === 'question') {
      notify(d.agent_name + ' needs your input', 'in ' + ch + ' — click to open its screen', '/ui/runs/' + d.id + '/screen', 'screen:' + (d.tmux_target || d.id));
    } else if (d.stalled) {
      notify(d.agent_name + ' looks stalled', 'no terminal output for 2+ minutes in ' + ch, '/ui/runs/' + d.id + '/screen', 'screen:' + (d.tmux_target || d.id));
    }
  }
  // The redirect after a composer post carries the warning banner in the
  // URL; drop it so a reload does not resurrect a stale notice. A "message
  // queued" notice (data-queued=<agent>) turns into that agent's delivery
  // note when it arrives, and goes away on its own once delivered.
  var banner = document.getElementById('warning');
  if (banner && window.history && history.replaceState) {
    try { history.replaceState(null, '', location.pathname); } catch (e) { /* ignore */ }
  }
  function onMessageEvent(d) {
    if (d.kind === 'report') {
      notify('report from ' + (d.author_name || 'agent'), 'in ' + channelName(d.channel_id) + ': ' + String(d.body || '').slice(0, 120), '/ui/channels/' + d.channel_id, 'erbrus-report-' + d.id);
    }
    if (banner && banner.dataset.queued && d.kind === 'system' && String(d.channel_id) === channelID) {
      var body = String(d.body || '');
      if (body.indexOf(banner.dataset.queued + ': queued message') === 0) {
        banner.textContent = body;
        delete banner.dataset.queued;
        if (body.indexOf('NOT delivered') < 0) {
          setTimeout(function () { if (banner.parentNode) banner.parentNode.removeChild(banner); banner = null; }, 6000);
        }
      }
    }
  }

  var unselect = document.getElementById('unselect-all');
  if (unselect) {
    unselect.addEventListener('click', function () {
      document.querySelectorAll('.selbox').forEach(function (cb) { cb.checked = false; });
    });
  }

  messages.scrollTop = messages.scrollHeight;

  // Composer conveniences: Ctrl/Cmd+Enter sends, and the target selection
  // sticks per channel via a cookie (falls back to memo when the
  // remembered agent is no longer an option).
  var composer = document.querySelector('form.composer');
  if (composer) {
    var ta = composer.querySelector('textarea');
    var sel = composer.querySelector('select[name="target"]');
    if (ta) {
      ta.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
          e.preventDefault();
          if (composer.requestSubmit) composer.requestSubmit();
          else composer.submit();
        }
      });
    }
    if (sel) {
      var ckey = 'erbrus_target_' + channelID;
      var m = document.cookie.match(new RegExp('(?:^|; )' + ckey + '=([^;]*)'));
      if (m) {
        var v = decodeURIComponent(m[1]);
        if (sel.querySelector('option[value="' + CSS.escape(v) + '"]')) sel.value = v;
      }
      composer.addEventListener('submit', function () {
        document.cookie = ckey + '=' + encodeURIComponent(sel.value) +
          '; path=/; max-age=2592000; SameSite=Lax';
      });
    }
  }

  // bumpUnread increments (or creates) the sidebar badge for a channel the
  // user is NOT looking at. Server-side unread state is the truth; this
  // only keeps the open page honest until the next full render.
  function bumpUnread(id, kind) {
    var link = document.querySelector('.sidebar a.chan[href="/ui/channels/' + id + '"]');
    if (!link) return;
    var b = link.querySelector('.unreadb');
    if (!b) {
      b = document.createElement('span');
      b.className = 'unreadb';
      b.dataset.unread = id;
      b.textContent = '0';
      link.appendChild(b);
    }
    b.textContent = String((parseInt(b.textContent, 10) || 0) + 1);
    if (kind === 'report' || kind === 'system') b.classList.add('attn');
  }

  function refreshAll() {
    refresh(messages, '/ui/channels/' + channelID + '/stream');
    if (runs) refresh(runs, '/ui/channels/' + channelID + '/runs-panel');
  }

  // Liveness watchdog: the server pings every 20s. EventSource only
  // auto-reconnects on errors it can SEE — a silently dead connection
  // (e.g. the Windows->WSL2 localhost relay outliving the server) never
  // errors, so without this the page sits on a dead pipe until F5.
  var es = null;
  var lastSeen = Date.now();
  function alive() { lastSeen = Date.now(); }

  function connect() {
    if (es) es.close();
    es = new EventSource('/events');
    es.onopen = alive;
    es.addEventListener('ping', alive);
    es.addEventListener('message', function (e) {
      alive();
      try {
        var d = JSON.parse(e.data);
        onMessageEvent(d);
        if (String(d.channel_id) === channelID) {
          refresh(messages, '/ui/channels/' + channelID + '/stream');
        } else {
          bumpUnread(d.channel_id, d.kind);
        }
      } catch (err) { /* ignore malformed */ }
    });
    es.addEventListener('run', function (e) {
      alive();
      try {
        var d = JSON.parse(e.data);
        onRunEvent(d);
        if (String(d.channel_id) === channelID && runs) {
          refresh(runs, '/ui/channels/' + channelID + '/runs-panel');
        }
      } catch (err) { /* ignore malformed */ }
    });
  }
  connect();

  setInterval(function () {
    if (Date.now() - lastSeen > 45000) {
      alive(); // one reconnect attempt per stale period, not every tick
      connect();
      refreshAll(); // catch messages missed while the pipe was dead
    }
  }, 15000);
  // The agent badges carry "working 3m"-style durations that only the
  // server renders, so re-fetch the rail on a slow clock too.
  if (runs) setInterval(function () { refresh(runs, '/ui/channels/' + channelID + '/runs-panel'); }, 15000);
})();

// Interactive terminal: xterm.js over a websocket to a pty running a
// size-neutral tmux client (docs/superpowers/specs/2026-09-08-web-terminal-design.md).
// The terminal is exactly the tmux window's size (server-sent), never
// fitted to the page; when the socket fails the read-only <pre> returns.
(function () {
  var el = document.getElementById('term');
  if (!el) return;
  var screen = document.getElementById('screen');
  var errEl = document.getElementById('screenerr');
  function fallback(reason) {
    if (el.hidden) return;
    el.hidden = true;
    if (screen) screen.hidden = false;
    if (reason && errEl) errEl.textContent = reason;
    document.dispatchEvent(new CustomEvent('erbrus:terminal-fallback'));
  }
  if (typeof Terminal === 'undefined') { fallback('terminal script did not load'); return; }
  var term = new Terminal({
    cursorBlink: true, scrollback: 0, fontSize: 13,
    fontFamily: 'ui-monospace, Menlo, Consolas, "DejaVu Sans Mono", monospace',
    theme: { background: '#0c0d0f', foreground: '#d6d8dc' }
  });
  term.open(el);
  var ws = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + el.dataset.ws);
  ws.binaryType = 'arraybuffer';
  var gotOutput = false;
  ws.onmessage = function (ev) {
    if (typeof ev.data === 'string') {
      try {
        var sz = JSON.parse(ev.data);
        if (sz.cols > 0 && sz.rows > 0) term.resize(sz.cols, sz.rows);
      } catch (e) { /* not a size message */ }
      return;
    }
    gotOutput = true;
    term.write(new Uint8Array(ev.data));
  };
  ws.onclose = function (ev) {
    fallback(ev.reason || (gotOutput ? 'terminal closed' : 'terminal unavailable'));
  };
  term.onData(function (d) { if (ws.readyState === WebSocket.OPEN) ws.send(d); });
  term.onBinary(function (d) {
    if (ws.readyState !== WebSocket.OPEN) return;
    var b = new Uint8Array(d.length);
    for (var i = 0; i < d.length; i++) b[i] = d.charCodeAt(i) & 255;
    ws.send(b);
  });
  el.addEventListener('click', function () { term.focus(); });
  if (term.textarea) {
    term.textarea.addEventListener('focus', function () { el.classList.add('typing'); });
    term.textarea.addEventListener('blur', function () { el.classList.remove('typing'); });
  }
  term.focus();
})();

// Screen view: one EventSource per open screen page, frames swap the <pre>.
// The activity age is computed client-side from the last frame's unix
// timestamp so the server only talks when the screen actually changes.
(function () {
  var screen = document.getElementById('screen');
  if (!screen || screen.dataset.static) return; // not a live screen page
  // With the terminal showing, frames are for the header/keypad only:
  // ask the server to leave the html out. Fallback flips this back.
  var termEl = document.getElementById('term');
  var wantHTML = !termEl || termEl.hidden;
  document.addEventListener('erbrus:terminal-fallback', function () { wantHTML = true; connect(); });
  var runID = screen.dataset.run;
  var head = document.getElementById('screenhead');
  var act = document.getElementById('screenact');
  var errEl = document.getElementById('screenerr');
  var stateEl = document.getElementById('screenstate');
  var keypad = document.getElementById('keypad');
  var keypadKeys = document.getElementById('keypad-keys');
  function esc(t) { return String(t).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }
  function btn(key, label, cls, title) {
    return '<button class="btn small' + (cls ? ' ' + cls : '') + '" name="key" value="' + esc(key) + '"' +
      (title ? ' title="' + esc(title) + '"' : (label !== key ? ' title="' + esc(key + ' — ' + label) + '"' : '')) + '>' + esc(label) + '</button>';
  }
  // Rebuild the answer buttons from the dialog's own options: numbered
  // ones press the digit, cursor-style ones ("pick") are walked to and
  // confirmed by the server; arrows only when the dialog has none we could read.
  function renderKeys(options) {
    if (!keypadKeys) return;
    var html = '';
    (options || []).forEach(function (o) {
      if (o.pick) html += btn(o.key, (o.current ? '› ' : '') + o.label, 'opt pick', 'select this and confirm');
      else html += btn(o.key, o.key + ' · ' + o.label, 'opt');
    });
    if (!options || !options.length) html += btn('Up', '↑') + btn('Down', '↓');
    html += btn('Enter', 'Enter', 'primary') + btn('Escape', 'Esc');
    keypadKeys.innerHTML = html;
  }
  // Mirrors stallAfter in watch.go: silence this long while working (or
  // unclassified) is a stall; silence while waiting/question is normal.
  var stallAfter = 120;
  var activity = parseInt(act.dataset.activity, 10) || 0;
  var dead = false;
  var state = stateEl.dataset.state || '';
  var step = parseInt(stateEl.dataset.step, 10) || 0;

  function fmtDur(s) {
    if (s < 60) return s + 's';
    if (s < 3600) return Math.floor(s / 60) + 'm' + (s % 60) + 's';
    return Math.floor(s / 3600) + 'h' + Math.floor((s % 3600) / 60) + 'm';
  }
  function tickAge() {
    var label = state || 'unknown';
    if (state === 'working' && step) label += ' · step ' + fmtDur(step);
    if (state === 'waiting') label = 'idle — waiting for input';
    if (state === 'question') label = 'needs input';
    if (state === 'unavailable') label = 'not available';
    stateEl.textContent = label;
    // Unknown means no screen rule matched, not that nothing is happening:
    // a provider without rules (a plain shell, a new tool) is always unknown.
    stateEl.title = state ? '' :
      'no screen rule matched this provider\'s screen, so its state cannot be read. ' +
      'Rules exist for claude-code and codex; add yours under Settings → Screen rules (Test against agent shows what the screen looks like).';
    stateEl.className = 'state' + (state ? ' st-' + state : '');
    // The keypad exists to answer a dialog; hide it as soon as the
    // screen stops showing one (the next frame after a keypress).
    if (keypad) keypad.hidden = state !== 'question';
    if (!activity) { act.textContent = ''; head.classList.remove('idle'); return; }
    var age = Math.max(0, Math.floor(Date.now() / 1000 - activity));
    act.textContent = 'last output ' + fmtDur(age) + ' ago';
    var stalled = !dead && age > stallAfter && (state === 'working' || state === '');
    head.classList.toggle('idle', stalled);
    stateEl.classList.toggle('stalled', stalled);
  }
  setInterval(tickAge, 1000);
  tickAge();

  // Keypad presses go in the background: the page and its frame stream
  // stay up, and the next frame hides the keypad once the dialog is gone.
  if (keypad) {
    keypad.addEventListener('click', function (e) {
      var b = e.target.closest('button[name="key"]');
      if (!b) return;
      e.preventDefault();
      var fd = new FormData(keypad);
      fd.set('key', b.value);
      fetch(keypad.action, { method: 'POST', body: fd, redirect: 'manual' })
        .catch(function () { /* the stream will show what happened */ });
    });
  }

  var es = null;
  var lastSeen = Date.now();
  function alive() { lastSeen = Date.now(); }
  function connect() {
    if (es) es.close();
    es = new EventSource('/ui/runs/' + runID + '/screen/events' + (wantHTML ? '' : '?html=0'));
    es.onopen = alive;
    es.addEventListener('ping', alive);
    es.addEventListener('frame', function (e) {
      alive();
      try {
        var f = JSON.parse(e.data);
        if (f.error) {
          // The window is gone (or tmux is): the last known state would
          // be a lie, and the keypad has nothing to answer.
          errEl.textContent = f.error;
          state = 'unavailable'; activity = 0; step = 0;
          tickAge();
          return;
        }
        errEl.textContent = f.dead ? 'process exited — final screen' : '';
        dead = !!f.dead;
        if (f.html !== undefined) screen.innerHTML = f.html;
        activity = f.activity || 0;
        state = f.state || '';
        step = f.step || 0;
        if (state === 'question') renderKeys(f.options);
        tickAge();
      } catch (err) { /* ignore malformed */ }
    });
  }
  connect();
  setInterval(function () {
    if (Date.now() - lastSeen > 45000) { alive(); connect(); }
  }, 15000);
})();
