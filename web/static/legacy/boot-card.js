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
  // Bootstrap carries 20 cards for fast swiping; the island starts with three
  // server-rendered slides and clones compact cards from this data on demand.
  var bootCards = [];
  try { bootCards = (window.__YXQ_BOOTSTRAP__ && window.__YXQ_BOOTSTRAP__.cards) || []; } catch (e) {}
  var compactProto = slides[1] || null;

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

  function firstThumb(card) {
    if (!card) return '';
    if (card.photos_thumb && card.photos_thumb.length) return card.photos_thumb[0];
    if (card.photos && card.photos.length) return card.photos[0];
    return card.photo || '';
  }

  function fillCompactSlide(slide, card, i) {
    slide.setAttribute('data-index', String(i));
    slide.setAttribute('data-openid', card.openid || '');
    slide.classList.remove('is-active');

    var wrap = slide.querySelector('.card-photo-wrap');
    var img = slide.querySelector('.boot-photo');
    var src = firstThumb(card);
    // 原型可能是无照片卡（Go 不渲染 img）；克隆时补一个 img，避免 4..20 全无照片。
    if (wrap && !img) {
      img = document.createElement('img');
      img.className = 'card-photo-item boot-photo';
      img.alt = '';
      img.decoding = 'async';
      wrap.insertBefore(img, wrap.firstChild);
    }
    if (wrap) wrap.classList.remove('boot-photo-failed');
    if (img && wrap) {
      img.draggable = false;
      img.removeAttribute('src');
      img.removeAttribute('style');
      img.style.display = '';
      var oldPh = wrap.querySelector('.card-photo-placeholder');
      if (oldPh) oldPh.parentNode.removeChild(oldPh);
      if (src) {
        img.setAttribute('data-src', src);
        var faces = card.faces && card.faces[0];
        if (faces) { try { img.setAttribute('data-faces', JSON.stringify(faces)); } catch (e) {} }
        else img.removeAttribute('data-faces');
      } else {
        img.style.display = 'none';
        var ph = document.createElement('div');
        ph.className = 'card-photo-placeholder';
        ph.textContent = card.gender === '男性' ? '👨' : '👩';
        wrap.insertBefore(ph, wrap.firstChild);
      }
    }

    var oldDots = slide.querySelector('.photo-dots');
    if (oldDots) oldDots.parentNode.removeChild(oldDots);
    var photos = card.photos || [];
    if (photos.length > 1 && wrap) {
      var dots = document.createElement('div');
      dots.className = 'photo-dots';
      for (var d = 0; d < photos.length; d++) {
        var dot = document.createElement('div');
        dot.className = 'photo-dot' + (d === 0 ? ' active' : '');
        dots.appendChild(dot);
      }
      var overlay = wrap.querySelector('.card-photo-overlay');
      if (overlay) wrap.insertBefore(dots, overlay); else wrap.appendChild(dots);
    }

    var text = slide.querySelector('.card-overlay-text');
    if (text) {
      var badgeText = '';
      var badgeClass = '';
      if (card.is_self) { badgeText = '这是你 · 他人视角'; badgeClass = 'boot-badge--self'; }
      else if (card.recommended) { badgeText = '推荐'; badgeClass = 'boot-badge--recommend'; }
      var badge = text.querySelector('.boot-badge');
      if (badgeText) {
        if (!badge) {
          var badgeWrap = document.createElement('div');
          badgeWrap.style.marginBottom = '6px';
          badge = document.createElement('span');
          badge.className = 'boot-badge';
          badgeWrap.appendChild(badge);
          text.insertBefore(badgeWrap, text.firstChild);
        }
        badge.className = 'boot-badge ' + badgeClass;
        badge.textContent = badgeText;
      } else if (badge && badge.parentNode) {
        badge.parentNode.parentNode.removeChild(badge.parentNode);
      }

      var name = text.querySelector('.card-name');
      if (name) {
        name.textContent = card.nickname || '';
        var gender = document.createElement('span');
        gender.className = 'gender-icon';
        gender.textContent = card.gender === '男性' ? '♂' : '♀';
        name.appendChild(document.createTextNode(' '));
        name.appendChild(gender);
      }
      var subtitle = text.querySelector('.card-subtitle');
      var simple = card.display_fields && card.display_fields.simple;
      if (simple) {
        if (!subtitle) {
          subtitle = document.createElement('div');
          subtitle.className = 'card-subtitle';
          if (name && name.parentNode) name.parentNode.insertBefore(subtitle, name.nextSibling);
        }
        subtitle.textContent = simple;
      } else if (subtitle && subtitle.parentNode) {
        subtitle.parentNode.removeChild(subtitle);
      }
    }

    var tabs = slide.querySelectorAll('.card-tab');
    var msgTab = tabs.length > 1 ? tabs[1] : null;
    if (msgTab) {
      msgTab.textContent = '留言';
      var msgs = parseInt(card.msg_count, 10) || 0;
      if (msgs > 0) {
        var badgeEl = document.createElement('span');
        badgeEl.textContent = String(msgs);
        badgeEl.style.cssText = 'display:inline-block;min-width:16px;height:16px;line-height:16px;padding:0 4px;margin-left:4px;border-radius:8px;background:var(--yxq-brand-ink);color:#fff;font-size:11px;font-weight:600;vertical-align:1px;';
        msgTab.appendChild(badgeEl);
      }
    }
  }

  function appendSlide(i) {
    if (!compactProto || !bootCards[i]) return false;
    var slide = compactProto.cloneNode(true);
    fillCompactSlide(slide, bootCards[i], i);
    track.appendChild(slide);
    slides.push(slide);
    count = slides.length;
    track.style.setProperty('--boot-cards', String(count));
    return true;
  }

  function ensureSlides(target) {
    while (slides.length <= target && appendSlide(slides.length)) {}
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
    if (next > 0) ensureSlides(next);
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
    if (index + 1 >= slides.length) ensureSlides(index + 1);
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
    setIndex: setIndex,
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
  // starts the normal path. First card gets src now; next cards warm after the
  // first image decodes (no fixed timer, no bandwidth race with card 1).
  function warmAllowed() {
    var conn = navigator.connection || {};
    return !(conn.saveData || /(^|-)2g$/.test(conn.effectiveType || ''));
  }
  function warmSlide(i) {
    if (!warmAllowed()) return;
    var slide = slides[i];
    var img = slide && slide.querySelector('.boot-photo');
    if (img && !img.getAttribute('src') && img.getAttribute('data-src')) {
      try { img.fetchPriority = 'low'; } catch (e) {}
      img.setAttribute('src', img.getAttribute('data-src'));
    }
  }
  function warmAfterFirst() {
    warmSlide(1);
    if (window.requestIdleCallback) window.requestIdleCallback(function () { warmSlide(2); }, { timeout: 1200 });
    else setTimeout(function () { warmSlide(2); }, 600);
  }
  setIndex(0, false);
  var first = activeImage();
  if (first && first.complete && first.naturalWidth) warmAfterFirst();
  else if (first) first.addEventListener('load', warmAfterFirst, { once: true });
  else warmAfterFirst();

  // Slow-network hint: keep the complete card; takeover/failure is app.js.
  setTimeout(function () { if (!state.dismissed) root.classList.add('boot-card-slow'); }, 3000);
})();
