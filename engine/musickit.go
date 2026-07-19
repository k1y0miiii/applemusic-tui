package engine

// MusicKit JS snippets evaluated in the music.apple.com page context.
// Action snippets end with `return true` so Evaluate always has a value.

const stateJS = `(async () => {
  const mk = MusicKit.getInstance();
  const err = window.__amtuiErr || '';
  window.__amtuiErr = '';
  const fmt = (x) => {
    const a = (x && x.attributes) || {};
    return {
      id: String((x && x.id) || (a.playParams && a.playParams.id) || ''),
      title: a.name || '',
      artist: a.artistName || '',
      album: a.albumName || '',
      durMs: a.durationInMillis || 0,
    };
  };
  const q = mk.queue;
  const mip = (mk.services && mk.services.mediaItemPlayback) || mk._mediaItemPlayback;
  const cp = mip && mip._currentPlayer;
  const audio = document.querySelector('audio');
  const buffer = cp && cp._buffer;
  const mediaSource = buffer && buffer.mediaSource;
  const hasSourceBuffer = !!(
    mediaSource &&
    mediaSource.sourceBuffers &&
    mediaSource.sourceBuffers.length > 0
  );
  const initializing = !!(
    (cp && cp._deferredPlay) ||
    (mk.nowPlayingItem && mk.playbackState === 1) ||
    (mk.nowPlayingItem && audio && !audio.paused && audio.readyState < 2)
  );
  const mseFullyUsable = !!(
    mediaSource &&
    hasSourceBuffer &&
    (mediaSource.readyState === 'ended' || buffer.isFullyBuffered === true)
  );
  // Raw/non-MSE assets have no MediaSource. playbackState plus HAVE_FUTURE_DATA
  // is the safe fallback; an MSE pipeline without attached data stays below it.
  const nonMSEReady = !mediaSource;
  const engineReady = !!(
    mk.playbackState === 2 &&
    audio &&
    audio.readyState >= 3 &&
    !(cp && cp._deferredPlay) &&
    (mseFullyUsable || nonMSEReady)
  );
  const hiddenStall = !!(
    document.visibilityState === 'hidden' &&
    initializing &&
    mk.playbackState !== 2 &&
    cp &&
    cp._deferredPlay &&
    mediaSource &&
    mediaSource.readyState === 'closed' &&
    !hasSourceBuffer &&
    audio &&
    audio.readyState === 0
  );
  return JSON.stringify({
    err,
    playing: mk.playbackState === 2,
    initializing,
    engineReady,
    hiddenStall,
    pos: mk.currentPlaybackTime || 0,
    dur: mk.currentPlaybackDuration || 0,
    volume: Math.round((mk.volume || 0) * 100),
    shuffle: (mk.shuffleMode || 0) === 1,
    repeat: mk.repeatMode || 0,
    now: mk.nowPlayingItem ? fmt(mk.nowPlayingItem) : null,
    queuePos: q ? q.position : -1,
    queue: q ? q.items.slice(0, 200).map(fmt) : [],
  });
})()`

// Actions are fire-and-forget: MusicKit promises sometimes never resolve
// (awaiting them held the engine mutex until the 10s deadline), and the
// 500ms state poll picks up the real result anyway. Rejections are recorded
// in window.__amtuiErr and surfaced by the next state poll.

// trap(name) attaches a rejection recorder to a promise.
const trapJS = `const trap = (name) => (p) => { if (p && p.catch) p.catch((e) => { window.__amtuiErr = name + ': ' + ((e && e.message) || e); }); };`

// MusicKit action promises can remain pending after the action has succeeded.
// Wait for observable state instead: cold DRM setup to finish, audio to
// play/pause, or the queue position to move.
const playerWaitJS = `
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const mipOf = (mk) => (mk.services && mk.services.mediaItemPlayback) || mk._mediaItemPlayback;
  const cpOf = (mk) => {
    const mip = mipOf(mk);
    return mip && mip._currentPlayer;
  };
  const waitFor = async (condition, timeout, label) => {
    const deadline = Date.now() + timeout;
    while (!condition()) {
      if (Date.now() >= deadline) throw new Error('timeout waiting for ' + label);
      await sleep(25);
    }
  };
  const waitForColdStart = (mk) =>
    waitFor(() => {
      const cp = cpOf(mk);
      return !cp || !cp._deferredPlay;
    }, 30000, 'initial Apple Music playback');
`

