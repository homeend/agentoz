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

  var es = new EventSource('/events');
  es.addEventListener('message', function (e) {
    try {
      var d = JSON.parse(e.data);
      if (String(d.channel_id) === channelID) {
        refresh(messages, '/ui/channels/' + channelID + '/stream');
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
