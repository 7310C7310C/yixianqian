/* YIXIANQIAN registration form copy injection.
   The /register response ships no Chinese outside <title>: the server renders
   empty hooks (data-register-text / -placeholder / -option / -option-text /
   -value) plus one ASCII-only JSON catalog in <body data-copy-json>. Every
   non-ASCII rune in that catalog is a \uXXXX escape, so the HTML, this file
   and the stylesheets it loads all stay ASCII-only (scan guard, DECISIONS 42).
   Option values are Chinese; they are written here with element.value so the
   submitted form fields keep the legacy contract. */
(function () {
  'use strict';

  var body = document.body;
  if (!body) return;
  var raw = body.getAttribute('data-copy-json');
  if (!raw) return;

  var catalog;
  try {
    catalog = JSON.parse(raw);
  } catch (err) {
    return;
  }
  if (!catalog) return;

  var text = catalog.text || {};
  var options = catalog.options || {};
  var values = catalog.values || {};

  function has(table, key) {
    return Object.prototype.hasOwnProperty.call(table, key);
  }

  function fillText() {
    var nodes = document.querySelectorAll('[data-register-text]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-register-text');
      if (has(text, key)) nodes[i].textContent = text[key];
    }
  }

  function fillPlaceholders() {
    var nodes = document.querySelectorAll('[data-register-placeholder]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-register-placeholder');
      if (has(text, key)) nodes[i].setAttribute('placeholder', text[key]);
    }
  }

  function fillOptions() {
    var nodes = document.querySelectorAll('[data-register-option]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-register-option');
      if (!has(options, key)) continue;
      nodes[i].value = options[key];
      if (nodes[i].tagName === 'OPTION') nodes[i].textContent = options[key];
    }
    var labels = document.querySelectorAll('[data-register-option-text]');
    for (var j = 0; j < labels.length; j++) {
      var labelKey = labels[j].getAttribute('data-register-option-text');
      if (has(options, labelKey)) labels[j].textContent = options[labelKey];
    }
  }

  function fillValues() {
    var nodes = document.querySelectorAll('[data-register-value]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-register-value');
      if (has(values, key)) nodes[i].value = values[key];
    }
  }

  function fillError() {
    if (!has(text, 'page.error')) return;
    var nodes = document.querySelectorAll('[data-register-error]');
    for (var i = 0; i < nodes.length; i++) {
      nodes[i].textContent = text['page.error'];
    }
  }


  // ---------------------------------------------------------------------
  // Region cascade + realtime field validation
  // ---------------------------------------------------------------------
  var COPY_SELECT_BLANK = '\u8bf7\u9009\u62e9';
  var COPY_REGION_LOADING = '\u6b63\u5728\u52a0\u8f7d\u5730\u533a\u2026';
  var COPY_REGION_FAILED = '\u5730\u533a\u6570\u636e\u52a0\u8f7d\u5931\u8d25\uff0c\u8bf7\u6539\u7528\u624b\u52a8\u586b\u5199\u3002';
  var COPY_REGION_REQUIRED = '\u8bf7\u9009\u62e9\u5b8c\u6574\u7684\u5730\u533a\u3002';
  var COPY_REGION_MANUALREQUIRED = '\u8bf7\u586b\u5199\u5730\u533a\u3002';
  var COPY_ID_BAD = '\u8eab\u4efd\u8bc1\u53f7\u7801\u4e0d\u6b63\u786e';
  var COPY_PHONE_BAD = '\u624b\u673a\u53f7\u7801\u4e0d\u6b63\u786e';
  var COPY_WECHAT_BAD = '\u5fae\u4fe1\u53f7\u4e0d\u6b63\u786e';
  var COPY_BIRTHDAY_BAD = '\u751f\u65e5\u4e0d\u6b63\u786e\uff08\u9700\u5e74\u6ee1 18 \u5c81\uff09';
  var COPY_INVITER_BAD = '\u9080\u8bf7\u4eba ID \u4e0d\u6b63\u786e';
  var COPY_HEIGHT_BAD = '\u8eab\u9ad8\u4e0d\u6b63\u786e';
  var COPY_ANGEL_PRESSED = '\u5df2\u68c0\u6d4b\u5230\u957f\u6309\uff1b\u8bf7\u786e\u8ba4\u5df2\u4fdd\u5b58/\u6dfb\u52a0\u5c0f\u5929\u4f7f\u3002';
  var COPY_ANGEL_PRESS_HINT = '\u8bf7\u5148\u957f\u6309\u4e8c\u7ef4\u7801\u4fdd\u5b58/\u6dfb\u52a0\u5c0f\u5929\u4f7f\uff0c\u518d\u52fe\u9009\u786e\u8ba4\u3002';
  var COPY_ANGEL_PRESS_DONE = '\u5df2\u6539\u4e3a\u624b\u52a8\u786e\u8ba4\uff0c\u8bf7\u52fe\u9009\u5e76\u7ee7\u7eed\u3002';

  var COPY_PHOTO_COUNT = '\u5df2\u9009 %d / 9 \u5f20';
  var COPY_PHOTO_COVER = '\u5c01\u9762';
  var COPY_PHOTO_SET_COVER = '\u8bbe\u4e3a\u5c01\u9762';
  var COPY_PHOTO_DELETE = '\u5220\u9664';
  var COPY_PHOTO_MIN = '\u81f3\u5c11\u9009\u62e9 3 \u5f20\u7167\u7247\u3002';
  var COPY_PHOTO_MAX = '\u6700\u591a\u9009\u62e9 9 \u5f20\u7167\u7247\u3002';
  var COPY_PHOTO_SYNC_FAIL = '\u5f53\u524d\u6d4f\u89c8\u5668\u65e0\u6cd5\u8c03\u6574\u7167\u7247\u987a\u5e8f\uff0c\u8bf7\u5237\u65b0\u540e\u91cd\u65b0\u9009\u62e9\u3002';
  var COPY_PHOTO_BAD_TYPE = '\u8bf7\u9009\u62e9\u56fe\u7247\u6587\u4ef6\u3002';
  var COPY_PHOTO_TOO_LARGE = '\u6709\u7167\u7247\u8d85\u8fc7\u5927\u5c0f\u4e0a\u9650\uff0c\u8bf7\u538b\u7f29\u540e\u518d\u9009\u3002';
  var REGION_CACHE = null;

  function statusNode(input) {
    var wrap = input.closest('.register-field');
    if (!wrap) return null;
    var key = input.getAttribute('name') || '';
    var node = wrap.querySelector('[data-field-status="' + key + '"]');
    if (!node) {
      node = document.createElement('p');
      node.className = 'hint register-field-status';
      node.setAttribute('data-field-status', key);
      node.hidden = true;
      input.parentNode.insertBefore(node, input.nextSibling);
    }
    return node;
  }

  function setStatus(input, text, bad) {
    var node = statusNode(input);
    if (!node) return;
    node.textContent = text || '';
    node.hidden = !text;
    if (bad) node.classList.add('is-bad'); else node.classList.remove('is-bad');
    if (text && bad) input.setCustomValidity(text); else input.setCustomValidity('');
  }

  function setRegionStatus(wrap, text, bad) {
    var node = wrap.querySelector('[data-region-status]');
    if (!node) return;
    node.textContent = text || '';
    node.hidden = !text;
    if (bad) node.classList.add('is-bad'); else node.classList.remove('is-bad');
  }

  function fillSelect(select, items, blank, selected) {
    if (!select) return;
    select.innerHTML = '';
    var opt = document.createElement('option');
    opt.value = '';
    opt.textContent = blank;
    select.appendChild(opt);
    for (var i = 0; i < items.length; i++) {
      var o = document.createElement('option');
      o.value = items[i].code;
      o.textContent = items[i].name;
      select.appendChild(o);
    }
    select.value = selected || '';
  }

  function provinceByCode(code) {
    if (!REGION_CACHE) return null;
    for (var i = 0; i < REGION_CACHE.length; i++) {
      if (REGION_CACHE[i].code === code) return REGION_CACHE[i];
    }
    return null;
  }

  function cityByCode(province, code) {
    if (!province) return null;
    for (var i = 0; i < province.cities.length; i++) {
      if (province.cities[i].code === code) return province.cities[i];
    }
    return null;
  }

  function initRegionField(field) {
    var name = field.getAttribute('data-region-field');
    var level = field.getAttribute('data-region-level');
    var wrap = field.closest('.register-field');
    if (!wrap) return;
    var provSel = field.querySelector('[data-region-province]');
    var citySel = field.querySelector('[data-region-city]');
    var areaSel = field.querySelector('[data-region-area]');
    var provCode = wrap.querySelector('[name="' + name + '_province"]');
    var cityCode = wrap.querySelector('[name="' + name + '_city"]');
    var areaCode = wrap.querySelector('[name="' + name + '_area"]');
    var manual = wrap.querySelector('[data-region-manual]');
    var toggle = wrap.querySelector('[data-region-manual-toggle]');
    if (!provSel || !citySel || !manual || !toggle) return;

    function clearCodes() {
      if (provCode) provCode.value = '';
      if (cityCode) cityCode.value = '';
      if (areaCode) areaCode.value = '';
    }

    function clearValidity() {
      if (provSel) provSel.setCustomValidity('');
      if (citySel) citySel.setCustomValidity('');
      if (areaSel) areaSel.setCustomValidity('');
      if (manual) manual.setCustomValidity('');
      setRegionStatus(wrap, '', false);
    }

    function applyManual(on) {
      field.hidden = !!on;
      manual.hidden = !on;
      manual.required = !!on;
      if (on) {
        clearCodes();
        if (!manual.value) manual.focus();
      } else {
        manual.value = '';
        syncCodes();
      }
      clearValidity();
    }

    function syncCodes() {
      if (provCode) provCode.value = provSel.value || '';
      if (cityCode) cityCode.value = citySel.value || '';
      if (areaCode) areaCode.value = areaSel ? (areaSel.value || '') : '';
    }

    function fillCities() {
      var province = provinceByCode(provSel.value);
      fillSelect(citySel, province ? province.cities : [], COPY_SELECT_BLANK, '');
      if (areaSel) fillSelect(areaSel, [], COPY_SELECT_BLANK, '');
      syncCodes();
    }

    function fillAreas() {
      if (!areaSel) { syncCodes(); return; }
      var province = provinceByCode(provSel.value);
      var city = cityByCode(province, citySel.value);
      var areas = city && city.areas ? city.areas : [];
      fillSelect(areaSel, areas, COPY_SELECT_BLANK, '');
      syncCodes();
    }

    fillSelect(provSel, REGION_CACHE || [], COPY_SELECT_BLANK, '');
    fillSelect(citySel, [], COPY_SELECT_BLANK, '');
    if (areaSel) fillSelect(areaSel, [], COPY_SELECT_BLANK, '');

    // Restore a rejected POST: codes are ASCII and safe to echo in the HTML.
    if (provCode && provCode.value) {
      provSel.value = provCode.value;
      fillCities();
      if (cityCode && cityCode.value) {
        citySel.value = cityCode.value;
        fillAreas();
        if (areaSel && areaCode && areaCode.value) areaSel.value = areaCode.value;
      }
      syncCodes();
    }

    provSel.addEventListener('change', function () {
      fillCities();
      clearValidity();
    });
    citySel.addEventListener('change', function () {
      fillAreas();
      clearValidity();
    });
    if (areaSel) areaSel.addEventListener('change', function () {
      syncCodes();
      clearValidity();
    });
    manual.addEventListener('input', clearValidity);

    toggle.addEventListener('change', function () {
      applyManual(toggle.checked);
    });

    // A manual value survives a rejected POST via data-register-value.
    if (manual.value && (!provCode || !provCode.value)) {
      toggle.checked = true;
      applyManual(true);
    }

    // Last line of defence before submit; the server validates again.
    var form = field.closest('form');
    if (form && !form.getAttribute('data-region-bound')) {
      form.setAttribute('data-region-bound', '1');
      form.addEventListener('submit', function (event) {
        var fields = form.querySelectorAll('[data-region-field]');
        for (var i = 0; i < fields.length; i++) {
          var f = fields[i];
          var w = f.closest('.register-field');
          if (!w) continue;
          var m = w.querySelector('[data-region-manual]');
          var t = w.querySelector('[data-region-manual-toggle]');
          // Clear a previous custom error before re-validating; otherwise a
          // fixed field would keep blocking the next submit.
          var ps = f.querySelector('[data-region-province]');
          var cs = f.querySelector('[data-region-city]');
          var as = f.querySelector('[data-region-area]');
          if (ps) ps.setCustomValidity('');
          if (cs) cs.setCustomValidity('');
          if (as) as.setCustomValidity('');
          if (m) m.setCustomValidity('');
          if (t && t.checked) {
            if (!m.value.trim()) {
              m.setCustomValidity(COPY_REGION_MANUALREQUIRED);
              setRegionStatus(w, COPY_REGION_MANUALREQUIRED, true);
              var manualStatus = w.querySelector('[data-region-status]');
              if (manualStatus) manualStatus.scrollIntoView({ block: 'center' });
              event.preventDefault();
              m.focus();
              if (manualStatus) manualStatus.focus({ preventScroll: true });
              return;
            }
          } else {
            var p = w.querySelector('[data-region-code="province"]');
            var c = w.querySelector('[data-region-code="city"]');
            var a = w.querySelector('[data-region-code="area"]');
            var areaSel = f.querySelector('[data-region-area]');
            var needArea = f.getAttribute('data-region-level') === 'cd' && areaSel && areaSel.options.length > 1;
            if (!p.value || !c.value || (needArea && (!a || !a.value))) {
              var first = f.querySelector('[data-region-province]');
              if (first) first.setCustomValidity(COPY_REGION_REQUIRED);
              setRegionStatus(w, COPY_REGION_REQUIRED, true);
              var regionStatus = w.querySelector('[data-region-status]');
              if (regionStatus) {
                regionStatus.scrollIntoView({ block: 'center' });
                regionStatus.focus();
              }
              event.preventDefault();
              if (first) first.focus();
              if (regionStatus) regionStatus.focus({ preventScroll: true });
              return;
            }
          }
        }
      }, true);
    }
  }

  function loadRegions() {
    var fields = document.querySelectorAll('[data-region-field]');
    if (!fields.length) return;
    for (var i = 0; i < fields.length; i++) {
      var wrap = fields[i].closest('.register-field');
      if (wrap) setRegionStatus(wrap, COPY_REGION_LOADING, false);
    }
    fetch('/register/regions', { credentials: 'same-origin' })
      .then(function (resp) {
        if (!resp.ok) throw new Error('regions');
        return resp.json();
      })
      .then(function (data) {
        REGION_CACHE = (data && data.provinces) || [];
        for (var j = 0; j < fields.length; j++) initRegionField(fields[j]);
      })
      .catch(function () {
        REGION_CACHE = [];
        for (var k = 0; k < fields.length; k++) {
          initRegionField(fields[k]);
          var wrap = fields[k].closest('.register-field');
          if (!wrap) continue;
          setRegionStatus(wrap, COPY_REGION_FAILED, true);
          var toggle = wrap.querySelector('[data-region-manual-toggle]');
          if (toggle && !toggle.checked) {
            toggle.checked = true;
            toggle.dispatchEvent(new Event('change'));
          }
        }
      });
  }

  var ID_WEIGHTS = [7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2];
  var ID_CODES = '10X98765432';

  function idChecksum(id17) {
    var sum = 0;
    for (var i = 0; i < 17; i++) sum += parseInt(id17.charAt(i), 10) * ID_WEIGHTS[i];
    return ID_CODES.charAt(sum % 11);
  }

  function birthdayOfId(value) {
    if (/^[0-9]{17}[0-9X]$/.test(value)) return value.substr(6, 4) + '-' + value.substr(10, 2) + '-' + value.substr(12, 2);
    if (/^[0-9]{15}$/.test(value)) return '19' + value.substr(6, 2) + '-' + value.substr(8, 2) + '-' + value.substr(10, 2);
    return '';
  }

  function validDate(value) {
    if (!/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/.test(value)) return false;
    var parts = value.split('-');
    var d = new Date(Number(parts[0]), Number(parts[1]) - 1, Number(parts[2]));
    return d.getFullYear() === Number(parts[0]) && d.getMonth() === Number(parts[1]) - 1 && d.getDate() === Number(parts[2]);
  }

  function ageAtLeast18(value) {
    if (!validDate(value)) return false;
    var parts = value.split('-');
    var birth = new Date(Number(parts[0]), Number(parts[1]) - 1, Number(parts[2]));
    var cutoff = new Date();
    cutoff.setFullYear(cutoff.getFullYear() - 18);
    cutoff.setHours(0, 0, 0, 0);
    return birth.getTime() <= cutoff.getTime();
  }

  function normalizePhone(value) {
    var v = String(value || '').replace(/[\s\-()]/g, '');
    if (v.indexOf('+86') === 0) v = v.slice(3);
    if (v.indexOf('0086') === 0) v = v.slice(4);
    return v;
  }

  function validateIDCard(input) {
    var raw = String(input.value || '').replace(/\s+/g, '').toUpperCase();
    if (raw !== input.value) input.value = raw;
    if (!raw) { setStatus(input, '', false); return; }
    var birthday = '';
    if (/^[0-9]{17}[0-9X]$/.test(raw)) {
      if (idChecksum(raw.substr(0, 17)) !== raw.charAt(17)) {
        setStatus(input, COPY_ID_BAD, true);
        return;
      }
      birthday = birthdayOfId(raw);
    } else if (/^[0-9]{15}$/.test(raw)) {
      birthday = birthdayOfId(raw);
    } else {
      setStatus(input, COPY_ID_BAD, true);
      return;
    }
    if (!validDate(birthday) || !ageAtLeast18(birthday)) {
      setStatus(input, COPY_ID_BAD, true);
      return;
    }
    var birthdayInput = document.getElementById('f_birthday');
    if (birthdayInput) {
      if (!birthdayInput.value) {
        birthdayInput.value = birthday;
      } else if (birthdayInput.value !== birthday) {
        setStatus(input, COPY_ID_BAD, true);
        return;
      }
    }
    setStatus(input, '', false);
  }

  function validatePhone(input) {
    var raw = String(input.value || '').trim();
    if (!raw) { setStatus(input, '', false); return; }
    if (/^1[3-9][0-9]{9}$/.test(normalizePhone(raw))) {
      setStatus(input, '', false);
    } else {
      setStatus(input, COPY_PHONE_BAD, true);
    }
  }

  function validateWechat(input) {
    var raw = String(input.value || '').trim();
    if (!raw) { setStatus(input, '', false); return; }
    if (/^[A-Za-z0-9_.@+-]{5,64}$/.test(raw)) {
      setStatus(input, '', false);
    } else {
      setStatus(input, COPY_WECHAT_BAD, true);
    }
  }

  function validateBirthday(input) {
    if (!input.value) { setStatus(input, '', false); return; }
    if (!validDate(input.value) || !ageAtLeast18(input.value)) {
      setStatus(input, COPY_BIRTHDAY_BAD, true);
      return;
    }
    setStatus(input, '', false);
    var id = document.getElementById('f_id_card');
    if (id && id.value.trim()) validateIDCard(id);
  }

  function validateHeight(input) {
    if (!input.value) { setStatus(input, '', false); return; }
    var n = Number(input.value);
    if (!isFinite(n) || n < 100 || n > 250) {
      setStatus(input, COPY_HEIGHT_BAD, true);
    } else {
      setStatus(input, '', false);
    }
  }

  function validateInviter(input) {
    var raw = String(input.value || '').trim().toUpperCase();
    if (raw !== input.value) input.value = raw;
    if (!raw) { setStatus(input, '', false); return; }
    if (!/^U-[0-9]{4}$/.test(raw)) {
      setStatus(input, COPY_INVITER_BAD, false);
    } else {
      setStatus(input, '', false);
    }
  }

  function bindValidation() {
    var id = document.getElementById('f_id_card');
    var phone = document.getElementById('f_phone');
    var wechat = document.getElementById('f_wechat');
    var birthday = document.getElementById('f_birthday');
    var height = document.getElementById('f_height_cm');
    var inviter = document.getElementById('f_inviter');
    if (id) {
      id.addEventListener('input', function () { validateIDCard(id); });
      id.addEventListener('blur', function () { validateIDCard(id); });
      if (id.value) validateIDCard(id);
    }
    if (phone) {
      phone.addEventListener('input', function () { validatePhone(phone); });
      phone.addEventListener('blur', function () { validatePhone(phone); });
      if (phone.value) validatePhone(phone);
    }
    if (wechat) {
      wechat.addEventListener('input', function () { validateWechat(wechat); });
      wechat.addEventListener('blur', function () { validateWechat(wechat); });
      if (wechat.value) validateWechat(wechat);
    }
    if (birthday) {
      birthday.addEventListener('change', function () { validateBirthday(birthday); });
      birthday.addEventListener('blur', function () { validateBirthday(birthday); });
    }
    if (height) {
      height.addEventListener('input', function () { validateHeight(height); });
      height.addEventListener('blur', function () { validateHeight(height); });
    }
    if (inviter) {
      inviter.addEventListener('blur', function () { validateInviter(inviter); });
    }
  }

  function fillAria() {
    var nodes = document.querySelectorAll('[data-register-aria]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-register-aria');
      if (has(text, key)) nodes[i].setAttribute('aria-label', text[key]);
    }
  }



  function initPhotoPicker() {
    var input = document.getElementById('f_photos');
    var grid = document.querySelector('[data-photo-grid]');
    var status = document.querySelector('[data-photo-status]');
    var count = document.querySelector('[data-photo-count]');
    var picker = document.querySelector('.register-photo-picker');
    if (!input || !grid) return;
    var files = [];
    var urls = [];
    var canSync = true;
    try { new DataTransfer(); } catch (e) { canSync = false; }
    if (!canSync) {
      input.hidden = false;
      if (picker) picker.hidden = true;
      grid.hidden = true;
      return;
    }

    function setStatus(text, bad) {
      if (!status) return;
      status.textContent = text || '';
      status.hidden = !text;
      if (bad) status.classList.add('is-bad'); else status.classList.remove('is-bad');
    }

    function revoke() {
      for (var i = 0; i < urls.length; i++) URL.revokeObjectURL(urls[i]);
      urls = [];
    }

    function sync() {
      try {
        var dt = new DataTransfer();
        for (var i = 0; i < files.length; i++) dt.items.add(files[i]);
        input.files = dt.files;
        return true;
      } catch (e) {
        return false;
      }
    }

    function render() {
      revoke();
      grid.innerHTML = '';
      for (var i = 0; i < files.length; i++) {
        var card = document.createElement('div');
        card.className = 'register-photo-card';
        var img = document.createElement('img');
        var url = URL.createObjectURL(files[i]);
        urls.push(url);
        img.src = url;
        img.alt = '';
        card.appendChild(img);
        if (i === 0) {
          var badge = document.createElement('span');
          badge.className = 'register-photo-cover';
          badge.textContent = COPY_PHOTO_COVER;
          card.appendChild(badge);
        }
        var actions = document.createElement('div');
        actions.className = 'register-photo-actions';
        if (i > 0) {
          var cover = document.createElement('button');
          cover.type = 'button';
          cover.textContent = COPY_PHOTO_SET_COVER;
          cover.addEventListener('click', (function (index) {
            return function () {
              var item = files.splice(index, 1)[0];
              files.unshift(item);
              sync();
              render();
            };
          })(i));
          actions.appendChild(cover);
        }
        var del = document.createElement('button');
        del.type = 'button';
        del.textContent = COPY_PHOTO_DELETE;
        del.addEventListener('click', (function (index) {
          return function () {
            files.splice(index, 1);
            sync();
            render();
          };
        })(i));
        actions.appendChild(del);
        card.appendChild(actions);
        grid.appendChild(card);
      }
      grid.hidden = files.length === 0;
      if (count) {
        count.textContent = files.length ? COPY_PHOTO_COUNT.replace('%d', String(files.length)) : '';
      }
      if (files.length && files.length < 3) setStatus(COPY_PHOTO_MIN, true);
      else setStatus('', false);
    }

    input.addEventListener('change', function () {
      var picked = Array.prototype.slice.call(input.files || []);
      var maxMB = parseInt(input.getAttribute('data-max-mb'), 10) || 10;
      var errorText = '';
      var overflow = files.length + picked.length > 9;
      for (var i = 0; i < picked.length; i++) {
        var file = picked[i];
        if (!file.type || file.type.indexOf('image/') !== 0) {
          errorText = COPY_PHOTO_BAD_TYPE;
          continue;
        }
        if (file.size > maxMB * 1024 * 1024) {
          errorText = COPY_PHOTO_TOO_LARGE;
          continue;
        }
        if (files.length >= 9) break;
        var duplicate = files.some(function (f) {
          return f.name === file.name && f.size === file.size && f.lastModified === file.lastModified;
        });
        if (!duplicate) files.push(file);
      }
      if (!sync()) errorText = COPY_PHOTO_SYNC_FAIL;
      if (overflow && !errorText) errorText = COPY_PHOTO_MAX;
      render();
      if (errorText) setStatus(errorText, true);
    });

    var form = input.closest('form');
    if (form) {
      form.addEventListener('submit', function (event) {
        if (files.length < 3 || files.length > 9) {
          event.preventDefault();
          setStatus(files.length < 3 ? COPY_PHOTO_MIN : COPY_PHOTO_MAX, true);
          if (status) {
            status.scrollIntoView({ block: 'center' });
            status.focus();
          }
        }
      }, true);
    }
    render();
  }

  function initAngelQR() {
    var img = document.querySelector('[data-angel-qr-image]');
    var ack = document.querySelector('input[name="angel_ack"]');
    var status = document.querySelector('[data-angel-press-status]');
    var fallback = document.querySelector('[data-angel-fallback]');
    if (!img || !ack) return;
    var timer = null;

    function setStatus(text) {
      if (!status) return;
      status.textContent = text || '';
      status.hidden = !text;
    }

    function unlock(manual) {
      ack.disabled = false;
      if (!manual) ack.checked = true;
      if (fallback) fallback.hidden = true;
      setStatus(manual ? COPY_ANGEL_PRESS_DONE : COPY_ANGEL_PRESSED);
    }

    function clearTimer() {
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
    }

    // Browsers do not expose a "saved to album" event. We can only detect a
    // long press/right-click; it unlocks and checks the explicit confirmation
    // box, which remains the enforceable submit gate. The fallback button
    // keeps keyboard/desktop users unblocked.
    // Always require a fresh long-press/right-click (or the explicit
    // fallback) on a QR page, even when a rejected POST re-rendered the box as
    // checked. The server cannot distinguish a forged checked field anyway;
    // this is a UX gate, not an authorization control.
    ack.disabled = true;
    if (fallback) fallback.hidden = false;
    setStatus(COPY_ANGEL_PRESS_HINT);
    if (fallback) {
      fallback.addEventListener('click', function () {
        unlock(true);
        ack.focus();
      });
    }
    var option = img.closest('.register-angel-option');
    if (option) {
      option.addEventListener('click', function () {
        if (!ack.disabled) return;
        setStatus(COPY_ANGEL_PRESS_HINT);
        if (status) {
          status.classList.remove('is-warn');
          void status.offsetWidth;
          status.classList.add('is-warn');
          setTimeout(function () { status.classList.remove('is-warn'); }, 1200);
        }
      });
    }
    img.addEventListener('contextmenu', function () {
      unlock(false);
    });
    img.addEventListener('touchstart', function () {
      clearTimer();
      timer = setTimeout(function () { unlock(false); }, 700);
    }, { passive: true });
    img.addEventListener('touchmove', clearTimer, { passive: true });
    img.addEventListener('touchend', clearTimer, { passive: true });
    img.addEventListener('touchcancel', clearTimer, { passive: true });

    var form = img.closest('form');
    if (form) {
      form.addEventListener('submit', function (event) {
        if (ack.disabled) {
          event.preventDefault();
          setStatus(COPY_ANGEL_PRESS_HINT);
          if (status) {
            status.scrollIntoView({ block: 'center' });
            status.focus();
          }
          if (fallback) fallback.focus();
          if (status) status.focus({ preventScroll: true });
        }
      }, true);
    }
  }

  function initConditionalFields() {
    var nodes = document.querySelectorAll('[data-show-when-field]');
    for (var i = 0; i < nodes.length; i++) {
      var node = nodes[i];
      var targetName = node.getAttribute('data-show-when-field');
      var target = document.querySelector('select[name="' + targetName + '"]');
      if (!target) continue;
      var input = node.querySelector('input[name="' + node.id.replace(/^field_/, '') + '"]');
      var optionIndex = parseInt(node.getAttribute('data-show-when-option'), 10);
      if (!isFinite(optionIndex)) continue;
      (function (node, target, input, optionIndex) {
        function sync() {
          var option = target.options[optionIndex + 1];
          var show = !!option && target.value === option.value;
          node.hidden = !show;
          if (!show && input) input.value = '';
        }
        target.addEventListener('change', sync);
        sync();
      })(node, target, input, optionIndex);
    }
  }

  function start() {
    fillText();
    fillPlaceholders();
    fillOptions();
    fillValues();
    fillError();
    fillAria();
    initConditionalFields();
    initPhotoPicker();
    initAngelQR();
    bindValidation();
    loadRegions();
    body.classList.remove('copy-pending');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
