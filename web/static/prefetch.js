/* YIXIANQIAN: warm the SPA shell assets on login/activate/register pages.
   External file (not inline) so scan-safe pages keep script-src 'self'.
   Respects Save-Data / 2G; uses rel=prefetch (low priority). */
(function () {
  'use strict';
  try {
    var body = document.body;
    if (!body || body.getAttribute('data-prefetch-spa') !== '1') return;
    var conn = navigator.connection || {};
    if (conn.saveData || /(^|-)2g$/.test(conn.effectiveType || '')) return;
    var version = body.getAttribute('data-asset-version') || '';
    var paths = ['app.min.js', 'app.render.js', 'app.css', 'lib/vant.min.css', 'lib/vue.global.prod.js'];
    function warm() {
      for (var i = 0; i < paths.length; i++) {
        var link = document.createElement('link');
        link.rel = 'prefetch';
        link.href = '/static/legacy/' + paths[i] + '?v=' + version;
        document.head.appendChild(link);
      }
    }
    if (document.readyState === 'complete') warm();
    else window.addEventListener('load', warm, { once: true });
  } catch (e) {}
})();