// Pause and resume only through MusicKit so its PlayActivity state stays in
// sync. During the first DRM load, wait for _deferredPlay instead of starting
// overlapping operations. Never await play/pause promises themselves.
const playPauseJS = `(() => {
  ` + trapJS + playerWaitJS + `
  const mk = MusicKit.getInstance();
  const el = document.querySelector('audio');
  const playing = mk.playbackState === 2 && (!el || !el.paused);
  if (window.__amtuiTransportRunning) return true;
  window.__amtuiTransportRunning = true;
  const toggle = async () => {
    try {
      await waitForColdStart(mk);
      const mip = mipOf(mk);
      const cp = cpOf(mk);
      if (playing) {
        if (cp && cp.pause) trap('pause')(cp.pause());
        else if (mip && mip.pause) trap('pause')(mip.pause());
        else if (mk.pause) trap('pause')(mk.pause());
        else if (el) el.pause();
        await waitFor(() => {
          const audio = document.querySelector('audio');
          return !audio || audio.paused || mk.playbackState !== 2;
        }, 3000, 'pause');
        return;
      }

      const started = () => {
        const audio = document.querySelector('audio');
        return !!audio && !audio.paused && audio.readyState >= 2;
      };
      // startPlaying may already be completing the cold start.
      if (started()) return;
      if (cp && cp.play) trap('resume')(cp.play(true));
      else if (mip && mip.play) trap('resume')(mip.play());
      else trap('resume')(mk.play());
      await waitFor(started, 3000, 'resume');
    } finally {
      window.__amtuiTransportRunning = false;
    }
  };
  trap(playing ? 'pause' : 'resume')(toggle());
  return true;
})()`

// autoplayJS turns on Apple Music's infinity autoplay: when the queue runs
// out, MusicKit keeps appending similar tracks (like the web player's ∞).
const autoplayJS = `(() => {
  try { MusicKit.getInstance().autoplayEnabled = true; } catch (e) {}
  return true;
})()`

const nextJS = `(() => {
  ` + trapJS + `
  trap('next')(MusicKit.getInstance().skipToNextItem());
  return true;
})()`
const prevJS = `(() => {
  ` + trapJS + `
  trap('prev')(MusicKit.getInstance().skipToPreviousItem());
  return true;
})()`

const seekJS = `(() => {
  ` + trapJS + `
  trap('seek')(MusicKit.getInstance().seekToTime(%f));
  return true;
})()`

const volumeJS = `(() => { MusicKit.getInstance().volume = %f; return true; })()`

const shuffleJS = `(() => {
  const mk = MusicKit.getInstance();
  mk.shuffleMode = mk.shuffleMode === 1 ? 0 : 1;
  return true;
})()`

const repeatJS = `(() => {
  const mk = MusicKit.getInstance();
  mk.repeatMode = ((mk.repeatMode || 0) + 1) % 3;
  return true;
})()`

// Jump by walking the already-resolved queue. A skip changes queue.position in
// ~50 ms but its Promise may never settle, so observe position directly.
// Repeated Enter updates one pending target instead of spawning races.
const jumpJS = `(() => {
  ` + trapJS + playerWaitJS + `
  const mk = MusicKit.getInstance();
  const q = mk.queue;
  if (!q || !q.items || !q.items.length) return true;
  const i = %d;
  if (i < 0 || i >= q.items.length) return true;
  window.__amtuiJumpTarget = i;
  if (window.__amtuiJumpRunning) return true;
  window.__amtuiJumpRunning = true;
  const jump = async () => {
    try {
      await waitForColdStart(mk);
      let guard = 0;
      while (guard++ < 200) {
        const target = window.__amtuiJumpTarget | 0;
        const queue = mk.queue;
        if (!queue || target < 0 || target >= queue.items.length) {
          throw new Error('jump target left the queue');
        }
        const before = queue.position | 0;
        if (before === target) return;
        try {
          const pending = before < target
            ? mk.skipToNextItem()
            : mk.skipToPreviousItem();
          trap('jump step')(pending);
        } catch (e) {
          throw e;
        }
        await waitFor(
          () => !!mk.queue && (mk.queue.position | 0) !== before,
          3000,
          'queue position'
        );
      }
      throw new Error('jump exceeded queue length');
    } finally {
      window.__amtuiJumpRunning = false;
    }
  };
  trap('jump')(jump());
  return true;
})()`

