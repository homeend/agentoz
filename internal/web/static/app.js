// erbrus UI script: SSE-driven refresh for the channel view.
// Contract: on "message"/"run" events whose channel_id matches the open
// channel, refetch the corresponding partial and swap innerHTML.
(function () {
  var messages = document.getElementById('messages');
  if (!messages) return; // not on a channel page
  var channelID = messages.dataset.channel;
  var runs = document.getElementById('runs');

  function refresh(el, path) {
    fetch(path)
      .then(function (r) { if (!r.ok) throw new Error('refresh failed'); return r.text(); })
      .then(function (html) {
        el.innerHTML = html;
        if (el === messages) el.scrollTop = el.scrollHeight;
      })
      .catch(function () { /* transient; next event retries */ });
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
})();
