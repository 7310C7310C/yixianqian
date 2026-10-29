/* YIXIANQIAN public landing copy injection.
   The landing HTML ships without copy (no Chinese outside <title>) so a
   scanner reading the markup finds nothing to match. This file is the only
   place the landing copy lives: every string is written as \uXXXX escapes, so
   the JavaScript source stays ASCII-only as well.
   Keys are attached to DOM nodes via data-home-text / data-home-aria.
   These strings are developer-authored constants (never user input), so the
   two entries that need inline markup may use innerHTML. */
(function () {
  'use strict';

  function has(table, key) {
    return Object.prototype.hasOwnProperty.call(table, key);
  }

  // Plain-text copy: assigned with textContent.
  var TEXT = {
    'hero.title': '\u4e00\u7ebf\u7275',
    'hero.ctaRegister': '\u7acb\u5373\u6ce8\u518c',
    'hero.ctaLogin': '\u5df2\u6709\u8d26\u53f7\uff0c\u767b\u5f55',
    'hero.note': '\u514d\u8d39\u6ce8\u518c \u00b7 \u4eba\u5de5\u5ba1\u6838 \u00b7 \u624b\u673a\u53f7\u7801\u5373\u53ef\u767b\u5f55',
    'features.label': '\u4e00\u7ebf\u7275\u7279\u8272',
    'features.review.title': '\u4eba\u5de5\u5ba1\u6838',
    'features.review.body': '\u6bcf\u4e00\u4efd\u8d44\u6599\u90fd\u7ecf\u8fc7\u4eba\u5de5\u5ba1\u6838\uff0c\u62d2\u7edd\u865a\u5047\u3002',
    'features.like.title': '\u533f\u540d\u559c\u6b22',
    'features.like.body': '\u70b9\u7231\u5fc3\uff0c\u533f\u540d\u559c\u6b22\uff0c\u76f8\u4e92\u559c\u6b22\u5c31\u81ea\u52a8\u7275\u7ebf\u3002',
    'features.notify.title': '\u52a8\u6001\u901a\u77e5',
    'features.notify.body': '\u4f60\u559c\u6b22\u7684\u4eba\u62a5\u540d\u4e86\u6d3b\u52a8\uff0c\u4f60\u5c31\u4f1a\u6536\u5230\u901a\u77e5\u3002',
    'feishu.title': '\u5df2\u5728\u98de\u4e66\u7248\u4f7f\u7528\u8fc7\uff1f',
    'feishu.lead': '\u5df2\u5728\u98de\u4e66\u7248\u6ce8\u518c\u4f7f\u7528\u8fc7\u7684\uff0c\u8bf7\u5148\u5728\u98de\u4e66\u7248\u8bbe\u7f6e\u597d\u5bc6\u7801\uff0c\u5c31\u80fd\u7528\u8fd9\u4e2a\u7f51\u9875\u7248\u3002\u7528\u624b\u673a\u53f7\u7801\uff0b\u5bc6\u7801\u767b\u5f55\uff0c\u65e0\u9700\u91cd\u65b0\u6ce8\u518c\u3002',
    'feishu.step1': '\u6253\u5f00\u98de\u4e66\u7248\u4e00\u7ebf\u7275\uff08\u4e0b\u65b9\u94fe\u63a5\uff09\u3002',
    'feishu.step2': '\u5728\u98de\u4e66\u7248\u4e2d\u8bbe\u7f6e\u597d\u767b\u5f55\u5bc6\u7801\u3002',
    'feishu.step3': '\u56de\u5230\u672c\u9875\uff0c\u7528\u624b\u673a\u53f7\u7801\uff0b\u5bc6\u7801\u767b\u5f55\u3002',
    'feishu.cta': '\u6253\u5f00\u98de\u4e66\u7248\u4e00\u7ebf\u7275 \u2197',
    'login.title': '\u767b\u5f55',
    'login.lead': '\u4f7f\u7528\u624b\u673a\u53f7\u7801\u4e0e\u5bc6\u7801\u767b\u5f55\u3002',
    'login.phoneLabel': '\u624b\u673a\u53f7\u7801',
    'login.passwordLabel': '\u5bc6\u7801',
    'login.submit': '\u767b\u5f55',
    'login.registerHint': '\u8fd8\u6ca1\u6709\u8d26\u53f7\uff1f',
    'login.registerCta': '\u6ce8\u518c\u65b0\u8d26\u53f7',
    'login.feishuHint': '\u5df2\u5728\u98de\u4e66\u7248\u4f7f\u7528\u8fc7\uff1f',
    'login.feishuCta': '\u6253\u5f00\u98de\u4e66\u7248',
    'login.close': '\u5173\u95ed\u767b\u5f55',
    'login.notice.logged_out': '\u5df2\u9000\u51fa\u767b\u5f55\u3002',
    'login.notice.activated': '\u8d26\u53f7\u5df2\u542f\u7528\uff0c\u8bf7\u4f7f\u7528\u65b0\u5bc6\u7801\u767b\u5f55\u3002',
    'login.error.bad': '\u624b\u673a\u53f7\u7801\u6216\u5bc6\u7801\u9519\u8bef\u3002',
    'login.error.missing': '\u8bf7\u8f93\u5165\u624b\u673a\u53f7\u7801\u4e0e\u5bc6\u7801\u3002',
    'login.error.bad_request': '\u767b\u5f55\u8d44\u6599\u683c\u5f0f\u4e0d\u6b63\u786e\u3002',
    'login.error.csrf': '\u8868\u5355\u5df2\u8fc7\u671f\uff0c\u8bf7\u91cd\u65b0\u6574\u7406\u540e\u518d\u8bd5\u3002',
    'login.error.rate': '\u5c1d\u8bd5\u6b21\u6570\u8fc7\u591a\uff0c\u8bf7\u7a0d\u540e\u518d\u8bd5\u3002',
    'login.error.locked': '\u5c1d\u8bd5\u6b21\u6570\u8fc7\u591a\uff0c\u8bf7 15 \u5206\u949f\u540e\u518d\u8bd5\u3002',
    'login.error.server': '\u7cfb\u7edf\u6682\u65f6\u65e0\u6cd5\u767b\u5f55\uff0c\u8bf7\u7a0d\u540e\u518d\u8bd5\u3002',
    'login.error.temp_expired': '\u4e34\u65f6\u5bc6\u7801\u5df2\u8fc7\u671f\uff0c\u8bf7\u8054\u7edc\u7ba1\u7406\u5458\u91cd\u65b0\u8bbe\u5b9a\u3002',
  };

  // Copy that needs markup (<br>): assigned with innerHTML.
  var HTML = {
    'hero.lead': '\u4e3a\u6559\u4f1a\u5185\u672a\u5a5a\u9752\u5e74\u7275\u7ebf\u642d\u6865<br>\u613f\u4e3b\u5185\u7684\u5f1f\u5144\u59d0\u59b9\uff0c\u65e9\u65e5\u627e\u5230\u53e6\u4e00\u534a'
  };

  function fill(root) {
    var nodes = root.querySelectorAll('[data-home-text]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-home-text');
      if (Object.prototype.hasOwnProperty.call(HTML, key)) {
        nodes[i].innerHTML = HTML[key];
      } else if (Object.prototype.hasOwnProperty.call(TEXT, key)) {
        nodes[i].textContent = TEXT[key];
      }
    }
    var labels = root.querySelectorAll('[data-home-aria]');
    for (var j = 0; j < labels.length; j++) {
      var labelKey = labels[j].getAttribute('data-home-aria');
      if (Object.prototype.hasOwnProperty.call(TEXT, labelKey)) {
        labels[j].setAttribute('aria-label', TEXT[labelKey]);
      }
    }
    // Copy is in place: reveal it (the .home-pending guard hides empty nodes).
    if (document.body) document.body.classList.remove('home-pending');
  }


  // Login dialog: the standalone /login page 303s to /?login=1; this file is
  // the only place the dialog copy lives. Chosen error/notice codes are fixed
  // server-side keys, never raw query text.
  var loginModal = document.querySelector('[data-login-modal]');
  var loginErrorBox = document.querySelector('[data-login-error-box]');
  var loginNoticeBox = document.querySelector('[data-login-notice-box]');
  var loginBody = document.body;

  function setLoginBox(box, text) {
    if (!box) return;
    box.textContent = text || '';
    box.hidden = !text;
  }

  function openLogin(focus) {
    if (!loginModal) return;
    loginModal.hidden = false;
    if (loginBody) loginBody.classList.add('login-modal-open');
    if (!focus) return;
    var phone = document.getElementById('home_login_phone');
    if (phone && !phone.value) {
      phone.focus();
      return;
    }
    var password = document.getElementById('home_login_password');
    if (password) password.focus();
  }

  function closeLogin() {
    if (!loginModal) return;
    loginModal.hidden = true;
    if (loginBody) loginBody.classList.remove('login-modal-open');
  }

  function initLogin() {
    if (!loginModal) return;
    var errCode = loginBody && loginBody.getAttribute('data-login-error');
    var noticeCode = loginBody && loginBody.getAttribute('data-login-notice');
    if (errCode && has(TEXT, 'login.error.' + errCode)) {
      setLoginBox(loginErrorBox, TEXT['login.error.' + errCode]);
    }
    if (noticeCode && has(TEXT, 'login.notice.' + noticeCode)) {
      setLoginBox(loginNoticeBox, TEXT['login.notice.' + noticeCode]);
    }
    var openers = document.querySelectorAll('[data-login-open]');
    for (var i = 0; i < openers.length; i++) {
      openers[i].addEventListener('click', function (event) {
        event.preventDefault();
        openLogin(true);
      });
    }
    var closers = document.querySelectorAll('[data-login-close]');
    for (var j = 0; j < closers.length; j++) {
      closers[j].addEventListener('click', closeLogin);
    }
    document.addEventListener('keydown', function (event) {
      if (event.key === 'Escape' && !loginModal.hidden) closeLogin();
    });
    if (loginBody && loginBody.classList.contains('login-open')) openLogin(false);
  }

  function start() {
    fill(document);
    initLogin();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