const playItemJS = `(() => {
  const mk = MusicKit.getInstance();
  const generation = window.__amtuiPlayGeneration =
    (window.__amtuiPlayGeneration | 0) + 1;
  const reportSetQueueError = (e) => {
    if (window.__amtuiPlayGeneration === generation) {
      window.__amtuiErr = 'setQueue: ' + ((e && e.message) || e);
    }
  };
  try {
    const setQueuePending = mk.setQueue({ %s: %s, startPlaying: true });
    if (setQueuePending && setQueuePending.catch) {
      setQueuePending.catch(reportSetQueueError);
    }
  } catch (e) {
    reportSetQueueError(e);
  }
  try { mk.autoplayEnabled = true; } catch (e) {}
  const watchdog = async () => {
    const superseded = () => window.__amtuiPlayGeneration !== generation;
    const reportRecoveryError = (e) => {
      if (!superseded()) {
        window.__amtuiErr = 'recovery: ' + ((e && e.message) || e);
      }
    };
    const trapRecovery = (pending) => {
      if (pending && pending.catch) pending.catch(reportRecoveryError);
    };
    let ownsRecovery = false;
    try {
      const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
      const healthy = () => {
        const audio = document.querySelector('audio');
        return mk.playbackState === 2 || !!(audio && audio.readyState >= 2);
      };
      const configuredDelay = window.__amtuiWatchdogDelay;
      const delay = Number.isFinite(configuredDelay)
        ? Math.max(0, configuredDelay)
        : 4000;
      const watchdogDeadline = Date.now() + delay;
      while (Date.now() < watchdogDeadline) {
        if (superseded() || healthy()) return;
        await sleep(Math.min(50, watchdogDeadline - Date.now()));
      }
      if (superseded() || healthy()) return;

      const mip = (mk.services && mk.services.mediaItemPlayback) || mk._mediaItemPlayback;
      const cp = mip && mip._currentPlayer;
      const audio = document.querySelector('audio');
      const mediaSource = cp && cp._buffer && cp._buffer.mediaSource;
      const hasSourceBuffer = !!(
        mediaSource &&
        mediaSource.sourceBuffers &&
        mediaSource.sourceBuffers.length > 0
      );
      const stalled = !!(
        mk.playbackState !== 2 &&
        cp &&
        cp._deferredPlay &&
        mediaSource &&
        mediaSource.readyState === 'closed' &&
        !hasSourceBuffer &&
        audio &&
        audio.readyState === 0
      );
      if (
        !stalled ||
        window.__amtuiRecovering ||
        window.__amtuiRecoveredGeneration === generation
      ) return;
      window.__amtuiRecovering = true;
      window.__amtuiRecoveredGeneration = generation;
      ownsRecovery = true;

      cp.finishPlaybackSequence();

      const cleanupDeadline = Date.now() + 1500;
      const waitForCleanup = async () => {
        while (Date.now() < cleanupDeadline) {
          const buffer = cp._buffer;
          if (!buffer || !buffer.mediaSource || buffer.mediaSource.readyState !== 'closed') return;
          await sleep(Math.min(25, cleanupDeadline - Date.now()));
        }
      };
      const stopped = Promise.resolve(cp.stopMediaAndCleanup());
      await Promise.race([stopped, waitForCleanup()]);
      if (superseded()) return;

      const controller = mk._playbackController;
      trapRecovery(controller._changeToMediaAtIndex(
        mk.queue.position | 0,
        { userInitiated: true }
      ));
    } catch (e) {
      reportRecoveryError(e);
    } finally {
      if (ownsRecovery) window.__amtuiRecovering = false;
    }
  };
  setTimeout(watchdog, 0);
  return true;
})()`

const queueNextJS = `(() => {
  ` + trapJS + `
  trap('playNext')(MusicKit.getInstance().playNext({ %s: %s }));
  return true;
})()`
const queueLaterJS = `(() => {
  ` + trapJS + `
  trap('playLater')(MusicKit.getInstance().playLater({ %s: %s }));
  return true;
})()`

const libraryJS = `(async () => {
  const mk = MusicKit.getInstance();
  const get = async (path, params) => {
    try {
      const r = await mk.api.music(path, params);
      return (r.data && r.data.data) || [];
    } catch (e) { return []; }
  };
  const [albums, playlists, recent] = await Promise.all([
    get('/v1/me/library/albums', { limit: 100 }),
    get('/v1/me/library/playlists', { limit: 100 }),
    get('/v1/me/recent/played', { limit: 10 }), // API max is 10 for this endpoint
  ]);
  const item = (kind) => (x) => ({
    id: String(x.id), kind,
    title: (x.attributes && x.attributes.name) || '',
    artist: (x.attributes && (x.attributes.artistName || x.attributes.curatorName)) || '',
    album: '', durMs: 0,
  });
  const rec = recent.map((x) => {
    const t = x.type || '';
    const kind = t.includes('album') ? 'album'
      : t.includes('playlist') ? 'playlist'
      : t.includes('song') ? 'song' : '';
    return item(kind)(x);
  }).filter((x) => x.kind);
  return JSON.stringify({
    songs: [], recent: rec,
    albums: albums.map(item('album')),
    playlists: playlists.map(item('playlist')),
  });
})()`

const searchJS = `(async () => {
  const mk = MusicKit.getInstance();
  const sf = mk.storefrontId || 'us';
  const r = await mk.api.music('/v1/catalog/' + sf + '/search',
    { term: %s, types: 'songs,albums,playlists', limit: 12 });
  const res = (r.data && r.data.results) || {};
  const song = (s) => ({
    id: String(s.id), kind: 'song',
    title: s.attributes.name, artist: s.attributes.artistName || '',
    album: s.attributes.albumName || '', durMs: s.attributes.durationInMillis || 0,
  });
  const item = (kind) => (x) => ({
    id: String(x.id), kind, title: x.attributes.name,
    artist: x.attributes.artistName || x.attributes.curatorName || '',
    album: '', durMs: 0,
  });
  return JSON.stringify({
    recent: [],
    songs: ((res.songs || {}).data || []).map(song),
    albums: ((res.albums || {}).data || []).map(item('album')),
    playlists: ((res.playlists || {}).data || []).map(item('playlist')),
  });
})()`
