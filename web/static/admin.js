/* 一线牵管理台交互（Phase A）
   - 活动列表：搜索 + 状态 chips 客户端过滤
   - 工作台：tabs / hash 深链 / 二次确认
   - CSP: script-src 'self'，本文件不得依赖 inline script 或 inline style。
   所有增强均为渐进式：元素不存在或浏览器不支持时直接跳过，不抛错。 */
(function () {
  'use strict';

  function toArray(list) {
    return Array.prototype.slice.call(list || []);
  }

  /* ------------------------------------------------------------------------
     活动列表过滤
     ---------------------------------------------------------------------- */
  function initActivityFilters() {
    var grid = document.querySelector('[data-adm-activity-grid]');
    if (!grid) {
      return;
    }
    var cards = toArray(grid.querySelectorAll('[data-adm-activity]'));
    if (!cards.length) {
      return;
    }
    var search = document.querySelector('[data-adm-activity-search]');
    var chips = toArray(document.querySelectorAll('[data-adm-activity-filter]'));
    var status = document.querySelector('[data-adm-activity-status]');
    var empty = document.querySelector('[data-adm-activity-empty]');
    var active = 'all';

    var counts = {};
    cards.forEach(function (card) {
      var key = card.getAttribute('data-adm-status') || '';
      counts[key] = (counts[key] || 0) + 1;
    });
    toArray(document.querySelectorAll('[data-adm-count]')).forEach(function (el) {
      var key = el.getAttribute('data-adm-count');
      el.textContent = String(key === 'all' ? cards.length : (counts[key] || 0));
    });

    function term() {
      return search ? String(search.value || '').trim().toLowerCase() : '';
    }

    function apply() {
      var q = term();
      var visible = 0;
      cards.forEach(function (card) {
        var cardStatus = card.getAttribute('data-adm-status') || '';
        var haystack = String(card.getAttribute('data-adm-search') || '').toLowerCase();
        var show = (active === 'all' || cardStatus === active) && (!q || haystack.indexOf(q) !== -1);
        card.hidden = !show;
        if (show) {
          visible += 1;
        }
      });
      if (empty) {
        empty.hidden = visible !== 0;
      }
      if (status) {
        status.textContent = (active === 'all' && !q)
          ? '共 ' + cards.length + ' 个活动'
          : '显示 ' + visible + ' / ' + cards.length + ' 个活动';
      }
    }

    chips.forEach(function (chip) {
      chip.addEventListener('click', function () {
        active = chip.getAttribute('data-adm-activity-filter') || 'all';
        chips.forEach(function (other) {
          other.setAttribute('aria-pressed', other === chip ? 'true' : 'false');
        });
        apply();
      });
    });

    if (search) {
      search.addEventListener('input', apply);
      search.addEventListener('search', apply);
    }
    apply();
  }

  /* ------------------------------------------------------------------------
     工作台 tabs（报名名单 / 签到 / 广播 / 设置）
     ---------------------------------------------------------------------- */
  function initTabs() {
    var bar = document.querySelector('[data-adm-tabs]');
    if (!bar) {
      return;
    }
    var tabs = toArray(bar.querySelectorAll('[data-adm-tab]'));
    var panels = toArray(document.querySelectorAll('[data-adm-panel]'));
    if (!tabs.length || !panels.length) {
      return;
    }
    var map = {};
    tabs.forEach(function (tab) {
      map[tab.getAttribute('data-adm-tab')] = tab;
    });

    function panelFor(name) {
      for (var i = 0; i < panels.length; i += 1) {
        if (panels[i].getAttribute('data-adm-panel') === name) {
          return panels[i];
        }
      }
      return null;
    }

    function nameFromHash() {
      var hash = String(window.location.hash || '').replace(/^#/, '');
      if (hash.indexOf('member-') === 0) {
        return { name: 'roster', target: hash };
      }
      if (hash === 'roster' || hash === 'signin' || hash === 'broadcast') {
        return { name: hash, target: '' };
      }
      if (hash === 'settings' || hash === 'grouping' || hash === 'poster' || hash === 'summary') {
        return { name: 'settings', target: '' };
      }
      return { name: 'roster', target: '' };
    }

    function activate(name) {
      if (!panelFor(name)) {
        name = 'roster';
      }
      tabs.forEach(function (tab) {
        var on = tab.getAttribute('data-adm-tab') === name;
        tab.setAttribute('aria-selected', on ? 'true' : 'false');
        tab.setAttribute('tabindex', on ? '0' : '-1');
      });
      panels.forEach(function (panel) {
        panel.hidden = panel.getAttribute('data-adm-panel') !== name;
      });
    }

    function scrollToPanel(name) {
      var panel = panelFor(name);
      if (!panel) {
        return;
      }
      var offset = 118;
      var y = panel.getBoundingClientRect().top + window.pageYOffset - offset;
      if (y < 0) {
        y = 0;
      }
      window.scrollTo(0, y);
    }

    function applyHash(forceScroll) {
      var info = nameFromHash();
      activate(info.name);
      if (!window.location.hash) {
        return;
      }
      if (info.target) {
        var row = document.getElementById(info.target);
        if (row) {
          row.scrollIntoView({ block: 'center' });
          return;
        }
      }
      if (forceScroll) {
        scrollToPanel(info.name);
      }
    }

    tabs.forEach(function (tab, index) {
      tab.addEventListener('click', function (event) {
        event.preventDefault();
        var name = tab.getAttribute('data-adm-tab') || 'roster';
        if (window.history && window.history.replaceState) {
          window.history.replaceState(null, '', '#' + name);
        } else {
          window.location.hash = name;
        }
        activate(name);
        scrollToPanel(name);
        tab.focus({ preventScroll: true });
      });
      tab.addEventListener('keydown', function (event) {
        var next = null;
        if (event.key === 'ArrowRight') {
          next = tabs[(index + 1) % tabs.length];
        } else if (event.key === 'ArrowLeft') {
          next = tabs[(index - 1 + tabs.length) % tabs.length];
        } else if (event.key === 'Home') {
          next = tabs[0];
        } else if (event.key === 'End') {
          next = tabs[tabs.length - 1];
        }
        if (next) {
          event.preventDefault();
          next.click();
        }
      });
    });

    window.addEventListener('hashchange', function () {
      applyHash(true);
    });

    applyHash(true);
  }


  /* ------------------------------------------------------------------------
     签到统计（未签到 / 签到率）与签到页翻页保留 tab
     ---------------------------------------------------------------------- */
  function initSigninStats() {
    var box = document.querySelector('[data-adm-signin-stats]');
    if (!box) {
      return;
    }
    var signup = Number(box.getAttribute('data-signup')) || 0;
    var checked = Number(box.getAttribute('data-checked')) || 0;
    var unchecked = box.querySelector('[data-adm-stat="unchecked"]');
    var rate = box.querySelector('[data-adm-stat="rate"]');
    if (unchecked) {
      unchecked.textContent = String(Math.max(signup - checked, 0));
    }
    if (rate) {
      rate.textContent = signup > 0 ? Math.round(checked * 100 / signup) + '%' : '—';
    }
  }

  function initSigninPagination() {
    toArray(document.querySelectorAll('[data-adm-signin-page]')).forEach(function (link) {
      var href = link.getAttribute('href') || '';
      if (href.indexOf('#roster') !== -1) {
        link.setAttribute('href', href.replace('#roster', '#signin'));
      }
    });
  }

  /* ------------------------------------------------------------------------
     二次确认 + 复制签到码
     ---------------------------------------------------------------------- */
  function initConfirm() {
    document.addEventListener('click', function (event) {
      var target = event.target;
      if (!target || !target.closest) {
        return;
      }
      var trigger = target.closest('[data-adm-confirm]');
      if (!trigger) {
        return;
      }
      var message = trigger.getAttribute('data-adm-confirm');
      if (message && !window.confirm(message)) {
        event.preventDefault();
        event.stopPropagation();
      }
    }, true);
  }

  function initCopy() {
    toArray(document.querySelectorAll('[data-adm-copy]')).forEach(function (button) {
      button.addEventListener('click', function () {
        var value = button.getAttribute('data-adm-copy') || '';
        var done = function () {
          var label = button.getAttribute('data-adm-copy-label') || '复制签到码';
          button.textContent = '已复制';
          window.setTimeout(function () {
            button.textContent = label;
          }, 1600);
        };
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(value).then(done, function () {});
        }
      });
    });
  }

  function boot() {
    initActivityFilters();
    initTabs();
    initSigninStats();
    initSigninPagination();
    initConfirm();
    initCopy();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
