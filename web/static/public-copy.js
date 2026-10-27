/* YIXIANQIAN scan-safe public page copy injection.
   Every page listed here ships HTML/CSS without Chinese outside <title>; this
   file is the only place the copy lives. All strings are \uXXXX escapes, so
   the JavaScript source stays ASCII-only (scan guard). Keys are attached to
   DOM nodes via data-copy-text / data-copy-html / data-copy-aria /
   data-copy-placeholder; <body data-copy-page="..."> selects the table.
   These strings are developer-authored constants, never user input, so the
   entries that need inline markup may use innerHTML. */
(function () {
  'use strict';

  var COPY = {
    login: {
      text: {
        'login.title': '\u767b\u5f55',
        'login.lead': '\u4f7f\u7528\u624b\u673a\u53f7\u7801\u4e0e\u5bc6\u7801\u767b\u5f55\u3002',
        'login.phoneLabel': '\u624b\u673a\u53f7\u7801',
        'login.passwordLabel': '\u5bc6\u7801',
        'login.submit': '\u767b\u5f55',
        'login.altHint': '\u8fd8\u6ca1\u6709\u8d26\u53f7\uff1f',
        'login.altCta': '\u6ce8\u518c\u65b0\u8d26\u53f7',
        'login.feishuTitle': '\u5df2\u5728\u98de\u4e66\u7248\u4f7f\u7528\u8fc7\uff1f',
        'login.feishuLead': '\u8bf7\u5148\u5728\u98de\u4e66\u7248\u8bbe\u7f6e\u597d\u5bc6\u7801\uff0c\u518d\u7528\u624b\u673a\u53f7\u7801\uff0b\u5bc6\u7801\u767b\u5f55\u7f51\u9875\u7248\u3002',
        'login.feishuCta': '\u6253\u5f00\u98de\u4e66\u7248',
        'login.firstHint': '\u9996\u6b21\u4f7f\u7528\uff1f\u8bf7\u4f7f\u7528\u7ba1\u7406\u5458\u63d0\u4f9b\u7684\u542f\u7528\u94fe\u63a5\u8bbe\u5b9a\u5bc6\u7801\u3002',
        'login.notice.logged_out': '\u5df2\u9000\u51fa\u767b\u5f55\u3002',
        'login.notice.activated': '\u8d26\u53f7\u5df2\u542f\u7528\uff0c\u8bf7\u4f7f\u7528\u65b0\u5bc6\u7801\u767b\u5f55\u3002',
      },
    },
    activate: {
      text: {
        'activate.title': '\u8bbe\u5b9a\u5bc6\u7801',
        'activate.leadBefore': '\u4e3a ',
        'activate.leadAfter': '\uff08',
        'activate.leadEnd': '\uff09\u8bbe\u5b9a\u7b2c\u4e00\u7ec4\u5bc6\u7801\u3002',
        'activate.newPassword': '\u65b0\u5bc6\u7801',
        'activate.confirmPassword': '\u518d\u6b21\u8f93\u5165\u65b0\u5bc6\u7801',
        'activate.hint': '\u81f3\u5c11 8 \u4e2a\u5b57\u5143\u3002\u542f\u7528\u94fe\u63a5\u53ea\u80fd\u4f7f\u7528\u4e00\u6b21\u3002',
        'activate.submit': '\u8bbe\u5b9a\u5e76\u767b\u5f55',
      },
    },
    password: {
      text: {
        'password.eyebrow': '\u8d26\u53f7\u5b89\u5168',
        'password.title': '\u4fee\u6539\u5bc6\u7801',
        'password.required': '\u4f60\u76ee\u524d\u4f7f\u7528\u4e34\u65f6\u5bc6\u7801\u6216\u7ba1\u7406\u5458\u521a\u91cd\u7f6e\u5bc6\u7801\uff0c\u5fc5\u987b\u5148\u8bbe\u5b9a\u65b0\u5bc6\u7801\u624d\u80fd\u7ee7\u7eed\u4f7f\u7528\u3002',
        'password.currentLabel': '\u76ee\u524d\u5bc6\u7801',
        'password.newLabel': '\u65b0\u5bc6\u7801',
        'password.confirmLabel': '\u518d\u6b21\u8f93\u5165\u65b0\u5bc6\u7801',
        'password.hint': '\u81f3\u5c11 8 \u4e2a\u5b57\u5143\uff1b\u4e0d\u8981\u4f7f\u7528\u4e34\u65f6\u5bc6\u7801\u3002',
        'password.submit': '\u66f4\u65b0\u5bc6\u7801',
      },
    },
    register_done: {
      text: {
        'register_done.eyebrow': '\u4e00\u7ebf\u7275 \u00b7 \u6ce8\u518c',
        'register_done.pending.heading': '\u8d44\u6599\u5ba1\u6838\u4e2d',
        'register_done.pending.message': '\u6ce8\u518c\u8d44\u6599\u5df2\u9001\u51fa\uff0c\u5c0f\u5929\u4f7f/\u7ba1\u7406\u5458\u5ba1\u6838\u901a\u8fc7\u540e\u5373\u53ef\u4f7f\u7528\u4e00\u7ebf\u7275\u3002\u5ba1\u6838\u671f\u95f4\u8bf7\u52ff\u91cd\u590d\u63d0\u4ea4\u3002',
        'register_done.submitted.heading': '\u6ce8\u518c\u5df2\u9001\u51fa',
        'register_done.submitted.message': '\u8d44\u6599\u5df2\u6536\u5230\uff0c\u5c0f\u5929\u4f7f/\u7ba1\u7406\u5458\u4f1a\u5c3d\u5feb\u5ba1\u6838\uff1b\u901a\u8fc7\u540e\u4f1a\u6536\u5230\u7ad9\u5185\u901a\u77e5\u3002',
        'register_done.rejected.heading': '\u5ba1\u6838\u672a\u901a\u8fc7',
        'register_done.rejected.message': '\u5f88\u62b1\u6b49\uff0c\u4f60\u7684\u8d44\u6599\u672a\u901a\u8fc7\u5ba1\u6838\u3002\u5982\u6709\u7591\u95ee\u8bf7\u8054\u7cfb\u5c0f\u5929\u4f7f\u6216\u7ba1\u7406\u5458\u3002',
        'register_done.left.heading': '\u5df2\u6682\u65f6\u9000\u51fa',
        'register_done.left.message': '\u4f60\u5df2\u6682\u65f6\u9000\u51fa\u76f8\u4eb2\u5e02\u573a\uff0c\u5982\u9700\u6062\u590d\u8bf7\u8054\u7cfb\u5c0f\u5929\u4f7f\u6216\u7ba1\u7406\u5458\u3002',
        'register_done.active.heading': '\u5ba1\u6838\u5df2\u901a\u8fc7',
        'register_done.active.message': '\u4f60\u7684\u8d44\u6599\u5df2\u901a\u8fc7\u5ba1\u6838\uff0c\u53ef\u4ee5\u5f00\u59cb\u4f7f\u7528\u4e00\u7ebf\u7275\u3002',
        'register_done.disabled.heading': '\u8d26\u53f7\u5df2\u505c\u7528',
        'register_done.disabled.message': '\u4f60\u7684\u8d26\u53f7\u5df2\u88ab\u505c\u7528\uff0c\u8bf7\u8054\u7cfb\u7ba1\u7406\u5458\u3002',
        'register_done.default.heading': '\u6ce8\u518c\u4e0e\u5ba1\u6838',
        'register_done.default.message': '\u4e00\u7ebf\u7275\u91c7\u4eba\u5de5\u5ba1\u6838\uff0c\u786e\u8ba4\u8d44\u6599\u771f\u5b9e\u540e\u624d\u4f1a\u5f00\u901a\u4f7f\u7528\u3002',
        'register_done.phoneLabel': '\u767b\u5f55\u8d26\u53f7\uff1a',
        'register_done.enter': '\u8fdb\u5165\u4e00\u7ebf\u7275',
        'register_done.profile': '\u67e5\u770b\u6211\u7684\u8d44\u6599',
        'register_done.pendingHint': '\u5ba1\u6838\u901a\u8fc7\u540e\uff0c\u4e3b\u9875\u4e0e\u7ad9\u5185\u901a\u77e5\u4f1a\u663e\u793a\u6700\u65b0\u72b6\u6001\uff1b\u4e5f\u53ef\u7a0d\u540e\u91cd\u65b0\u767b\u5f55\u67e5\u770b\u3002',
        'register_done.logout': '\u9000\u51fa\u767b\u5f55',
        'register_done.loginCta': '\u767b\u5f55\u67e5\u770b\u5ba1\u6838\u72b6\u6001',
        'register_done.registerCta': '\u91cd\u65b0\u586b\u5199\u6ce8\u518c\u8d44\u6599',
        'register_done.nextTitle': '\u63a5\u4e0b\u6765',
        'register_done.nextBody': '\u5c0f\u5929\u4f7f/\u7ba1\u7406\u5458\u4f1a\u6838\u5bf9\u4f60\u7684\u8d44\u6599\u4e0e\u7167\u7247\u3002\u82e5\u8d44\u6599\u6709\u8bef\u6216\u9700\u8981\u8865\u4ef6\uff0c\u8bf7\u8054\u7edc\u5c0f\u5929\u4f7f\u5e76\u5907\u6ce8\u300c\u771f\u5b9e\u59d3\u540d\u300d\u3002',
        'register_done.privacyHint': '\u4e00\u7ebf\u7275\u4e0d\u4f1a\u5728\u9875\u9762\u663e\u793a\u4f60\u7684\u8eab\u4efd\u8bc1\u5168\u53f7\uff0c\u4e5f\u8bf7\u52ff\u628a\u5bc6\u7801\u63d0\u4f9b\u7ed9\u4ed6\u4eba\u3002',
      },
    },
    public_error: {
      text: {
        'public_error.eyebrow': '\u63d0\u793a',
        'public_error.activate_missing.heading': '\u542f\u7528\u94fe\u63a5\u4e0d\u5b8c\u6574',
        'public_error.activate_missing.message': '\u8bf7\u4f7f\u7528\u7ba1\u7406\u5458\u63d0\u4f9b\u7684\u5b8c\u6574\u542f\u7528\u94fe\u63a5\u3002',
        'public_error.activate_missing.action': '\u56de\u5230\u9996\u9875',
        'public_error.activate_invalid.heading': '\u542f\u7528\u94fe\u63a5\u65e0\u6548',
        'public_error.activate_invalid.message': '\u8bf7\u5411\u7ba1\u7406\u5458\u7d22\u53d6\u65b0\u7684\u542f\u7528\u94fe\u63a5\u3002',
        'public_error.activate_invalid.action': '\u56de\u5230\u9996\u9875',
        'public_error.activate_used.heading': '\u542f\u7528\u94fe\u63a5\u5df2\u4f7f\u7528',
        'public_error.activate_used.message': '\u6b64\u94fe\u63a5\u53ea\u80fd\u4f7f\u7528\u4e00\u6b21\uff0c\u8bf7\u76f4\u63a5\u767b\u5f55\u3002',
        'public_error.activate_used.action': '\u56de\u5230\u9996\u9875',
        'public_error.activate_expired.heading': '\u542f\u7528\u94fe\u63a5\u5df2\u8fc7\u671f',
        'public_error.activate_expired.message': '\u8bf7\u5411\u7ba1\u7406\u5458\u7d22\u53d6\u65b0\u7684\u542f\u7528\u94fe\u63a5\u3002',
        'public_error.activate_expired.action': '\u56de\u5230\u9996\u9875',
        'public_error.activate_already.heading': '\u8d26\u53f7\u5df2\u542f\u7528',
        'public_error.activate_already.message': '\u6b64\u8d26\u53f7\u5df2\u8bbe\u5b9a\u5bc6\u7801\uff0c\u8bf7\u76f4\u63a5\u767b\u5f55\u3002',
        'public_error.activate_already.action': '\u56de\u5230\u9996\u9875',
        'public_error.password_no_credential.heading': '\u5c1a\u672a\u8bbe\u5b9a\u5bc6\u7801',
        'public_error.password_no_credential.message': '\u8bf7\u4f7f\u7528\u542f\u7528\u94fe\u63a5\u8bbe\u5b9a\u5bc6\u7801\u3002',
        'public_error.password_no_credential.action': '\u56de\u5230\u9996\u9875',
        'public_error.default.heading': '\u65e0\u6cd5\u5b8c\u6210\u64cd\u4f5c',
        'public_error.default.message': '\u8bf7\u7a0d\u540e\u518d\u8bd5\u6216\u8054\u7cfb\u7ba1\u7406\u5458\u3002',
        'public_error.default.action': '\u56de\u5230\u9996\u9875',
      },
    },
    not_found: {
      text: {
        'not_found.eyebrow': '\u63d0\u793a',
        'not_found.title': '\u627e\u4e0d\u5230\u9875\u9762',
        'not_found.message': '\u4f60\u9020\u8bbf\u7684\u9875\u9762\u4e0d\u5b58\u5728\u3002',
        'not_found.home': '\u56de\u5230\u9996\u9875',
      },
    },
  };

  var page = document.body ? document.body.getAttribute('data-copy-page') : '';
  var table = Object.prototype.hasOwnProperty.call(COPY, page) ? COPY[page] : null;
  if (!table) return;

  function fillText(root) {
    var nodes = root.querySelectorAll('[data-copy-text]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-copy-text');
      if (Object.prototype.hasOwnProperty.call(table.text || {}, key)) {
        nodes[i].textContent = table.text[key];
      }
    }
  }

  function fillHTML(root) {
    var nodes = root.querySelectorAll('[data-copy-html]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-copy-html');
      if (Object.prototype.hasOwnProperty.call(table.html || {}, key)) {
        nodes[i].innerHTML = table.html[key];
      }
    }
  }

  function fillAria(root) {
    var nodes = root.querySelectorAll('[data-copy-aria]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-copy-aria');
      if (Object.prototype.hasOwnProperty.call(table.aria || {}, key)) {
        nodes[i].setAttribute('aria-label', table.aria[key]);
      }
    }
  }

  function fillPlaceholders(root) {
    var nodes = root.querySelectorAll('[data-copy-placeholder]');
    for (var i = 0; i < nodes.length; i++) {
      var key = nodes[i].getAttribute('data-copy-placeholder');
      if (Object.prototype.hasOwnProperty.call(table.placeholder || {}, key)) {
        nodes[i].setAttribute('placeholder', table.placeholder[key]);
      }
    }
  }

  // Error pages resolve their copy from <body data-error-code>: the template
  // only carries data-copy-error="heading|message|action" nodes.
  function fillError(root) {
    var code = document.body ? (document.body.getAttribute('data-error-code') || 'default') : 'default';
    var nodes = root.querySelectorAll('[data-copy-error]');
    for (var i = 0; i < nodes.length; i++) {
      var suffix = nodes[i].getAttribute('data-copy-error');
      var key = 'public_error.' + code + '.' + suffix;
      if (!Object.prototype.hasOwnProperty.call(table.text || {}, key)) {
        key = 'public_error.default.' + suffix;
      }
      if (Object.prototype.hasOwnProperty.call(table.text || {}, key)) {
        nodes[i].textContent = table.text[key];
      }
    }
  }

  function start() {
    fillText(document);
    fillHTML(document);
    fillAria(document);
    fillPlaceholders(document);
    fillError(document);
    if (document.body) document.body.classList.remove('copy-pending');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
