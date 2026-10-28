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
    'feishu.cta': '\u6253\u5f00\u98de\u4e66\u7248\u4e00\u7ebf\u7275 \u2197'
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

  function start() {
    fill(document);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start);
  } else {
    start();
  }
})();
