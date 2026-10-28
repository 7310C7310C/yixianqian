/* YIXIANQIAN server-rendered boot mini deck.
   Loaded with defer before app.js: first card is fully rendered, cards 2/3 are
   compact; swipe immediately, Vue takes over at the active card. */
(function () {
  'use strict';
  var root = document.getElementById('boot-card');
  if (!root) return;
  var track = root.querySelector('.boot-deck-track');
  var slides = Array.prototype.slice.call(root.querySelectorAll('.boot-deck-slide'));
  var count = slides.length;
  if (!track || !count) return;
  var total = parseInt(root.getAttribute('data-total'), 10) || 1;
  var index = 0;
  var state = { dismissed: false, removedAt: null, reason: '' };

  // Same formula as app.js computeFaceCrop: static card and real card crop match.
  function computeFaceCrop(f, cw, ch, iw, ih) {
    if (!f || !f.c || !cw || !ch || !iw || !ih) return '';
    try {
      var scale = Math.max(cw / iw, ch / ih);
      var dh = ih * scale;
      var tb = (f.tb && f.tb.length === 2) ? f.tb : [0, 0];
      var ct = tb[0], cb = tb[1], cr = 1 - ct - cb;
      if (cr < 0.55) return '';
      var eh = dh * cr;
      var cy_e = (f.c[1] - ct) / cr;
      var fh = (f.box && f.box[1]) ? f.box[1] : 0;
      var fh_e = fh / cr;
      var off_e;
      if (eh <= ch + 1) {
        off_e = (ch - eh) / 2;
      } else {
        var top_e = (cy_e - fh_e / 2) * eh;
        var bottom_e = (cy_e + fh_e / 2) * eh;
        var headTop_e = top_e - fh_e * 0.35 * eh;
        var offDef_e = (ch - eh) / 2;
        if (offDef_e + headTop_e >= 0 && offDef_e + bottom_e <= ch) off_e = offDef_e;
        else if (offDef_e + headTop_e < 0) off_e = -headTop_e;
        else if (offDef_e + bottom_e > ch) off_e = ch - bottom_e;
        else off_e = offDef_e;
        off_e = Math.max(ch - eh, Math.min(0, off_e));
      }
      var off = off_e - ct * dh;
      off = Math.max(ch - dh, Math.min(0, off));
      var offDef = (ch - dh) / 2;
      if (Math.abs(off - offDef) < 1) return '';
      return '50% ' + Math.round(off) + 'px';
    } catch (e) { return ''; }
  }

  function applyFacesTo(img, wrap, faces) {
    try {
      var f = (faces && faces.length) ? faces[0] : faces;
      if (!f || !img || !wrap) return;
      var tries = 0;
      function run() {
        if (!img.naturalWidth) return;
        if (!wrap.clientWidth || !wrap.clientHeight) {
          if (tries++ < 20) setTimeout(run, 50);
          return;
        }
        var pos = computeFaceCrop(f, wrap.clientWidth, wrap.clientHeight,
          img.naturalWidth, img.naturalHeight);
        if (pos) img.style.objectPosition = pos;
      }
      if (img.complete && img.naturalWidth) run();
      else img.addEventListener('load', run, { once: true });
    } catch (e) {}
  }

  function applySlideFaces(slide) {
    var img = slide && slide.querySelector('.boot-photo');
    if (!img) return;
    var raw = img.getAttribute('data-faces');
    if (raw) {
      try { applyFacesTo(img, img.closest('.card-photo-wrap'), JSON.parse(raw)); } catch (e) {}
    }
  }

  function bindPhotoError(img) {
    if (!img || img.__yxqErrBound) return;
    img.__yxqErrBound = true;
    function failed() {
      try {
        img.style.display = 'none';
        var wrap = img.closest('.card-photo-wrap');
        if (wrap) wrap.classList.add('boot-photo-failed');
      } catch (e) {}
    }
    img.addEventListener('error', failed, { once: true });
    if (img.getAttribute('src') && img.complete && !img.naturalWidth) failed();
  }

  function activeSlide() { return slides[index] || null; }
  function activeImage() {
    var slide = activeSlide();
    return slide && slide.querySelector('.boot-photo');
  }

  function setObjectPosition(pos) {
    var img = activeImage();
    if (img && pos && !state.dismissed) img.style.objectPosition = pos;
  }

  function setIndex(next, animate) {
    next = Math.max(0, Math.min(count - 1, next));
    index = next;
    track.style.transition = animate ? 'transform .22s ease' : 'none';
    track.style.transform = 'translateX(-' + (index * 100 / count) + '%)';
    for (var i = 0; i < count; i++) {
      slides[i].classList.toggle('is-active', i === index);
    }
    var counter = root.querySelector('.card-counter');
    if (counter) counter.textContent = (index + 1) + ' / ' + total;
    var slide = activeSlide();
    if (slide) {
      var img = slide.querySelector('.boot-photo');
      if (img) {
        bindPhotoError(img);
        if (!img.getAttribute('src') && img.getAttribute('data-src')) {
          img.setAttribute('src', img.getAttribute('data-src'));
        }
      }
      applySlideFaces(slide);
    }
    window.__YXQ_BOOT_DECK__ = {
      index: index,
      openid: slide ? (slide.getAttribute('data-openid') || '') : '',
      count: count,
      total: total
    };
    try {
      if (window.__YXQ_BOOT_DECK_ON_CHANGE__) window.__YXQ_BOOT_DECK_ON_CHANGE__();
    } catch (e) {}
  }

  function dismiss(reason) {
    if (state.dismissed) return;
    state.dismissed = true;
    state.reason = reason || '';
    state.removedAt = Math.round(performance.now());
    root.classList.add('boot-card-out');
    setTimeout(function () {
      if (root.parentNode) root.parentNode.removeChild(root);
      window.__YXQ_BOOT_CARD_DETACHED_AT__ = Math.round(performance.now());
      if (window.__YXQ_APP_MOUNTED__ && !window.__YXQ_CARD_INTERACTIVE_AT__) {
        window.__YXQ_CARD_INTERACTIVE_AT__ = Math.round(performance.now());
      }
    }, 180);
  }

  // Swipe: pointer events on the track; horizontal >60px changes card.
  var drag = null;
  function point(e) {
    var t = (e.touches && e.touches[0]) || (e.changedTouches && e.changedTouches[0]) || e;
    return { x: t.clientX, y: t.clientY };
  }
  function down(e) {
    var p = point(e);
    drag = { x: p.x, y: p.y, dx: 0, dy: 0, horiz: false, id: e.pointerId };
    try { if (track.setPointerCapture && e.pointerId != null) track.setPointerCapture(e.pointerId); } catch (_) {}
  }
  function move(e) {
    if (!drag) return;
    var p = point(e);
    drag.dx = p.x - drag.x;
    drag.dy = p.y - drag.y;
    if (Math.abs(drag.dx) > Math.abs(drag.dy) * 1.2 && Math.abs(drag.dx) > 8) drag.horiz = true;
    if (drag.horiz) {
      track.style.transition = 'none';
      track.style.transform = 'translateX(calc(-' + (index * 100 / count) + '% + ' + drag.dx + 'px))';
    }
  }
  function up() {
    if (!drag) return;
    var dx = drag.dx;
    var horiz = drag.horiz;
    drag = null;
    track.style.transition = 'transform .22s ease';
    if (horiz && Math.abs(dx) > 60) {
      setIndex(dx < 0 ? index + 1 : index - 1, true);
    } else {
      setIndex(index, true);
    }
  }
  function cancel() {
    if (!drag) return;
    drag = null;
    setIndex(index, true);
  }
  if (window.PointerEvent) {
    track.addEventListener('pointerdown', down, { passive: true });
    track.addEventListener('pointermove', move, { passive: true });
    track.addEventListener('pointerup', up, { passive: true });
    track.addEventListener('pointercancel', cancel, { passive: true });
  } else {
    // 旧 WebView 无 Pointer Events：退回 touch，保证首卡出现后仍可立刻左滑。
    track.addEventListener('touchstart', down, { passive: true });
    track.addEventListener('touchmove', move, { passive: true });
    track.addEventListener('touchend', up, { passive: true });
    track.addEventListener('touchcancel', cancel, { passive: true });
  }

  window.__YXQ_BOOT_CARD__ = {
    setObjectPosition: setObjectPosition,
    dismiss: dismiss,
    dismissed: function () { return state.dismissed; },
    reason: function () { return state.reason || ''; },
    removedAt: function () { return state.removedAt || null; }
  };

  if (document.documentElement.classList.contains('boot-skip')) {
    state.dismissed = true;
    state.reason = 'deep-link';
    if (root.parentNode) root.parentNode.removeChild(root);
    return;
  }

  // data-src: boot-skip never requests the first card image; prefetch link only
  // starts the normal path. First card gets src now; next card warms at idle.
  setIndex(0, false);
  setTimeout(function () {
    var next = slides[1];
    var img = next && next.querySelector('.boot-photo');
    if (img && !img.getAttribute('src') && img.getAttribute('data-src')) {
      img.setAttribute('src', img.getAttribute('data-src'));
    }
  }, 1200);

  // Slow-network hint: keep the complete card; takeover/failure is app.js.
  setTimeout(function () { if (!state.dismissed) root.classList.add('boot-card-slow'); }, 3000);
})();
