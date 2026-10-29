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
  var COPY_ID_OK = '\u8eab\u4efd\u8bc1\u6821\u9a8c\u901a\u8fc7';
  var COPY_ID_FORMAT = '\u8eab\u4efd\u8bc1\u53f7\u683c\u5f0f\u4e0d\u6b63\u786e';
  var COPY_ID_CHECKSUM = '\u8eab\u4efd\u8bc1\u6821\u9a8c\u4f4d\u4e0d\u6b63\u786e';
  var COPY_ID_BIRTH = '\u8eab\u4efd\u8bc1\u5185\u51fa\u751f\u65e5\u671f\u65e0\u6548';
  var COPY_ID_MISMATCH = '\u4e0e\u586b\u5199\u7684\u751f\u65e5\u4e0d\u4e00\u81f4';
  var COPY_ID_UNDERAGE = '\u672a\u6ee1 18 \u5c81';
  var COPY_ID_AUTOFILL = '\u5df2\u6309\u8eab\u4efd\u8bc1\u586b\u5199\u751f\u65e5';
  var COPY_ID_PROVINCE = '\u8eab\u4efd\u8bc1\u7701\u4efd\u4ee3\u7801\u4e0d\u5728\u5e38\u89c1\u8303\u56f4\u5185';
  var COPY_PHONE_OK = '\u624b\u673a\u53f7\u7801\u683c\u5f0f\u6b63\u786e';
  var COPY_PHONE_BAD = '\u8bf7\u586b\u5199 11 \u4f4d\u4e2d\u56fd\u5927\u9646\u624b\u673a\u53f7\u7801';
  var COPY_WECHAT_OK = '\u5fae\u4fe1\u53f7\u683c\u5f0f\u6b63\u786e';
  var COPY_WECHAT_BAD = '\u5fae\u4fe1\u53f7\u9700\u4e3a 5-64 \u4e2a\u5b57\u6bcd\u3001\u6570\u5b57\u6216 _ - . @ +';
  var COPY_BIRTHDAY_BAD = '\u9700\u5e74\u6ee1 18 \u5c81\u624d\u80fd\u6ce8\u518c';
  var COPY_INVITER_BAD = '\u9080\u8bf7\u4eba ID \u5f62\u5982 U-0001';
  var COPY_HEIGHT_BAD = '\u8eab\u9ad8\u9700\u5728 100-250 \u5398\u7c73\u4e4b\u95f4';

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
              event.preventDefault();
              m.focus();
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
              event.preventDefault();
              if (first) first.focus();
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
        setStatus(input, COPY_ID_CHECKSUM, true);
        return;
      }
      birthday = birthdayOfId(raw);
    } else if (/^[0-9]{15}$/.test(raw)) {
      birthday = birthdayOfId(raw);
    } else {
      setStatus(input, COPY_ID_FORMAT, true);
      return;
    }
    if (!validDate(birthday)) {
      setStatus(input, COPY_ID_BIRTH, true);
      return;
    }
    if (!ageAtLeast18(birthday)) {
      setStatus(input, COPY_ID_UNDERAGE, true);
      return;
    }
    var birthdayInput = document.getElementById('f_birthday');
    if (birthdayInput) {
      if (!birthdayInput.value) {
        birthdayInput.value = birthday;
        setStatus(input, COPY_ID_AUTOFILL, false);
      } else if (birthdayInput.value !== birthday) {
        setStatus(input, COPY_ID_MISMATCH, true);
        return;
      }
    }
    var province = raw.substr(0, 2);
    var known = false;
    if (REGION_CACHE) {
      for (var i = 0; i < REGION_CACHE.length; i++) {
        if (REGION_CACHE[i].code === province) { known = true; break; }
      }
    }
    if (REGION_CACHE && !known) {
      setStatus(input, COPY_ID_PROVINCE, false);
      return;
    }
    setStatus(input, COPY_ID_OK, false);
  }

  function validatePhone(input) {
    var raw = String(input.value || '').trim();
    if (!raw) { setStatus(input, '', false); return; }
    if (/^1[3-9][0-9]{9}$/.test(normalizePhone(raw))) {
      setStatus(input, COPY_PHONE_OK, false);
    } else {
      setStatus(input, COPY_PHONE_BAD, true);
    }
  }

  function validateWechat(input) {
    var raw = String(input.value || '').trim();
    if (!raw) { setStatus(input, '', false); return; }
    if (/^[A-Za-z0-9_.@+-]{5,64}$/.test(raw)) {
      setStatus(input, COPY_WECHAT_OK, false);
    } else {
      setStatus(input, COPY_WECHAT_BAD, true);
    }
  }

  function validateBirthday(input) {
    if (!input.value) { setStatus(input, '', false); return; }
    if (!validDate(input.value)) { setStatus(input, COPY_BIRTHDAY_BAD, true); return; }
    if (!ageAtLeast18(input.value)) {
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

  function start() {
    fillText();
    fillPlaceholders();
    fillOptions();
    fillValues();
    fillError();
    fillAria();
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
