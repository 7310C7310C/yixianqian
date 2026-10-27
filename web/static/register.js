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

  function start() {
    fillText();
    fillPlaceholders();
    fillOptions();
    fillValues();
    fillError();
    body.classList.remove('copy-pending');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
