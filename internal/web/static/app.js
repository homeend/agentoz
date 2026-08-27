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

  var es = new EventSource('/events');
  es.addEventListener('message', function (e) {
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
    try {
      var d = JSON.parse(e.data);
      if (String(d.channel_id) === channelID && runs) {
        refresh(runs, '/ui/channels/' + channelID + '/runs-panel');
      }
    } catch (err) { /* ignore malformed */ }
  });
})();
