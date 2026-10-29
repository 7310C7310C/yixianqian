// anti-copy: disable text selection and long-press save on images
(function(){
  try{
    function isEditable(el){
      try{
        if(!el) return false;
        if(el.tagName==='INPUT'||el.tagName==='TEXTAREA'||el.isContentEditable) return true;
        if(el.closest && el.closest('input, textarea, [contenteditable="true"], [contenteditable=""]')) return true;
        var p=el.parentElement;
        while(p){
          if(p.tagName==='INPUT'||p.tagName==='TEXTAREA'||p.isContentEditable) return true;
          p=p.parentElement;
        }
      }catch(e){}
      return false;
    }
    document.addEventListener('contextmenu', function(e){ if(isEditable(e.target)) return; e.preventDefault(); });
    document.addEventListener('selectstart', function(e){ if(isEditable(e.target)) return; e.preventDefault(); });
    document.addEventListener('copy', function(e){ if(isEditable(e.target)) return; e.preventDefault(); });
    document.addEventListener('cut', function(e){ if(isEditable(e.target)) return; e.preventDefault(); });
    document.addEventListener('dragstart', function(e){ e.preventDefault(); });
    function disableImgDrag(){
      document.querySelectorAll('img').forEach(function(img){ img.setAttribute('draggable','false'); });
    }
    if(document.readyState==='loading'){ document.addEventListener('DOMContentLoaded', disableImgDrag); } else { disableImgDrag(); }
    new MutationObserver(disableImgDrag).observe(document.documentElement,{childList:true,subtree:true});
  }catch(e){}
})();
// 自身脚本地址：用于在 Vue 就绪后再按需加载 Vant（Vant 是依赖 Vue 的 UMD，
// Vue 缺失时提前执行只会在 vendor 里抛 pageerror，盖住我们的可见错误页）。
var yxqAssetVersion = '';
var yxqLibBase = '/static/legacy/lib/';
try {
  var yxqSrc = (document.currentScript && document.currentScript.src) || '';
  var yxqMatch = yxqSrc.match(/[?&]v=([^&]+)/);
  if (yxqMatch) yxqAssetVersion = yxqMatch[1];
  if (yxqSrc) yxqLibBase = yxqSrc.replace(/[^/]*$/, '') + 'lib/';
} catch (e) {}
// runtime-only Vue（3.4.27 无编译器构建）不能再把 template 当兜底：渲染器或运行库缺失时
// 必须给出可诊断的可见错误，而不是静默白屏。这里只依赖 DOM，不依赖 Vue。
function yxqBootFailure(detail){
  try{ if (window.__YXQ_HERO__) window.__YXQ_HERO__.dismiss('boot-fail'); }catch(e){}
  try{
    var root = document.getElementById('app');
    if (root){
      // 壳上的 v-cloak 会一直 display:none（Vue 没挂载就没人摘它），错误页也会跟着隐身。
      root.removeAttribute('v-cloak');
      root.setAttribute('data-boot-error','1');
      root.innerHTML = '<div class="boot-fail">'
        + '<div class="boot-fail-title">页面加载失败</div>'
        + '<div class="boot-fail-detail"></div>'
        + '<button class="boot-fail-retry" type="button">重新加载</button>'
        + '</div>';
      var detailEl = root.querySelector('.boot-fail-detail');
      if (detailEl) detailEl.textContent = detail;
      var retry = root.querySelector('.boot-fail-retry');
      if (retry) retry.onclick = function(){ location.reload(); };
    }
  }catch(e){}
  try{ console.error('[yxq] boot failed:', detail); }catch(e){}
}
// 批次6：Vant 子集必须覆盖模板用到的 17 个组件；缺失时给出可见错误页，
// 不让 resolveComponent 静默渲染空白。清单与 tools/spa_vendor/manifest 同步。
var YXQ_VANT_REQUIRED = ['van-button','van-cell','van-cell-group','van-dialog','van-empty','van-field','van-icon','van-loading','van-popup','van-radio','van-radio-group','van-switch','van-tab','van-tabbar','van-tabbar-item','van-tabs','van-tag'];
function yxqCheckVantSubset(){
  try {
    var shipped = (typeof vant !== 'undefined' && vant && vant.names) || [];
    var forced = window.__YXQ_VANT_FORCE_MISSING__;
    var missing = YXQ_VANT_REQUIRED.filter(function(n){
      if (forced && n === forced) return true;
      return shipped.length && shipped.indexOf(n) < 0;
    });
    if (missing.length) { yxqBootFailure('UI 组件缺失：' + missing.join('、')); return false; }
  } catch (e) {}
  return true;
}
// Vant 只在 Vue 就绪后加载；失败走同一套可见错误页，不抛异常。
function yxqLoadVant(onReady){
  if (typeof vant !== 'undefined') { onReady(); return; }
  var script = document.createElement('script');
  script.src = yxqLibBase + 'vant.min.js' + (yxqAssetVersion ? '?v=' + yxqAssetVersion : '');
  script.onload = function(){
    if (typeof vant === 'undefined') { yxqBootFailure('UI 组件库未初始化（vant.min.js）'); return; }
    if (!yxqCheckVantSubset()) return;
    onReady();
  };
  script.onerror = function(){ yxqBootFailure('UI 组件库加载失败（vant.min.js）'); };
  document.head.appendChild(script);
}
// 首帧让位：挂载本身只渲染启动骨架，数据加载与卡片页首次渲染如果紧跟在同一任务里，
// 会把壳内 hero 的首图绘制推迟到长任务结束。rAF 后 setTimeout 让浏览器先提交一帧，
// 再开始拉/渲染数据；隐藏标签页 rAF 不触发时用 300ms 兜底。
function yxqAfterFirstPaint(fn){
  var done = false;
  function run(){ if (done) return; done = true; try { fn(); } catch (e) { try { console.error(e); } catch (_) {} } }
  requestAnimationFrame(function(){ setTimeout(run, 0); });
  setTimeout(run, 300);
}
// 守衛必須在解構 `const { ref } = Vue` 之前：否则 Vue 缺失时是 ReferenceError，
// 错误页根本来不及渲染（Reviewer C3）。这里不 throw——未捕获异常会变成 pageerror，
// 渲染完错误页就停下，初始化整体放进 else 块。
if (typeof Vue === 'undefined' || !Vue) {
  yxqBootFailure('运行库未加载（vue.global.prod.js）');
} else {
const { createApp, ref, computed, onMounted, watch, nextTick, onUnmounted, getCurrentInstance } = Vue;
// 管理台共用「慢载入」loading：超过 6s 在转圈下方追加提示（页面以为卡死而重载是本次要缓解的问题）。
// 组件卸载自动清定时器；写操作遮罩等不需要的地方不用它。
const GaLoading = {
  props: { size: { type: String, default: '24px' } },
  setup() {
    const SLOW_HINT_MS = 6000;
    const slow = ref(false);
    const timer = setTimeout(() => { slow.value = true; }, SLOW_HINT_MS);
    onUnmounted(() => clearTimeout(timer));
    return { slow };
  },
  render() {
    return Vue.h('div', { class: 'ga-slow-loading' }, [
      Vue.h(Vue.resolveComponent('van-loading'), { size: this.size }, { default: () => '加载中...' }),
      this.slow ? Vue.h('div', { class: 'ga-slow-hint' }, '网络较慢，仍在加载…') : null
    ]);
  }
};

// ===== 页面 chunk（批次6）=====
// 26 个 tab/子页的 render function 拆成 6 个 chunk：cards 在 app.render.js 里，
// 其余按页面分组懒加载；加载失败显示可见错误页（不白屏）。
var YXQ_PAGE_GROUPS = {
  cards: null,
  likes: 'likes', mutual: 'likes', liked_me: 'likes', my_likes: 'likes',
  my_messages: 'likes', received_messages: 'likes', reported_messages: 'likes',
  home: 'home', my_activities: 'home', my_groups: 'home',
  profile: 'profile', password: 'profile', profile_detail: 'profile',
  profile_edit: 'profile', points: 'profile',
  user_detail: 'detail',
  group_admin: 'group', group_detail: 'group', group_roster: 'group',
  checkin: 'group', group_select: 'group', group_result: 'group',
  onetoone_result: 'group', activity_detail: 'group', points_admin: 'group'
};
var yxqPageLoading = {};
var yxqRootProxy = null;

function yxqLoadPageChunk(page, done, onError){
  if (window.__YXQ_PAGES__ && typeof window.__YXQ_PAGES__[page] === 'function') {
    if (done) done();
    return;
  }
  var group = YXQ_PAGE_GROUPS[page];
  if (!group) { yxqBootFailure('页面未注册：' + page); return; }
  if (yxqPageLoading[group]) { if (done) yxqPageLoading[group].push(done); return; }
  yxqPageLoading[group] = done ? [done] : [];
  var base = yxqLibBase.replace(/lib\/$/, 'pages/');
  var script = document.createElement('script');
  script.src = base + group + '.render.js' + (yxqAssetVersion ? '?v=' + yxqAssetVersion : '');
  script.onload = function(){
    var callbacks = yxqPageLoading[group] || [];
    yxqPageLoading[group] = null;
    if (!window.__YXQ_PAGES__ || typeof window.__YXQ_PAGES__[page] !== 'function') {
      yxqBootFailure('页面资源未就绪：' + page);
      return;
    }
    callbacks.forEach(function(cb){ try { cb(); } catch (e) {} });
  };
  script.onerror = function(){
    yxqPageLoading[group] = null;
    // 前台导航（tab 点击守卫/当前页）必须升级为可见错误；后台预取失败只跳过。
    if (onError) { try { onError(page); } catch (e) {} return; }
    // 只有用户仍停在目标页时才升级为致命错误；已切走则下次进入重试。
    var current = yxqRootProxy && yxqRootProxy.page;
    if (current === page) yxqBootFailure('页面资源加载失败：' + page);
  };
  document.head.appendChild(script);
}

// 批次10 追加：tab 页 chunk 的「就绪」判断、点击守卫与预取（likes 立即，其余 LCP 后 idle）。
// 预取在应用挂载后的 requestIdleCallback（timeout 200ms + 1.2s 绝对兜底）启动，
// 此时 hero 已绘制，不抢首屏图/首屏脚本。
function yxqPageChunkReady(page){
  if (!page) return true;
  if (!YXQ_PAGE_GROUPS[page]) return true; // cards 的 render 在 app.render.js
  return !!(window.__YXQ_PAGES__ && typeof window.__YXQ_PAGES__[page] === 'function');
}
function yxqEnsurePageChunk(page){
  if (yxqPageChunkReady(page)) return Promise.resolve(true);
  return new Promise(function(resolve){
    var settled = false;
    var timer = setTimeout(function(){ if (settled) return; settled = true; resolve(false); }, 4000);
    yxqLoadPageChunk(page, function(){
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(yxqPageChunkReady(page));
    }, function(){
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      yxqBootFailure('页面资源加载失败：' + page);
      resolve(false);
    });
  });
}
function yxqPrefetchPageChunks(){
  // 消息 tab 是用户最常首次点击且 chunk 最小（~5KB gzip）：挂载后立即预取，
  // 不等 LCP，保证首次点「消息」≤1.5s；其余 chunk 等 LCP 后再预取。
  if (!yxqPageChunkReady('likes')) {
    // 立即发起（不等 LCP/idle）：likes chunk 仅 ~5KB gzip，优先保证首次点「消息」。
    yxqLoadPageChunk('likes', function(){}, function(){});
  }
  var pages = ['home', 'profile', 'user_detail', 'group_admin'];
  var index = 0, started = false;
  function next(){
    if (index >= pages.length) return;
    var page = pages[index++];
    if (yxqPageChunkReady(page)) { next(); return; }
    // 与导航共用 yxqLoadPageChunk 的 in-flight 去重与缓存；80ms 间隔避免瞬时并发。
    yxqLoadPageChunk(page, function(){ setTimeout(next, 80); }, function(){ setTimeout(next, 80); });
  }
  function start(){
    if (started) return;
    started = true;
    if ('requestIdleCallback' in window) {
      window.requestIdleCallback(function(){ next(); }, { timeout: 2500 });
    } else {
      setTimeout(next, 1200);
    }
  }
  // 等 LCP 条目出现后再预取：hero/最终卡图的绘制已完成，预取不会抢 LCP；
  // likes 仍排第一，保证首次点「消息」时 chunk 尽快就绪。2.5s 绝对兜底。
  var prefetchStarted = false;
  function startAfterLCP(){
    if (prefetchStarted) return;
    prefetchStarted = true;
    if ('requestIdleCallback' in window) {
      window.requestIdleCallback(start, { timeout: 100 });
    } else {
      setTimeout(start, 0);
    }
  }
  try {
    new PerformanceObserver(function(list){
      var entries = list.getEntries();
      for (var i = 0; i < entries.length; i++) {
        if (entries[i].startTime > 0) { startAfterLCP(); return; }
      }
    }).observe({ type: 'largest-contentful-paint', buffered: true });
  } catch (e) {
    setTimeout(startAfterLCP, 600);
  }
  setTimeout(startAfterLCP, 2500);

}

// page-host 在根 render 内渲染当前页；页面 render 用根组件 proxy 调用，
// 模板 ref 通过 setupState 代理写回根 ref（photoInput/gsReorderBtnEl 等）。
const PageHost = {
  props: { page: { type: String, required: true } },
  setup(props) {
    const root = yxqRootProxy;
    const cache = [];
    const tick = ref(0);
    if (!window.__YXQ_PAGES__ || typeof window.__YXQ_PAGES__[props.page] !== 'function') {
      yxqLoadPageChunk(props.page, function(){ tick.value++; });
    }
    return new Proxy({}, {
      get(_, key) {
        if (key === '__root') return root;
        if (key === '__cache') return cache;
        if (key === '__tick') return tick.value;
        return root[key];
      },
      set(_, key, value) { root[key] = value; return true; },
      has() { return true; },
      ownKeys() { return Reflect.ownKeys(root); },
      getOwnPropertyDescriptor() { return { enumerable: true, configurable: true }; }
    });
  },
  render() {
    void this.__tick;
    const renderPage = window.__YXQ_PAGES__ && window.__YXQ_PAGES__[this.page];
    if (typeof renderPage !== 'function') {
      // 无文字骨架：点击/深链时 chunk 未就绪也不出现「加载中...」；tabbar 点击则由
      // guardTabChange 保留当前页直到 chunk 就绪，正常路径不会看到这个骨架。
      return Vue.h('div', { class: 'page-chunk-skeleton', 'aria-busy': 'true',
        style: { minHeight: '70vh', padding: '16px', boxSizing: 'border-box' } }, [
        Vue.h('div', { class: 'boot-shimmer', style: { width: '46%', height: '18px', borderRadius: '9px', margin: '12px 0 20px' } }),
        Vue.h('div', { class: 'boot-shimmer', style: { width: '100%', height: '112px', borderRadius: '12px', marginBottom: '12px' } }),
        Vue.h('div', { class: 'boot-shimmer', style: { width: '100%', height: '112px', borderRadius: '12px', marginBottom: '12px' } }),
        Vue.h('div', { class: 'boot-shimmer', style: { width: '100%', height: '112px', borderRadius: '12px', marginBottom: '12px' } })
      ]);
    }
    return renderPage(this.__root, this.__cache);
  }
};

const yxqApp = createApp({
  render: window.__YXQ_RENDER__,
  components: { 'ga-loading': GaLoading },
  setup() {
    try { yxqRootProxy = getCurrentInstance().proxy; } catch (e) {}
    // 管理台 iframe 内的原版活动管理：/app?native_admin=1&p=group_admin。
    // 启动期先按请求隐藏 App 外壳；can_admin 校验在首页数据就绪后完成。
    let nativeAdminRequested = false, nativeAdminEntryPage = 'group_admin';
    try {
      const nativeQuery = new URLSearchParams(location.search);
      nativeAdminRequested = nativeQuery.get('native_admin') === '1';
      if (nativeQuery.get('p')) nativeAdminEntryPage = nativeQuery.get('p');
    } catch (e) {}
    const nativeAdmin = ref(nativeAdminRequested);
    let nativeAdminEntryPending = false;
    // #app 是挂载容器，不在模板内，:class 不会被编译；用原生 class 同步去掉底栏预留。
    watch(nativeAdmin, (v) => {
      const root = document.getElementById('app');
      if (root) root.classList.toggle('native-admin', !!v);
    }, { immediate: true });
    const page = ref('cards');
    // 四 Tab / 子页流畅过渡：按 tab 顺序与子页层级自动选动画
    const pageTransition = ref('tab-transition');
    const TAB_ORDER = ['cards','likes','home','profile'];
    function pickTransition(nv, ov){
      if(!ov) return 'tab-transition';
      const isTab = p => TAB_ORDER.includes(p);
      const isSub = p => subPages.includes(p);
      if(isTab(nv) && isTab(ov)){
        return TAB_ORDER.indexOf(nv) > TAB_ORDER.indexOf(ov) ? 'tab-slide-left' : 'tab-slide-right';
      }
      if(isSub(nv) && !isSub(ov)) return 'page-slide';
      if(!isSub(nv) && isSub(ov)) return 'page-slide-back';
      if(isSub(nv) && isSub(ov)) return 'page-slide';
      return 'tab-transition';
    }
    // 夜间模式（默认日间）
    const isDark = ref(false);
    try { isDark.value = localStorage.getItem('yxq_dark') === '1'; } catch(e) {}
    function toggleTheme(){
      isDark.value = !isDark.value;
      document.documentElement.classList.toggle('van-theme-dark', isDark.value);
      try { localStorage.setItem('yxq_dark', isDark.value ? '1' : '0'); } catch(e) {}
      if (embedOpen.value) {
        let doc = null;
        try { doc = embedFrame.value && embedFrame.value.contentDocument; } catch(e) {}
        if (doc) applyEmbedTheme(doc);   // class 操作不受 CSP 限制；深色由外部样式表接管
      }
    }
    document.documentElement.classList.toggle('van-theme-dark', isDark.value);
    // —— 返回拦截（飞书手势/物理返回）——
    let isHandlingPopState = false;
    let ignoreNextPop = false;
    // dropCurrent 在真实历史模式下的待消费标记：back() 的 popstate 只改写 top state，不切页。
    let dropCurrentPending = null;
    // 「控制台→签到」历史塌缩的一次性旗标：openCheckinPage 已把当前条目 replaceState 为 checkin，
    // watch(page) 见旗标且 nv==='checkin' 时跳过补 push（仅此一次；其它页面切换不受影响、不误食）
    let suppressPagePushOnce = false;

    // —— 顶层浏览器历史：默认虚拟栈（消除 Via/WebView 顶部进度条）——
    // Via 在 pushState/replaceState/hist.back() 时会闪顶部加载条；tab→tab 不触边所以正常。
    // 方案：启动时只在真实历史压一个哨兵；子页/浮层/应用内返回全部走内存栈，popstate 只负责
    // 安卓硬件返回；每次消费哨兵后按需重压，保证下一次返回仍可捕获。
    // 回退：?history=legacy 或 localStorage.yxq_history_mode='legacy'（native_admin iframe 恒原生）。
    function navURL(){
      return nativeAdmin.value ? (location.pathname + location.search) : location.pathname;
    }
    const historyMode = (() => {
      try {
        const q = new URLSearchParams(location.search).get('history');
        if (q === 'legacy' || q === 'virtual') return q;
        const stored = localStorage.getItem('yxq_history_mode');
        if (stored === 'legacy' || stored === 'virtual') return stored;
      } catch(e) {}
      return 'virtual';
    })();
    const realHistory = window.history;
    const virtualHistory = { stack: [{page:'cards'}], sentinel: false };
    function useVirtualHistory(){ return historyMode === 'virtual' && !nativeAdmin.value; }
    function virtualTop(){ return virtualHistory.stack[virtualHistory.stack.length - 1]; }
    function deferMicrotask(fn){
      try { Promise.resolve().then(fn); } catch(e) { setTimeout(fn, 0); }
    }
    const hist = {
      get state(){ return useVirtualHistory() ? virtualTop() : realHistory.state; },
      replace(state, url){
        const target = url === undefined ? navURL() : url;
        if(!useVirtualHistory()){
          try{ realHistory.replaceState(state, '', target); }catch(e){}
          return;
        }
        virtualHistory.stack[virtualHistory.stack.length - 1] = state;
        // 只有真正需要清理地址（启动去 hash/query、登录回写）才动原生 URL；
        // 子页/浮层的状态更新不碰浏览器历史，避免 Via 进度条。
        const now = location.pathname + location.search + location.hash;
        if(target && target !== now){
          try{ realHistory.replaceState(state, '', target); }catch(e){}
        }
      },
      push(state){
        if(!useVirtualHistory()){
          try{ realHistory.pushState(state, '', navURL()); }catch(e){}
          return;
        }
        virtualHistory.stack.push(state);
      },
      back(){
        if(!useVirtualHistory()){
          try{ realHistory.back(); }catch(e){}
          return;
        }
        if(virtualHistory.stack.length > 1){
          // 异步一拍，保持与原生 hist.back() 的时序一致（watch 先看到旧 state 再消费）
          deferMicrotask(function(){
            if(virtualHistory.stack.length <= 1) return;
            virtualHistory.stack.pop();
            handlePopState({state: virtualTop()});
          });
          return;
        }
        // 主 Tab：交给容器关闭（与旧逻辑一致）；真实哨兵的 popstate 由 handleRealPopState 处理
        try{ realHistory.back(); }catch(e){}
      },
      // 丢弃当前条目并把新的 top 设为 state（不触发 UI 切页）。
      // 用于保存成功后消除当前 edit/password 条目：虚拟栈直接 pop；真实历史 back() 到父条目，
      // popstate 由 dropCurrentPending 吞掉并 replaceState 成目标 state。
      dropCurrent(state){
        if(!useVirtualHistory()){
          dropCurrentPending = state;
          try{ realHistory.back(); }catch(e){ dropCurrentPending = null; }
          return;
        }
        if(virtualHistory.stack.length > 1){
          virtualHistory.stack.pop();
          virtualHistory.stack[virtualHistory.stack.length - 1] = state;
        } else {
          virtualHistory.stack[0] = state;
        }
      }
    };
    // 诊断钩子：浏览器回归断言虚拟栈长度/内容；不参与业务逻辑。
    window.__YXQ_HIST_DEBUG__ = function(){
      if(useVirtualHistory()){
        return {
          mode: 'virtual',
          length: virtualHistory.stack.length,
          stack: virtualHistory.stack.map(function(s){ return s ? Object.assign({}, s) : s; })
        };
      }
      return { mode: 'legacy', length: realHistory.length, state: realHistory.state };
    };
    function pushHistorySentinel(){
      if(!useVirtualHistory() || virtualHistory.sentinel) return;
      try{
        // 虚拟栈自己管理滚动（tab 记忆/子页回顶/浮层锁滚动）；关闭浏览器自动恢复，
        // 否则硬件返回消费哨兵时会被浏览器拉回 base 条目的旧滚动位置（盖掉 unlockPageScroll）。
        if('scrollRestoration' in realHistory) realHistory.scrollRestoration = 'manual';
        // 刷新后当前条目可能仍是上一轮哨兵（history.state 跨 reload 保留）：先改回页面状态，
        // 否则会叠加第二层哨兵，第一次硬件返回只退到另一个哨兵、页面不动。
        const cur = realHistory.state;
        if(cur && cur.yxqSentinel === 1){
          try{ realHistory.replaceState({page: page.value}, '', navURL()); }catch(e){}
        }
        // 哨兵只用于捕获安卓硬件返回；不带 hash/query，不产生文档导航。
        realHistory.pushState({yxqSentinel:1}, '', navURL());
        virtualHistory.sentinel = true;
      }catch(e){}
    }
    // 真实 popstate 分派：legacy/nativeAdmin 走原逻辑；虚拟模式只消费启动哨兵。
    function handleRealPopState(e){
      if(!useVirtualHistory()){ handlePopState(e); return; }
      if(e && e.state && e.state.yxqSentinel === 1){
        virtualHistory.sentinel = true;   // 前进回到哨兵：无需处理
        return;
      }
      virtualHistory.sentinel = false;
      if(virtualHistory.stack.length > 1){
        virtualHistory.stack.pop();
        handlePopState({state: virtualTop()});
        pushHistorySentinel();
        return;
      }
      // 已在主 tab：不拦截，继续后退离开 SPA；若没有上一页可退，稍后把哨兵补回。
      try{ realHistory.back(); }catch(e){}
      setTimeout(function(){ if(useVirtualHistory() && !virtualHistory.sentinel) pushHistorySentinel(); }, 300);
    }
    // 守卫收尾的历史条目消费队列：overlay/未保存弹窗关闭时「一层一条」串行 back()，
    // 每次 back 的 popstate 用计数抑制。同 tick 连关两层（弹窗 + 其下被拦浮层）时按队列
    // 逐条消费，不等 hist.state 的异步更新，避免 overlay 条目残留→返回重进/震荡。
    let pendingPopConsume = 0;
    let popConsumeBusy = false;
    let consumeIgnorePop = 0;
    function popConsumeStep(){
      if(popConsumeBusy || pendingPopConsume <= 0) return;
      const st = hist.state;
      if(!st || !st.overlay){ pendingPopConsume = 0; return; }  // 已回到底层页面条目：停手，绝不越吃
      pendingPopConsume--;
      popConsumeBusy = true;
      consumeIgnorePop++;
      try { hist.back(); } catch(e){ consumeIgnorePop--; popConsumeBusy = false; }
    }
    function requestPopConsume(){
      pendingPopConsume++;
      popConsumeStep();
    }
    function pushHistory(state){
      if(isHandlingPopState) return;
      // 不带 hash：Safari/部分浏览器会把 hash 变化当锚点导航，导致 tabbar 闪动与进度条。
      // 页面/浮层状态已存在 hist.state，back/forward 仍由 popstate 消费。
      hist.push(state);
    }
    function handleBack(){
      // 次序托盘/未保存弹窗打开：返回只关浮层、停留（不触发离开拦截）
      if(showGsTray.value){
        hist.back();
        return;
      }
      if(showUnsavedConfirm.value){
        cancelLeaveConfirm();
        return;
      }
      // 未保存离开拦截（守卫注册表）：dirty → 弹窗；提交中 → 提示并停留
      if(guardLeave(() => { hist.back(); })) return;
      // 统一返回：有浮层先关浮层，无则 hist.back() 让 popstate 处理页面
      if(showPhotoViewer.value || showFilter.value || showLikeDialog.value || showFeedbackDialog.value || showReportDialog.value || showSuggestionDialog.value || showInviteDialog.value || showAdminHelp.value || showSelectPopup.value || showMultiPopup.value || signupSheetShow.value || showRedeemPanel.value || showPointsRules.value || showRedemptions.value || signinDialogShow.value || embedOpen.value){
        hist.back();
        return;
      }
      if(page.value !== 'cards' && page.value !== 'home' && page.value !== 'likes' && page.value !== 'profile'){
        hist.back();
        return;
      }
      // 主Tab下直接返回由容器处理（关闭网页）
      hist.back();
    }
    // 原版活动管理在 native_admin 下的返回：列表页回管理台总览，子页保持原层级返回。
    function gaBack(){
      if (!nativeAdmin.value) { handleBack(); return; }
      if (page.value === 'group_admin') { location.href = '/admin?embed=1'; return; }
      handleBack();
    }
    const user = ref(null);
    const isObserver = computed(() => !!(user.value && user.value.is_observer));
    const availableRoles = computed(() => (user.value && user.value.available_roles) || []);
    // 服务端在 SPA 外壳注入登录旗标：已登录刷新时直接走骨架+home，不闪登录页。
    const SERVER_AUTHENTICATED = window.__YXQ_AUTHENTICATED__ === true;
    const loggingIn = ref(!SERVER_AUTHENTICATED);
    const bootLoading = ref(SERVER_AUTHENTICATED);
    const bootError = ref('');
    let bootEntryHash = '';
    const loginError = ref(false);
    const loginErrorMessage = ref('');
    const showPersonalGuide = ref(false);
    const loginTip = ref('');
    // 预报名认领（微信/浏览器先填过表，现场补飞书身份）
    const showClaim = ref(false);
    const claimName = ref('');
    const claimPhone = ref('');
    const claiming = ref(false);
    let claimEntryFlag = false;  // 机器人「认领」链接 ?claim=1：直接进入认领页（正式功能，生产保留）
    let adminEntryFlag = false;  // ?admin=1：loadHome 成功后自动开内嵌管理台
    // 账号状态门禁（待审核/审核不通过/已退出）：后端 /api/home 返回 403 时整屏提示
    const gateMessage = ref('');
    const gateTitle = computed(() => {
      const m = gateMessage.value;
      if (m.includes('审核中')) return '资料审核中';
      if (m.includes('未通过审核')) return '未通过审核';
      if (m.includes('已暂时退出')) return '账号已退出';
      return '账号无法使用';
    });
    const gateIcon = computed(() => {
      const m = gateMessage.value;
      if (m.includes('审核中')) return '⏳';
      if (m.includes('未通过审核')) return '💔';
      if (m.includes('已暂时退出')) return '🚪';
      return '⚠️';
    });
    function retryGate() { retryBoot(); }   // 原地重试，不整页刷新
    const subPages = ['activity_detail','group_select','group_result','onetoone_result','profile_detail','my_likes','my_activities','my_groups','user_detail','mutual','liked_me','points','points_admin','group_admin','group_detail','group_roster','checkin','password'];
    // 修改密码：App 内子页。不在 URL/hash 留状态，返回用 history 落在 profile。
    const passwordForm = ref({ current: '', password: '', confirm: '' });
    const passwordErrors = ref({ current: '', password: '', confirm: '' });
    const passwordSubmitError = ref('');
    const passwordSubmitting = ref(false);
    // 改密成功回 profile：就地改写 password 历史条目，避免返回又落回已提交的表单。
    let passwordFormDone = false;
    // 资料保存成功回 profile_detail：page watcher 把它当「前进」会多压一条，
    // 用一次性旗标改成 hist.replace（与 passwordFormDone 同款），失败不设置。
    let profileFormDone = false;
    // 从「我的」进入改密页前的滚动位置，返回/改密成功回 profile 时还原
    let passwordReturnScroll = 0;
    // 四 Tab 各自记忆滚动位置：主内容共用一个文档滚动容器，Transition out-in 切换 tab 时
    // 浏览器会沿用上一个 tab 的 window.scrollY（长页→短页被夹到底部、短页→长页整页下移），
    // 表现为「活动页下滑，其他 tab 也被下滑」。这里按 tab 记忆并在进场时复位/还原。
    const tabScroll = { cards: 0, likes: 0, home: 0, profile: 0 };
    function readScrollY(){
      return Math.max(0, Math.round(window.scrollY || (document.scrollingElement && document.scrollingElement.scrollTop) || 0));
    }
    function rememberScroll(name){
      if(TAB_ORDER.includes(name)) tabScroll[name] = readScrollY();
    }
    // Transition 的 @enter：离场动画结束后挂载、进场首帧（opacity 0）绘制前触发，
    // 此时复位滚动看不到跳动；子页（活动详情/报名/我的子页）一律从顶部开始。
    function applyPageScroll(y){
      try { window.scrollTo(0, y); } catch(e) {}
    }
    // 进场后图片/列表数据可能继续把文档撑高，刚才的目标位置会被高度夹住；内容到位后补齐。
    // 只补目标 > 0 的 tab，且用户一旦自己操作（触摸/滚轮/按键）立即放弃，最多补 1.2s。
    function retryPageScroll(targetPage, target){
      const started = Date.now();
      let aborted = false;
      const abort = () => { aborted = true; };
      const events = ['touchstart', 'wheel', 'keydown'];
      const cleanup = () => events.forEach((ev) => window.removeEventListener(ev, abort));
      events.forEach((ev) => window.addEventListener(ev, abort, {once: true, passive: true}));
      const tick = () => {
        if(aborted || page.value !== targetPage) { cleanup(); return; }
        if(readScrollY() >= target - 2) { cleanup(); return; }
        applyPageScroll(target);
        if(Date.now() - started < 1200) setTimeout(tick, 200); else cleanup();
      };
      setTimeout(tick, 120);
    }
    function onPageEnter(){
      const targetPage = page.value;
      const target = TAB_ORDER.includes(targetPage) ? (tabScroll[targetPage] || 0) : 0;
      applyPageScroll(target);
      if(target > 0 && readScrollY() < target - 2) retryPageScroll(targetPage, target);
    }
    // Vant 弹层（van-popup / van-dialog）锁滚动只给 body 加 van-overflow-hidden；
    // 本页 html 带 overflow-x: clip，按规范会阻止 body 的 overflow 传播到视口，
    // 弹层打开时背景仍会被滚轮/触摸带走。把同一个类同步到根元素锁住视口。
    function syncViewportScrollLock(){
      const locked = document.body.classList.contains('van-overflow-hidden');
      document.documentElement.classList.toggle('van-overflow-hidden', locked);
    }
    new MutationObserver(syncViewportScrollLock).observe(document.body, {attributes: true, attributeFilter: ['class']});
    syncViewportScrollLock();
    // 四 Tab 过渡：监听 page 切换实时选动画（需在 subPages 定义后，sync 确保与 Transition 同帧）
    watch(page, (nv, ov) => {
      if(nv !== ov){ pageTransition.value = pickTransition(nv, ov); rememberScroll(ov); }
    }, {flush: 'sync'});

    // 活动
    const activities = ref([]);
    const loadingActivities = ref(false);
    const activitiesLoaded = ref(false);
    const currentActivity = ref(null);
    const currentActivityId = ref('');
    const loadingActivityDetail = ref(false);
    // 现场签到三态（用户侧，R3 设计 §3.6）：已到 = 按时/迟到；未到/未签到 = 停用填志愿入口
    function isAttendancePresent(state) { return state === '按时' || state === '迟到'; }
    // 后端按时间派生「已结束」后，自助签到开关统一看 can_self_signin；
    // 后端未部署时退回旧字段判断（不含签到状态，签到展示另由 my_attendance 判断）。
    function isActivityEnded(act) { return !!(act && (act.status === '已结束' || act.status === '已取消')); }
    function canSelfSignin(act) {
      if (!act) return false;
      // status 是硬兜底：即使旧 payload/旧 fallback 仍带 signin_enabled，已结束也不放行。
      if (isActivityEnded(act)) return false;
      if (typeof act.can_self_signin === 'boolean') return act.can_self_signin;
      return !!(act.signin_enabled && act.my_signup);
    }
    // 现场签到入口：已签到的人仍显示「已签到」；已结束/已取消不再显示入口。
    function showSelfSigninFor(act) {
      if (!act || !act.my_signup || isActivityEnded(act)) return false;
      return canSelfSignin(act) || isAttendancePresent(act.my_attendance);
    }
    // 签到门禁：已签到不拦；活动要求签到但当前还不能签到（未到/已结束）才拦。
    function signinBeforeFillBlocked(act) {
      if (!act || !act.signin_enabled) return false;
      if (isAttendancePresent(act.my_attendance)) return false;
      if (isActivityEnded(act)) return true;   // 已结束：不再要求/允许现场签到后填志愿
      return !canSelfSignin(act);
    }
    const isDetailCheckedIn = computed(() => isAttendancePresent(currentActivity.value && currentActivity.value.my_attendance));
    const detailSigninBlocked = computed(() => signinBeforeFillBlocked(currentActivity.value));
    // 用户自助现场签到（4 位码，后端校验；已到幂等）
    const signinDialogShow = ref(false);
    const signinTargetAct = ref(null);
    const signinCode = ref('');
    const signinSubmitting = ref(false);
    const signinCodeOk = computed(() => /^\d{4}$/.test(signinCode.value) && !signinSubmitting.value);
    function openSelfSignin(act) {
      if (!act || signinSubmitting.value) return;
      if (!act.signin_enabled || !act.my_signup || isAttendancePresent(act.my_attendance)) return;
      if (!canSelfSignin(act)) return;
      signinTargetAct.value = act;
      signinCode.value = '';
      signinDialogShow.value = true;
    }
    function closeSigninDialog() {
      if (signinSubmitting.value) return;
      signinDialogShow.value = false;
    }
    async function submitSelfSignin() {
      if (!signinCodeOk.value || !signinTargetAct.value) return;
      const act = signinTargetAct.value;
      signinSubmitting.value = true;
      try {
        const data = await api('/api/activities/' + encodeURIComponent(act.record_id || act.activity_id) + '/self-signin',
          { method: 'POST', body: JSON.stringify({ code: signinCode.value }) });
        const attendance = data.attendance || '按时';
        act.my_attendance = attendance;
        if (currentActivity.value && currentActivity.value.record_id === act.record_id) {
          currentActivity.value.my_attendance = attendance;
        }
        signinDialogShow.value = false;
        vant.showToast('签到成功');
      } catch(e) {
        vant.showToast(e.message || '签到失败，请重试');
      } finally {
        signinSubmitting.value = false;
      }
    }
    // 详情页加载态：进入新活动/首次加载时显示 loading；同活动内部刷新保留现有内容
    const activityDetailLoading = computed(() => loadingActivityDetail.value
      && !(currentActivity.value && String(currentActivity.value.record_id) === String(currentActivityId.value)));
    const actionLoading = ref(false);
    // —— 页面级忙碌锁（P16）——
    // 整页动作（提交志愿/报名/保存资料/发送留言等）进入请求前置 busy、结束解除：
    // 期间全屏遮罩（.page-busy-mask，z-index 高于底栏与托盘）不可点穿/不可关闭；
    // guardLeave / handlePopState 命中 pageBusy（或页面自身 busy）时 → toast「正在提交，请稍候」并停留。
    const pageBusy = ref(false);
    const pageBusyText = ref('');
    function setPageBusy(text){ pageBusyText.value = text || '正在提交…'; pageBusy.value = true; }
    function clearPageBusy(){ pageBusy.value = false; pageBusyText.value = ''; }
    const activityDetailFrom = ref('home'); // 进入活动详情前的页面，用于返回

    // 喜欢卡片
    const cards = ref([]);
    const loadingCards = ref(false);
    const cardIndex = ref(0);
    const showSwipeGuide = ref(false); // 牵线左右滑动新手引导，仅首次显示一次
    const SWIPE_GUIDE_KEY = 'yxq_swipe_guide_v1';
    const showActivityPromo = ref(false);               // 牵线页活动报名引导弹窗
    const ACTIVITY_PROMO_KEY = 'yxq_activity_promo_v1'; // sessionStorage：本会话已弹过
    // 引导报名的活动ID：由 /api/home 按环境下发（活动ID是自动编号，测试库与生产库编号不同），
    // 未下发时保持空串，此时不弹——避免拿生产活动ID去测试库里空找。
    const promoActivityId = ref('');
    const promoPoster = ref('');                        // 弹窗顶部活动海报（活动没传海报时为空，只显示文字）
    const showUpdateNotes = ref(false);                 // 首页更新说明弹窗
    const UPDATE_NOTES_KEY = 'yxq_update_notes_v1';     // localStorage：本版本已看过。改文案就把 v1 bump 成 v2
    const cardsTotal = ref(0);
    const cardsVersion = ref(null);   // 服务端快照版本：变化则翻页序列失效需重拉
    const cardsLoadingMore = ref(false);
    const cardsStart = ref(0);        // 已载窗口在全序中的起始偏移（循环翻页窗口游标）
    const cardsPageCache = new Map(); // offset -> {cards,total,version}：翻页回退时避免重复请求
    // /legacy/api/home 会在无筛选时把本人卡置顶，而 /legacy/api/cards 不返回本人卡。
    // 清除筛选后重载走 cards，必须把 home 缓存下来的本人卡放回首位，并保持分页全序一致。
    const selfCard = ref(null);

    // ===== 客户端重载/恢复痕迹（只观测，不改变业务行为）=====
    // boot 必须在页面初始化时先快照：随后 loadHome 会把 cardIndex 归零，再读 localStorage
    // 就丢掉上次离开时的位置了。诊断信息一律走 localStorage（跨 WebView 重启保留）。
    const TX_URL = '/legacy/api/client-events';
    const TX_MAX_BATCH = 30;
    const TX_FLUSH_MS = 15000;
    const TX_ERR_LIMIT = 10;
    let txQueue = [];
    let txBootSent = false;
    let txDeckTimer = null;
    let txDeckLast = 0;
    let txErrTimes = [];
    function txLocalGet(k){ try{ return localStorage.getItem(k); }catch(e){ return null; } }
    function txLocalSet(k, v){ try{ localStorage.setItem(k, String(v)); }catch(e){} }
    function txNavType(){
      try{
        const e = (performance.getEntriesByType && performance.getEntriesByType('navigation')[0]);
        const t = e && e.type;
        if(t === 'navigate' || t === 'reload' || t === 'back_forward') return t;
      }catch(e){}
      return 'unknown';
    }
    function txSidInit(){
      try{
        const old = localStorage.getItem('yxq_client_sid');
        if(old) return old;
        let sid = '';
        try{
          if(typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') sid = crypto.randomUUID();
        }catch(e){}
        if(!sid) sid = 'sid-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10);
        localStorage.setItem('yxq_client_sid', sid);
        return sid;
      }catch(e){
        return 'sid-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10);
      }
    }
    function txBootBuild(){
      const now = Date.now();
      const lastSeen = Number(txLocalGet('yxq_last_seen') || 0);
      let prevReason = txLocalGet('yxq_reload_reason') || '';
      try{ localStorage.removeItem('yxq_reload_reason'); }catch(e){}
      if(prevReason && prevReason !== 'version' && prevReason !== 'unknown'){
        prevReason = prevReason.indexOf('version') === 0 ? 'version' : 'unknown';
      }
      const posRaw = txLocalGet('yxq_deck_pos');
      const posNum = posRaw === null ? NaN : Number(posRaw);
      const prevPos = Number.isFinite(posNum) && posNum >= 0 ? Math.trunc(posNum) : null;
      let cleanExit = false;
      try{
        cleanExit = localStorage.getItem('yxq_clean_exit') === 'true';
        localStorage.setItem('yxq_clean_exit', 'false');
      }catch(e){}
      txLocalSet('yxq_last_seen', now);
      return {
        nav: txNavType(),
        at: now,
        prev_reason: prevReason,
        since_boot: lastSeen > 0 ? Math.max(0, now - lastSeen) : null,
        prev_pos: prevPos,
        clean_exit: cleanExit
      };
    }
    const txSid = txSidInit();
    const txBoot = txBootBuild();
    function txCsrf(){
      try{ return (document.cookie.match(/(?:^|; )yxq_csrf=([^;]*)/) || [])[1] || ''; }catch(e){ return ''; }
    }
    function txSend(events){
      if(!events || !events.length || !user.value) return;
      let body = '';
      try{ body = JSON.stringify({sid: txSid, boot: txBoot, events: events}); }catch(e){ return; }
      // Beacon cannot attach X-CSRF-Token, so telemetry uses a keepalive fetch.
      try{
        const headers = {'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest'};
        const csrf = txCsrf();
        if(csrf) headers['X-CSRF-Token'] = csrf;
        fetch(TX_URL, {method: 'POST', headers, credentials: 'same-origin', body, keepalive: true}).catch(()=>{});
      }catch(e){}
    }
    function txQueueEvent(k, d){
      try{
        txQueue.push({t: Date.now(), k, d: d || {}});
        if(txQueue.length > 100) txQueue = txQueue.slice(-100);
        if(txQueue.length >= TX_MAX_BATCH) txFlush();
      }catch(e){}
    }
    function txFlush(){
      try{
        if(!txQueue.length || !user.value) return;
        txSend(txQueue.splice(0, TX_MAX_BATCH));
      }catch(e){}
    }
    function txMarkSeen(){
      try{ txLocalSet('yxq_last_seen', Date.now()); }catch(e){}
    }
    function txSendBoot(){
      try{
        if(txBootSent || !user.value) return;
        txBootSent = true;
        txSend([{t: Date.now(), k: 'boot', d: {nav: txBoot.nav, prev_reason: txBoot.prev_reason || ''}}]);
        txFlush();
      }catch(e){}
    }
    function txReportError(type, message){
      try{
        const now = Date.now();
        txErrTimes = txErrTimes.filter(ts => now - ts < 60000);
        if(txErrTimes.length >= TX_ERR_LIMIT) return;
        txErrTimes.push(now);
        txQueueEvent('error', {type, message: String(message == null ? '' : message).slice(0, 200)});
      }catch(e){}
    }
    setInterval(txFlush, TX_FLUSH_MS);
    setInterval(() => { try{ if(document.visibilityState === 'visible') txMarkSeen(); }catch(e){} }, 10000);
    watch([cardIndex, cardsStart], () => {
      try{
        const now = Date.now();
        const write = () => { try{ txLocalSet('yxq_deck_pos', cardsStart.value + cardIndex.value); }catch(e){} };
        if(now - txDeckLast >= 500){
          txDeckLast = now;
          write();
        } else if(!txDeckTimer){
          txDeckTimer = setTimeout(() => {
            txDeckTimer = null;
            txDeckLast = Date.now();
            write();
          }, 500 - (now - txDeckLast));
        }
      }catch(e){}
    });
    watch(page, (nv, ov) => {
      try{
        if(nv === ov) return;
        if(nv === 'cards' || ov === 'cards') txQueueEvent('page', {to: nv, from: ov});
      }catch(e){}
    });
    watch(user, (v) => { if(v) txSendBoot(); });
    window.addEventListener('pagehide', () => {
      try{
        txMarkSeen();
        txLocalSet('yxq_clean_exit', 'true');
        txSendBoot();
        txFlush();
      }catch(e){}
    });
    document.addEventListener('visibilitychange', () => {
      try{
        if(document.hidden){
          txMarkSeen();
          txLocalSet('yxq_clean_exit', 'true');
          txSendBoot();
          txFlush();
        } else {
          txLocalSet('yxq_clean_exit', 'false');
          txMarkSeen();
        }
      }catch(e){}
    });
    window.addEventListener('error', (e) => {
      try{ txReportError('error', e.message || (e.error && e.error.message) || 'error'); }catch(_){}
    });
    window.addEventListener('unhandledrejection', (e) => {
      try{
        const r = e.reason;
        txReportError('unhandledrejection', (r && (r.message || r)) || 'rejection');
      }catch(_){}
    });

    const photoIndex = ref(0);
    const photoSlideDir = ref('');
    const currentCard = computed(() => cards.value[cardIndex.value] || null);
    const currentMsgCount = computed(() => (currentCard.value && currentCard.value.msg_count) || 0);
    const currentPhotoUrl = computed(() => {
      if (!currentCard.value) return '';
      const photos = cardDisplayPhotos(currentCard.value);
      return photos[photoIndex.value] || '';
    });
    const currentPhotoList = computed(() => {
      if (!currentCard.value) return [];
      return cardDisplayPhotos(currentCard.value);
    });
    // 牌堆只解码当前与下一张；其余用同尺寸占位，避免首屏一次拉 9 张。
    function photoSrc(i) {
      if (i === photoIndex.value || i === photoIndex.value + 1) return currentPhotoList.value[i] || '';
      return '';
    }
    const photoLoading = ref(false);
    let photoLoadingTimer = null;
    const loadedPhotoSet = new Set();
    // 人脸对准：默认居中时人脸完整可见则不动；不完整才上下平移最小量（顶对齐/底对齐），不做居中
    const photoPositions = ref([]);
    // 人脸裁切纯计算：给定 faces 项与容器/原图尺寸，返回 objectPosition 值。
    // recalcPhotoPosition（真实卡）与壳内 hero 共用同一公式，避免两处各写一遍而漂移。
    function computeFaceCrop(f, cw, ch, iw, ih) {
      if (!f || !f.c || !cw || !ch || !iw || !ih) return '';
      try {
        const scale = Math.max(cw / iw, ch / ih);
        const dh = ih * scale;
        // 黑边：上下无用区比例，等效到「内容区坐标系」内计算，显示时把黑边排除出可视区
        const tb = (f.tb && f.tb.length === 2) ? f.tb : [0, 0];
        const ct = tb[0], cb = tb[1];
        const cr = 1 - ct - cb;             // 内容区占原图高比例
        if (cr < 0.55) return '';           // 有效内容过少（异常），放弃
        const eh = dh * cr;                 // 内容区显示高度
        const cy_e = (f.c[1] - ct) / cr;    // 脸中心在内容区坐标
        const fh = (f.box && f.box[1]) ? f.box[1] : 0;
        const fh_e = fh / cr;
        let off_e;                          // 内容区顶相对容器顶的偏移
        if (eh <= ch + 1) {
          off_e = (ch - eh) / 2;            // 内容区完整可见：内容区居中（黑边照片必须调整，否则露黑边）
        } else {
          const top_e = (cy_e - fh_e / 2) * eh;
          const bottom_e = (cy_e + fh_e / 2) * eh;
          const headTop_e = top_e - fh_e * 0.35 * eh; // 头顶（框顶上方留 35% 框高=头发+留白）
          const offDef_e = (ch - eh) / 2;
          // 内容区内「头顶~下巴」完整可见 → 内容区居中即可
          if (offDef_e + headTop_e >= 0 && offDef_e + bottom_e <= ch) off_e = offDef_e;
          else if (offDef_e + headTop_e < 0) off_e = -headTop_e;       // 头顶被裁 → 上移
          else if (offDef_e + bottom_e > ch) off_e = ch - bottom_e;    // 下巴被裁 → 下移
          else off_e = offDef_e;
          off_e = Math.max(ch - eh, Math.min(0, off_e));
        }
        let off = off_e - ct * dh;          // 换算回原图偏移
        off = Math.max(ch - dh, Math.min(0, off));
        const offDef = (ch - dh) / 2;
        if (Math.abs(off - offDef) < 1) return ''; // 几乎没动，保持默认
        return '50% ' + Math.round(off) + 'px';
      } catch (_) {
        return '';
      }
    }
    function recalcPhotoPosition(e, i, attempt) {
      attempt = attempt || 0;
      try {
        const f = (currentCard.value.faces || [])[i];
        const img = e.target;
        if (!f || !f.c || !img || !img.naturalWidth) return;
        const wrap = img.closest('.card-photo-wrap');
        if (!wrap) return;
        const cw = wrap.clientWidth, ch = wrap.clientHeight;
        if (!cw || !ch) {
          // 真机缓存图 @load 可能早于首帧布局，容器尺寸未就绪 → 稍后重试
          if (attempt < 8) setTimeout(() => recalcPhotoPosition(e, i, attempt + 1), 120);
          return;
        }
        const pos = computeFaceCrop(f, cw, ch, img.naturalWidth, img.naturalHeight);
        const arr = photoPositions.value.slice();
        const next = pos || '';
        // 值相等不写 ref：ResizeObserver/resize 会高频触发，避免无意义的渲染。
        if ((arr[i] || '') === next) return;
        if (next) arr[i] = next;
        else delete arr[i];   // 尺寸回到「默认居中即可」时清掉旧像素偏移
        photoPositions.value = arr;
      } catch (_) {}
    }
    // —— 人脸裁切重算：容器尺寸变化/工具栏伸缩/横竖屏/切卡动画/faces 晚到 ——
    let photoCropObserver = null;
    let photoCropRaf = 0;
    let photoCropSettleTimer = null;
    function recalcLoadedPhotoPositions() {
      try {
        const imgs = document.querySelectorAll('.swipe-card .card-photo-item');
        for (let i = 0; i < imgs.length; i++) {
          const img = imgs[i];
          if (img && img.complete && img.naturalWidth) recalcPhotoPosition({ target: img }, i);
        }
      } catch (_) {}
    }
    function schedulePhotoCropRecalc() {
      if (photoCropRaf) return;
      const raf = window.requestAnimationFrame || function (cb) { return setTimeout(cb, 16); };
      photoCropRaf = raf(() => {
        photoCropRaf = 0;
        recalcLoadedPhotoPositions();
      });
    }
    function bindPhotoCropObserver() {
      try {
        if (!('ResizeObserver' in window)) return;
        if (!photoCropObserver) photoCropObserver = new ResizeObserver(schedulePhotoCropRecalc);
        photoCropObserver.disconnect();
        const wraps = document.querySelectorAll('.swipe-card .card-photo-wrap');
        for (let i = 0; i < wraps.length; i++) photoCropObserver.observe(wraps[i]);
      } catch (_) {}
    }
    function onViewportCropChange() {
      try { if (window.__YXQ_SET_VH__) window.__YXQ_SET_VH__(); } catch (_) {}
      schedulePhotoCropRecalc();
      if (photoCropSettleTimer) clearTimeout(photoCropSettleTimer);
      // 浏览器地址栏/工具栏伸缩是渐进的：rAF 后尺寸仍在变，动画结束后再补一次。
      photoCropSettleTimer = setTimeout(schedulePhotoCropRecalc, 360);
    }
    function onViewportVisibility() {
      if (!document.hidden) onViewportCropChange();   // 回前台：工具栏/键盘状态可能已变
    }
    function onPhotoLoad(e) {
      try {
        const raw = e && e.target && e.target.getAttribute ? e.target.getAttribute('src') : '';
        if (raw) loadedPhotoSet.add(raw);
        if (e && e.target && e.target.src) loadedPhotoSet.add(e.target.src);
        if (currentPhotoUrl.value) loadedPhotoSet.add(currentPhotoUrl.value);
      } catch (_) {}
      clearTimeout(photoLoadingTimer);
      photoLoading.value = false;
    }
    function triggerPhotoLoading() {
      clearTimeout(photoLoadingTimer);
      photoLoading.value = false;
      const url = currentPhotoUrl.value;
      if (!url) return;
      if (loadedPhotoSet.has(url)) return;
      for (const s of loadedPhotoSet) { if (s.endsWith(url) || url.endsWith(s)) return; }
      try {
        const imgs = document.querySelectorAll('.card-photo-item');
        const img = imgs[photoIndex.value];
        if (img && img.complete && img.naturalWidth) { loadedPhotoSet.add(url); return; }
      } catch (_) {}
      photoLoadingTimer = setTimeout(() => { if (currentPhotoUrl.value === url) photoLoading.value = true; }, 160);
    }
    const likeMessage = ref('');
    const showLikeDialog = ref(false);
    const likeDialogTitle = ref('');
    const likeTarget = ref(null);  // 当前要喜欢的对象 {openid, nickname}
    const likeType = ref('匿名');  // 喜欢方式：匿名/实名
    const likeSubmitting = ref(false);  // 发送在途（弹窗按钮禁用、不可 before-close 关闭，P16）
    const showFeedbackDialog = ref(false);
    const feedbackReason = ref('已有对象');
    const feedbackText = ref('');
    const feedbackTarget = ref(null);  // 当前要反馈的对象 {openid, nickname}
    const feedbackSubmitting = ref(false);  // 提交在途（弹窗按钮禁用、不可 before-close 关闭，P16）
    const showSuggestionDialog = ref(false);
    const suggestionType = ref('功能建议');
    const suggestionText = ref('');
    const suggestionSubmitting = ref(false);
    const showInviteDialog = ref(false);
    const showAdminHelp = ref(false);
    const showReportDialog = ref(false);
    const reportTarget = ref(null);
    const reportReason = ref('骚扰/引战');
    const reportReasonText = ref('');
    const reportSubmitting = ref(false);
    const reportedMessages = ref([]);
    const loadingReported = ref(false);
    const reportedLoaded = ref(false);
    const reportedCount = ref(0);
    const adminCommands = [
      { title: "审核管理", items: [
        "【待审核】查看待审核用户",
        "【通过 U-xxx或姓名】审核通过该用户",
        "【拒绝/隐藏 U-xxx或姓名】拒绝/隐藏该用户",
        "【通知 U-xxx 内容】给用户发消息",
        "【用户统计】查看统计数据",
      ]},
      { title: "村情六处", items: [
        "【生成村情六处邀请码 N】批量生成村情六处邀请码",
        "【查看村情六处邀请码】查看邀请码使用状态",
      ]},
      { title: "分组活动", items: [
        "【开启分组功能 活动ID 开/关】控制「我的分组」入口",
        "【查看未提交 活动ID】查看未提交志愿的报名者",
        "【执行分组 活动ID 每组男 每组女 [轮次]】执行分组算法（轮次缺省为最新轮次+1）",
        "【清空分组结果 活动ID】清空该活动全部历史分组结果",
        "【分组状态 活动ID】查看分组进度",
        "【分组帮助】分组指令说明",
      ]},
      { title: "一对一", items: [
        "【开启一对一功能 活动ID 开/关】控制活动页/活动卡上的志愿与结果入口",
        "【查看未提交一对一 活动ID】查看未提交志愿的报名者",
        "【执行一对一 活动ID 女生场次 [轮次]】女生坐镇、男生上门：每位女生固定接待 N 位男生（N＝女生场次），全场 N×女生数 场由男生分摊上门（轮次缺省为最新轮次+1）",
        "【清空一对一结果 活动ID】清空该活动全部历史一对一结果",
        "【一对一状态 活动ID】查看一对一进度",
        "【一对一帮助】一对一指令说明",
      ]},
      { title: "系统", items: [
        "【重连】手动重连机器人",
        "【管理员帮助】查看本帮助",
      ]},
    ];
    const REGISTER_FORM_BASE = 'https://lcnz8zx7fjk4.feishu.cn/share/base/form/shrcnbUryFlARPYl8I60aIA4qAf';
    const registerFormUrl = ref(REGISTER_FORM_BASE);
    const inviteShareText = computed(() => {
      const uid = (user.value && user.value.user_id) || '';
      return uid
        ? `注册时，在“邀请人ID”里填：\n${uid}`
        : '注册时，在“邀请人ID”里填邀请人编号';
    });

    // ===== 我的麦穗（积分）=====
    // 余额在「我的」页入口就要显示，所以登录后静默拉一次；点进页面再拉全量（含明细）
    const pointsBalance = ref(0);
    const pointsEarned = ref(0);
    const pointsSpent = ref(0);
    const pointsPending = ref(0);      // 确认期内的穗：能看见但不能花，页面上必须写清
    const pointsPendingCount = ref(0);
    const pointsEntries = ref([]);
    const loadingPoints = ref(false);
    const pointsLoaded = ref(false);

    const pointsRules = ref({ items: [], config: {}, notes: [] }); // 结构固定，模板在加载完成前也能安全取 .items/.notes
    const pointsRulesLoaded = ref(false);
    const showPointsRules = ref(false);

    const showRedeemPanel = ref(false);
    const redeemItem = ref(null);      // null=还在选条目；非空=在填该条目的参数
    const redeemActivityId = ref('');
    const redeemTarget = ref(null);    // wish：选中的心愿对象 {openid, nickname, user_id}；提交时只取 openid
    const redeemCondition = ref('');   // matchmaker：牵线条件
    const redeemSubmitting = ref(false);

    // 心愿对象搜索（走 /api/points/wish-targets：只返回异性单身）
    const wishQuery = ref('');
    const wishTargets = ref([]);
    const loadingWishTargets = ref(false);
    let wishSearchTimer = null;
    let wishSearchLastQ = null; // 已拉过的关键词：进心愿步骤时会先主动拉一次空关键词，避免 watcher 再重复请求

    // 兑换记录
    const showRedemptions = ref(false);
    const redemptionsList = ref([]);
    const loadingRedemptions = ref(false);
    const redemptionsLoaded = ref(false);

    const inviteInfo = ref(null);
    const loadingInvite = ref(false);
    const inviteLoaded = ref(false);

    const inviteConfirmDays = computed(() => (pointsRules.value.config && pointsRules.value.config.invite_confirm_days) || 0);
    const inviteRewardFemale = computed(() => (pointsRules.value.config && pointsRules.value.config.invite_reward_female) || 0);
    const inviteRewardMale = computed(() => (pointsRules.value.config && pointsRules.value.config.invite_reward_male) || 0);
    const matchmakerConditionMax = computed(() => (pointsRules.value.config && pointsRules.value.config.matchmaker_condition_max_len) || 200);
    // 需要先选活动的条目；real_like 只是加一次名额，与活动无关
    const REDEEM_ACTIVITY_ITEMS = ['priority_signup', 'wish'];
    const redeemPanelItems = computed(() => (pointsRules.value.items || []));
    const redeemNeedsActivity = computed(() => !!(redeemItem.value && REDEEM_ACTIVITY_ITEMS.includes(redeemItem.value.item)));
    // 心愿限定「自己报了名的活动」（接口也会拦，这里只是别让人白填一遍）。
    // 其余活动类兑换项照旧全列——优先名额在没报名时用才是它的意义。
    const redeemActivityOptions = computed(() => {
      if (redeemItem.value && redeemItem.value.item === 'wish') return activities.value.filter(a => a.my_signup);
      return activities.value;
    });

    // 幂等键：兑换接口按 request_key 去重，连点/超时重试不会重复扣穗。
    // crypto.randomUUID 只在安全上下文可用（部分飞书 WebView 里没有），取不到就退回 Math.random 拼 32 位十六进制。
    function newRequestKey(){
      try {
        if (window.crypto && typeof window.crypto.randomUUID === 'function') return window.crypto.randomUUID().replace(/-/g, '');
      } catch(e) {}
      let s = '';
      for (let i = 0; i < 32; i++) s += Math.floor(Math.random() * 16).toString(16);
      return s;
    }

    // 「我的」页入口显示余额用：失败就安静算了，不打扰用户
    async function loadPointsBalance() {
      try { const data = await api('/api/points/me'); pointsBalance.value = data.balance || 0; }
      catch(e) {}
    }

    async function openPoints() {
      page.value = 'points';
      loadPointsRules(false); // 待确认那行要写清确认期几天，规则没到就先不写天数（已加载过不会重复请求）
      await loadPoints(false);
    }
    async function loadPoints(force) {
      if (pointsLoaded.value && !force) return;
      loadingPoints.value = true;
      try {
        const data = await api('/api/points/me');
        pointsBalance.value = data.balance || 0;
        pointsEarned.value = data.earned || 0;
        pointsSpent.value = data.spent || 0;
        pointsPending.value = data.pending_points || 0;
        pointsPendingCount.value = data.pending_count || 0;
        pointsEntries.value = data.entries || [];
        pointsLoaded.value = true;
      } catch(e) { vant.showToast(e.message); }
      loadingPoints.value = false;
    }

    async function loadPointsRules(force) {
      if (pointsRulesLoaded.value && !force) return;
      try { const data = await api('/api/points/rules'); pointsRules.value = { items: data.items || [], config: data.config || {}, notes: data.notes || [] }; pointsRulesLoaded.value = true; }
      catch(e) { vant.showToast(e.message); }
    }
    function openPointsRules() { showPointsRules.value = true; loadPointsRules(false); }

    // ===== 管理员·麦穗总览 =====
    const paLoading = ref(false);
    const paLedgerLoading = ref(false);
    const paStats = ref({ user_count: 0, entry_count: 0, earned: 0, spent: 0, outstanding: 0, redemption_count: 0, invite_count: 0 });
    const paConfig = ref([]);
    const paUsers = ref([]);
    const paLedger = ref([]);
    const paRedemptions = ref([]);
    const paInvites = ref([]);
    const paTab = ref(0);
    const paUserQuery = ref('');
    const paLedgerFilter = ref('');
    const paLedgerFilterNick = ref('');
    const paUserDetail = ref(null);
    const paUserDetailShow = ref(false);
    const paUserDetailLoading = ref(false);
    const paUserDetailOid = ref('');

    const paFilteredUsers = computed(() => {
      const q = paUserQuery.value.trim().toLowerCase();
      if (!q) return paUsers.value;
      return paUsers.value.filter(u =>
        (u.nickname || '').toLowerCase().includes(q) ||
        (u.user_id || '').toLowerCase().includes(q));
    });

    async function loadPaOverview() {
      const data = await api('/api/admin/points/overview');
      paStats.value = data.stats || paStats.value;
      paConfig.value = data.config || [];
      paUsers.value = data.users || [];
    }
    async function loadPaLedger() {
      const q = paLedgerFilter.value ? ('?user_oid=' + encodeURIComponent(paLedgerFilter.value)) : '';
      paLedgerLoading.value = true;
      try {
        const data = await api('/api/admin/points/ledger' + q);
        paLedger.value = data.rows || [];
      } finally { paLedgerLoading.value = false; }
    }
    async function loadPaRedemptions() {
      const data = await api('/api/admin/points/redemptions');
      paRedemptions.value = data.rows || [];
    }
    async function loadPaInvites() {
      const data = await api('/api/admin/points/invites');
      paInvites.value = data.rows || [];
    }

    async function openPointsAdmin() {
      page.value = 'points_admin';
      paTab.value = 0;
      paUserQuery.value = '';
      paLedgerFilter.value = '';
      paLedgerFilterNick.value = '';
      if (paLoading.value) return;
      paLoading.value = true;
      try {
        await Promise.all([loadPaOverview(), loadPaLedger(), loadPaRedemptions(), loadPaInvites()]);
      } catch(e) { vant.showToast(e.message); }
      paLoading.value = false;
    }
    async function paOpenUser(u) {
      paUserDetailOid.value = u.user_oid;
      paUserDetail.value = null;
      paUserDetailShow.value = true;
      paUserDetailLoading.value = true;
      try {
        paUserDetail.value = await api('/api/admin/points/user?user_oid=' + encodeURIComponent(u.user_oid));
      } catch(e) { vant.showToast(e.message); paUserDetailShow.value = false; }
      paUserDetailLoading.value = false;
    }
    function paShowLedger(oid, nick) {
      paLedgerFilter.value = oid;
      paLedgerFilterNick.value = nick;
      paUserDetailShow.value = false;
      paTab.value = 1;
      loadPaLedger().catch(e => vant.showToast(e.message));
    }
    function clearPaFilter() {
      paLedgerFilter.value = '';
      paLedgerFilterNick.value = '';
      loadPaLedger().catch(e => vant.showToast(e.message));
    }

    async function openRedemptions() {
      showRedemptions.value = true;
      if (redemptionsLoaded.value) return; // 已加载则直接展示缓存（兑完会置回 false 强制重拉）
      loadingRedemptions.value = true;
      try { const data = await api('/api/points/redemptions'); redemptionsList.value = data.list || []; redemptionsLoaded.value = true; }
      catch(e) { vant.showToast(e.message); }
      loadingRedemptions.value = false;
    }

    function openRedeemPanel() {
      redeemItem.value = null;
      showRedeemPanel.value = true;
      loadPointsRules(false);
      loadActivities(); // 复用活动页的缓存列表（已加载过就不会再请求）
    }
    function closeRedeemPanel() {
      showRedeemPanel.value = false;
      redeemItem.value = null;
      redeemActivityId.value = '';
      redeemTarget.value = null;
      redeemCondition.value = '';
      wishQuery.value = '';
      wishTargets.value = [];
      wishSearchLastQ = null;
    }
    // 心愿搜索：停顿 300ms 再打接口，边打字边发会把接口打满
    async function loadWishTargets() {
      const q = wishQuery.value.trim();
      wishSearchLastQ = q;
      loadingWishTargets.value = true;
      try { const data = await api('/api/points/wish-targets?q=' + encodeURIComponent(q)); wishTargets.value = data.list || []; }
      catch(e) { vant.showToast(e.message); }
      loadingWishTargets.value = false;
    }
    watch(wishQuery, () => {
      if (wishSearchTimer) clearTimeout(wishSearchTimer);
      wishSearchTimer = setTimeout(() => {
        if (wishQuery.value.trim() === wishSearchLastQ) return; // 这个关键词已经拉过了（进步骤时那次）
        loadWishTargets();
      }, 300);
    });
    function pickWishTarget(u) { redeemTarget.value = u; }
    function clearWishTarget() {
      redeemTarget.value = null;
      wishQuery.value = '';
      wishSearchLastQ = null; // 清空后回到「前 50 个」，重新拉一次
      loadWishTargets();
    }
    async function pickRedeemItem(it) {
      if (pointsBalance.value < it.cost) return; // 置灰行不响应，页面上已写明差多少
      if (it.item === 'real_like') {
        try {
          await vant.showConfirmDialog({ title: '兑换' + it.label, message: `消耗 ${it.cost} 穗，多一次实名喜欢机会，不过期。确定兑换吗？`, confirmButtonColor: '#ff6b81' });
        } catch(e) { return; } // 取消
        await submitRedeem(it);
        return;
      }
      redeemItem.value = it;
      // 进心愿步骤先把「前 50 个可选的人」铺出来，用户不搜也能直接点
      if (it.item === 'wish') { wishQuery.value = ''; wishTargets.value = []; wishSearchLastQ = null; loadWishTargets(); }
    }
    // 传 it 时用于「确认后直接兑换」（real_like），不传则取当前表单里的条目；返回 true=兑换成功
    async function submitRedeem(it) {
      const target = it || redeemItem.value;
      if (!target) return false;
      if (redeemNeedsActivity.value && !redeemActivityId.value) { vant.showToast('请先选择活动'); return false; }
      if (target.item === 'wish' && !redeemTarget.value) { vant.showToast('请选择心愿对象'); return false; }
      if (target.item === 'matchmaker' && !redeemCondition.value.trim()) { vant.showToast('请填写牵线条件'); return false; }
      redeemSubmitting.value = true;
      let ok = false;
      try {
        const body = { item: target.item, request_key: newRequestKey() };
        if (redeemNeedsActivity.value) body.activity_record_id = redeemActivityId.value;
        if (target.item === 'wish') body.target_open_id = redeemTarget.value.openid;
        if (target.item === 'matchmaker') body.condition = redeemCondition.value.trim();
        const data = await api('/api/points/redeem', { method: 'POST', body: JSON.stringify(body) });
        vant.showToast(data.message || '兑换成功');
        if (typeof data.balance === 'number') pointsBalance.value = data.balance;
        closeRedeemPanel();
        ok = true;
        redemptionsLoaded.value = false; // 兑换记录多了一条，下次打开重新拉
        await loadPoints(true); // 余额和明细都要跟着变
      } catch(e) { vant.showToast(e.message); }
      redeemSubmitting.value = false;
      return ok;
    }
    // 兑换面板「取消」/返回：有未提交内容 → 先走通用离开拦截
    function requestCloseRedeemPanel(){
      if(guardOverlay('redeemPanel', () => closeRedeemPanel())) return;
      closeRedeemPanel();
    }

    async function loadInviteInfo(force) {
      if (inviteLoaded.value && !force) return;
      loadingInvite.value = true;
      try { const data = await api('/api/points/invite'); inviteInfo.value = data; inviteLoaded.value = true; }
      catch(e) { vant.showToast(e.message); }
      loadingInvite.value = false;
    }
    // 打开邀请弹窗时带出进度和已邀请列表，数据必须是最新的
    watch(showInviteDialog, (v) => { if (v) loadInviteInfo(false); });
    // 兑换面板里要用活动列表；面板打开时兜底加载一次（loadActivities 自带「已加载不重复请求」）
    watch(showRedeemPanel, (v) => { if (v) loadActivities(); });

    // 喜欢我的
    const likesData = ref({ liked_me: [], mutual: [] });
    const likesInitialized = ref(false);
    // 本会话内已成功喜欢（未取消）的目标：本地即时排除/置灰，覆盖后端快照刷新的时间窗
    const justLiked = new Set();
    const lastLikeAt = ref(0);

    const loadingLikes = ref(false);

    // 我的喜欢
    const myLikesList = ref([]);
    const loadingMyLikes = ref(false);
    const myLikesCount = ref(0);
    const myLikesLoaded = ref(false);

    // 我的活动
    const myActivitiesList = ref([]);
    const loadingMyActivities = ref(false);
    const myActivitiesLoaded = ref(false);

    // 我的分组
    const myGroupsList = ref([]);
    const loadingMyGroups = ref(false);
    const myGroupsLoaded = ref(false);
    const hasGroupEntry = ref(false); // 是否显示「我的分组」入口（报名过且该活动开启分组功能）

    // 我的资料
    const profileData = ref({});
    const profileLoaded = ref(false);
    const loadingProfileDetail = ref(false);
    const showIdInvalidDialog = ref(false);
    const idInvalidCountdown = ref(5);
    let idInvalidTimer = null;
    const showFaceInvalidDialog = ref(false);
    const faceInvalidCountdown = ref(5);
    let faceInvalidTimer = null;
    // 我的资料-分组展示：各组是否有内容（无内容则不显示整卡）
    const hasProfileBasic = computed(() => !!(profileData.value.real_name || profileData.value.baptismal_name || profileData.value.birthday || profileData.value.height || profileData.value.education || profileData.value.city || profileData.value.native_place || profileData.value.church || profileData.value.group || profileData.value.church_location));
    const hasProfileWork = computed(() => !!(profileData.value.industry || profileData.value.position || profileData.value.income || profileData.value.house || profileData.value.driving));
    const hasProfileAbout = computed(() => !!(profileData.value.personality || profileData.value.self_traits || profileData.value.hobbies || profileData.value.self_sports || profileData.value.mbti || profileData.value.live_with_parents || profileData.value.family));
    const hasProfilePartner = computed(() => !!(profileData.value.partner_criteria || profileData.value.partner_traits || profileData.value.partner_hobbies || profileData.value.partner_sports));
    const hasProfileContact = computed(() => !!(profileData.value.wechat || profileData.value.phone || profileData.value.register_time));

    // 修改资料
    const editableFields = ref([]);
    const showUnsavedConfirm = ref(false);
    let pendingLeaveCb = null;             // 被拦截的离开动作（弹窗确认后执行）
    const pendingGuard = ref(null);        // 触发弹窗的守卫项（页面级/浮层级），两钮分派按它走
                                           // （必须是 ref：unsavedConfirmBody 按它做响应式求值，普通 let 会被 computed 永久缓存）
    let profileEditSnapshot = '';
    function _editFingerprint(){
      const cur = {};
      editableFields.value.forEach(f => {
        let v = f.value;
        if (Array.isArray(v)) v = v.join('|');
        if (v === null || v === undefined) v = '';
        cur[f.field] = String(v);
      });
      return JSON.stringify(cur);
    }
    function isProfileEditDirty(){
      if(page.value !== 'profile_edit') return false;
      try { return _editFingerprint() !== profileEditSnapshot; } catch(e){ return false; }
    }
    const unsavedConfirmBody = computed(() => (pendingGuard.value && pendingGuard.value.body)
      || (page.value === 'group_select' ? '志愿还没提交，离开后本次修改不会保存。' : '资料已修改，是否保存？'));
    // —— 未保存离开拦截（守卫注册表：页面级 leaveGuards + 浮层级 overlayGuards，共用同一弹窗与历史挂载）——
    // 每项约定：dirty() 判脏（与进页/进浮层 baseline 快照比对）；discard(cb) 放弃本次修改并归位快照、执行原离开；
    // 弹窗恒为「放弃/继续编辑」两钮，不提供代保存/代发送：sent 型浮层（like/留言/反馈/举报/认领/兑换）离开动作不触发对外发送，
    // 页面级（资料编辑/志愿选择）也不提供保存快捷——保存/提交走页面自身的「保存 / 提交志愿」钮。
    // 卡片留言输入（cards 页）：切卡/切 tab/顶部与系统返回/应用筛选前拦截未发送草稿；
    // 页面级（leaveGuards.cards）与浮层级（switchCard 等）共用同一守卫项。
    const cardsMsgGuard = {
      dirty: () => page.value === 'cards' && !!msgText.value.trim(),
      body: '留言还没发送，离开后内容不会保存。',
      discard: (cb) => { msgText.value = ''; replyTarget.value = null; if(cb) cb(); }
      // sent 型场景不代发送：发送只走留言面板自身的发送钮
    };
    const leaveGuards = {
      profile_edit: {
        dirty: () => isProfileEditDirty(),
        discard: (cb) => {
          try { profileEditSnapshot = _editFingerprint(); } catch(e){}
          if(cb) cb(); else page.value = 'profile_detail';
        }
      },
      group_select: {
        dirty: () => isGroupSelectDirty(),
        busy: () => actionLoading.value,   // 提交执行中：顶部/系统返回 → 提示并停留（P15）
        discard: (cb) => {
          gsMarkClean();
          if(cb) cb(); else leaveGroupSelectByFrom();
        }
      },
      cards: cardsMsgGuard
    };
    // —— 浮层守卫注册表（overlayGuards）——
    // 拦截点：van-dialog before-close（取消/遮罩）、van-popup @click-overlay/取消钮、popstate 对应分支、页面内关闭函数。
    let filterBaselineKey = null;            // 筛选面板「已应用」基线（打开/应用/清除时对齐，放弃时恢复）
    let gaRunBaselineKey = '';               // 分组执行弹层「每组人数」进层基线
    function gaRunKey(){ return JSON.stringify([Number(gaRunM.value), Number(gaRunF.value)]); }
    function restoreGaRunBaseline(){
      try {
        const a = JSON.parse(gaRunBaselineKey);
        if(Array.isArray(a) && a.length === 2){ gaRunM.value = a[0]; gaRunF.value = a[1]; }
      } catch(e){}
    }
    function restoreFilterBaseline(){
      try { if(filterBaselineKey !== null) filter.value = JSON.parse(filterBaselineKey); } catch(e){}
    }
    const overlayGuards = {
      cardsMsg: cardsMsgGuard,
      likeDialog: {
        dirty: () => !!likeMessage.value.trim(),
        busy: () => likeSubmitting.value,   // 发送执行中：关闭/返回 → 提示并停留（P16）
        body: '留言还没发送，离开后内容不会保存。',
        discard: (cb) => { likeMessage.value = ''; if(cb) cb(); else showLikeDialog.value = false; }
      },
      onetooneMsg: {
        dirty: () => !!onetooneMsgText.value.trim(),
        busy: () => onetooneMsgSubmitting.value,   // 发送执行中：关闭/返回 → 提示并停留（P16）
        body: '留言还没发送，离开后内容不会保存。',
        discard: (cb) => { onetooneMsgText.value = ''; if(cb) cb(); else showOnetooneMsg.value = false; }
      },
      feedbackDialog: {
        dirty: () => !!feedbackText.value.trim() || feedbackReason.value !== '已有对象',
        busy: () => feedbackSubmitting.value,   // 提交执行中：关闭/返回 → 提示并停留（P16）
        body: '反馈还没提交，离开后内容不会保存。',
        discard: (cb) => { feedbackReason.value = '已有对象'; feedbackText.value = ''; if(cb) cb(); else showFeedbackDialog.value = false; }
      },
      reportDialog: {
        dirty: () => !!reportReasonText.value.trim() || reportReason.value !== '骚扰/引战',
        busy: () => reportSubmitting.value,   // 提交执行中：关闭/返回 → 提示并停留（P16）
        body: '举报还没提交，离开后内容不会保存。',
        discard: (cb) => { reportReason.value = '骚扰/引战'; reportReasonText.value = ''; if(cb) cb(); else showReportDialog.value = false; }
      },
      suggestionDialog: {
        dirty: () => !!suggestionText.value.trim() || suggestionType.value !== '功能建议',
        busy: () => suggestionSubmitting.value,   // 提交执行中：关闭/返回 → 提示并停留（P16）
        body: '反馈内容还没提交，离开后内容不会保存。',
        discard: (cb) => { suggestionType.value = '功能建议'; suggestionText.value = ''; if(cb) cb(); else showSuggestionDialog.value = false; }
      },
      claim: {
        dirty: () => !!(claimName.value.trim() || claimPhone.value.trim()),
        busy: () => claiming.value,   // 认领执行中：返回 → 提示并停留（P16）
        body: '认领信息还没提交，离开后需要重新填写。',
        discard: (cb) => { claimName.value = ''; claimPhone.value = ''; if(cb) cb(); else backToLogin(); }
      },
      redeemPanel: {
        dirty: () => !!redeemItem.value && (!!redeemCondition.value.trim() || !!wishQuery.value.trim() || !!redeemTarget.value),
        busy: () => redeemSubmitting.value,   // 兑换执行中：关闭/返回 → 提示并停留（P16）
        body: '兑换信息还没提交，离开后需要重新填写。',
        discard: (cb) => { if(cb) cb(); else closeRedeemPanel(); }
      },
      filter: {
        dirty: () => showFilter.value && filterBaselineKey !== null && JSON.stringify(filter.value) !== filterBaselineKey,
        body: '筛选条件还没应用，离开后本次修改不会保留。',
        discard: (cb) => { restoreFilterBaseline(); if(cb) cb(); else showFilter.value = false; }
        // 筛选为「两钮」场景：无保存语义，要应用的走面板里的「应用筛选」
      },
      gaRun: {
        dirty: () => gaRunPopupShow.value && gaRunKey() !== gaRunBaselineKey,
        body: '每组人数已修改，离开后本次修改不会保留。',
        discard: (cb) => { restoreGaRunBaseline(); if(cb) cb(); else gaRunPopupShow.value = false; }
        // 执行会启动后端任务，弹窗不代执行；要执行的走弹层里的「确认执行」
      },
      gaOtoRun: {
        dirty: () => gaOtoRunShow.value && String(gaOtoRunFemaleChats.value || '').trim() !== '',
        body: '女生场次已填写，离开后输入不会保留。',
        discard: (cb) => { gaOtoRunFemaleChats.value = ''; if(cb) cb(); else gaOtoRunShow.value = false; }
        // 同上：执行走弹层里的「确认执行」，弹层关闭不提供保存
      }
    };
    // 页面级统一入口：命中脏守卫 → 挂待执行回调并开弹窗（history 挂载交给 overlay watch），返回 true=已拦截；
    // busy（提交执行中）→ 提示并停留（P15：防提交中误触返回）。
    function guardLeave(doLeave){
      if(showUnsavedConfirm.value) return false;   // 弹窗已开：由调用方走「只关弹窗」分支
      const g = leaveGuards[page.value];
      // 页面级忙碌（pageBusy）或页面自身 busy（如志愿提交 actionLoading）→ 提示并停留（P15/P16 同口径）
      if(pageBusy.value || (g && g.busy && g.busy())){ vant.showToast('正在提交，请稍候'); return true; }
      if(!g) return false;
      if(!g.dirty()) return false;
      pendingGuard.value = g;
      pendingLeaveCb = doLeave;
      showUnsavedConfirm.value = true;
      return true;
    }
    // 浮层版统一入口：命中脏守卫 → 拦截本次关闭/离开（调用方不得继续关闭流程），返回 true=已拦截。
    function guardOverlay(key, doLeave){
      if(showUnsavedConfirm.value) return false;   // 弹窗已开：由调用方走「只关弹窗」分支
      // 页面级忙碌（pageBusy）：浮层/浮层性动作（如切卡键盘操作）也统一拦截（与 guardLeave 同口径）
      if(pageBusy.value){ vant.showToast('正在提交，请稍候'); return true; }
      const g = overlayGuards[key];
      if(!g) return false;
      if(g.busy && g.busy()){ vant.showToast('正在提交，请稍候'); return true; }
      if(!g.dirty()) return false;
      pendingGuard.value = g;
      pendingLeaveCb = doLeave;
      showUnsavedConfirm.value = true;
      return true;
    }
    function cancelLeaveConfirm(){
      showUnsavedConfirm.value = false;
      pendingLeaveCb = null;
      pendingGuard.value = null;
    }
    function discardLeaveConfirm(){
      const g = pendingGuard.value;
      const cb = pendingLeaveCb;
      pendingLeaveCb = null;
      pendingGuard.value = null;
      showUnsavedConfirm.value = false;
      if(g){ g.discard(cb); }
    }
    const showSelectPopup = ref(false);
    const showMultiPopup = ref(false);
    const pickerField = ref(null);
    const savingProfile = ref(false);
    const photoInput = ref(null);
    const uploadingPhoto = ref(false);
    const uploadProgress = ref(0);
    const uploadStage = ref(''); // '' | 'compress' | 'upload' | 'process'
    const faceChecking = ref(false); // 上传后人脸检测中：此时红字隐藏，显示检测中
    const profilePhotos = ref([]);
    const profilePhotosThumb = ref([]);
    function syncProfilePhotos() {
      // 同步头像（第一张照片，用缩图）与全局 user，保证保存后首页/消息头像一致
      profileData.value.photo = profilePhotosThumb.value[0] || profilePhotos.value[0] || '';
      if (user.value) user.value.photo = profileData.value.photo;
    }
    function openProfileEdit() {
      editableFields.value = (profileData.value.editable || []).map(f => ({ ...f, value: Array.isArray(f.value) ? [...f.value] : f.value }));
      profilePhotos.value = (profileData.value.photos && profileData.value.photos.length)
        ? [...profileData.value.photos]
        : (profileData.value.photo ? [profileData.value.photo] : []);
      profilePhotosThumb.value = (profileData.value.photos_thumb && profileData.value.photos_thumb.length)
        ? [...profileData.value.photos_thumb]
        : [...profilePhotos.value];
      try { profileEditSnapshot = _editFingerprint(); } catch(e){ profileEditSnapshot = ''; }
      pendingLeaveCb = null;
      pendingGuard.value = null;
      page.value = 'profile_edit';
    }
    function pickPhoto() { if (uploadingPhoto.value) return; if (photoInput.value) photoInput.value.click(); }
    function pollFaceResult() {
      // 上传/删除后 has_face 仍 false 时轮询最终态，达标即隐去红字；最多约20秒
      faceChecking.value = true;
      let facePolls = 0;
      const faceTimer = setInterval(async()=>{
        facePolls++;
        try{
          const d=await api('/api/profile');
          if(typeof d.has_face==='boolean'){ profileData.value.has_face=d.has_face; }
          if(d.has_face===true || facePolls>=10){ clearInterval(faceTimer); faceChecking.value=false; }
        }catch(_){
          if(facePolls>=10){ clearInterval(faceTimer); faceChecking.value=false; }
        }
      }, 2000);
    }
    // 最佳实践：前端 canvas 预压缩至 1080 + XHR 进度 + 分段文案；HEIC 交给后端转换
    function isHEICFile(file) {
      const type = (file.type||'').toLowerCase();
      const name = (file.name||'').toLowerCase();
      return type==='image/heic' || type==='image/heif' || name.endsWith('.heic') || name.endsWith('.heif');
    }
    async function compressImage(file, maxWidth=1080, quality=0.82) {
      // HEIC/HEIF 无法用 canvas 解码；直接上传原文件，由后端 heif-convert 处理。
      if (isHEICFile(file)) return file;
      try {
        const img = await new Promise((res, rej)=>{
          const u = URL.createObjectURL(file);
          const im = new Image();
          im.onload = ()=>{ URL.revokeObjectURL(u); res(im); };
          im.onerror = ()=>{ URL.revokeObjectURL(u); rej(new Error('图片解码失败')); };
          im.src = u;
        });
        let w = img.naturalWidth, h = img.naturalHeight;
        if (w<=maxWidth && h<=maxWidth) return file;
        // 等比缩放至最长边 maxWidth
        let nw=w, nh=h;
        if (w>h) { nw=maxWidth; nh=Math.round(h*maxWidth/w); } else { nh=maxWidth; nw=Math.round(w*maxWidth/h); }
        const canvas = document.createElement('canvas');
        canvas.width=nw; canvas.height=nh;
        const ctx = canvas.getContext('2d');
        // JPEG has no alpha; fill white first so transparent PNG/WebP pixels
        // do not become black before the server's JPEG normalization.
        ctx.fillStyle = '#ffffff';
        ctx.fillRect(0, 0, nw, nh);
        ctx.drawImage(img, 0, 0, nw, nh);
        const blob = await new Promise(r=> canvas.toBlob(r, 'image/jpeg', quality));
        if (!blob || blob.size>=file.size) return file;
        return new File([blob], (file.name||'photo.jpg').replace(/\.\w+$/, '.jpg'), {type:'image/jpeg'});
      } catch(e) { return file; }
    }
    function uploadWithProgress(url, formData) {
      return new Promise((resolve, reject)=>{
        const xhr = new XMLHttpRequest();
        xhr.open('POST', url, true);
        xhr.withCredentials = true;
        xhr.setRequestHeader('X-Requested-With', 'XMLHttpRequest');
        const csrf = (document.cookie.match(/(?:^|; )yxq_csrf=([^;]*)/) || [])[1] || '';
        if (csrf) xhr.setRequestHeader('X-CSRF-Token', csrf);
        xhr.upload.onprogress = (e)=>{
          if (e.lengthComputable) {
            const pct = Math.round(e.loaded*100/e.total);
            // 映射到 50-85 区间，避免小文件直接 100%
            uploadProgress.value = 50 + Math.round(pct*0.35);
          }
        };
        xhr.onload = ()=>{
          try {
            const ct = xhr.getResponseHeader('Content-Type')||'';
            const data = ct.includes('application/json') ? JSON.parse(xhr.responseText||'{}') : {};
            if (xhr.status>=200 && xhr.status<300) resolve(data);
            else reject(new Error(data.error||'上传失败，请稍后重试'));
          } catch(err){ reject(err); }
        };
        xhr.onerror = ()=> reject(new Error(NETWORK_ERROR_MESSAGE));
        xhr.ontimeout = ()=> reject(new Error('上传超时，请重试'));
        xhr.timeout = 60000;
        xhr.send(formData);
      });
    }
    async function onPhotoChange(e) {
      const file = e.target.files && e.target.files[0];
      if (uploadingPhoto.value) { e.target.value = ''; return; }   // 上传在途：不叠加第二次上传
      if (!file) return;
      if ((!file.type || !file.type.startsWith('image/')) && !isHEICFile(file)) { vant.showToast('请选择图片文件'); return; }
      if (file.size > 10 * 1024 * 1024) { vant.showToast('图片不能超过10MB'); return; }
      uploadingPhoto.value = true; uploadProgress.value = 0; uploadStage.value = 'compress';
      try {
        // 1) 前端预压缩（降低上行 3-5 倍，2.5s→0.6s）
        uploadProgress.value = 5;
        const compressed = await compressImage(file);
        uploadProgress.value = 45;
        uploadStage.value = 'upload';
        uploadProgress.value = 50;
        const fd = new FormData();
        fd.append('file', compressed, compressed.name||file.name);
        // 2) 带进度上传
        const data = await uploadWithProgress('/api/profile/photo', fd);
        uploadStage.value = 'process';
        uploadProgress.value = 90;
        profilePhotos.value = data.photos || [];
        profilePhotosThumb.value = (data.photos_thumb && data.photos_thumb.length) ? data.photos_thumb : profilePhotos.value;
        profileData.value.photos = profilePhotos.value;
        profileData.value.photos_thumb = profilePhotosThumb.value;
        if (typeof data.has_face === 'boolean') profileData.value.has_face = data.has_face;
        syncProfilePhotos();
        vant.showToast(data.has_face===false ? '已上传，人脸检测中…' : '照片已添加');
        uploadProgress.value = 100;
        // 若服务端仍异步，轮询 has_face 最终态，达标即隐去红字
        if (data.has_face===false) pollFaceResult();
      } catch (err) { vant.showToast(err.message || '上传失败'); }
      uploadingPhoto.value = false; uploadStage.value=''; uploadProgress.value=0;
      e.target.value = '';
    }
    async function deletePhoto(i) {
      if (i < 0 || i >= profilePhotos.value.length) return;
      if (uploadingPhoto.value) { vant.showToast('照片处理中，请稍候'); return; }   // 并发照片操作按索引互踩：在途时禁点
      // 删除会同时删掉 R2 对象，不可恢复；最后一张还会触发人脸门禁，必须二次确认
      const isLast = profilePhotos.value.length <= 1;
      let message;
      if (isLast) {
        message = '这是最后一张照片，删除后将无法使用（需重新上传本人清晰照片并通过审核）。删除后无法恢复。';
      } else if (i === 0) {
        message = '第一张是头像与牵线封面，删除后会自动顺延下一张。删除后无法恢复。';
      } else {
        message = '删除后无法恢复。';
      }
      try {
        await vant.showConfirmDialog({
          title: '删除这张照片？',
          message,
          confirmButtonText: '删除',
          cancelButtonText: '取消',
          confirmButtonColor: '#ee0a24',
        });
      } catch (e) {
        return;   // 取消/关闭
      }
      // 避免 forbidClick 永久遮罩：用轻量 loading，不阻塞点击
      uploadingPhoto.value = true; uploadStage.value='process'; uploadProgress.value=30;
      let done=false;
      const tid = setTimeout(()=>{ if(!done){ vant.showToast('删除较慢，请稍候…'); } }, 2000);
      try {
        const data = await api('/api/profile/photo', { method: 'DELETE', body: JSON.stringify({ index: i }) });
        done=true; clearTimeout(tid);
        profilePhotos.value = data.photos || [];
        profilePhotosThumb.value = (data.photos_thumb && data.photos_thumb.length) ? data.photos_thumb : profilePhotos.value;
        profileData.value.photos = profilePhotos.value;
        profileData.value.photos_thumb = profilePhotosThumb.value;
        if (typeof data.has_face === 'boolean') profileData.value.has_face = data.has_face;
        syncProfilePhotos();
        vant.showToast('已删除');
        if (data.has_face===false) pollFaceResult();
      } catch (err) {
        done=true; clearTimeout(tid);
        vant.showToast(err.message || '删除失败');
      } finally {
        done=true; clearTimeout(tid);
        uploadingPhoto.value = false; uploadStage.value=''; uploadProgress.value=0;
      }
    }
    async function setCover(i) {
      if (i <= 0 || i >= profilePhotos.value.length) return;
      if (uploadingPhoto.value) { vant.showToast('照片处理中，请稍候'); return; }   // 与删除/上传共用同一互斥态：在途时禁点
      try {
        const data = await api('/api/profile/photo/cover', { method: 'POST', body: JSON.stringify({ index: i }) });
        profilePhotos.value = data.photos || [];
        profilePhotosThumb.value = (data.photos_thumb && data.photos_thumb.length) ? data.photos_thumb : profilePhotos.value;
        profileData.value.photos = profilePhotos.value;
        profileData.value.photos_thumb = profilePhotosThumb.value;
        syncProfilePhotos();
        vant.showToast('已设为封面');
      } catch (err) { vant.showToast(err.message || '操作失败'); }
    }
    function openPicker(f) { pickerField.value = f; showSelectPopup.value = true; }
    function openMultiPicker(f) { pickerField.value = f; showMultiPopup.value = true; }
    function pickSelect(opt) { if (pickerField.value) pickerField.value.value = opt; showSelectPopup.value = false; }
    function clearPickerField() { if (pickerField.value) pickerField.value.value = ''; showSelectPopup.value = false; }
    function toggleMulti(opt) {
      if (!pickerField.value) return;
      if (!Array.isArray(pickerField.value.value)) pickerField.value.value = [];
      const arr = pickerField.value.value;
      const i = arr.indexOf(opt);
      if (i >= 0) arr.splice(i, 1); else arr.push(opt);
    }
    const showPhonePasswordDialog = ref(false);
    const phonePassword = ref('');
    const phonePasswordError = ref('');
    const phonePasswordBusy = ref(false);
    function _normalizePhone(v){ return String(v == null ? '' : v).replace(/\D/g, ''); }
    function _profileField(field){ return editableFields.value.find(f => f.field === field) || null; }
    // 手机号是登录账号：归一化后与当前号码不同才算「改了手机号」，此时后端强制当前密码。
    function phoneChangePending(){
      const f = _profileField('手机号');
      if (!f) return false;
      const next = _normalizePhone(f.value);
      const current = _normalizePhone(profileData.value.phone);
      return !!next && next !== current;
    }
    async function saveProfile() {
      if (savingProfile.value) return;
      if (uploadingPhoto.value) { vant.showToast('照片还在处理中，请稍候'); return; }   // 照片在途：保存会让页面与服务端状态对不上
      if (phoneChangePending()) {
        phonePassword.value = '';
        phonePasswordError.value = '';
        showPhonePasswordDialog.value = true;
        return;
      }
      await _submitProfile('');
    }
    function cancelPhonePassword() {
      if (phonePasswordBusy.value || savingProfile.value) return;
      showPhonePasswordDialog.value = false;
      phonePassword.value = '';
      phonePasswordError.value = '';
    }
    async function confirmPhonePassword() {
      if (phonePasswordBusy.value || savingProfile.value) return;
      const pw = String(phonePassword.value || '');
      if (!pw) { phonePasswordError.value = '请输入当前密码'; return; }
      phonePasswordError.value = '';
      await _submitProfile(pw);
    }
    async function _submitProfile(currentPassword) {
      const payload = {};
      editableFields.value.forEach(f => { payload[f.field] = f.value; });
      if (currentPassword) payload.current_password = currentPassword;
      savingProfile.value = true;
      if (currentPassword) phonePasswordBusy.value = true;
      setPageBusy('保存中…');   // 页面级锁：保存在途期间页面不可改（P16）
      try {
        await api('/api/profile', { method: 'PUT', body: JSON.stringify(payload) });
        vant.showToast('保存成功');
        showPhonePasswordDialog.value = false;
        phonePassword.value = '';
        phonePasswordError.value = '';
        try { profileEditSnapshot = _editFingerprint(); } catch(e){}
        profileLoaded.value = false;
        profileFormDone = true;      // 让 page watcher 用 replace 回 profile_detail，不新增返回层
        await openProfileDetail();   // 成功后重拉资料（手机号等以服务端为准）
      } catch(e) {
        const code = (e && e.code) ? e.code : '';
        if (currentPassword && code === 'password_invalid') {
          phonePasswordError.value = '当前密码不正确，请重试';
        } else if (currentPassword && code === 'password_required') {
          phonePasswordError.value = '请输入当前密码';
        } else if (code === 'password_not_set') {
          showPhonePasswordDialog.value = false;
          vant.showToast('该账号尚未设置密码，请联络管理员修改手机号');
        } else if (code === 'phone_taken') {
          showPhonePasswordDialog.value = false;
          vant.showToast(e.message || '该手机号已被其他账号使用');
        } else if (currentPassword && code === 'rate_limited') {
          phonePasswordError.value = e.message || '密码尝试过于频繁，请稍后再试';
        } else if (currentPassword && showPhonePasswordDialog.value) {
          phonePasswordError.value = e.message || '保存失败，请重试';
        } else {
          vant.showToast(e.message || '保存失败，请重试');
        }
      } finally {
        savingProfile.value = false;
        phonePasswordBusy.value = false;
        clearPageBusy();
      }
    }

    // 分组
    const candidates = ref([]);
    const selectedChoices = ref([]);
    const candidateQuery = ref('');
    const visibleCandidates = computed(() => {
      const q = (candidateQuery.value||'').trim().toLowerCase();
      if (!q) return candidates.value;
      return candidates.value.filter(c => (c.nickname||'').toLowerCase().includes(q)
        || (c.user_id||'').toLowerCase().includes(q)
        || (c.baptismal_name||'').toLowerCase().includes(q));
    });
    const loadingCandidates = ref(false);
    const candidatesError = ref(false);
    // 慢加载提示（F-01）：>3s 补一句话、>10s 给重试钮；计时器在成功/失败/离开时统一清理
    const gsSlowLoading = ref(false);
    const gsVerySlowLoading = ref(false);
    let gsCandReq = 0;
    let gsSlowTimer = null, gsVerySlowTimer = null;
    function clearGsLoadTimers() {
      if (gsSlowTimer) { clearTimeout(gsSlowTimer); gsSlowTimer = null; }
      if (gsVerySlowTimer) { clearTimeout(gsVerySlowTimer); gsVerySlowTimer = null; }
    }
    const groupResult = ref({});
    const groupResultLoading = ref(false);
    const myGender = computed(() => user.value?.gender || '');
    const groupSelectFrom = ref('my_groups');   // 进入分组选择前的页面，用于返回
    const groupResultFrom = ref('my_groups');   // 进入分组结果前的页面，用于返回
    // 志愿选择重设计：底部操作栏（调整次序/提交志愿）/ 调整次序托盘 / 未保存离开拦截
    const showGsTray = ref(false);
    const gsReorderBtnEl = ref(null);     // 「调整次序」按钮（选人飞入落点 / 落定后脉冲）
    const gsSigninBlocked = ref(false);   // 签到被拦（403/503）：整页阻断卡
    const gsPrefillNotice = ref(false);   // 预填提示条（可关）
    const gsUnlistedCount = ref(0);       // 历史志愿中不在当前候选池的人数（P9）
    let gsBaseline = '';                  // 进页预填完成/提交成功/放弃时的快照（dirty 口径）
    let gsTouchedOids = new Set();        // 加载窗口内用户手动动过的 openid（P10 合并口径）
    let gsTouchStartY = null;             // 10px 位移防误触：touchstart 位置
    let gsSuppressClickUntil = 0;         // touchend 后抑制合成 click，防双触发
    const gsCountText = computed(() => {
      const n = selectedChoices.value.length;
      if (n === 0) return '已选 0/7 · 需选满 7 人';
      if (n === 7) return '已选 7/7 · 已满';
      return '已选 ' + n + '/7 · 还需选 ' + (7 - n) + ' 人';
    });
    const gsCanSubmit = computed(() => {
      if (actionLoading.value) return false;
      if (selectedChoices.value.length !== 7) return false;   // 必须恰好 7 人
      return true;
    });

    // 一对一
    const onetooneResult = ref({});
    const onetooneResultLoading = ref(false);
    const onetooneResultFrom = ref('my_activities');   // 进入一对一结果前的页面，用于返回
    const onetooneMsgTarget = ref(null);
    const onetooneMsgText = ref('');
    const onetooneMsgSubmitting = ref(false);  // 发送在途（弹窗按钮禁用、不可 before-close 关闭，P16）
    const showOnetooneMsg = ref(false);

    // 全屏图片
    const showPhotoViewer = ref(false);
    // 大图打开遮罩：点击后立即出现，PhotoSwipe loadComplete/loadError/close/超时后隐藏。
    const viewerLoading = ref(false);
    let viewerLoadingTimer = null;
    let photoSwipePrefetchStarted = false;
    // PhotoSwipe 在图片 loading 期间 close/destroy 不会移除根节点；用 seq 标记本次实例，
    // 取消后立即隐藏根节点，并让 in-flight 回调识别过期实例。
    let viewerPendingSeq = 0;
    let viewerCancelledSeq = 0;
    let viewerAbortTimers = [];

    // 进入牵线前的页面（喜欢完最后一人后返回）
    const prevPage = ref('likes');

    // 消息-动态
    const notifications = ref([]);
    // 未读角标：SSE 推送 / 轮询兜底 / 进入消息页时更新；0 时隐藏
    const unreadCount = ref(0);
    const unreadBadge = computed(() => unreadCount.value > 99 ? '99+' : (unreadCount.value > 0 ? String(unreadCount.value) : ''));

    // 站内嵌入页（仅管理后台）：同源 iframe + 返回栏，不整页跳出 SPA
    const embedOpen = ref(false);
    const embedUrl = ref('');
    const embedTitle = ref('');
    const embedLoading = ref(false);
    const embedReady = ref(false);
    const embedFrame = ref(null);
    const embedFailed = ref(false);      // iframe 载入失败/被拦截：显示兜底面板，不留白屏
    const embedTargetUrl = ref('');      // 重试时回到的原 URL
    let embedWatchdog = null;

    // 报名名单
    const signupSheetShow = ref(false);
    const signupList = ref([]);
    const loadingSignups = ref(false);
    const signupCache = ref({});  // activity_id -> [{...}] 报名名单缓存
    // 报名信息展示开关：false=隐藏「已报名人数/查看报名名单」（用户看不到任何报名信息）；解除隐藏时改为 true
    const showSignupInfo = ref(false);

    // 他人资料页
    const userDetail = ref(null);
    const userDetailFrom = ref('home');
    const userDetailFromSheet = ref(false); // 是否从报名名单弹层进入他人资料页，用于返回时重开名单
    // 纯阅览入口（志愿选择/分组结果/必聊结果）：只看资料与照片，不显示喜欢/点赞
    const userDetailHideActions = computed(() => ['group_select', 'group_result', 'onetoone_result'].includes(userDetailFrom.value));
    // 他人资料页「喜欢TA」按钮门禁：以后端 can_like 为准（单身异性、非自己、未喜欢过）；
    // 老数据无该字段时降级为本地判断（观察员/自己/同性/性别缺失一律隐藏）。
    const canLikeUserDetail = computed(() => {
      const u = userDetail.value;
      const me = user.value;
      if (!u || !me) return false;
      if (typeof u.can_like === 'boolean') return u.can_like;
      if (isObserver.value) return false;
      if (u.is_self) return false;
      if (u.user_id && me.user_id && u.user_id === me.user_id) return false;
      if (!u.gender || !me.gender) return false;
      return u.gender !== me.gender;
    });

    // ===== 点赞 =====
    // 和「喜欢」是两条独立的线：不配对、不解锁聊天、不发穗、不占爱心额度。
    // 状态一次性从 /api/praise/me 拿全（今天还能点几个 + 我点过谁），
    // **不在卡片列表里逐张查**——那个接口是打开最频繁的页面，
    // 「喜欢」为它专门做了一套快照，点赞没必要去拖慢它。
    const praiseState = ref(null);   // null = 还没拿到（没配置 / 拉取失败）
    const praiseBusy = ref(false);
    const praisedSet = computed(() => new Set((praiseState.value && praiseState.value.praised) || []));
    const praiseReady = computed(() => !!praiseState.value);
    const praiseLeft = computed(() => (praiseState.value && praiseState.value.left) || 0);
    const praiseReceivedWeek = computed(() => (praiseState.value && praiseState.value.received_week) || 0);
    // 卡片页：卡片列表已经按性别筛过了，门禁和 ♥ 保持一致，再加一条
    // 「已喜欢过就不显示点赞按钮」（需求第 9 条）。
    const canPraiseCard = computed(() => {
      const c = currentCard.value;
      if (!c || !c.openid || isObserver.value || c.is_self) return false;
      return !c.liked && praiseReady.value;
    });
    const cardPraised = computed(() => !!currentCard.value && praisedSet.value.has(currentCard.value.openid));
    // 他人资料页：liked 为真时走 💕，不再显示点赞。
    const canPraiseDetail = computed(() => {
      const u = userDetail.value;
      if (!u || !u.openid || isObserver.value || u.is_self || u.liked) return false;
      return praiseReady.value;
    });
    async function loadPraiseState() {
      try {
        praiseState.value = await api('/api/praise/me');
      } catch (e) {
        // 表没配（503）/ 服务异常：整块按钮不出现就行。这里弹提示只会
        // 让每个用户开机就看到一条看不懂的错误——这是配置问题，不是他的问题。
        praiseState.value = null;
      }
    }
    async function togglePraise(u) {
      if (!u || !u.openid || praiseBusy.value) return;
      const wasPraised = praisedSet.value.has(u.openid);
      // 今日额度已用完：直接弹限额说明，别先弹「点赞成功」再被后端 400 打回。
      if (!wasPraised && praiseReady.value && praiseLeft.value <= 0) {
        const limit = (praiseState.value && praiseState.value.limit) || 0;
        vant.showToast(`今天已经点满 ${limit} 个赞了，明天再来`);
        return;
      }
      praiseBusy.value = true;

      // 先有反应再记数据：立刻翻转按钮状态并弹提示，网络请求放后台跑。
      // 失败就回滚——用「先快后准」换掉「点了半天没动静」。
      const prev = praiseState.value;
      const limit = (prev && prev.limit) || 0;
      const used = Math.max(0, limit - ((prev && prev.left) || 0));
      const nextUsed = wasPraised ? Math.max(0, used - 1) : used + 1;
      praiseState.value = {
        ...(prev || {}),
        praised: wasPraised
          ? ((prev && prev.praised) || []).filter(o => o !== u.openid)
          : [...((prev && prev.praised) || []), u.openid],
        used: nextUsed,
        left: Math.max(0, limit - nextUsed),
      };
      // 资料页的 praised 由后端单独算，就地同步，免得返回再进还是旧的
      if (userDetail.value && userDetail.value.openid === u.openid) {
        userDetail.value = { ...userDetail.value, praised: !wasPraised };
      }
      vant.showToast(wasPraised ? '已取消点赞' : '点赞成功');

      try {
        const r = wasPraised
          ? await api('/api/praise/' + encodeURIComponent(u.openid), { method: 'DELETE' })
          : await api('/api/praise', { method: 'POST', body: JSON.stringify({ target_openid: u.openid }) });
        praiseState.value = { ...(praiseState.value || {}), ...r };
      } catch (e) {
        praiseState.value = prev;
        if (userDetail.value && userDetail.value.openid === u.openid) {
          userDetail.value = { ...userDetail.value, praised: wasPraised };
        }
        vant.showToast(e.message);
      } finally {
        praiseBusy.value = false;
      }
    }

    // 触摸滑动（卡片/图片分离）

    const BASE_PATH = '/legacy';
    // ===== 会话失效（401）：先出 App 内提示，用户点了才跳登录页 =====
    // 旧行为是 401 直接 location.href='/login?next=/'，用户操作中途会突然闪整页进度条。
    const sessionExpiredOpen = ref(false);
    let sessionPromptAt = 0;
    function notifySessionExpired(){
      // 管理台 iframe 内沿用原有处理（gaAuthBack / onEmbedLoad），不在这里弹
      if (nativeAdmin.value) return;
      if (sessionExpiredOpen.value) return;
      if (Date.now() - sessionPromptAt < 300000) return;   // 关掉后 5 分钟内不重复弹
      sessionExpiredOpen.value = true;
      sessionPromptAt = Date.now();
      vant.showConfirmDialog({
        title: '登录已过期',
        message: '登录状态已失效，请重新登录后继续。',
        confirmButtonText: '重新登录',
        cancelButtonText: '稍后',
        confirmButtonColor: '#ff6b81'
      }).then(() => {
        sessionExpiredOpen.value = false;
        goLogin();
      }).catch(() => { sessionExpiredOpen.value = false; });
    }
    function goLogin(){ location.href = '/login?next=/'; }   // 登录页是服务端页面，必须整页
    // 401 已经用对话框提示时，不再叠一条「未登录」toast（会盖住对话框按钮）
    const toastRaw = vant.showToast.bind(vant);
    vant.showToast = function(message, options){
      const text = (message && typeof message === 'object') ? message.message : message;
      if (sessionExpiredOpen.value && String(text || '').trim() === '未登录') return;
      return toastRaw(message, options);
    };
    function handleApiAuthError(err, options){
      if (!err) return;
      if (err.code === 'id_card_required') {
        // 服务端 P0 门禁：任何被拦的 API 都回到身份证补填浮层，由服务端事实驱动。
        try { startIdInvalidOverlay(); } catch(e) {}
        return;
      }
      if (err.status !== 401) return;
      if (options && options.suppressAuthPrompt) return;
      notifySessionExpired();
    }
    // 断网/连接被重置时浏览器只给英文原文（Failed to fetch / Load failed / TypeError），
    // 用户看不懂；网络层失败统一成中文，业务错误（服务端 error 文案）原样保留。
    const NETWORK_ERROR_MESSAGE = '网络不稳定，请稍后重试';
    function isNetworkError(e){
      if (!e) return false;
      if (e.network === true) return true;
      if (e.name === 'TypeError') return true;   // fetch 网络层失败的典型形态
      const m = String(e.message || '');
      return m.includes('Failed to fetch') || m.includes('Load failed') || m.includes('NetworkError');
    }
    function asNetworkError(e){
      if (!isNetworkError(e)) return e;
      const err = new Error(NETWORK_ERROR_MESSAGE);
      err.network = true;
      err.cause = e;
      return err;
    }
    async function api(url, options = {}) {
      // 单次请求；每次重新读 CSRF、新建 controller/超时，避免复用已 abort 的 controller。
      const attempt = async () => {
        const csrf = (document.cookie.match(/(?:^|; )yxq_csrf=([^;]*)/) || [])[1] || '';
        const baseHeaders = { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' };
        if (csrf) baseHeaders['X-CSRF-Token'] = csrf;
        const controller = new AbortController();
        // 管理读端点最慢 p99 约 10s（fresh 读多表），15s 上限会把最终成功的请求误判为超时；
        // 放宽到 30s 覆盖 p99.9；超时直接报错，不参与 GET 重试。
        const timeout = setTimeout(()=> controller.abort(), 30000);
        let resp;
        try {
          resp = await fetch(BASE_PATH + url, {
            credentials: 'same-origin',
            headers: { ...baseHeaders, ...(options.headers||{}) },
            signal: controller.signal,
            ...options
          });
        } catch(e) {
          clearTimeout(timeout);
          if (e.name==='AbortError') {
            const err = new Error('请求超时，请检查网络后重试');
            err.timeout = true;
            throw err;
          }
          // fetch 网络层失败（DNS/连接重置/断网），打标记供 GET 重试判断；文案统一中文
          const err = asNetworkError(e);
          throw err;
        }
        clearTimeout(timeout);
        const contentType = resp.headers.get('Content-Type') || '';
        if (!contentType.includes('application/json')) {
          // 反代超时等会返回 HTML；带 status/nonJson 标记供 GET 重试判断
          const err = new Error('服务器返回了非预期的响应，请稍后重试');
          err.status = resp.status;
          err.nonJson = true;
          throw err;
        }
        const data = await resp.json();
        if (!resp.ok) {
          // 附带 code/status，供调用方按语义分流（如签到闸门 SIGNIN_REQUIRED/503）
          const err = new Error(data.error || '请求失败');
          err.code = data.code || '';
          err.status = resp.status;
          throw err;
        }
        return data;
      };

      const method = String(options.method || 'GET').toUpperCase();
      try {
        return await attempt();
      } catch(first) {
        // 仅 GET 重试一次；网络错误、502/503/504、非 JSON 响应可重试，超时不重试。
        const retryable = first.network === true
          || first.nonJson === true
          || first.status === 502 || first.status === 503 || first.status === 504;
        if (method !== 'GET' || first.timeout || !retryable) {
          handleApiAuthError(first, options);
          throw first;
        }
        await new Promise(r => setTimeout(r, 300));
        try {
          return await attempt();
        } catch(second) {
          // 二次失败仍抛第一次的错误，保留原文案/status/code
          handleApiAuthError(first, options);
          throw first;
        }
      }
    }

    // 登录（app_id 由后端注入：生产/测试服各自不同）
    const FEISHU_APP_ID = '__FEISHU_APP_ID__';
    // 当前前端版本（后端按 index.html mtime 注入），用于发现新版本后自动刷新
    const APP_VERSION = window.__YXQ_VERSION__ || 'legacy';
    function isInFeishu() {
      const ua = navigator.userAgent.toLowerCase();
      return ua.includes('lark') || ua.includes('feishu');
    }
    // 飞书网页免登：静默获取预授权码（无需用户点授权）
    // 硬超时兜底：飞书 JSSDK 在个别账号（如纯个人版、无团队身份）下可能既不回调
    // success 也不回调 fail，若不兜底会一直停在“正在登录”，直到 WebView 整体超时报错。
    function getFeishuSilentCode(timeoutMs) {
      return new Promise((resolve) => {
        let settled = false;
        const done = (r) => { if (!settled) { settled = true; clearTimeout(timer); resolve(r); } };
        const timer = setTimeout(() => done({code: null, error: '免登回调超时'}), timeoutMs || 8000);
        let waited = 0;
        const tryGetCode = () => {
          const tt = window.tt || window.h5sdk;
          if (!tt) {
            if (waited < 3000) { waited += 200; setTimeout(tryGetCode, 200); return; }
            done({code: null, error: 'JSSDK未加载'}); return;
          }
          // 优先用 requestAuthCode（兼容性最好），失败再试 requestAccess
          if (typeof tt.requestAuthCode === 'function') {
            tt.requestAuthCode({
              appId: FEISHU_APP_ID,
              success: (res) => done({code: (res && res.code) ? res.code : null, error: null}),
              fail: (err) => {
                if (typeof tt.requestAccess === 'function') {
                  tt.requestAccess({
                    appID: FEISHU_APP_ID,
                    scopeList: [],
                    success: (res2) => done({code: (res2 && res2.code) ? res2.code : null, error: null}),
                    fail: (err2) => done({code: null, error: JSON.stringify(err2)})
                  });
                } else {
                  done({code: null, error: JSON.stringify(err)});
                }
              }
            });
          } else if (typeof tt.requestAccess === 'function') {
            tt.requestAccess({
              appID: FEISHU_APP_ID,
              scopeList: [],
              success: (res) => done({code: (res && res.code) ? res.code : null, error: null}),
              fail: (err) => done({code: null, error: JSON.stringify(err)})
            });
          } else {
            done({code: null, error: '无可用免登API'});
          }
        };
        tryGetCode();
      });
    }
    // 用 code 换取登录态，返回 'ok' | 'need_register' | 'error'
    async function loginWithCode(code) {
      try {
        const data = await api('/api/auth/feishu?code=' + code);
        user.value = data.user;
        user.value.anon_left = data.user.anon_left || 0;
        user.value.real_left = data.user.real_left || 0;
        hist.replace({page: page.value}, window.location.pathname);
        await loadHome();
        restoreDeck();
        return 'ok';
      } catch (e) {
        if (e.message.includes('尚未注册')) {
          // 未注册：展示「预报名认领」面板（微信/浏览器先填过表的用户）；新用户走机器人注册
          loggingIn.value = false;
          loginError.value = true;
          showClaim.value = true;
          return 'need_register';
        }
        return 'error';
      }
    }
    // 预报名认领：重新取飞书 code，连同 手机号+姓名 提交后端合并到预填记录；返回 true=认领成功
    async function submitClaim() {
      const name = (claimName.value || '').trim();
      const phone = (claimPhone.value || '').replace(/\D/g, '');
      if (name.replace(/\s+/g, '').length < 2) { vant.showToast('请输入注册时填写的真实姓名'); return false; }
      if (phone.length !== 11 || !phone.startsWith('1')) { vant.showToast('请输入正确的 11 位手机号'); return false; }
      if (!isInFeishu()) { vant.showToast('请在飞书 App 里打开本页面后再认领'); return false; }
      if (claiming.value) return false;
      claiming.value = true;
      try {
        const silent = await getFeishuSilentCode(6000);
        if (!silent || !silent.code) { vant.showToast('飞书登录凭证获取失败，请点「重新登录」再试'); return false; }
        const data = await api('/api/auth/claim', {
          method: 'POST',
          body: JSON.stringify({ code: silent.code, phone: claimPhone.value, name: claimName.value })
        });
        user.value = data.user;
        user.value.anon_left = data.user.anon_left || 0;
        user.value.real_left = data.user.real_left || 0;
        hist.replace({page: page.value}, window.location.pathname);
        showClaim.value = false; loginError.value = false;
        await loadHome();
        restoreDeck();
        if (data.auto_signed_up) { vant.showToast('认领成功，已为你报名活动 🎉'); }
        else if (data.auto_signed_note) {
          vant.showToast((data.claimed ? '认领成功' : '登录成功') + '；' + data.auto_signed_note);
        }
        else { vant.showToast(data.claimed ? '认领成功' : '登录成功'); }
        return true;
      } catch (e) {
        vant.showToast(e.message || '认领失败，请重试');
        return false;
      } finally {
        claiming.value = false;
      }
    }
    function backToLogin() {
      showClaim.value = false; claimName.value = ''; claimPhone.value = '';
    }
    // 认领面板「返回」：有未提交输入 → 先走通用离开拦截
    function requestBackToLogin(){
      if(guardOverlay('claim', () => backToLogin())) return;
      backToLogin();
    }
    async function doLogin() {
      loggingIn.value = true; loginError.value = false; loginTip.value = ''; showPersonalGuide.value = false; showClaim.value = false;

      // 机器人「认领」链接 ?claim=1：直接展示预报名认领面板，提交时才走飞书免登
      if (claimEntryFlag) {
        loggingIn.value = false; loginError.value = false; loginErrorMessage.value = ''; showClaim.value = true;
        return;
      }

      // 1. 已有会话（cookie 有效则直接进）
      try {
        await loadHome({ suppressAuthPrompt: true });
        loggingIn.value = false;
        restoreDeck();
        return;
      } catch (e) {}

      // 2. 无会话：交给 v2 登录页，不再走飞书免登
      goLogin();
      return;

      // 2b. 飞书内：网页免登（静默，不弹授权页）；偶发抖动时自动重试，最多 3 次
      if (isInFeishu()) {
        let loggedInOk = false;
        for (let attempt = 0; attempt < 3; attempt++) {
          loginTip.value = attempt === 0 ? '飞书登录中...' : `网络较慢，正在自动重试（第 ${attempt + 1}/3 次）...`;
          const silentResult = await getFeishuSilentCode(6000);
          if (silentResult && silentResult.code) {
            const r = await loginWithCode(silentResult.code);
            if (r === 'ok' || r === 'need_register') { loggedInOk = true; break; }
          }
          if (attempt < 2) await new Promise(res => setTimeout(res, 700));
        }
        if (loggedInOk) { loggingIn.value = false; return; }
        // 多次免登仍失败：显示错误 + 个人版引导（纯个人版无团队身份会走到这里）
        loginError.value = true;
        showPersonalGuide.value = true;
        loginErrorMessage.value = '多次登录失败。若你使用的是飞书个人版（尚未创建或加入团队），网页版需要团队身份，请按下方指引创建免费团队后再进入。';
        loggingIn.value = false;
        return;
      }

      // 3. 外部浏览器且无会话：提示重新登录
      loginError.value = true; loggingIn.value = false;
    }
    function retryLogin() { showPersonalGuide.value = false; loginError.value = false; doLogin(); }

    // 启动成功后的公共收尾：入口深链、点赞状态、SSE 红点与证件/人脸检查。
    // 已登录启动与 doLogin 成功两条路径都走这里，避免行为分叉。
    function afterHomeReady(entryHash) {
      if (!user.value) return;
      if (nativeAdminRequested) {
        nativeAdminRequested = false;
        if (!user.value.can_admin) { location.replace('/'); return; }   // 普通用户退回首页（管理台 iframe 内）
        nativeAdmin.value = true;
        nativeAdminEntryPage = 'group_admin';   // 目前只支持活动管理入口
        nativeAdminEntryPending = true;
        page.value = nativeAdminEntryPage;
        return;   // 管理页不启动通知/引导/身份证人脸门禁
      }
      if (entryHash === '#/likes') { page.value = 'likes'; }
      else if (entryHash === '#/activities') { page.value = 'home'; }
      loadPraiseState();   // 点赞的今日剩余 / 我点过谁，和切到卡片页时同一份
      startNotificationSync();   // SSE 实时红点；不可用时 12s 轮询兜底
      setTimeout(()=>{ checkIdValidForPage(page.value); }, 1000);
      setTimeout(()=>{ checkFaceValidForPage(page.value); }, 1200);
      // ?admin=1（直接访问或站内跳转而来）：先清 query，再由后台自动开内嵌管理台。
      // replaceState 必须在 openEmbed 之前：openEmbed 会 push 一条 overlay 历史条目。
      if (adminEntryFlag) {
        adminEntryFlag = false;
        try{ hist.replace({page: page.value}, location.pathname); }catch(e){}
        if (user.value.can_admin) openAdminConsole();
      }
    }
    // 首屏首个长任务收尾标记：mount 开始 → 卡片页 DOM 已 patch。
    function markBootRendered(){
      try{
        if(!performance.mark) return;
        performance.mark('yxq-boot-end');
        performance.measure('yxq-boot', 'yxq-mount-start', 'yxq-boot-end');
      }catch(e){}
    }
    // 已有服务端会话时直接拉首页数据：先展示 App 骨架，不经过登录页/iframe。
    async function bootAuthenticated() {
      bootError.value = '';
      gateMessage.value = '';   // 重试时先清门禁文案：审核通过后要能正常进站
      bootLoading.value = true;
      try {
        await loadHome();
        // 403 门禁在 loadHome 内写入 gateMessage 后正常返回，交给 gate 页展示。
        if (gateMessage.value) { bootLoading.value = false; return; }
        // 服务端会话有效但没返回账号资料：不落回登录页，停在可重试错误页。
        if (!user.value) {
          bootLoading.value = false;
          bootError.value = '无法加载账号资料，请重试';
          return;
        }
        bootLoading.value = false;
        nextTick(markBootRendered);
        restoreDeck();   // 刷新后回到原卡片（与 doLogin 成功路径一致）
        afterHomeReady(bootEntryHash);
      } catch (e) {
        if (e && (e.status === 401 || e.message === '未登录')) {
          // 会话在启动时已失效：api() 会弹「登录已过期」提示；这里停在未登录页，
          // 由用户点提示里的按钮再跳登录页（不自动整页跳转）。
          bootLoading.value = false;
          return;
        }
        // 非特定文案的 403（如「账号目前无法使用」）同样走 gate 页，不误报成网络错误。
        if (e && e.status === 403) {
          gateMessage.value = e.message || '账号目前无法使用，请联络管理员';
          bootLoading.value = false;
          return;
        }
        bootLoading.value = false;
        bootError.value = (e && e.message) || '加载失败，请稍后重试';
      }
    }
    function retryBoot() { bootAuthenticated(); }

    // 一次拉取首页全部数据（我的信息 + 卡片 + 喜欢 + 活动）
    async function loadHome(opts) {
      window.__YXQ_LOAD_HOME_STARTED__ = true;
      let data = null;
      if (window.__YXQ_HOME_PROMISE__) {
        try { data = await window.__YXQ_HOME_PROMISE__; } catch (e) { data = null; }
        window.__YXQ_HOME_PROMISE__ = null;
      }
      try {
        if (!data) data = await api('/api/home', opts || {});
      } catch (e) {
        // 账号状态门禁（待审核/审核不通过/已退出）：整屏提示，不当作登录失败
        if (e.message && (e.message.includes('审核中') || e.message.includes('未通过审核') || e.message.includes('已暂时退出'))) {
          gateMessage.value = e.message;
          return;
        }
        throw e;
      }
      user.value = data.user;
      if (data.user) loadPointsBalance(); // 麦穗余额：入口单元格要显示数字，静默拉，失败不打扰
      if (data.user) loadNotifications(); // 登录后立即载入未读数，消息 tab 红点不等首次切页
      if (data.user && data.user.is_admin) setTimeout(()=>{ try{ refreshReportedCount(); }catch(e){} }, 600);
      if (data.user && data.user.can_admin) setTimeout(()=>{ try{ refreshAdminBadge(); }catch(e){} }, 900);
      if (data.register_form_url) registerFormUrl.value = data.register_form_url;
      if (data.promo_activity_id) promoActivityId.value = data.promo_activity_id;
      const homeCards = data.cards || [];
      const homeSelf = homeCards.find(c => c && c.is_self);
      if (homeSelf) selfCard.value = homeSelf;
      cards.value = homeCards; cardIndex.value = 0; photoIndex.value = 0;
      cardsTotal.value = (typeof data.cards_total === 'number' ? data.cards_total : cards.value.length);
      cardsVersion.value = null;
      const homeLikes = data.likes || { liked_me: [], mutual: [] };
      likesData.value = {
        liked_me: homeLikes.liked_me || [],
        mutual: normalizeMutualItems(homeLikes.mutual),
      };
      activities.value = data.activities || [];
      triggerPhotoLoading();
      schedulePreloadAround();
      maybeShowSwipeGuide();
      maybeShowActivityPromo();
      maybeShowUpdateNotes();
    }

    // 活动
    // 时间字段来自 legacy 视图：start_time/end_time 为 "2026-10-17 09:00" 或「待定」。
    function activityListTime(act){
      const start = String((act && act.start_time) || '').trim();
      if (!start || start === '待定') return '🗓️ 活动时间待定';
      return '🗓️ ' + start + ' 开始';
    }
    function activityRangeText(act){
      const start = String((act && act.start_time) || '').trim();
      const end = String((act && act.end_time) || '').trim();
      const startOk = start && start !== '待定';
      const endOk = end && end !== '待定';
      if (!startOk && !endOk) return '待定';
      if (!startOk) return '结束 ' + end;
      if (!endOk) return start + ' 开始';
      if (start === end) return start;
      return start + ' – ' + end;
    }
    // 每次进入活动页都拉一次：活动状态（分组/一对一）由 bot 写入，缓存会让卡片显示
    // 过期的「填志愿/看结果」入口；已有数据时静默替换、不闪加载态，并发去重
    async function loadActivities() {
      if (loadingActivities.value) return;
      loadingActivities.value = true;
      try { const data = await api('/api/activities'); activities.value = data.activities; activitiesLoaded.value = true; }
      catch(e) { if (!activitiesLoaded.value) vant.showToast(e.message); }
      loadingActivities.value = false;
    }
    // 进入活动详情：先切页显示 loading，不等接口返回（接口慢时用户点了卡片却像没反应）
    let activityDetailReq = 0;
    async function openActivity(id) {
      // 记录来路，用于返回（从活动详情内部刷新时不重置）
      const noReset = ['activity_detail', 'group_select', 'group_result'];
      if (!noReset.includes(page.value)) activityDetailFrom.value = page.value;
      currentActivityId.value = id;
      const req = ++activityDetailReq;
      page.value = 'activity_detail';
      loadingActivityDetail.value = true;
      try {
        const data = await api('/api/activities/' + id);
        if (req !== activityDetailReq) return; // 已有更新的进入请求，丢弃过期响应
        currentActivity.value = data;
      } catch(e) {
        if (req !== activityDetailReq) return;
        vant.showToast(e.message);
        // 新活动没加载出来：退回上一页，不停留在空详情页（同活动刷新失败保留原内容）
        const same = currentActivity.value && String(currentActivity.value.record_id) === String(currentActivityId.value);
        if (!same && page.value === 'activity_detail') handleBack();
      } finally {
        if (req === activityDetailReq) loadingActivityDetail.value = false;
      }
    }
    // 报名缴费弹窗：收费活动先展示微信缴费二维码，确认缴费后才真正报名；免费活动直接报名
    const showPayDialog = ref(false);
    function onSignupClick() {
      const fee = Number(currentActivity.value?.fee || 0);
      if (fee > 0) {
        showPayDialog.value = true;
      } else {
        doSignup();
      }
    }
    async function confirmPay() {
      showPayDialog.value = false;
      await doSignup();
    }
    async function doSignup() {
      if (actionLoading.value) return;
      actionLoading.value = true;
      setPageBusy('正在报名…');   // 页面级锁：报名在途期间不可再操作/返回（P16）
      try {
        const data = await api('/api/activities/' + currentActivity.value.record_id + '/signup',
          { method: 'POST' });
        vant.showToast(data.message || '报名成功');
        activities.value = []; activitiesLoaded.value = false; // 刷新缓存
        myActivitiesLoaded.value = false; myGroupsLoaded.value = false;
        await openActivity(currentActivity.value.record_id);
      } catch(e) { vant.showToast(e.message); }
      finally { actionLoading.value = false; clearPageBusy(); }
    }
    async function cancelSignup() {
      if (actionLoading.value) return;
      actionLoading.value = true;
      setPageBusy('正在取消报名…');   // 页面级锁：取消在途期间不可再操作/返回（P16）
      try {
        await api('/api/activities/' + currentActivity.value.record_id + '/signup', { method: 'DELETE' });
        vant.showToast('已取消报名');
        activities.value = []; activitiesLoaded.value = false;
        myActivitiesLoaded.value = false; myGroupsLoaded.value = false;
        await openActivity(currentActivity.value.record_id);
      } catch(e) { vant.showToast(e.message); }
      finally { actionLoading.value = false; clearPageBusy(); }
    }

    // 牵线左右滑动新手引导（首次进入牵线且有卡片时弹一次，localStorage 记忆；成功翻页或点击后关闭）
    function maybeShowSwipeGuide() {
      let seen = false;
      try { seen = !!localStorage.getItem(SWIPE_GUIDE_KEY); } catch(e) {}
      if (seen) return;
      setTimeout(() => {
        let s = false;
        try { s = !!localStorage.getItem(SWIPE_GUIDE_KEY); } catch(e) {}
        if (!s && page.value === 'cards' && cards.value && cards.value.length) showSwipeGuide.value = true;
      }, 500);
    }
    function closeSwipeGuide() {
      showSwipeGuide.value = false;
      try { localStorage.setItem(SWIPE_GUIDE_KEY, '1'); } catch(e) {}
      maybeShowActivityPromo(); // 新手引导让位后再补弹活动引导
      maybeShowUpdateNotes();
    }

    // 牵线页活动报名引导：每个会话弹一次，且只对「未报名目标活动」的用户弹。
    // 观察员无报名权限、活动不在「报名中」、后端未下发活动ID时都不弹
    // （弹了按钮必然报错或文案失实）。
    function maybeShowActivityPromo() {
      if (isObserver.value) return;
      if (!promoActivityId.value) return;
      try { if (sessionStorage.getItem(ACTIVITY_PROMO_KEY)) return; } catch(e) {}
      setTimeout(() => {
        try { if (sessionStorage.getItem(ACTIVITY_PROMO_KEY)) return; } catch(e) {}
        if (page.value !== 'cards' || showActivityPromo.value || showSwipeGuide.value || showUpdateNotes.value) return;
        const act = (activities.value || []).find(a => a.activity_id === promoActivityId.value);
        if (!act || act.my_signup) return;
        if (act.status !== '报名中') return;
        const reveal = () => {
          // 等图期间用户可能已经翻页，落地前再验一次，避免弹在别的页面上
          if (page.value !== 'cards' || showActivityPromo.value) return;
          showActivityPromo.value = true;
        };
        if (!act.poster) { promoPoster.value = ''; reveal(); return; }
        // 海报先加载完再弹，避免弹出瞬间图还是空白；最多等 600ms，加载失败就退回纯文字
        promoPoster.value = act.poster;
        let settled = false;
        const settle = () => { if (settled) return; settled = true; reveal(); };
        const img = new Image();
        img.onload = settle;
        img.onerror = () => { promoPoster.value = ''; settle(); };
        setTimeout(settle, 600);
        img.src = act.poster;
      }, 800);
    }
    async function gotoPromoActivity() {
      // 先同步关弹窗（watch 会记账本会话已弹 + 回退 overlay 历史），再跳活动详情，
      // 与 openUserDetail 同款写法：避免「关闭」与「跳页」两个 history 操作互相踩踏。
      showActivityPromo.value = false;
      if (!promoActivityId.value) return;
      await openActivity(promoActivityId.value);
    }
    // 首页更新说明：每个版本弹一次（localStorage 按 UPDATE_NOTES_KEY 记忆）。
    // 和新手引导、活动引导互斥——都抢首页第一屏，串行弹，别叠在一起。
    function maybeShowUpdateNotes() {
      try { if (localStorage.getItem(UPDATE_NOTES_KEY)) return; } catch(e) {}
      setTimeout(() => {
        try { if (localStorage.getItem(UPDATE_NOTES_KEY)) return; } catch(e) {}
        if (page.value !== 'cards' || showUpdateNotes.value || showSwipeGuide.value || showActivityPromo.value) return;
        showUpdateNotes.value = true;
      }, 1500);
    }

    // 牵线卡片
    const filter = ref({
      gender: '',
      nickname: '',
      user_id: '',
      height_min: '', height_max: '',
      age_min: '', age_max: '',
      education: [], income: [],
      house: [], driving: []
    });
    const educationOptions = ['大专以下', '大专', '本科', '硕士', '博士'];
    const incomeOptions = ['10W以下', '10W - 20W', '20W - 30W', '30W - 50W', '50W以上', '未填'];
    const showFilter = ref(false);
    const slideDir = ref('');
    const hasActiveFilter = computed(() => {
      const f = filter.value;
      return !!(f.gender || f.nickname || f.user_id || f.height_min || f.height_max || f.age_min || f.age_max || f.education.length || f.income.length || f.house.length || f.driving.length);
    });
    function buildFilterQuery() {
      const f = filter.value;
      const parts = [];
      if (f.gender) parts.push('gender=' + encodeURIComponent(f.gender));
      const nick = (f.nickname || '').trim();
      if (nick) parts.push('nickname=' + encodeURIComponent(nick));
      ['height_min','height_max'].forEach(k => { if (f[k] !== '') parts.push(k + '=' + encodeURIComponent(f[k])); });
      ['age_min','age_max'].forEach(k => { if (f[k] !== '') parts.push(k + '=' + encodeURIComponent(f[k])); });
      f.education.forEach(e => parts.push('education=' + encodeURIComponent(e)));
      f.income.forEach(i => parts.push('income=' + encodeURIComponent(i)));
      f.house.forEach(h => parts.push('house=' + encodeURIComponent(h)));
      f.driving.forEach(d => parts.push('driving=' + encodeURIComponent(d)));
      const uid = (f.user_id || '').trim();
      if (uid) parts.push('user_id=' + encodeURIComponent(uid.toUpperCase().startsWith('U-') ? uid.toUpperCase() : 'U-' + uid));
      return parts.length ? ('?' + parts.join('&')) : '';
    }
    function toggleMultiTag(field, val) {
      const arr = filter.value[field];
      const idx = arr.indexOf(val);
      if (idx >= 0) arr.splice(idx, 1);
      else arr.push(val);
    }
    async function resetFilter() {
      // 卡片留言草稿会被随后的切卡/重载清掉：提交筛选动作前先拦截
      if(guardOverlay('cardsMsg', () => resetFilter())) return;
      filter.value = { gender: '', nickname: '', user_id: '', height_min: '', height_max: '', age_min: '', age_max: '', education: [], income: [], house: [], driving: [] };
      filterBaselineKey = JSON.stringify(filter.value);
      showFilter.value = false;
      cards.value = [];
      cardIndex.value = 0;
      await loadCards(true);
    }

    async function applyFilter() {
      // 卡片留言草稿会被随后的切卡/重载清掉：提交筛选动作前先拦截
      if(guardOverlay('cardsMsg', () => applyFilter())) return;
      filterBaselineKey = JSON.stringify(filter.value);
      showFilter.value = false;
      cards.value = [];
      cardIndex.value = 0;
      await loadCards(true);
    }
    // 筛选面板打开时对齐基线：面板里改的算「未应用」，关闭/返回时拦截
    watch(showFilter, (v) => { if(v) filterBaselineKey = JSON.stringify(filter.value); });
    // 筛选面板「取消」/返回：changed 未应用 → 先走通用离开拦截（两钮）
    function requestCloseFilter(){
      if(guardOverlay('filter', () => { showFilter.value = false; })) return;
      showFilter.value = false;
    }
    function preloadImage(url) {
      if (!url) return;
      const img = new Image();
      img.src = url;
    }
    function cardPhotosOf(c) {
      return (c && (c.photos || (c.photo ? [c.photo] : []))) || [];
    }
    function cardDisplayPhotos(c) {
      if (!c) return [];
      if (c.photos_thumb && c.photos_thumb.length) return c.photos_thumb;
      return cardPhotosOf(c);
    }
    function schedulePreloadAround() {
      // 首屏不抢带宽：下一张卡的首图等 load 后空闲再预取；切卡仍走 preloadAround()。
      var run = function(){ try { preloadAround(); } catch (e) {} };
      var start = function(){
        if ('requestIdleCallback' in window) requestIdleCallback(run, { timeout: 3000 });
        else setTimeout(run, 1500);
      };
      if (document.readyState === 'complete') setTimeout(start, 5000);
      else window.addEventListener('load', function(){ setTimeout(start, 5000); }, { once: true });
    }
    function preloadAround() {
      // 只预取下一张卡片的首图；当前卡片的相邻照片由 deck 自身按需加载。
      const next = cards.value[cardIndex.value + 1];
      if (!next) return;
      const p = cardDisplayPhotos(next);
      if (p[0]) preloadImage(p[0]);
    }
    function cardsQuery() {
      const q = buildFilterQuery();
      return q ? q + '&' : '?';
    }
    // —— 本人卡与本地全序 ——
    // 服务端全序只含候选；home 额外把本人卡放在最前并把 total +1。
    // 本地牌堆统一用「含本人卡」的全序：offset=0 插入本人卡，向服务端取数时 offset-1，
    // 首屏 limit 少 1，保证窗口条数与 total 不重复、不跳卡。
    function deckSelfActive() { return !hasActiveFilter.value && !!selfCard.value; }
    function deckOffsetParam(offset) { return deckSelfActive() ? Math.max(0, offset - 1) : offset; }
    function deckLimitParam(offset) { return deckSelfActive() && offset === 0 ? 49 : 50; }
    function deckCardView(c) {
      return c ? { ...c, liked: !!(c.liked || (c.openid && justLiked.has(c.openid))) } : c;
    }
    function deckPage(offset, data) {
      const self = deckSelfActive() ? deckCardView(selfCard.value) : null;
      // 服务端若将来也下发本人卡，先剔除，避免本地再插一张造成重复。
      const raw = ((data && data.cards) || []).map(deckCardView)
        .filter(c => c && !c.is_self && (!self || c.openid !== self.openid));
      const cardsOut = (self && offset === 0) ? [self].concat(raw) : raw;
      const baseTotal = (data && typeof data.total === 'number') ? data.total : raw.length;
      return {
        cards: cardsOut,
        total: baseTotal + (self ? 1 : 0),
        version: data ? data.version : null
      };
    }
    async function loadCards(force) {
      if (cards.value.length && !force) return; // 缓存
      if (force) cardsPageCache.clear();
      loadingCards.value = true;
      try {
        const data = await api('/api/cards' + cardsQuery() + 'offset=0&limit=' + deckLimitParam(0));
        // 已喜欢的人保留在牌堆（liked=true 置灰），justLiked 覆盖快照刷新窗口；
        // 无筛选时 deckPage 会把 home 缓存的本人卡放回首位。
        const page = deckPage(0, data);
        cards.value = page.cards;
        cardsTotal.value = page.total;
        cardsVersion.value = (page.version != null ? page.version : null);
        cardsStart.value = 0;
        cardIndex.value = 0; photoIndex.value = 0;
        triggerPhotoLoading();
        preloadAround();
        maybeShowSwipeGuide();
      }
      catch(e) { vant.showToast(e.message); }
      loadingCards.value = false;
    }
    function trimCardsWindow() {
      // 内存上限：窗口最多 150 张；只在本窗口内截头/截尾，绝不引用窗口外页
      // （截头/截尾均为正偏移切片，杜绝负切片空窗 0/0）
      const MAX = 150;
      if (cards.value.length <= MAX) return;
      const seqCur = cardsStart.value + cardIndex.value;
      const start = cardsStart.value;
      const end = start + cards.value.length;
      // 尾部多余：截尾至 seqCur+100
      const tailCap = seqCur + 100;
      if (end > tailCap) {
        cards.value = cards.value.slice(0, tailCap - start);
      }
      // 头部多余：截头至 seqCur-50
      const headCap = seqCur - 50;
      if (headCap > start) {
        cards.value = cards.value.slice(headCap - start);
        cardsStart.value = headCap;
        cardIndex.value = seqCur - headCap;
      }
    }
    async function loadMoreCards() {
      if (cardsLoadingMore.value || cardsStart.value + cards.value.length >= cardsTotal.value) return;
      cardsLoadingMore.value = true;
      const nextOffset = cardsStart.value + cards.value.length;
      try {
        const data = await api('/api/cards' + cardsQuery() + 'offset=' + deckOffsetParam(nextOffset) + '&limit=' + deckLimitParam(nextOffset));
        // users 快照版本变化：顺序已变，按当前全序位置重建窗口（不跳回第一张）
        if (data.version != null && cardsVersion.value !== null && data.version !== cardsVersion.value) {
          cardsVersion.value = data.version;
          cardsPageCache.clear();
          const seqCur = cardsStart.value + cardIndex.value;
          const off = Math.floor(seqCur / 50) * 50;
          const ok = await loadPageAt(off);
          if (ok) cardIndex.value = Math.min(seqCur - off, cards.value.length - 1);
          cardsLoadingMore.value = false;
          return;
        }
        const page = deckPage(nextOffset, data);
        if (page.version != null) cardsVersion.value = page.version;
        const more = page.cards;
        cardsPageCache.set(nextOffset, { cards: more, total: page.total, version: page.version });
        const existing = new Set(cards.value.map(c => c.openid));
        const fresh = more.filter(c => !existing.has(c.openid));
        if (!fresh.length && nextOffset < cardsTotal.value) {
          // 全部重复（顺序漂移/服务重启换序）：按当前全序位置重建窗口，避免卡死
          const seqCur = cardsStart.value + cardIndex.value;
          const off = Math.floor(seqCur / 50) * 50;
          const ok = await loadPageAt(off);
          if (ok) cardIndex.value = Math.min(seqCur - off, cards.value.length - 1);
          cardsLoadingMore.value = false;
          return;
        }
        cards.value = cards.value.concat(fresh);
        cardsTotal.value = page.total;
        trimCardsWindow();
      }
      catch(e) { vant.showToast(e.message); }
      cardsLoadingMore.value = false;
    }
    function onPhotoError() { clearTimeout(photoLoadingTimer); photoLoading.value = false; }
    // 弱网图片自愈：加载失败自动重试（逐次加 _r 穿透坏缓存），仍失败才执行 done（如清空占位）
    function retryImg(e, tries, done) {
      try {
        const el = e && e.target;
        const max = (typeof tries === 'number' && tries >= 0) ? tries : 2;
        if (el && el.getAttribute) {
          const n = parseInt(el.dataset.yxqRetry || '0', 10) || 0;
          if (n < max) {
            el.dataset.yxqRetry = String(n + 1);
            const src = el.getAttribute('src') || '';
            if (src) {
              const base = src.replace(/[?&]_r=[^&]*/g, '').replace(/[?&]$/, '');
              const sep = base.indexOf('?') >= 0 ? '&' : '?';
              const tag = base + sep + '_r=' + Date.now() + '' + n;
              setTimeout(() => { el.src = tag; }, 400 * (n + 1));
              return;
            }
          }
        }
      } catch (_) { /* 降级：直接执行 done */ }
      if (done) done();
    }
    // —— 卡片滑动（仅下半区） ——
    let cardTouchStartX = 0, cardTouchStartY = 0, cardTouchIgnore = false;
    function onCardTouchStart(e) {
      const t = e.target;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'BUTTON' || t.tagName === 'TEXTAREA' ||
                (t.closest && t.closest('input, textarea, button')))) {
        cardTouchIgnore = true;
        return;
      }
      cardTouchIgnore = false;
      cardTouchStartX = e.touches[0].clientX; cardTouchStartY = e.touches[0].clientY;
    }
    function onCardTouchMove(e) {}
    async function loadPageAt(offset) {
      // 整窗替换：版本变化时按全序位置重建窗口；失败返回 false 不抛异常
      try {
      const cached = cardsPageCache.get(offset);
      if (cached && (cached.version == null || cardsVersion.value == null || cached.version === cardsVersion.value)) {
        cards.value = cached.cards;
        cardsTotal.value = cached.total;
        cardsStart.value = offset;
        return true;
      }
      const data = await api('/api/cards' + cardsQuery() + 'offset=' + deckOffsetParam(offset) + '&limit=' + deckLimitParam(offset));
      if (data.version != null && cardsVersion.value !== null && data.version !== cardsVersion.value) {
        cardsVersion.value = data.version;
        cardsPageCache.clear();
        return loadPageAt(offset);
      }
      const page = deckPage(offset, data);
      if (!page.cards.length) return false;  // 空页绝不替换窗口（防 0/0）
      if (page.version != null) cardsVersion.value = page.version;
      cardsPageCache.set(offset, page);
      cards.value = page.cards;
      cardsTotal.value = page.total;
      cardsStart.value = offset;
      return true;
      } catch (e) { return false; }
    }
    let cardsLoadingPrev = false;
    async function loadPrevPage() {
      // 前置一页（往回连续翻）：prepend 前一页，返回 prepend 条数供 cardIndex 对齐
      if (cardsLoadingPrev) return 0;
      cardsLoadingPrev = true;
      try {
      const offset = Math.max(0, cardsStart.value - 50);
      const data = await api('/api/cards' + cardsQuery() + 'offset=' + deckOffsetParam(offset) + '&limit=' + deckLimitParam(offset));
      if (data.version != null && cardsVersion.value !== null && data.version !== cardsVersion.value) {
        cardsVersion.value = data.version;
        cardsPageCache.clear();
        const seqCur = cardsStart.value + cardIndex.value;
        const off = Math.floor(seqCur / 50) * 50;
        const ok = await loadPageAt(off);
        if (ok) cardIndex.value = Math.min(seqCur - off, cards.value.length - 1);
        return 0;
      }
      const page = deckPage(offset, data);
      if (!page.cards.length) return 0;  // 空页：保持窗口不动
      if (page.version != null) cardsVersion.value = page.version;
      cardsPageCache.set(offset, page);
      const existing = new Set(cards.value.map(c => c.openid));
      const prepend = page.cards.filter(c => !existing.has(c.openid));
      cards.value = prepend.concat(cards.value);
      cardsTotal.value = page.total;
      cardsStart.value = offset;
      return prepend.length;
      } finally { cardsLoadingPrev = false; }
    }
    let lastBoundaryToastTs = 0;
    function boundaryToast(msg){
      const now = Date.now();
      if (now - lastBoundaryToastTs < 1500) return;
      lastBoundaryToastTs = now;
      vant.showToast({ message: msg, position: 'top' });
    }
    function switchCard(dir) {
      const total = cardsTotal.value;
      if (total <= 1) return;
      const n = cards.value.length;
      const seq = cardsStart.value + cardIndex.value;  // 当前全序位置
      const canMove = dir < 0 ? (cardIndex.value < n - 1 || seq < total - 1) : (cardIndex.value > 0 || seq > 0);
      // 未发送留言草稿：切卡前拦截（放弃=清空草稿后继续切卡；发送只走留言面板自身的发送钮）
      if (canMove && guardOverlay('cardsMsg', () => switchCard(dir))) return;
      if (dir < 0) {
        // 下一张
        if (cardIndex.value < n - 1) {
          slideDir.value = 'right';
          cardIndex.value += 1;
          photoIndex.value = 0;
          photoSlideDir.value = '';
          if (cardIndex.value >= cards.value.length - 10 && cardsStart.value + cards.value.length < cardsTotal.value) {
            loadMoreCards();
          }
          return;
        } else if (seq < total - 1) {
          // 窗口尾且后面还有：加载下一页后前进
          if (!cardsLoadingMore.value) {
            loadMoreCards().then(() => {
              if (cardIndex.value < cards.value.length - 1) {
                cardIndex.value += 1;
                slideDir.value = 'right';
              }
            }).catch(() => {});
          }
          photoIndex.value = 0;
          photoSlideDir.value = '';
          return;
        }
        // 真·最后一张：提示后原地不动
        boundaryToast('已是最后一张');
        return;
      } else if (dir > 0) {
        // 上一张
        if (cardIndex.value > 0) {
          slideDir.value = 'left';
          cardIndex.value -= 1;
          photoIndex.value = 0;
          photoSlideDir.value = '';
          return;
        } else if (seq > 0) {
          // 窗口头且前面还有：前置上一页
          if (!cardsLoadingPrev) {
            loadPrevPage().then((prep) => {
              if (prep > 1) {
                cardIndex.value = prep - 1;
                slideDir.value = 'left';
                trimCardsWindow();
              }
            }).catch(() => {});
          }
          photoIndex.value = 0;
          photoSlideDir.value = '';
          return;
        }
        // 真·第一张：提示后原地不动
        boundaryToast('已是第一张');
        return;
      }
      photoIndex.value = 0;
      photoSlideDir.value = '';
    }
    function onCardTouchEnd(e) {
      if (cardTouchIgnore) { cardTouchIgnore = false; return; }
      const dx = e.changedTouches[0].clientX - cardTouchStartX;
      const dy = e.changedTouches[0].clientY - cardTouchStartY;
      if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy)) {
        switchCard(dx < 0 ? -1 : 1);
      }
    }
    // —— 卡片滑动（鼠标）——
    let cardMouseDown = false;
    let cardMouseStartX = 0, cardMouseStartY = 0;
    function onCardMouseDown(e) {
      if (e.button !== 0) return;
      // 输入框/按钮等交互元素放行，否则 preventDefault 会导致无法聚焦输入、无法点击
      const t = e.target;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'BUTTON' || t.tagName === 'TEXTAREA' ||
                (t.closest && t.closest('input, textarea, button')))) return;
      cardMouseDown = true;
      cardMouseStartX = e.clientX;
      cardMouseStartY = e.clientY;
      // 防止拖拽时选中文本
      e.preventDefault();
    }
    function onCardMouseMove(e) {
      if (!cardMouseDown) return;
      // 轻微移动时阻止文本选中
      const dx = Math.abs(e.clientX - cardMouseStartX);
      const dy = Math.abs(e.clientY - cardMouseStartY);
      if (dx > 5 || dy > 5) e.preventDefault();
    }
    function onCardMouseUp(e) {
      if (!cardMouseDown) return;
      cardMouseDown = false;
      const dx = e.clientX - cardMouseStartX;
      const dy = e.clientY - cardMouseStartY;
      if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy)) {
        switchCard(dx < 0 ? -1 : 1);
      }
    }
    function onCardMouseLeave(e) {
      if (!cardMouseDown) return;
      // 鼠标移出时视为结束，不触发切换，避免卡死
      cardMouseDown = false;
    }
    // 兼容旧模板（如有残留调用）
    const onTouchStart = onCardTouchStart;
    const onTouchMove = onCardTouchMove;
    const onTouchEnd = onCardTouchEnd;
    // —— 图片区：横滑切换照片，点击放大 ——
    let photoTouchStartX = 0, photoTouchStartY = 0, photoTouchStartTime = 0;
    let photoDidSwipe = false;
    function onPhotoTouchStart(e) {
      photoTouchStartX = e.touches[0].clientX;
      photoTouchStartY = e.touches[0].clientY;
      photoTouchStartTime = Date.now();
      photoDidSwipe = false;
    }
    function onPhotoTouchMove(e) {
      if (!photoTouchStartTime) return;
      const dx = Math.abs(e.touches[0].clientX - photoTouchStartX);
      const dy = Math.abs(e.touches[0].clientY - photoTouchStartY);
      if (dx > 15 || dy > 15) photoDidSwipe = true;
    }
    function onPhotoTouchEnd(e) {
      const dx = e.changedTouches[0].clientX - photoTouchStartX;
      const dy = e.changedTouches[0].clientY - photoTouchStartY;
      photoTouchStartTime = 0;
      const photos = currentCard.value ? (currentCard.value.photos || (currentCard.value.photo ? [currentCard.value.photo] : [])) : [];
      if (Math.abs(dx) > 35 && Math.abs(dx) > Math.abs(dy)) {
        photoDidSwipe = true;
        if (photos.length > 1) {
          photoSlideDir.value = dx < 0 ? 'left' : 'right';
          if (dx < 0) photoIndex.value = (photoIndex.value + 1) % photos.length;
          else photoIndex.value = (photoIndex.value - 1 + photos.length) % photos.length;
        } else {
          boundaryToast('没有更多照片了');
        }
        return;
      }
    }
    function onPhotoClick(e) {
      if (photoDidSwipe) { photoDidSwipe = false; return; }
      const photos = currentCard.value ? (currentCard.value.photos || (currentCard.value.photo ? [currentCard.value.photo] : [])) : [];
      if (photos.length) openPhotoViewerForCurrent();
    }
    // —— 图片区：鼠标拖动切换照片 ——
    let photoMouseDown = false;
    let photoMouseStartX = 0, photoMouseStartY = 0, photoMouseStartTime = 0;
    function onPhotoMouseDown(e) {
      if (e.button !== 0) return;
      photoMouseDown = true;
      photoMouseStartX = e.clientX;
      photoMouseStartY = e.clientY;
      photoMouseStartTime = Date.now();
      photoDidSwipe = false;
      e.preventDefault();
    }
    function onPhotoMouseMove(e) {
      if (!photoMouseDown) return;
      const dx = Math.abs(e.clientX - photoMouseStartX);
      const dy = Math.abs(e.clientY - photoMouseStartY);
      if (dx > 15 || dy > 15) photoDidSwipe = true;
      if (photoDidSwipe) e.preventDefault();
    }
    function onPhotoMouseUp(e) {
      if (!photoMouseDown) return;
      photoMouseDown = false;
      const dx = e.clientX - photoMouseStartX;
      const dy = e.clientY - photoMouseStartY;
      const dt = Date.now() - photoMouseStartTime;
      const photos = currentCard.value ? (currentCard.value.photos || (currentCard.value.photo ? [currentCard.value.photo] : [])) : [];
      if (Math.abs(dx) > 35 && Math.abs(dx) > Math.abs(dy)) {
        photoDidSwipe = true;
        if (photos.length > 1) {
          photoSlideDir.value = dx < 0 ? 'left' : 'right';
          if (dx < 0) photoIndex.value = (photoIndex.value + 1) % photos.length;
          else photoIndex.value = (photoIndex.value - 1 + photos.length) % photos.length;
        } else {
          boundaryToast('没有更多照片了');
        }
        e.preventDefault();
        e.stopPropagation();
        return;
      }
    }
    function onPhotoMouseLeave(e) {
      if (!photoMouseDown) return;
      photoMouseDown = false;
    }
    // —— 全屏图片预览（PhotoSwipe 5）——
    let viewerLightbox = null;
    let viewerOpenSeq = 0;
    let viewerDims = new Map();
    // 本次打开的原图/缩图数组与升级状态：gallery href 用缩图即时出画，原图后台替换。
    let viewerFullPhotos = [];
    let viewerDisplayPhotos = [];
    let viewerFullState = {};
    // 自管理单击/双击：PhotoSwipe 内置双击检测会给单击加固定 300ms 延迟，
    // 这里自行实现 250ms 短窗口——单击即关、双击缩放。
    // 图片保持 pointer-events:none（拦截长按保存菜单），事件在 zoom-wrap/item 上捕获。
    let vTapBound = false;
    let vTapTimer = null;
    let vLastTap = { t: 0, x: 0, y: 0 };
    let vDown = null;
    let vPtrCount = 0;
    function vTapReset() {
      if (vTapTimer) { clearTimeout(vTapTimer); vTapTimer = null; }
      vLastTap.t = 0; vDown = null; vPtrCount = 0;
    }
    function vTapInViewer(e) {
      const p = viewerLightbox && viewerLightbox.pswp;
      if (!p || p.isDestroying || p.isClosing || !p.currSlide) return null;
      const el = e.target;
      if (!el || !el.closest) return null;
      if (el.closest('.pswp__button, .pswp__top-bar')) return null;
      return el.closest('.pswp') === p.element ? p : null;
    }
    function vTapDown(e) {
      if (!vTapInViewer(e)) { vDown = null; return; }
      if (e.type === 'pointerdown') {
        vPtrCount++;
        if (vPtrCount > 1) { vDown = null; return; }
        if (e.pointerType === 'mouse') { vDown = null; return; }
      }
      if (e.touches && e.touches.length > 1) { vDown = null; return; }
      const t = e.touches ? e.touches[0] : e;
      vDown = { x: t.clientX, y: t.clientY, t: Date.now(), moved: false };
    }
    function vTapMove(e) {
      if (!vDown) return;
      const t = e.touches ? e.touches[0] : e;
      if (Math.abs(t.clientX - vDown.x) > 12 || Math.abs(t.clientY - vDown.y) > 12) vDown.moved = true;
    }
    function vTapUp(e) {
      if (e.type === 'pointerup') vPtrCount = Math.max(0, vPtrCount - 1);
      const p = vTapInViewer(e);
      if (!vDown || !p) return;
      if (e.type === 'pointerup' && e.pointerType === 'mouse') { vDown = null; return; }
      const t = e.changedTouches && e.changedTouches.length ? e.changedTouches[0] : e;
      const d = vDown;
      vDown = null;
      if (d.moved) return;
      if (Date.now() - d.t > 500) return;
      const now = Date.now();
      if (now - vLastTap.t < 250 && Math.abs(t.clientX - vLastTap.x) < 40 && Math.abs(t.clientY - vLastTap.y) < 40) {
        if (vTapTimer) { clearTimeout(vTapTimer); vTapTimer = null; }
        vLastTap.t = 0;
        const rect = p.element.getBoundingClientRect();
        p.currSlide.toggleZoom({ x: t.clientX - rect.left, y: t.clientY - rect.top });
      } else {
        vLastTap.t = now; vLastTap.x = t.clientX; vLastTap.y = t.clientY;
        vTapTimer = setTimeout(() => {
          vTapTimer = null;
          const ps = viewerLightbox && viewerLightbox.pswp;
          if (ps && !ps.isDestroying && !ps.isClosing) ps.close();
        }, 250);
      }
    }
    function vTapBlockLongPress(e) {
      // 长按拦截：touchstart preventDefault 从手势层阻断系统/飞书/微信的长按保存菜单，
      // 比 pointer-events:none 更彻底（加载期占位图/部分 WebView 原生命中绕过 CSS 也能拦）。
      const p = viewerLightbox && viewerLightbox.pswp;
      if (!p || p.isDestroying) return;
      const el = e.target;
      if (!el || !el.closest) return;
      if (el.closest('.pswp__button, .pswp__top-bar')) return;  // 关闭/缩放按钮与顶栏保留点击
      if (el.closest('.pswp') === p.element) e.preventDefault();
    }
    function vTapBind() {
      if (vTapBound) return;
      vTapBound = true;
      document.addEventListener('touchstart', vTapBlockLongPress, { passive: false, capture: true });
      if (window.PointerEvent) {
        document.addEventListener('pointerdown', vTapDown, true);
        document.addEventListener('pointermove', vTapMove, true);
        document.addEventListener('pointerup', vTapUp, true);
      } else {
        document.addEventListener('touchstart', vTapDown, true);
        document.addEventListener('touchmove', vTapMove, true);
        document.addEventListener('touchend', vTapUp, true);
      }
    }
    let photoSwipePromise = null;
    function loadPhotoSwipe() {
      if (window.PhotoSwipe && window.PhotoSwipeLightbox) return Promise.resolve();
      if (photoSwipePromise) return photoSwipePromise;
      const v = encodeURIComponent(window.__ASSET_VERSION__ || 'legacy');
      photoSwipePromise = new Promise((resolve, reject) => {
        const css = document.createElement('link');
        css.rel = 'stylesheet'; css.href = 'lib/photoswipe.css?v=' + v;
        document.head.appendChild(css);
        const first = document.createElement('script');
        first.src = 'lib/photoswipe.umd.min.js?v=' + v;
        first.onload = () => {
          const second = document.createElement('script');
          second.src = 'lib/photoswipe-lightbox.umd.min.js?v=' + v;
          second.onload = () => resolve();
          second.onerror = () => reject(new Error('photoswipe-lightbox'));
          document.head.appendChild(second);
        };
        first.onerror = () => reject(new Error('photoswipe'));
        document.head.appendChild(first);
      }).catch((err) => { photoSwipePromise = null; throw err; });
      return photoSwipePromise;
    }
    function clearViewerLoading() {
      if (viewerLoadingTimer) { clearTimeout(viewerLoadingTimer); viewerLoadingTimer = null; }
      if (viewerLoading.value) viewerLoading.value = false;
    }
    function armViewerLoadingTimeout() {
      if (viewerLoadingTimer) clearTimeout(viewerLoadingTimer);
      // 超时与用户点击遮罩走同一取消路径：作废 seq + 隐藏根节点 + close/destroy。
      viewerLoadingTimer = setTimeout(cancelViewerLoading, 10000);
    }
    function clearViewerCancelledClass() {
      try {
        const roots = document.querySelectorAll('.pswp.yxq-viewer-cancelled');
        for (let i = 0; i < roots.length; i++) {
          roots[i].classList.remove('yxq-viewer-cancelled');
          roots[i].style.display = '';
        }
      } catch (e) {}
    }
    function hideViewerRoot(pswp) {
      const root = (pswp && pswp.element) || (viewerLightbox && viewerLightbox.pswp && viewerLightbox.pswp.element) ||
        document.querySelector('.pswp');
      if (!root) return;
      // 不能只依赖 250ms destroy：PhotoSwipe 在 loadAndOpen 未完成时可能不移除根节点。
      root.classList.add('yxq-viewer-cancelled');
      root.style.display = 'none';
    }
    function clearViewerAbortTimers() {
      for (let i = 0; i < viewerAbortTimers.length; i++) clearTimeout(viewerAbortTimers[i]);
      viewerAbortTimers = [];
    }
    function discardViewerInstance(pswp) {
      if (!pswp) return;
      hideViewerRoot(pswp);
      try { pswp.destroy(); } catch (e) {}
      // PhotoSwipeLightbox.loadAndOpen 会因 window.pswp 仍存在而直接 return false；
      // 若 destroy 是异步/被 loading 卡住，手动移除旧根节点并清掉全局引用，保证新实例能创建。
      if (window.pswp === pswp) {
        try {
          const root = pswp.element || document.querySelector('.pswp');
          if (root && root.parentNode) root.parentNode.removeChild(root);
        } catch (e) {}
        try { delete window.pswp; } catch (e) { window.pswp = undefined; }
      }
      if (viewerLightbox && viewerLightbox.pswp === pswp) viewerLightbox.pswp = undefined;
    }
    function clearViewerLoadingIfLoaded(seq) {
      if (seq !== viewerOpenSeq || viewerCancelledSeq === viewerOpenSeq) return;
      const pswp = viewerLightbox && viewerLightbox.pswp;
      const content = pswp && pswp.currSlide && pswp.currSlide.content;
      const el = content && content.element;
      if (el && el.complete && el.naturalWidth) {
        clearViewerLoading();
        upgradeViewerSlide(pswp.currSlide, seq);
      }
    }
    function abortViewerInstance(pswp) {
      if (!pswp) return;
      hideViewerRoot(pswp);
      try { pswp.close(); } catch (e) {}
      const abortSeq = viewerOpenSeq;
      const timer = setTimeout(function () {
        viewerAbortTimers = viewerAbortTimers.filter(function (t) { return t !== timer; });
        // 取消后 250ms 内用户可能已重新打开：seq 变了或 pswp 实例已被替换时绝不能 destroy。
        if (viewerOpenSeq !== abortSeq) return;
        if (!viewerLightbox || viewerLightbox.pswp !== pswp) return;
        try { pswp.destroy(); } catch (e) {}
        hideViewerRoot(pswp);
      }, 250);
      viewerAbortTimers.push(timer);
    }
    function cancelViewerLoading() {
      viewerOpenSeq++;                 // 让仍在探测尺寸/加载的旧请求失效
      viewerCancelledSeq = viewerOpenSeq;
      clearViewerLoading();
      const pswp = viewerLightbox && viewerLightbox.pswp;
      if (pswp) abortViewerInstance(pswp);
      else hideViewerRoot(null);
      if (showPhotoViewer.value) showPhotoViewer.value = false;
    }
    // 大图左右切换：>=2 张时显示左右按键，首尾循环。
    // PhotoSwipe 5 自带的箭头在触摸端默认隐藏；这里自绘按钮并用取模跳转，
    // 保证移动端也可见，且任意张数（含 2 张）都能从首张/末张循环。
    function viewerStep(pswp, dir) {
      if (!pswp || pswp.isDestroying || pswp.isClosing) return;
      if (!viewerLightbox || viewerLightbox.pswp !== pswp) return;
      if (pswp.opener && !pswp.opener.isOpen) return;
      const n = pswp.getNumItems();
      if (n < 2) return;
      pswp.goTo((pswp.currIndex + dir + n) % n);
    }
    function viewerInjectArrows(pswp) {
      if (!pswp || !pswp.scrollWrap) return;
      if (pswp.scrollWrap.querySelector('.yxq-viewer-arrow')) return;
      [
        { dir: -1, cls: 'prev', label: '上一张', path: 'M15 5l-7 7 7 7' },
        { dir: 1, cls: 'next', label: '下一张', path: 'M9 5l7 7-7 7' },
      ].forEach(function (d) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'pswp__button pswp__hide-on-close yxq-viewer-arrow yxq-viewer-arrow--' + d.cls;
        btn.setAttribute('aria-label', d.label);
        btn.setAttribute('aria-controls', 'pswp__items');
        btn.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">' +
          '<path d="' + d.path + '" fill="none" stroke="currentColor" stroke-width="2.2" ' +
          'stroke-linecap="round" stroke-linejoin="round"/></svg>';
        btn.addEventListener('click', function (e) {
          e.preventDefault();
          e.stopPropagation();
          viewerStep(pswp, d.dir);
        });
        pswp.scrollWrap.appendChild(btn);
      });
    }
    // 卡片页空闲时预加载 PhotoSwipe 资源：首次点大图不再等 JS/CSS 下载。
    function prefetchPhotoSwipe() {
      if (photoSwipePrefetchStarted) return;
      photoSwipePrefetchStarted = true;
      const run = () => { try { loadPhotoSwipe().catch(() => {}); } catch (e) {} };
      const later = () => {
        try {
          if ('requestIdleCallback' in window) requestIdleCallback(run, { timeout: 300 });
          else setTimeout(run, 50);
        } catch (e) { setTimeout(run, 1500); }
      };
      // 大图要求秒开：PhotoSwipe 资源在挂载后立即后台抓取（不抢 hero/首卡绘制）。
      // 不再等 load+1.2s，否则用户首次点图仍要等 JS 下载。
      if (document.readyState === 'complete') setTimeout(later, 0);
      else window.addEventListener('load', () => setTimeout(later, 0), { once: true });
    }
    function viewerEnsure() {
      if (viewerLightbox) return;
      viewerLightbox = new PhotoSwipeLightbox({
        gallery: '#pswp-gallery',
        children: 'a',
        pswpModule: () => PhotoSwipe,
        bgOpacity: 1,
        closeOnVerticalDrag: true,
        tapAction: null,
        doubleTapAction: null,
        bgClickAction: 'close',
        imageClickAction: 'close',
        hideAnimationDuration: 160,
        preloaderDelay: 200,
        // 用自绘左右按键（触摸端可见 + 循环），关闭自带箭头避免桌面端重复。
        arrowPrev: false,
        arrowNext: false,
      });
      viewerLightbox.on('afterInit', () => {
        // 标记本次实例；若打开过程中已被取消，立即隐藏并关闭，不等待 loadComplete。
        const pswp = viewerLightbox && viewerLightbox.pswp;
        if (pswp && viewerPendingSeq) pswp.__yxqOpenSeq = viewerPendingSeq;
        viewerInjectArrows(pswp);
        if (viewerCancelledSeq === viewerOpenSeq) {
          hideViewerRoot(pswp);
          setTimeout(function () { abortViewerInstance(pswp); }, 0);
        }
      });
      viewerLightbox.on('slideInit', (e) => {
        const d = e.slide ? viewerDims.get(e.slide.index) : null;
        if (d && e.slide && e.slide.content) {
          e.slide.width = d.w; e.slide.height = d.h;
          e.slide.content.width = d.w; e.slide.content.height = d.h;
        } else if (e.slide && e.slide.container) {
          // 尺寸未知：先隐藏，尺寸就绪（探针或 loadComplete）再显示，避免加载期被拉成视口比例
          e.slide.container.classList.add('yxq-dim-wait');
        }
      });
      viewerLightbox.on('slideActivate', (e) => {
        if (e && e.slide) upgradeViewerSlide(e.slide, viewerOpenSeq);
      });
      viewerLightbox.on('loadComplete', (e) => {
        const pswp = (e && e.slide && e.slide.pswp) || (viewerLightbox && viewerLightbox.pswp);
        if (pswp && pswp.__yxqOpenSeq && pswp.__yxqOpenSeq !== viewerOpenSeq) {
          // 过期实例的 loadComplete：只隐藏旧根节点，不能清掉新打开的遮罩。
          hideViewerRoot(pswp);
          return;
        }
        if (viewerCancelledSeq === viewerOpenSeq) {
          hideViewerRoot(pswp);
          clearViewerLoading();
          return;
        }
        // 缩图/原图已可显示：撤掉遮罩，并以后台原图继续升级。
        clearViewerLoading();
        const s = e && e.slide;
        const c = s && s.content;
        if (!s || !c) return;
        if (s.container) s.container.classList.remove('yxq-dim-wait');
        const nw = c.element && c.element.naturalWidth;
        const nh = c.element && c.element.naturalHeight;
        // 已经按原图尺寸（或同宽高比）定过框：不要再按缩图的 natural size 重算。
        // 缩图与原图宽高比不同时（旧缩图裁切），重算会先小后大跳一下。
        if (nw && nh && !viewerDims.has(s.index)) applyViewerSlideSize(s, nw, nh);
        upgradeViewerSlide(s, viewerOpenSeq);
      });
      viewerLightbox.on('loadError', () => {
        const pswp = viewerLightbox && viewerLightbox.pswp;
        if (pswp && pswp.__yxqOpenSeq && pswp.__yxqOpenSeq !== viewerOpenSeq) {
          hideViewerRoot(pswp);
          return;
        }
        if (viewerCancelledSeq === viewerOpenSeq) hideViewerRoot(pswp);
        clearViewerLoading();
      });
      viewerLightbox.on('close', () => {
        const pswp = viewerLightbox && viewerLightbox.pswp;
        if (pswp && pswp.__yxqOpenSeq && pswp.__yxqOpenSeq !== viewerOpenSeq) {
          hideViewerRoot(pswp);
          return;
        }
        vTapReset();
        clearViewerLoading();
        if (showPhotoViewer.value) showPhotoViewer.value = false;
      });
      viewerLightbox.init();
      vTapBind();
    }
    function attrEscape(s) {
      return String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }
    function viewerGetDims(src, cb) {
      const im = new Image();
      im.onload = () => cb(im.naturalWidth ? { w: im.naturalWidth, h: im.naturalHeight } : null);
      im.onerror = () => cb(null);
      im.src = src;
    }
    function applyViewerSlideSize(s, nw, nh) {
      if (!s || !nw || !nh) return;
      s.width = nw;
      s.height = nh;
      if (s.content) {
        s.content.width = nw;
        s.content.height = nh;
      }
      if (s.data) { s.data.w = nw; s.data.h = nh; }
      try {
        s.calculateSize();
        s.currentResolution = 0;
        s.zoomAndPanToInitial();
        s.applyCurrentZoomPan();
        s.updateContentSize(true);
      } catch (e) {}
    }
    // viewerFrameMatches 判断已确定的显示框是否与原图宽高比一致。宽高比一致时
    // 绝不重算布局（缩图与原图共用同一个框，零跳变）；只有缺尺寸或缩图被裁切
    // 导致比例不同才重算，避免原图被 cover 裁掉。
    function viewerFrameMatches(index, nw, nh) {
      const d = viewerDims.get(index);
      if (!d || !d.w || !d.h || !nw || !nh) return false;
      const known = d.w / d.h;
      const real = nw / nh;
      return Math.abs(known - real) <= 0.005 * Math.max(known, real);
    }
    // 原图后台升级：缩图已经显示，原图加载完成后替换当前 slide 的 img src。
    // 同一 index 只发起一次；失败保留缩图，不弹错误。
    function upgradeViewerSlide(slide, seq) {
      if (!slide || seq !== viewerOpenSeq || viewerCancelledSeq === viewerOpenSeq) return;
      const i = slide.index;
      const full = viewerFullPhotos[i];
      const thumb = viewerDisplayPhotos[i];
      if (!full || !thumb || full === thumb) return;
      if (viewerFullState[i]) {
        if (viewerFullState[i] === 'done' && slide.content && slide.content.element) {
          try {
            if (slide.content.element.dataset.yxqFull !== '1') {
              slide.content.element.src = full;
              slide.content.element.dataset.yxqFull = '1';
            }
          } catch (e) {}
        }
        return;
      }
      viewerFullState[i] = 'loading';
      const im = new Image();
      im.onload = function () {
        if (seq !== viewerOpenSeq) return;
        viewerFullState[i] = 'done';
        const content = slide.content;
        if (content && content.element) {
          try {
            content.element.src = full;
            content.element.dataset.yxqFull = '1';
          } catch (e) {}
        }
        if (!viewerFrameMatches(slide.index, im.naturalWidth, im.naturalHeight)) {
          applyViewerSlideSize(slide, im.naturalWidth, im.naturalHeight);
        }
      };
      im.onerror = function () {
        if (seq === viewerOpenSeq) viewerFullState[i] = 'error';
      };
      im.src = full;
    }
    function applyDimsToLiveSlide(seq, index, d) {
      // 快速滑动竞态：slide 已在尺寸探针完成前创建，content 宽高为 0，
      // PhotoSwipe updateContentSize 会回退成视口尺寸导致图片被拉伸；
      // 尺寸就绪后就地修正已创建 slide 的宽高并重算布局。
      if (!d || seq !== viewerOpenSeq) return;
      const p = viewerLightbox && viewerLightbox.pswp;
      if (!p || p.isDestroying || !p.mainScroll || !p.mainScroll.itemHolders) return;
      for (const h of p.mainScroll.itemHolders) {
        const s = h && h.slide;
        if (!s || s.index !== index || !s.content) continue;
        s.content.width = d.w;
        s.content.height = d.h;
        s.width = d.w;
        s.height = d.h;
        if (s.data) { s.data.w = d.w; s.data.h = d.h; }
        if (s.container) s.container.classList.remove('yxq-dim-wait');
        try {
          s.calculateSize();
          s.currentResolution = 0;
          s.zoomAndPanToInitial();
          s.applyCurrentZoomPan();
          s.updateContentSize(true);
        } catch (e) {}
        break;
      }
    }
    // 当前照片若已经在卡片 DOM 里（缩图/原图同宽高比），直接拿 naturalWidth/Height，
    // 避免点开大图时再发一次探测请求等 whole image。
    function viewerDimsFromCard(index) {
      try {
        if (page.value !== 'cards') return null;
        const imgs = document.querySelectorAll('.swipe-card .card-photo-item');
        const img = imgs[index];
        if (img && img.naturalWidth && img.naturalHeight) {
          return { w: img.naturalWidth, h: img.naturalHeight };
        }
      } catch (_) {}
      return null;
    }
    async function viewerOpen(photos, index, thumbs, meta) {
      if (!photos || !photos.length) return;
      const idx = Math.min(Math.max(index || 0, 0), photos.length - 1);
      // 原图尺寸（API photos_meta，与 photos 索引对齐）：第一帧就用最终显示框，
      // 不下载原图、不探测缩图即确定全屏区域。
      const metaDim = function (i) {
        const m = meta && meta[i];
        if (!m || !(m.w > 0) || !(m.h > 0)) return null;
        return { w: m.w, h: m.h };
      };
      // 缩图与 photos 对齐；没有缩图的项回退原图（海报等）。
      const displayPhotos = photos.map(function (p, i) {
        return (thumbs && thumbs[i]) ? thumbs[i] : p;
      });
      const seq = ++viewerOpenSeq;
      viewerFullPhotos = photos.slice();
      viewerDisplayPhotos = displayPhotos.slice();
      viewerFullState = {};
      // 新一轮打开前取消旧的 abort destroy timer，避免它误杀新实例。
      clearViewerAbortTimers();
      // 点击后立即显示遮罩；PhotoSwipe 资源未就绪也先给反馈（≤100ms 可见）。
      viewerLoading.value = true;
      armViewerLoadingTimeout();
      try {
        await loadPhotoSwipe();
      } catch (e) {
        clearViewerLoading();
        if (seq !== viewerOpenSeq) return;
        try { vant.showToast('图片查看器加载失败，已直接打开原图'); } catch (_) {}
        window.open(photos[idx], '_blank');
        return;
      }
      if (seq !== viewerOpenSeq) return;
      viewerEnsure();
      // 上次取消可能仍在 close/destroy 的异步过程中：必须彻底移除旧实例，
      // 否则 PhotoSwipeLightbox.loadAndOpen 会因为 window.pswp 仍存在而直接失败。
      discardViewerInstance((viewerLightbox && viewerLightbox.pswp) || window.pswp);
      // 上一次取消留下的隐藏类：新实例创建前清掉，避免影响正常打开。
      clearViewerCancelledClass();
      const gallery = document.getElementById('pswp-gallery');
      if (!gallery) { clearViewerLoading(); return; }
      // href 优先缩图：PhotoSwipe 一打开就有画面；原图由 upgradeViewerSlide 后台替换。
      gallery.innerHTML = displayPhotos.map(p => '<a href="' + attrEscape(p) + '"></a>').join('');
      const anchors = gallery.querySelectorAll('a');
      viewerDims = new Map();
      const setDim = (i, d) => {
        if (!d || i < 0 || i >= displayPhotos.length) return;
        viewerDims.set(i, d);
        if (anchors[i]) {
          anchors[i].dataset.pswpWidth = d.w;
          anchors[i].dataset.pswpHeight = d.h;
        }
        applyDimsToLiveSlide(seq, i, d);
      };
      const openAtIndex = (d) => {
        if (seq !== viewerOpenSeq) return;
        if (d) setDim(idx, d);
        showPhotoViewer.value = true;
        viewerPendingSeq = seq;
        viewerLightbox.loadAndOpen(idx);
        // 缩图已缓存/初始化前 complete 时 loadComplete 可能不再触发；
        // 30/100/250ms 兜底撤遮罩并触发原图后台升级。
        setTimeout(function () { clearViewerLoadingIfLoaded(seq); }, 30);
        setTimeout(function () { clearViewerLoadingIfLoaded(seq); }, 100);
        setTimeout(function () { clearViewerLoadingIfLoaded(seq); }, 250);
        // 只预探当前+前后各一张缩图，避免一次探测整组原图。
        [idx - 1, idx + 1].forEach(function (j) {
          if (j < 0 || j >= displayPhotos.length) return;
          const md = metaDim(j);
          if (md) { setDim(j, md); return; }
          viewerGetDims(displayPhotos[j], function (dj) {
            if (seq === viewerOpenSeq) setDim(j, dj);
          });
        });
      };
      // 优先 API 的原图尺寸；没有时退回卡片缩图 natural size（仅海报/旧接口）。
      const hint = metaDim(idx) || viewerDimsFromCard(idx);
      if (hint) { openAtIndex(hint); return; }
      // 无卡片 DOM（他人资料页/海报）：只探测当前一张。缩图很小；无缩图时是原图 fallback。
      let settled = false;
      let capTimer = null;
      const finish = (d) => {
        if (settled) return;
        settled = true;
        if (capTimer) clearTimeout(capTimer);
        openAtIndex(d);
      };
      capTimer = setTimeout(() => finish(null), 800);
      viewerGetDims(displayPhotos[idx], (d) => finish(d));
    }
    function openPhotoViewerForCurrent() {
      const photos = currentCard.value.photos || (currentCard.value.photo ? [currentCard.value.photo] : []);
      viewerOpen(photos, photoIndex.value, cardDisplayPhotos(currentCard.value), currentCard.value.photos_meta);
    }
    function viewPoster() {
      if (currentActivity.value && currentActivity.value.poster) viewerOpen([currentActivity.value.poster], 0);
    }
    function openPhotoViewerForUser(u) {
      const photos = u.photos || (u.photo ? [u.photo] : []);
      viewerOpen(photos, 0, u.photos_thumb || [], u.photos_meta);
    }
    function gotoCards(from) {
      prevPage.value = from || 'likes';
      page.value = 'cards';
    }
    let notificationsPromise = null;
    let readAllPromise = null;
    let notificationStream = null;
    let notificationPollTimer = null;
    let notificationReconnectTimer = null;
    let notificationSyncStarted = false;

    function applyNotificationSnapshot(data) {
      notifications.value = data.notifications || [];
      unreadCount.value = Number(data.unread) || 0;
    }
    function loadNotifications() {
      if (notificationsPromise) return notificationsPromise;
      notificationsPromise = api('/api/notifications').then((data) => {
        applyNotificationSnapshot(data);
      }).catch(() => {}).then(() => {
        notificationsPromise = null;
      });
      return notificationsPromise;
    }
    function markNotificationsRead() {
      if (readAllPromise) return readAllPromise;
      if (unreadCount.value <= 0) return Promise.resolve();
      readAllPromise = api('/api/notifications/read-all', { method: 'POST' }).then((data) => {
        unreadCount.value = Number(data.unread) || 0;
      }).catch(() => {}).then(() => {
        readAllPromise = null;
      });
      return readAllPromise;
    }
    function refreshNotifications() {
      return loadNotifications().then(() => {
        if (page.value === 'likes' && unreadCount.value > 0) return markNotificationsRead();
      });
    }
    function stopNotificationPolling() {
      if (notificationPollTimer) { clearInterval(notificationPollTimer); notificationPollTimer = null; }
    }
    function startNotificationPolling() {
      if (notificationPollTimer) return;
      notificationPollTimer = setInterval(refreshNotifications, 12000);   // 无 SSE 时 12s 轮询兜底
    }
    function scheduleNotificationReconnect() {
      if (notificationReconnectTimer) return;
      notificationReconnectTimer = setTimeout(() => {
        notificationReconnectTimer = null;
        if (user.value) connectNotificationStream();
      }, 30000);
    }
    function connectNotificationStream() {
      if (!user.value || notificationStream) return;
      if (typeof EventSource === 'undefined') { startNotificationPolling(); return; }
      let es = null;
      try { es = new EventSource('/legacy/api/notifications/stream'); } catch (e) {
        startNotificationPolling();
        scheduleNotificationReconnect();
        return;
      }
      notificationStream = es;
      es.addEventListener('unread', (ev) => {
        stopNotificationPolling();
        let data = null;
        try { data = JSON.parse(ev.data || '{}'); } catch (e) { return; }
        const next = Number(data.unread) || 0;
        const changed = next !== unreadCount.value;
        unreadCount.value = next;
        if (page.value === 'likes') {
          // 正在看消息页：把新到达的通知同步进列表并置为已读
          loadNotifications().then(() => { if (unreadCount.value > 0) markNotificationsRead(); });
        } else if (changed && data.latest_id) {
          loadNotifications();
        }
      });
      es.onopen = stopNotificationPolling;
      es.onerror = () => {
        if (notificationStream !== es) return;
        try { es.close(); } catch (e) {}
        notificationStream = null;
        startNotificationPolling();   // EventSource 自带重连不可靠时，先用轮询保证 badge 不落后
        scheduleNotificationReconnect();
      };
    }
    function startNotificationSync() {
      if (!user.value) return;
      loadNotifications();
      if (!notificationSyncStarted) {
        notificationSyncStarted = true;
        connectNotificationStream();
      }
    }
    function stopNotificationSync() {
      if (notificationStream) { try { notificationStream.close(); } catch (e) {} notificationStream = null; }
      stopNotificationPolling();
      if (notificationReconnectTimer) { clearTimeout(notificationReconnectTimer); notificationReconnectTimer = null; }
    }

    // 站内嵌入页仅供管理后台使用：同源 iframe + 返回栏；返回键/浏览器返回都只关 overlay。
    // 打开期间锁住文档滚动：iframe 内滚动/到底回弹不带动外层页面（iOS 对 body
    // overflow:hidden 不可靠，用 position:fixed + 负 top 把视口钉住，关闭时还原）。
    let embedLockY = 0;
    let embedLocked = false;
    function lockPageScroll(){
      if(embedLocked) return;
      embedLocked = true;
      embedLockY = readScrollY();
      const b = document.body;
      b.style.position = 'fixed';
      b.style.top = '-' + embedLockY + 'px';
      b.style.left = '0';
      b.style.right = '0';
      b.style.width = '100%';
      b.style.overflow = 'hidden';
    }
    function unlockPageScroll(){
      if(!embedLocked) return;
      embedLocked = false;
      const b = document.body;
      b.style.position = ''; b.style.top = ''; b.style.left = '';
      b.style.right = ''; b.style.width = ''; b.style.overflow = '';
      try { window.scrollTo(0, embedLockY); } catch(e) {}
    }
    function openEmbed(url, title) {
      embedTargetUrl.value = url;
      embedUrl.value = url;
      embedTitle.value = title || '';
      embedLoading.value = true;
      embedReady.value = false;
      embedFailed.value = false;
      embedOpen.value = true;
      lockPageScroll();
      startEmbedWatchdog();
    }
    function startEmbedWatchdog() {
      stopEmbedWatchdog();
      // 管理台首屏最慢 p99 约 10s；15s 还没 ready 就认为加载失败（断网/被拦/白屏）
      embedWatchdog = setTimeout(() => {
        embedWatchdog = null;
        if (embedOpen.value && !embedReady.value && !embedFailed.value) markEmbedFailed();
      }, 15000);
    }
    function stopEmbedWatchdog() {
      if (embedWatchdog) { clearTimeout(embedWatchdog); embedWatchdog = null; }
    }
    function markEmbedFailed() {
      if (!embedOpen.value || embedReady.value) return;
      embedFailed.value = true;
      embedLoading.value = false;
      stopEmbedWatchdog();
    }
    function retryEmbed() {
      if (!embedOpen.value) return;
      const target = embedTargetUrl.value || '/admin?embed=1';
      const title = embedTitle.value;
      closeEmbed();
      nextTick(() => { openEmbed(target, title); });
    }
    function returnAdminHome() {
      const title = '管理后台';
      closeEmbed();
      nextTick(() => { openEmbed('/admin?embed=1', title); });
    }
    function closeEmbed() {
      if (!embedOpen.value) return;
      stopEmbedWatchdog();
      embedOpen.value = false;
      embedLoading.value = false;
      embedReady.value = false;
      embedFailed.value = false;
      embedUrl.value = '';
      embedTitle.value = '';
      unlockPageScroll();
    }
    // 管理台沿用 SSR 自己的交互，只把外壳（页头/页脚/底色/字体/token）换成旧 App 风格。
    // /admin 的 CSP 是 style-src 'self'：主题走同源外部样式表 + class，禁止注入
    // <style> 或 inline style，否则 staging 会报 CSP console error。
    const EMBED_THEME_LINK_ID = 'yxq-embed-theme';
    const EMBED_THEME_HREF = '/static/embed-admin.css?v=1';
    function applyEmbedTheme(doc) {
      return new Promise((resolve) => {
        if (!doc || !doc.documentElement) { resolve(); return; }
        const root = doc.documentElement;
        try {
          root.classList.add('yxq-embed');
          root.classList.toggle('yxq-embed-dark', !!isDark.value);
        } catch (e) {}
        let settled = false;
        let timer = null;
        const done = () => {
          if (settled) return;
          settled = true;
          if (timer) clearTimeout(timer);
          resolve();
        };
        try {
          const head = doc.head || root;
          let link = doc.getElementById(EMBED_THEME_LINK_ID);
          if (!link) {
            link = doc.createElement('link');
            link.id = EMBED_THEME_LINK_ID;
            link.rel = 'stylesheet';
            link.href = EMBED_THEME_HREF;
            head.appendChild(link);
          }
          if (link.sheet) { done(); return; }
          link.addEventListener('load', done, { once: true });
          link.addEventListener('error', done, { once: true });
          timer = setTimeout(done, 2000);   // CSS 卡住时兜底，避免 iframe 一直停在骨架
        } catch (e) {
          done();
        }
      });
    }
    async function onEmbedLoad() {
      const frame = embedFrame.value;
      if (!frame) return;
      let doc = null, win = null;
      try { doc = frame.contentDocument; win = frame.contentWindow; } catch (e) { markEmbedFailed(); return; }
      if (!doc || !win) { markEmbedFailed(); return; }
      let path = '', search = '', href = '';
      try { path = win.location.pathname; search = win.location.search || ''; href = win.location.href || ''; } catch (e) { markEmbedFailed(); return; }
      if (!path || href === 'about:blank') return;
      // 管理台活动入口在 iframe 内切到 v1 原版 SPA（/app?native_admin=1&p=group_admin）。
      // SPA 自带自己的主题与布局，不能套 embed-admin.css（会改 body 字号/内边距）。
      let nativeAdminFrame = false;
      try { nativeAdminFrame = path === '/app' && new URLSearchParams(search).get('native_admin') === '1'; } catch (e) {}
      if (!nativeAdminFrame) await applyEmbedTheme(doc);
      // await 期间可能已导航/关闭：旧文档不得把新页面的骨架提前撤掉。
      let currentDoc = null;
      try { currentDoc = frame.contentDocument; } catch (e) { return; }
      if (embedFrame.value !== frame || currentDoc !== doc) return;
      // 管理台内部导航是整页跳转：离开前立刻盖回骨架，避免 iframe 白屏。
      try {
        if (win.__yxqEmbedBeforeUnload) win.removeEventListener('beforeunload', win.__yxqEmbedBeforeUnload);
        win.__yxqEmbedBeforeUnload = () => { embedLoading.value = true; embedReady.value = false; };
        win.addEventListener('beforeunload', win.__yxqEmbedBeforeUnload);
      } catch (e) {}
      const inScope = path === '/admin' || path.indexOf('/admin/') === 0 || nativeAdminFrame;
      if (inScope) {
        // 被 X-Frame-Options 拦截或被反代返回空白页时，iframe 也会触发 load；
        // SSR 管理页正文为空就按失败处理，给出「返回管理台」，不要留白屏。
        // native SPA 首帧是空骨架（文字靠 JS 异步填充），不能按 innerText 判定。
        if (!nativeAdminFrame) {
          let bodyText = '';
          try { bodyText = (doc.body && doc.body.innerText ? doc.body.innerText : '').trim(); } catch (e) {}
          if (!bodyText) { markEmbedFailed(); return; }
        }
        embedFailed.value = false;
        embedLoading.value = false;
        embedReady.value = true;
        stopEmbedWatchdog();
        return;
      }
      // 退出登录或会话失效：关闭 overlay，回到 SPA 的登录入口
      closeEmbed();
      if (path === '/login' || path === '/activate') {
        // 会话已失效：先出 App 内提示，用户点了再跳登录页，不自动整页导航
        notifySessionExpired();
        return;
      }
      if (path === '/') { vant.showToast('已完成'); return; }
      if (path === '/app') { vant.showToast('已完成'); return; }
      vant.showToast('已返回');
    }
    async function openSignupSheet() {
      signupSheetShow.value = true;
      const aid = currentActivity.value.record_id;
      // 命中缓存则直接展示，避免每次都加载
      if (signupCache.value[aid]) { signupList.value = signupCache.value[aid]; return; }
      if (loadingSignups.value) return;
      loadingSignups.value = true;
      try {
        const data = await api('/api/activities/' + aid + '/signups');
        signupList.value = data.signups || [];
        signupCache.value[aid] = signupList.value;
      } catch(e) { vant.showToast(e.message); }
      loadingSignups.value = false;
    }
    async function openUserDetail(openid) {
      // 先记下来路：关闭名单弹层与跳资料页会同时触发 watcher，同时 push+back 会互相踩踏，
      // 由名单 watcher 凭此标记跳过 back（只保留资料页的 push），栈保持 [详情, 名单, 资料] 供返回重开名单
      userDetailFromSheet.value = true;
      signupSheetShow.value = false;
      userDetailFrom.value = 'activity_detail';
      page.value = 'user_detail';
      userDetail.value = null;
      try {
        const data = await api('/api/users/' + openid + '/public');
        // 在途喜欢快照有 60s 滞后：刚喜欢过的人进资料页也隐藏按钮
        if (data && (data.liked || justLiked.has(openid))) { data.liked = true; data.can_like = false; }
        userDetail.value = data;
      } catch(e) { vant.showToast(e.message); }
    }
    async function openUserDetailFromPage(openid, from) {
      // 从志愿选择/分组结果/必聊结果进入：不经过报名名单弹层（否则返回会命中名单重开特例）
      userDetailFromSheet.value = false;
      userDetailFrom.value = from;
      page.value = 'user_detail';
      userDetail.value = null;
      try {
        const data = await api('/api/users/' + openid + '/public');
        // 在途喜欢快照有 60s 滞后：刚喜欢过的人进资料页也隐藏按钮
        if (data && (data.liked || justLiked.has(openid))) { data.liked = true; data.can_like = false; }
        userDetail.value = data;
      } catch(e) { vant.showToast(e.message); }
    }
    function openUserDetailFromGs(openid) {
      return openUserDetailFromPage(openid, 'group_select');
    }
    async function copyText(text, okMsg) {
      try {
        await navigator.clipboard.writeText(text);
        vant.showToast(okMsg || '已复制');
      } catch(e) {
        const ta = document.createElement('textarea');
        ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
        document.body.appendChild(ta); ta.select();
        try { document.execCommand('copy'); vant.showToast(okMsg || '已复制'); }
        catch(_) { vant.showToast('复制失败，请手动复制'); }
        document.body.removeChild(ta);
      }
    }
    function copyInviteFull() {
      const uid = (user.value && user.value.user_id) || '';
      if (!uid) { vant.showToast('暂无邀请码'); return; }
      copyText(inviteShareText.value, '已复制');
    }
    function copyInviteCode() {
      const uid = (user.value && user.value.user_id) || '';
      if (!uid) { vant.showToast('暂无邀请码'); return; }
      copyText(uid, '邀请码已复制');
    }

    // 账号状态切换（单身 <-> 已脱单）
    const accountStatusLabel = computed(() => (user.value && user.value.account_status === '已脱单') ? '已脱单' : '单身');
    let accountStatusProc = false;   // 确认弹窗阶段也要防连点（弹窗是异步的，actionLoading 覆盖不到）
    async function toggleAccountStatus() {
      if (accountStatusProc || actionLoading.value) return;
      accountStatusProc = true;
      try {
        const toHidden = user.value && user.value.account_status === '单身';
        try {
          await vant.showConfirmDialog({
            title: toHidden ? '脱单' : '恢复单身',
            message: toHidden
              ? '脱单后你将不出现在他人的牵线卡片中（自己仍可浏览），确定吗？'
              : '恢复单身后你的资料将重新出现在他人的牵线卡片中，确定吗？',
            confirmButtonColor: '#ff6b81',
          });
        } catch(e) { return; }
        actionLoading.value = true;
        try {
          const data = await api('/api/account/status', { method: 'POST' });
          if (user.value) user.value.account_status = data.account_status;
          vant.showToast(data.account_status === '已脱单' ? '已脱单，将不再出现在他人牵线中' : '已恢复单身');
        } catch(e) { vant.showToast(e.message); }
        finally { actionLoading.value = false; }
      } finally { accountStatusProc = false; }
    }

    // 切换身份（普通用户 <-> 观察员）
    async function switchRole() {
      if (actionLoading.value) return;
      const target = isObserver.value ? 'user' : 'observer';
      const roles = availableRoles.value || [];
      if (roles.length <= 1 || !roles.includes(target)) {
        const name = target === 'observer' ? '村情六处' : '普通用户';
        vant.showToast(`你尚未注册${name}身份，请先完成对应注册`);
        return;
      }
      actionLoading.value = true;
      try {
        await api('/api/account/switch', { method: 'POST', body: JSON.stringify({ role: target }) });
        // 原地按新身份重拉数据（旧实现是 location.reload()，会让浏览器闪整页进度条）
        await reloadUserInPlace();
      } catch(e) { vant.showToast(e.message); }
      finally { actionLoading.value = false; }
    }
    // 身份切换后等价于刷新：回到牵线页，走一次「启动已登录」的软重载，不产生整页导航。
    async function reloadUserInPlace(){
      deckRestored = false;      // 允许按新身份恢复牌堆位置
      bootEntryHash = '';
      page.value = 'cards';
      await bootAuthenticated();
    }

    // 喜欢 - 弹出附言对话框
    function openLikeDialog() {
      if (!currentCard.value || !currentCard.value.openid) return;
      if (currentCard.value.liked || justLiked.has(currentCard.value.openid)) {
        vant.showToast('你已经喜欢过TA了');
        return;
      }
      // 两池分开：匿名 10 颗/月，实名 1 次/月（外加麦穗兑换来的名额）。
      // 只有两池都空才拦——只看匿名会把有实名名额的用户挡在门外。
      if ((user.value.anon_left || 0) <= 0 && (user.value.real_left || 0) <= 0) {
        vant.showToast('本月喜欢额度已用完，邀请好友得麦穗，麦穗可换实名喜欢');
        showInviteDialog.value = true;
        return;
      }
      likeTarget.value = { openid: currentCard.value.openid, nickname: currentCard.value.nickname };
      likeMessage.value = '';
      // 匿名额度用完就直接落在实名上，免得用户点了个禁用的单选框、卡在发送按钮
      likeType.value = (user.value.anon_left || 0) > 0 ? '匿名' : '实名';
      likeDialogTitle.value = '送给「' + currentCard.value.nickname + '」一颗爱心';
      showLikeDialog.value = true;
    }
    function openLikeDialogForUser(u) {
      if (!u || !u.openid) return;
      if (u.is_self || (u.user_id && user.value && u.user_id === user.value.user_id)) {
        vant.showToast('不能喜欢自己');
        return;
      }
      if (isObserver.value) {
        vant.showToast('村情六处仅可浏览，不能喜欢');
        return;
      }
      if (u.gender && user.value && user.value.gender && u.gender === user.value.gender) {
        vant.showToast('仅限异性之间喜欢');
        return;
      }
      if (u.liked || justLiked.has(u.openid)) {
        vant.showToast('你已经喜欢过TA了');
        return;
      }
      // 两池分开：匿名 10 颗/月，实名 1 次/月（外加麦穗兑换来的名额）。
      // 只有两池都空才拦——只看匿名会把有实名名额的用户挡在门外。
      if ((user.value.anon_left || 0) <= 0 && (user.value.real_left || 0) <= 0) {
        vant.showToast('本月喜欢额度已用完，邀请好友得麦穗，麦穗可换实名喜欢');
        showInviteDialog.value = true;
        return;
      }
      likeTarget.value = { openid: u.openid, nickname: u.nickname };
      likeMessage.value = '';
      // 匿名额度用完就直接落在实名上，免得用户点了个禁用的单选框、卡在发送按钮
      likeType.value = (user.value.anon_left || 0) > 0 ? '匿名' : '实名';
      likeDialogTitle.value = '送给「' + u.nickname + '」一颗爱心';
      showLikeDialog.value = true;
    }
    // 校验+实名二次确认+发送：只由弹窗确认钮调用，返回 true=已发出
    async function trySubmitLike(){
      const msg = likeMessage.value.trim();
      if (!msg) {
        vant.showToast('请填写附言');
        return false;
      }
      if ([...msg].length < 10) {
        vant.showToast('附言需10字以上');
        return false;
      }
      // 实名喜欢二次确认：发出去就收不回来了，必须让用户明确知道这一点。
      // 匿名不需要——它 3 个月后自动失效，代价是可逆的。
      if (likeType.value === '实名') {
        try {
          await vant.showConfirmDialog({
            title: '确认使用实名喜欢？',
            message: 'TA 会收到通知，并且知道是你喜欢TA。\n\n实名喜欢发出后不可撤回，本月名额不会返还。',
            confirmButtonText: '确认发送',
            cancelButtonText: '再想想',
            confirmButtonColor: '#ff6b81'
          });
        } catch (e) {
          return false;  // 用户点了「再想想」，不关弹窗、不发送
        }
      }
      return await doLike();
    }
    async function onLikeDialogBeforeClose(action) {
      if (likeSubmitting.value) return false;   // 发送在途：确认/取消/遮罩/返回一律不关（结果回来才允许关，P16）
      if (action === 'confirm') return await trySubmitLike();
      // 取消/点遮罩关闭：有未发送附言 → 先走通用离开拦截
      if (showUnsavedConfirm.value) return false;   // 通用弹窗已开（叠在本弹窗上）：只允许通过它收尾
      if (guardOverlay('likeDialog', () => { showLikeDialog.value = false; })) return false;
      return true;
    }
    async function doLike() {
      if (!likeTarget.value || !likeTarget.value.openid) return false;
      if (likeSubmitting.value) return false;
      const target = likeTarget.value;
      actionLoading.value = true;
      likeSubmitting.value = true;
      let ok = false;
      try {
        const data = await api('/api/like', {
          method: 'POST',
          body: JSON.stringify({ target_openid: target.openid, message: likeMessage.value, like_type: likeType.value })
        });
        if (data.mutual) {
          vant.showDialog({ title: '💕 相互喜欢', message: '恭喜！你和' + target.nickname + '相互喜欢了！到「相互喜欢」填写并同意交换后，就能看到对方微信号。', confirmButtonColor: '#ff6b81' });
        } else { vant.showToast('喜欢成功'); }
        justLiked.add(target.openid);
        lastLikeAt.value = Date.now();
        // 原地置灰：卡片保留在牌堆，♥ 变灰不可点击（产品语义：喜欢≠消失）
        const deckCard = cards.value.find(c => c.openid === target.openid);
        if (deckCard) deckCard.liked = true;
        if (currentCard.value && currentCard.value.openid === target.openid) currentCard.value.liked = true;
        // 资料页（报名名单点进）喜欢成功后立即隐藏「喜欢TA」按钮
        if (userDetail.value && userDetail.value.openid === target.openid) {
          userDetail.value.liked = true;
          userDetail.value.can_like = false;
        }
        // 后端返回的是权威额度（已含在途意图）。回退分支按两池分开乐观扣。
        if (user.value) {
          if (typeof data.anon_left === 'number') {
            user.value.anon_left = data.anon_left;
            user.value.real_left = data.real_left;
            user.value.real_total = data.real_total;
            user.value.real_permanent = data.real_permanent;
          } else if (likeType.value === '实名') {
            user.value.real_left = Math.max(0, (user.value.real_left || 1) - 1);
          } else {
            user.value.anon_left = Math.max(0, (user.value.anon_left || 1) - 1);
          }
        }
        // 乐观更新消息页计数与列表，配合后端在途意图，避免 60s 快照滞后
        myLikesCount.value = (myLikesCount.value || 0) + 1;
        if (myLikesLoaded.value && deckCard) {
          const exists = myLikesList.value.some(x => x.openid === target.openid);
          if (!exists) myLikesList.value.push({ ...deckCard, mutual: false, like_type: likeType.value, message: '' });
        } else {
          myLikesLoaded.value = false; // 未加载过则标记下次重拉
        }
        ok = true;
      } catch(e) { vant.showToast(e.message); }
      actionLoading.value = false;
      likeSubmitting.value = false;
      return ok;
    }

    // 反馈 - 弹出反馈弹窗
    function openFeedbackDialog() {
      if (!currentCard.value || !currentCard.value.openid) return;
      feedbackTarget.value = { openid: currentCard.value.openid, nickname: currentCard.value.nickname };
      feedbackReason.value = '已有对象';
      feedbackText.value = '';
      showFeedbackDialog.value = true;
    }
    async function onFeedbackBeforeClose(action) {
      if (feedbackSubmitting.value) return false;   // 提交在途：任何关闭都被拦（结果回来才允许关，P16）
      if (action === 'confirm') return await doFeedback();
      // 取消/点遮罩关闭：有未提交内容 → 先走通用离开拦截
      if (showUnsavedConfirm.value) return false;
      if (guardOverlay('feedbackDialog', () => { showFeedbackDialog.value = false; })) return false;
      return true;
    }
    async function doFeedback() {
      if (!feedbackTarget.value || !feedbackTarget.value.openid) return false;
      if (feedbackSubmitting.value) return false;
      if (feedbackReason.value === '其他' && !feedbackText.value.trim()) {
        vant.showToast('请补充说明');
        return false;
      }
      let reason = feedbackReason.value;
      if (feedbackText.value.trim()) reason = reason + '：' + feedbackText.value.trim();
      actionLoading.value = true;
      feedbackSubmitting.value = true;
      let ok = false;
      try {
        await api('/api/feedback', {
          method: 'POST',
          body: JSON.stringify({ target_openid: feedbackTarget.value.openid, reason })
        });
        vant.showToast('感谢反馈，已提交管理员');
        showFeedbackDialog.value = false;
        ok = true;
      } catch(e) { vant.showToast(e.message); }
      actionLoading.value = false;
      feedbackSubmitting.value = false;
      return ok;
    }

    // 举报留言
    function openReportDialog(m) {
      reportTarget.value = m;
      reportReason.value = '骚扰/引战';
      reportReasonText.value = '';
      showReportDialog.value = true;
    }
    async function onReportBeforeClose(action) {
      if (reportSubmitting.value) return false;   // 提交在途：任何关闭都被拦（结果回来才允许关，P16）
      if (action === 'confirm') {
        if (reportSubmitting.value) return false;
        await doReport();
        return false;
      }
      // 取消/点遮罩关闭：有未提交内容 → 先走通用离开拦截
      if (showUnsavedConfirm.value) return false;
      if (guardOverlay('reportDialog', () => { showReportDialog.value = false; })) return false;
      return true;
    }
    async function doReport() {
      if (!reportTarget.value || !reportTarget.value.id) return false;
      if (reportReason.value === '其他' && !reportReasonText.value.trim()) {
        vant.showToast('请填写补充说明');
        return false;
      }
      let reason = reportReason.value;
      if (reportReasonText.value.trim()) reason = reason + '：' + reportReasonText.value.trim();
      if (reportSubmitting.value) return false;
      reportSubmitting.value = true;
      let ok = false;
      try {
        await api('/api/messages/' + reportTarget.value.id + '/report', {
          method: 'POST',
          body: JSON.stringify({ reason })
        });
        vant.showToast('已举报，等待管理员处理');
        showReportDialog.value = false;
        // 举报后保持可见，待管理员处理（不从列表移除）
        reportTarget.value = null;
        ok = true;
      } catch(e) { vant.showToast(e.message || '举报失败'); }
      reportSubmitting.value = false;
      return ok;
    }
    async function loadReportedMessages(force) {
      if (!user.value || !user.value.is_admin) return;
      if (!force && reportedLoaded.value) return;
      loadingReported.value = true;
      try {
        const data = await api('/api/messages/reported');
        reportedMessages.value = data.messages || [];
        reportedLoaded.value = true;
        reportedCount.value = reportedMessages.value.filter(x=>x.status==='待处理').length;
      } catch(e) { vant.showToast(e.message || '加载失败'); }
      loadingReported.value = false;
    }
    async function openReportedMessages() {
      page.value = 'reported_messages';
      await loadReportedMessages();
    }
    async function refreshReportedCount() {
      if (!user.value || !user.value.is_admin) return;
      try {
        const data = await api('/api/messages/reported');
        const all = data.messages || [];
        reportedCount.value = all.filter(x=>x.status==='待处理').length;
        if (reportedLoaded.value) reportedMessages.value = all;
        else reportedMessages.value = all;
      } catch(e) {}
    }
    async function handleReported(m, action) {
      const isDelete = action === 'delete';
      const title = isDelete ? '确认同意举报' : '确认驳回举报';
      const message = isDelete ? '确定删除该留言？一级留言将连带删除其下回复。' : '确定驳回举报并恢复该留言？';
      try {
        await vant.showConfirmDialog({ title, message, confirmButtonColor: isDelete ? '#ee0a24' : '#1989fa' });
      } catch(e) { return; }
      // 乐观更新：立即在界面上标记为已处理
      const backup = JSON.parse(JSON.stringify(reportedMessages.value));
      const backupCount = reportedCount.value;
      const backupMessages = JSON.parse(JSON.stringify(messages.value));
      const backupMsgCount = cards.value[cardIndex.value] ? cards.value[cardIndex.value].msg_count : null;
      const now = Date.now();
      // 更新举报管理列表中该条状态
      const idx = reportedMessages.value.findIndex(x => x.id === m.id);
      if (idx !== -1) {
        reportedMessages.value[idx] = { ...reportedMessages.value[idx], status: isDelete ? '已同意' : '已驳回', handled_at: now, action };
        // 保持举报时间倒序：已处理的按原 report_time 排序，不重排即可；徽标仅计待处理
        reportedCount.value = reportedMessages.value.filter(x=>x.status==='待处理').length;
      }
      // 同步更新当前卡片留言列表（乐观）
      if (isDelete) {
        messages.value = messages.value.filter(x => x.id !== m.id && x.parent_id !== m.id);
        if (cards.value[cardIndex.value]) cards.value[cardIndex.value].msg_count = messages.value.length;
      } else {
        // 驳回：若在当前卡片，尝试乐观恢复（若之前因举报隐藏，重新拉取更准，但先乐观）
        if (cards.value[cardIndex.value] && cards.value[cardIndex.value].openid === m.target_openid) {
          // 标记需要刷新，待接口成功后静默刷新
        }
      }
      try {
        await api('/api/messages/' + m.id + '/handle', {
          method: 'POST',
          body: JSON.stringify({ action: isDelete ? 'delete' : 'approve' })
        });
        vant.showToast(isDelete ? '已删除' : '已驳回');
        // 驳回成功后若在当前卡片，刷新以显示恢复的留言（不整页重载，仅刷新留言列表）
        if (!isDelete && cards.value[cardIndex.value] && cards.value[cardIndex.value].openid === m.target_openid) {
          msgLoadedFor = null;
          try { await loadMessages(true); } catch(e){}
        }
        // 后台静默对账：不强制重拉举报列表，保持乐观结果（历史已在本地）
      } catch(e) {
        // 失败回滚
        reportedMessages.value = backup;
        reportedCount.value = backupCount;
        messages.value = backupMessages;
        if (cards.value[cardIndex.value] && backupMsgCount !== null) cards.value[cardIndex.value].msg_count = backupMsgCount;
        vant.showToast(e.message || '操作失败');
      }
    }

    // 意见反馈
    function openSuggestionDialog() {
      suggestionType.value = '功能建议';
      suggestionText.value = '';
      showSuggestionDialog.value = true;
    }
    async function onSuggestionBeforeClose(action) {
      if (suggestionSubmitting.value) return false;   // 提交在途：任何关闭都被拦（结果回来才允许关，P16）
      if (action === 'confirm') {
        if (suggestionSubmitting.value) return false;
        await submitSuggestion();
        return false;
      }
      // 取消/点遮罩关闭：有未提交内容 → 先走通用离开拦截
      if (showUnsavedConfirm.value) return false;
      if (guardOverlay('suggestionDialog', () => { showSuggestionDialog.value = false; })) return false;
      return true;
    }
    // 提交意见反馈：只由弹窗确认钮调用，返回 true=已提交
    async function submitSuggestion() {
      if (suggestionSubmitting.value) return false;
      if (!suggestionText.value.trim()) {
        vant.showToast('请填写反馈内容');
        return false;
      }
      suggestionSubmitting.value = true;
      let ok = false;
      try {
        await api('/api/suggestions', {
          method: 'POST',
          body: JSON.stringify({ type: suggestionType.value, content: suggestionText.value.trim() })
        });
        vant.showToast('感谢反馈，已收到');
        showSuggestionDialog.value = false;
        ok = true;
      } catch(e) { vant.showToast(e.message); }
      suggestionSubmitting.value = false;
      return ok;
    }

    // 消息
    // 静默对账：额度由机器人每 25 秒重算，切页时轻量刷新（不阻塞界面）
    async function syncMe() {
      if (!user.value) return;
      try {
        const me = await api('/api/user/me');
        if (!me) return;
        for (const k of ['anon_left', 'anon_total', 'real_left', 'real_total', 'real_permanent']) {
          if (typeof me[k] === 'number') user.value[k] = me[k];
        }
      } catch (e) {}
    }
    // 相互喜欢页的微信号交换表单状态：保留用户正在输入的值，刷新列表时不丢。
    function normalizeMutualItems(list) {
      const prev = new Map((likesData.value.mutual || []).map(m => [m.initiator_openid, m]));
      return (list || []).map(m => {
        const old = prev.get(m.initiator_openid) || {};
        return {
          ...m,
          edit_wechat: typeof old.edit_wechat === 'string' ? old.edit_wechat : (m.my_wechat || ''),
          edit_agree: !!old.edit_agree,
          submitting: false,
        };
      });
    }
    async function loadLikes() {
      // 仅首次进入显示转圈；之后进入直接展示上次数据，后台静默刷新（接口已毫秒级）
      if (!likesInitialized.value) loadingLikes.value = true;
      try {
        const data = await api('/api/likes/me');
        likesData.value = {
          liked_me: data.liked_me || [],
          mutual: normalizeMutualItems(data.mutual),
        };
        likesInitialized.value = true;
      }
      catch(e) { vant.showToast(e.message); }
      await loadMyLikesCount();
      loadingLikes.value = false;
    }
    async function submitMutualWechat(m) {
      if (!m || m.submitting) return;
      const wechat = String(m.edit_wechat || '').trim();
      if (!wechat) { vant.showToast('请先填写微信号'); return; }
      if (!m.edit_agree) { vant.showToast('请先勾选同意交换微信号'); return; }
      m.submitting = true;
      try {
        await api('/api/mutual/wechat', {
          method: 'POST',
          body: JSON.stringify({ wechat, agree: true }),
        });
        vant.showToast('已保存，双方都同意后即可看到对方微信号');
        const data = await api('/api/likes/me');
        likesData.value = {
          liked_me: data.liked_me || [],
          mutual: normalizeMutualItems(data.mutual),
        };
      } catch (e) {
        vant.showToast(e.message || '保存失败，请稍后再试');
      } finally {
        m.submitting = false;
      }
    }
    async function loadMyLikesCount() {
      try {
        const data = await api('/api/likes/mine');
        const serverCount = (data.likes || []).length;
        // 喜欢后 120s 内不回退计数，避免在途意图过期与快照间隙导致 -1
        if (Date.now() - lastLikeAt.value < 120000 && serverCount < myLikesCount.value) {
          // keep optimistic
        } else {
          myLikesCount.value = serverCount;
        }
      } catch(e) {}
    }

    // 我的喜欢
    async function openMyLikes() {
      page.value = 'my_likes';
      if (myLikesLoaded.value) return; // 已加载则直接展示缓存
      loadingMyLikes.value = true;
      try {
        const data = await api('/api/likes/mine');
        const serverList = data.likes || [];
        // 喜欢后 120s 内不回退：若本地乐观列表更长则保留并合并
        if (Date.now() - lastLikeAt.value < 120000 && myLikesList.value.length > serverList.length) {
          const serverIds = new Set(serverList.map(x => x.openid));
          const merged = [...serverList];
          for (const it of myLikesList.value) {
            if (!serverIds.has(it.openid)) merged.push(it);
          }
          myLikesList.value = merged;
          myLikesCount.value = merged.length;
        } else {
          myLikesList.value = serverList;
          myLikesCount.value = serverList.length;
        }
        myLikesLoaded.value = true;
      } catch(e) { vant.showToast(e.message); }
      loadingMyLikes.value = false;
    }
    function openMutual() {
      loadLikes();
      page.value = 'mutual';
    }
    function openLikedMe() {
      loadLikes();
      page.value = 'liked_me';
    }
    // 取消喜欢已下线：喜欢的有效期是制度性的（匿名 3 个月自动失效、实名永久），
    // 不由用户手动撤销。相关入口和接口都已删除，这里不再保留空壳函数。

    // 我的活动
    // 每次进入都刷新（同活动页）：bot 写入的分组/一对一状态会变，缓存会让卡片显示过期的操作入口；
    // 已有数据时静默替换
    async function openMyActivities() {
      page.value = 'my_activities';
      if (loadingMyActivities.value) return;
      loadingMyActivities.value = true;
      try { const data = await api('/api/activities/mine'); myActivitiesList.value = data.activities; myActivitiesLoaded.value = true; }
      catch(e) { if (!myActivitiesLoaded.value) vant.showToast(e.message); }
      loadingMyActivities.value = false;
    }
    // 我的分组
    async function openMyGroups() {
      page.value = 'my_groups';
      if (myGroupsLoaded.value) return; // 已加载则直接展示缓存
      loadingMyGroups.value = true;
      try { const data = await api('/api/activities/mine/groups'); myGroupsList.value = data.activities; myGroupsLoaded.value = true; }
      catch(e) { vant.showToast(e.message); }
      loadingMyGroups.value = false;
    }
    // 检查「我的」页是否显示「我的分组」入口（报名过且该活动开启分组功能）
    async function checkGroupEntry() {
      try { const data = await api('/api/activities/mine/groups/flag'); hasGroupEntry.value = !!data.has_group_activity; }
      catch(e) { hasGroupEntry.value = false; }
    }

    // 我的资料
    async function openProfileDetail() {
      if (profileLoaded.value) { page.value = 'profile_detail'; return; } // 已加载则直接展示缓存
      page.value = 'profile_detail'; // 先切页，加载态先行（接口慢时用户点了入口却像没反应）
      loadingProfileDetail.value = true;
      try {
        const data = await api('/api/profile');
        profileData.value = data;
        profileLoaded.value = true;
        if (user.value && typeof data.id_valid === 'boolean') user.value.id_valid = data.id_valid;
        if (user.value && typeof data.has_face === 'boolean') user.value.has_face = data.has_face;
      }
      catch(e) {
        vant.showToast(e.message);
        // 没有任何旧数据可展示时退回上一页，不停留在空资料页（有旧数据则保留展示）
        if (!Object.keys(profileData.value || {}).length && page.value === 'profile_detail') handleBack();
      }
      finally { loadingProfileDetail.value = false; }
    }
    function openPasswordPage() {
      passwordForm.value = { current: '', password: '', confirm: '' };
      passwordErrors.value = { current: '', password: '', confirm: '' };
      passwordSubmitError.value = '';
      passwordReturnScroll = window.scrollY || (document.scrollingElement && document.scrollingElement.scrollTop) || 0;
      page.value = 'password';
      // 滚动复位交给 Transition @enter（onPageEnter）：out-in 离场期间旧「我的」页仍可见，
      // 若此处同步 scrollTo(0,0) 会把它闪到顶部；进场首帧 opacity 0 时复位才看不到跳动。
    }
    async function submitPassword() {
      if (passwordSubmitting.value) return;
      const f = passwordForm.value;
      const errs = { current: '', password: '', confirm: '' };
      if (!f.current) errs.current = '请输入目前密码';
      if (!f.password) errs.password = '请输入新密码';
      else if (f.password.length < 8) errs.password = '新密码至少 8 位';
      if (!f.confirm) errs.confirm = '请再次输入新密码';
      else if (f.password !== f.confirm) errs.confirm = '两次输入的新密码不一致';
      passwordErrors.value = errs;
      passwordSubmitError.value = '';
      if (errs.current || errs.password || errs.confirm) return;
      passwordSubmitting.value = true;
      try {
        await api('/api/password', {
          method: 'POST',
          body: JSON.stringify({
            current_password: f.current,
            password: f.password,
            password_confirm: f.confirm
          })
        });
        vant.showToast('密码已更新');
        // 请求在途时用户可能已返回/切页：成功只提示，不把画面硬拉回 profile。
        if (page.value === 'password') {
          passwordFormDone = true;
          page.value = 'profile';
        }
      } catch (e) {
        // 401 已由 api() 统一弹「登录已过期」提示，这里只处理字段级错误
        const msg = e.message || '修改失败，请重试';
        // 后端明确指出来源字段时贴在该字段下方，其余错误显示在表单顶部。
        if (msg.includes('目前密码') || msg.includes('密码不正确')) {
          passwordErrors.value = { ...passwordErrors.value, current: msg };
        } else {
          passwordSubmitError.value = msg;
        }
        vant.showToast(msg);
      } finally {
        passwordSubmitting.value = false;
      }
    }
    function startIdInvalidOverlay(){
      if(showIdInvalidDialog.value) return;
      showIdInvalidDialog.value = true;
      idInvalidCountdown.value = 5;
      if(idInvalidTimer) clearInterval(idInvalidTimer);
      idInvalidTimer = setInterval(()=>{
        idInvalidCountdown.value -= 1;
        if(idInvalidCountdown.value <= 0){
          clearInterval(idInvalidTimer);
          idInvalidTimer = null;
          showIdInvalidDialog.value = false;
          if(page.value !== 'profile_edit') openProfileEdit();
        }
      }, 1000);
    }
    async function checkIdValidForPage(targetPage){
      if(targetPage === 'profile_edit') return;
      if(nativeAdmin.value) return;   // 管理台原生管理页不受用户端门禁影响
      try{
        // 优先用首页已返回的服务端事实，其次 profileData，最后拉取资料
        let valid = user.value && typeof user.value.id_valid === 'boolean' ? user.value.id_valid : null;
        if (valid === null) {
          valid = profileData.value && typeof profileData.value.id_valid === 'boolean' ? profileData.value.id_valid : null;
        }
        if(valid === null){
          try{
            const d = await api('/api/profile');
            // 缓存但不切页
            profileData.value = Object.assign({}, profileData.value, d);
            profileLoaded.value = true;
            valid = d.id_valid;
          }catch(e){ return; }
        }
        if(valid === false) startIdInvalidOverlay();
      }catch(e){}
    }
    function guardTabChange(name){
      // 身份证有误时禁止 tab 跳离修改资料：弹全屏提醒，不切页
      const invalid = (user.value && user.value.id_valid === false)
        || (profileData.value && profileData.value.id_valid === false);
      if(invalid){
        startIdInvalidOverlay();
        return false;
      }
      // 无含人脸照片时禁止 tab 跳离修改资料
      const noFace = profileData.value && profileData.value.has_face === false;
      if(noFace){
        startFaceInvalidOverlay();
        return false;
      }
      // 未保存离开拦截（守卫注册表）：先弹弹窗，不切页；提交执行中 → 提示并停留
      if(guardLeave(() => { page.value = name; })) return false;
      // 批次10 追加：首次点击 tab 时若 chunk 未就绪，保留当前页直到加载完成，
      // 避免出现「加载中...」文字或空白骨架；预取通常已让这里直接通过。
      if (TAB_ORDER.includes(name) && !yxqPageChunkReady(name)) {
        return yxqEnsurePageChunk(name);
      }
      return true;
    }
    function goIdInvalidNow(){
      if(idInvalidTimer){ clearInterval(idInvalidTimer); idInvalidTimer=null; }
      showIdInvalidDialog.value = false;
      openProfileEdit();
    }
    // —— 人脸照片门禁（本人照片需至少 1 张可检出人脸，否则仅可进修改资料）——
    function startFaceInvalidOverlay(){
      if(showFaceInvalidDialog.value) return;
      showFaceInvalidDialog.value = true;
      faceInvalidCountdown.value = 5;
      if(faceInvalidTimer) clearInterval(faceInvalidTimer);
      faceInvalidTimer = setInterval(()=>{
        faceInvalidCountdown.value -= 1;
        if(faceInvalidCountdown.value <= 0){
          clearInterval(faceInvalidTimer);
          faceInvalidTimer = null;
          showFaceInvalidDialog.value = false;
          if(page.value !== 'profile_edit') openProfileEdit();
        }
      }, 1000);
    }
    async function checkFaceValidForPage(targetPage){
      if(targetPage === 'profile_edit') return;
      if(nativeAdmin.value) return;   // 管理台原生管理页不受用户端门禁影响
      try{
        let valid = profileData.value && typeof profileData.value.has_face === 'boolean' ? profileData.value.has_face : null;
        if(valid === null){
          try{
            const d = await api('/api/profile');
            profileData.value = Object.assign({}, profileData.value, d);
            profileLoaded.value = true;
            valid = d.has_face;
          }catch(e){ return; }
        }
        if(valid === false) startFaceInvalidOverlay();
      }catch(e){}
    }
    function goFaceInvalidNow(){
      if(faceInvalidTimer){ clearInterval(faceInvalidTimer); faceInvalidTimer=null; }
      showFaceInvalidDialog.value = false;
      openProfileEdit();
    }
    // 分组
    async function goGroupSelect(activityId) {
      groupSelectFrom.value = page.value; // 记录来路，用于返回（重试 reload 不覆盖来路，P11）
      currentActivityId.value = activityId;
      page.value = 'group_select';
      selectedChoices.value = [];
      candidateQuery.value = '';
      showGsTray.value = false;
      gsTouchedOids = new Set();
      gsPrefillNotice.value = false;
      gsUnlistedCount.value = 0;
      gsBaseline = '';
      await loadGroupCandidates(activityId);
    }
    async function loadGroupCandidates(activityId) {
      const req = ++gsCandReq;
      loadingCandidates.value = true;
      candidatesError.value = false;
      gsSigninBlocked.value = false;
      gsPrefillNotice.value = false;
      gsSlowLoading.value = false;
      gsVerySlowLoading.value = false;
      clearGsLoadTimers();
      gsSlowTimer = setTimeout(() => { if (req === gsCandReq && loadingCandidates.value) gsSlowLoading.value = true; }, 3000);
      gsVerySlowTimer = setTimeout(() => { if (req === gsCandReq && loadingCandidates.value) gsVerySlowLoading.value = true; }, 10000);
      try {
        const candData = await api('/api/activities/' + activityId + '/group/candidates');
        if (req !== gsCandReq) return;
        candidates.value = candData.candidates || [];
        await prefillGroupChoices(activityId);
      } catch(e) {
        if (req !== gsCandReq) return;
        candidates.value = [];
        if (e && (e.code === 'SIGNIN_REQUIRED' || e.code === 'SIGNIN_QUERY_FAILED')) {
          gsSigninBlocked.value = true;   // 签到被拦：整页阻断卡
        } else {
          candidatesError.value = true;   // 加载失败：名单没有加载出来 + 重新加载
        }
      }
      if (req !== gsCandReq) return;
      clearGsLoadTimers();
      gsMarkClean(); // 进页预填完成 → 更新 baseline（dirty 从此起算）
      loadingCandidates.value = false;
    }
    function reloadGroupCandidates() {
      const activityId = currentActivityId.value;
      if (!activityId) return;
      loadGroupCandidates(activityId); // 带参 reload：不重走 goGroupSelect，来路（groupSelectFrom）不被覆盖
    }
    async function prefillGroupChoices(activityId) {
      try {
        const statusData = await api('/api/activities/' + activityId + '/group/status');
        const myChoices = statusData.my_choices || [];
        if (!myChoices.length) return;
        const byOid = {};
        candidates.value.forEach(c => { byOid[c.openid] = c; });
        // P10：/status 返回时若用户已动过，不整段覆盖——只并入未被用户动过的项
        const merged = selectedChoices.value.slice();
        const chosen = new Set(merged.map(c => c.openid));
        let mergedCount = 0, unlisted = 0;
        myChoices.forEach(oid => {
          if (chosen.has(oid) || gsTouchedOids.has(oid)) return;
          const cand = byOid[oid];
          if (cand) { merged.push(cand); chosen.add(oid); mergedCount += 1; }
          else { unlisted += 1; }   // P9：不在当前候选池的历史志愿不并入（提交会被拦）
        });
        selectedChoices.value = merged;
        gsUnlistedCount.value = unlisted;
        if (mergedCount || unlisted) gsPrefillNotice.value = true;
      } catch(e) {
        // 状态拉取失败：保持现状（不覆盖用户已选），与旧实现口径一致
      }
    }
    function isSelected(oid) { return selectedChoices.value.some(c => c.openid === oid); }
    function gsDisplayName(c) { return (c && (c.real_name || c.nickname)) || ''; }
    function gsChoiceKey() { return selectedChoices.value.map(c => c.openid).join('|'); }
    function isGroupSelectDirty() { return page.value === 'group_select' && gsChoiceKey() !== gsBaseline; }
    function gsMarkClean() { gsBaseline = gsChoiceKey(); }
    function dismissGsNotice() { gsPrefillNotice.value = false; }
    function toggleChoice(c, ev) {
      const idx = selectedChoices.value.findIndex(x => x.openid === c.openid);
      if (idx >= 0) { removeChoiceToast(idx); return; }
      if (selectedChoices.value.length >= 7) { vant.showToast('最多 7 个志愿，先移除一个才能再选'); return; }
      selectedChoices.value.push(c);
      gsTouchedOids.add(c.openid);
      gsFlyChoiceAvatar(gsRowAvatarEl(ev));   // 新增成功：行头像飞入「调整次序」按钮
    }
    function removeChoiceToast(i) {
      const c = selectedChoices.value[i];
      if (!c) return;
      selectedChoices.value.splice(i, 1);
      gsTouchedOids.add(c.openid);
      vant.showToast('已移除 ' + gsDisplayName(c));
    }
    function moveChoice(i, delta) {
      const j = i + delta;
      if (j < 0 || j >= selectedChoices.value.length) return;
      const next = selectedChoices.value.slice();
      const tmp = next[i]; next[i] = next[j]; next[j] = tmp;
      selectedChoices.value = next;   // 顺序变也算改（dirty 以 openid 序列比对）
    }
    // 10px 位移防误触：touchstart/touchend 比较，滚动手势不触发选择
    function gsRowTouchStart(e) {
      const t = e.touches && e.touches[0];
      gsTouchStartY = t ? t.clientY : null;
    }
    function gsRowTouchEnd(e, c) {
      const t = e.changedTouches && e.changedTouches[0];
      const moved = (gsTouchStartY !== null && t) ? Math.abs(t.clientY - gsTouchStartY) : 0;
      gsTouchStartY = null;
      gsSuppressClickUntil = Date.now() + 600;  // 抑制随后的合成 click，防双触发
      if (moved <= 10 && t) toggleChoice(c, e);
    }
    function gsRowClick(e, c) {
      if (Date.now() < gsSuppressClickUntil) return;  // 触摸路径已处理
      toggleChoice(c, e);
    }
    // 选人飞入动画：克隆行头像一个（fixed 定位），rAF 后经 350ms ease-out 缩小淡出飞向
    // 「调整次序」按钮，落定后按钮轻脉冲（scale 1→1.08→1，200ms）。快速连点时每次调用
    // 独立（各自克隆、各自定时清理）；克隆清理走 try/finally，异常也不残留。
    // prefers-reduced-motion: reduce 时整体跳过（不克隆、不脉冲）。
    function gsRowAvatarEl(ev) {
      const row = ev && ev.currentTarget;
      return (row && row.querySelector) ? row.querySelector('.signup-sheet-avatar') : null;
    }
    function gsReorderBtnDom() {
      const r = gsReorderBtnEl.value;
      if (!r) return null;
      return r.$el || (r.nodeType === 1 ? r : null);
    }
    function gsReducedMotion() {
      try { return !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches); }
      catch(e) { return false; }
    }
    function gsPulseReorderBtn() {
      if (gsReducedMotion()) return;
      const el = gsReorderBtnDom();
      if (!el || !el.classList) return;
      el.classList.remove('gs-pulse');
      void el.offsetWidth;   // 强制重排：连点时脉冲可重复播放
      el.classList.add('gs-pulse');
      const stop = () => { el.classList.remove('gs-pulse'); el.removeEventListener('animationend', stop); };
      el.addEventListener('animationend', stop);
      setTimeout(stop, 500);   // 兜底：animationend 丢失也可清掉类名
    }
    function gsFlyChoiceAvatar(sourceEl) {
      if (gsReducedMotion()) return;
      if (!sourceEl || typeof sourceEl.getBoundingClientRect !== 'function') return;
      let clone = null;
      const removeClone = () => { if (clone) { if (clone.parentNode) clone.parentNode.removeChild(clone); clone = null; } };
      try {
        const targetEl = gsReorderBtnDom();
        if (!targetEl) return;
        const src = sourceEl.getBoundingClientRect();
        const dst = targetEl.getBoundingClientRect();
        if (!src.width || !src.height || !dst.width || !dst.height) return;
        clone = sourceEl.cloneNode(true);
        clone.classList.add('gs-fly-avatar');
        clone.style.left = src.left + 'px';
        clone.style.top = src.top + 'px';
        clone.style.width = src.width + 'px';
        clone.style.height = src.height + 'px';
        document.body.appendChild(clone);
        const dx = (dst.left + dst.width / 2) - (src.left + src.width / 2);
        const dy = (dst.top + dst.height / 2) - (src.top + src.height / 2);
        const raf = window.requestAnimationFrame ? window.requestAnimationFrame.bind(window) : (cb) => setTimeout(cb, 16);
        raf(() => raf(() => {
          if (!clone) return;
          clone.style.transform = 'translate(' + dx + 'px, ' + dy + 'px) scale(0.45)';
          clone.style.opacity = '0.2';
        }));
        setTimeout(() => {
          try { gsPulseReorderBtn(); } finally { removeClone(); }   // 350ms 过渡 + 余量后清理
        }, 380);
      } catch(e) {
        removeClone();   // 任何异常都不得遗留克隆
      }
    }
    function gsSubmitErrorMessage(e) {
      // 可读原因，不拼后端原文
      if (e && e.code === 'SIGNIN_REQUIRED') return '需要现场签到后才能填志愿，请联系管理员';
      if (e && e.code === 'SIGNIN_QUERY_FAILED') return '签到信息查询失败，请稍后重试';
      // 恰好 7 人：直接显示后端 error 文案（含已选数）
      if (e && e.code === 'SELECT_NOT_FULL') return e.message;
      if (e && typeof e.message === 'string' && e.message.includes('不在可选列表')) return '有志愿不在当前名单，请移除后重试';
      if (e && e.status === 409) return '当前有其他操作进行中，请稍后重试';
      return '提交失败，请稍后重试';
    }
    async function leaveGroupSelectByFrom() {
      const from = groupSelectFrom.value;
      if (from === 'activity_detail') await openActivity(currentActivityId.value);
      else if (from === 'home') { activitiesLoaded.value = false; page.value = 'home'; }
      else if (from === 'my_activities') { openMyActivities(); }
      else openMyGroups();
    }
    async function submitChoices() {
      const activityId = currentActivityId.value;
      if (!activityId) { vant.showToast('活动信息丢失'); return false; }
      if (actionLoading.value) return false;
      showGsTray.value = false;
      actionLoading.value = true;
      setPageBusy('正在提交志愿…');   // 页面级锁：提交期间点候选/托盘/搜索/返回皆无效（P16）
      try {
        await api('/api/activities/' + activityId + '/group/select', {
          method: 'POST', body: JSON.stringify({ choices: selectedChoices.value.map(c => c.openid) })
        });
        gsMarkClean();   // 提交成功先标净再跳转（避免自动返回被离开拦截误拦）
        // 注：删掉本行自动化抓不到（突变 M4「提交成功不 gsMarkClean」盲区）——该场景需靠手测覆盖。
        vant.showToast('志愿提交成功');
        myGroupsLoaded.value = false;
        await leaveGroupSelectByFrom();   // 按原路返回并刷新（既有逻辑）
        return true;
      } catch(e) {
        vant.showToast(gsSubmitErrorMessage(e));   // 留在本页、选中保留
        return false;
      } finally {
        actionLoading.value = false;
        clearPageBusy();   // 成功跳页也在此解除（无遗留计时器）；失败解锁后原选中保留
      }
    }
    async function loadGroupResult(activityId, round) {
      let url = '/api/activities/' + activityId + '/group/result';
      if (round) url += '?round=' + encodeURIComponent(round);
      groupResultLoading.value = true;
      try { const data = await api(url); groupResult.value = data; }
      catch(e) { vant.showToast(e.message); }
      finally { groupResultLoading.value = false; }
    }
    async function goGroupResult(activityId) {
      groupResultFrom.value = page.value; // 记录来路，用于返回
      currentActivityId.value = activityId;
      page.value = 'group_result';
      groupResult.value = {};
      await loadGroupResult(activityId);
    }
    function switchGroupRound(round) {
      loadGroupResult(currentActivityId.value, round);
    }
    // 一对一
    async function loadOnetooneResult(activityId, round) {
      let url = '/api/activities/' + activityId + '/onetoone/result';
      if (round) url += '?round=' + encodeURIComponent(round);
      onetooneResultLoading.value = true;
      try { const data = await api(url); onetooneResult.value = data; }
      catch(e) { vant.showToast(e.message); }
      finally { onetooneResultLoading.value = false; }
    }
    function switchOnetooneRound(round) {
      loadOnetooneResult(currentActivityId.value, round);
    }
    async function goOnetooneResult(activityId) {
      onetooneResultFrom.value = page.value; // 记录来路，用于返回
      currentActivityId.value = activityId;
      page.value = 'onetoone_result';
      onetooneResult.value = {};
      await loadOnetooneResult(activityId);
    }
    function openOnetooneMsg(p) {
      onetooneMsgTarget.value = p;
      onetooneMsgText.value = '';
      showOnetooneMsg.value = true;
    }
    async function onOnetooneMsgBeforeClose(action) {
      if (onetooneMsgSubmitting.value) return false;   // 发送在途：任何关闭都被拦（结果回来才允许关，P16）
      if (action === 'confirm') {
        if (!onetooneMsgText.value.trim()) { vant.showToast('请先输入留言内容'); return false; }
        return await doOnetooneMsg();
      }
      // 取消/点遮罩关闭：有未发送留言 → 先走通用离开拦截
      if (showUnsavedConfirm.value) return false;
      if (guardOverlay('onetooneMsg', () => { showOnetooneMsg.value = false; })) return false;
      return true;
    }
    async function doOnetooneMsg() {
      const t = onetooneMsgTarget.value;
      if (!t || !t.openid) return false;
      if (onetooneMsgSubmitting.value) return false;
      actionLoading.value = true;
      onetooneMsgSubmitting.value = true;
      let ok = false;
      try {
        await api('/api/messages', {
          method: 'POST',
          body: JSON.stringify({ target_openid: t.openid, content: onetooneMsgText.value.trim() })
        });
        vant.showToast('留言成功');
        showOnetooneMsg.value = false;
        const arr = onetooneResult.value.partners || [];
        const idx = arr.findIndex(x => x.openid === t.openid);
        if (idx >= 0) arr[idx].chatted = true;
        ok = true;
      } catch(e) { vant.showToast(e.message); }
      actionLoading.value = false;
      onetooneMsgSubmitting.value = false;
      return ok;
    }

    // 页面切换加载数据
    // 拦截 popstate：按 浮层 > 子页 优先级关闭
    function handlePopState(e){
      if(dropCurrentPending){
        // 保存/改密成功后的 dropCurrent：back() 已落到父条目，只修正 state，不切页。
        const state = dropCurrentPending;
        dropCurrentPending = null;
        try{ realHistory.replaceState(state, '', navURL()); }catch(err){}
        isHandlingPopState = false;
        return;
      }
      if(consumeIgnorePop > 0){ consumeIgnorePop--; popConsumeBusy = false; popConsumeStep(); return; }
      if(ignoreNextPop){ ignoreNextPop=false; return; }
      isHandlingPopState = true;
      // 页面级忙碌（pageBusy）：系统返回吞掉并停留（与 guardLeave / P15 同口径）
      if(pageBusy.value){ isHandlingPopState=false; pushHistory({page: page.value}); vant.showToast('正在提交，请稍候'); return; }
      // 分组管理台：执行中遮罩吞掉返回；其余弹层关掉并停在原页（re-push 防 history 漂移）
      if(gaRunShow.value){ isHandlingPopState=false; pushHistory({page:'group_detail'}); return; }
      // 通用未保存弹窗：返回只关弹窗、停留（不触发离开拦截）
      if(showUnsavedConfirm.value){
        cancelLeaveConfirm();
        isHandlingPopState=false;
        return;
      }
      // 内嵌管理后台：系统返回只关 overlay，不切 tab、不离开 SPA
      if(embedOpen.value){
        closeEmbed();
        isHandlingPopState=false;
        return;
      }
      // 认领面板（登录页内嵌步骤）：未提交内容 → 拦截；否则返回登录面板
      if(showClaim.value){
        if(guardOverlay('claim', () => backToLogin())){ isHandlingPopState=false; pushHistory({overlay:'claim'}); return; }
        backToLogin(); isHandlingPopState=false; return;
      }
      // 管理台「输入活动编号确认」类弹层（清空分组结果/清空签到/清空一对一结果）：
      // 确认级输入，关闭=作废输入符合直觉 → 豁免，不做未保存拦截。
      if(gaConfirmShow.value){
        // 清空执行中：返回吞掉并停留（防请求在途时关层暴露底层操作）
        if(gaConfirmBusy.value){ isHandlingPopState=false; pushHistory({page:'group_detail'}); vant.showToast('正在提交，请稍候'); return; }
        gaConfirmShow.value=false; isHandlingPopState=false; pushHistory({page:'group_detail'}); return;
      }
      if(gaRunPopupShow.value){
        if(guardOverlay('gaRun', () => { gaRunPopupShow.value=false; })){ isHandlingPopState=false; pushHistory({page:'group_detail'}); return; }
        closeGaRunPopup(); isHandlingPopState=false; pushHistory({page:'group_detail'}); return;
      }
      if(gaOtoRunShow.value){
        if(guardOverlay('gaOtoRun', () => { gaOtoRunShow.value=false; })){ isHandlingPopState=false; pushHistory({page:'group_detail'}); return; }
        closeOtoRun(); isHandlingPopState=false; pushHistory({page:'group_detail'}); return;
      }
      if(gaOtoResetShow.value){
        // 清空执行中：返回吞掉并停留（同 gaConfirmShow）
        if(gaOtoResetBusy.value){ isHandlingPopState=false; pushHistory({page:'group_detail'}); vant.showToast('正在提交，请稍候'); return; }
        cancelOtoReset(); isHandlingPopState=false; pushHistory({page:'group_detail'}); return;
      }
      if(gaUnknownShow.value){ gaUnknownShow.value=false; isHandlingPopState=false; pushHistory({page:'group_detail'}); return; }
      if(showPhotoViewer.value){
        const p = viewerLightbox && viewerLightbox.pswp;
        if (p && !p.isDestroying) p.close();
        showPhotoViewer.value=false;
        isHandlingPopState=false;
        return;
      }
      if(showFilter.value){
        if(guardOverlay('filter', () => { showFilter.value=false; })){ isHandlingPopState=false; pushHistory({overlay:'filter'}); return; }
        showFilter.value=false; isHandlingPopState=false; return;
      }
      if(showLikeDialog.value){
        if(guardOverlay('likeDialog', () => { showLikeDialog.value=false; })){ isHandlingPopState=false; pushHistory({overlay:'likeDialog'}); return; }
        showLikeDialog.value=false; isHandlingPopState=false; return;
      }
      if(showReportDialog.value){
        if(guardOverlay('reportDialog', () => { showReportDialog.value=false; })){ isHandlingPopState=false; pushHistory({overlay:'reportDialog'}); return; }
        showReportDialog.value=false; isHandlingPopState=false; return;
      }
      if(showFeedbackDialog.value){
        if(guardOverlay('feedbackDialog', () => { showFeedbackDialog.value=false; })){ isHandlingPopState=false; pushHistory({overlay:'feedbackDialog'}); return; }
        showFeedbackDialog.value=false; isHandlingPopState=false; return;
      }
      if(showOnetooneMsg.value){
        if(guardOverlay('onetooneMsg', () => { showOnetooneMsg.value=false; })){ isHandlingPopState=false; pushHistory({overlay:'onetooneMsg'}); return; }
        showOnetooneMsg.value=false; isHandlingPopState=false; return;
      }
      if(showSuggestionDialog.value){
        if(guardOverlay('suggestionDialog', () => { showSuggestionDialog.value=false; })){ isHandlingPopState=false; pushHistory({overlay:'suggestionDialog'}); return; }
        showSuggestionDialog.value=false; isHandlingPopState=false; return;
      }
      if(showInviteDialog.value){ showInviteDialog.value=false; isHandlingPopState=false; return; }
      if(showPointsRules.value){ showPointsRules.value=false; isHandlingPopState=false; return; }
      if(showRedemptions.value){ showRedemptions.value=false; isHandlingPopState=false; return; }
      if(showRedeemPanel.value){
        if(guardOverlay('redeemPanel', () => closeRedeemPanel())){ isHandlingPopState=false; pushHistory({overlay:'redeemPanel'}); return; }
        closeRedeemPanel(); isHandlingPopState=false; return;
      }
      if(showActivityPromo.value){ showActivityPromo.value=false; isHandlingPopState=false; return; }
      if(showUpdateNotes.value){ showUpdateNotes.value=false; isHandlingPopState=false; return; }
      if(showAdminHelp.value){ showAdminHelp.value=false; isHandlingPopState=false; return; }
      if(signinDialogShow.value){
        if(signinSubmitting.value){ isHandlingPopState=false; pushHistory({overlay:'signinDialog'}); vant.showToast('正在提交，请稍候'); return; }
        signinDialogShow.value=false; isHandlingPopState=false; return;
      }
      if(showSelectPopup.value){ showSelectPopup.value=false; isHandlingPopState=false; return; }
      if(showMultiPopup.value){ showMultiPopup.value=false; isHandlingPopState=false; return; }
      if(signupSheetShow.value){ signupSheetShow.value=false; isHandlingPopState=false; return; }
      if(showGsTray.value){ showGsTray.value=false; isHandlingPopState=false; return; }
      // 未保存离开拦截（守卫注册表）：系统返回已弹走一层 → 先补回，再挂弹窗/提示
      const leaveGuard = leaveGuards[page.value];
      if(leaveGuard && ((leaveGuard.busy && leaveGuard.busy()) || leaveGuard.dirty())){
        isHandlingPopState = false;   // 允许补历史条目 / 弹窗 watch 挂 overlay
        pushHistory({page: page.value});
        if(leaveGuard.busy && leaveGuard.busy()){
          vant.showToast('正在提交，请稍候');   // P15：提交中系统返回 → 提示并停留
        } else {
          pendingGuard.value = leaveGuard;
          pendingLeaveCb = () => { hist.back(); };
          showUnsavedConfirm.value = true;
        }
        return;
      }
      // 子页回退：优先用 state.page，否则按 parent 映射
      const st = e.state;
      // native_admin：iframe 内退到主 tab 历史条目时，直接回管理台总览，不渲染用户端 App。
      if (nativeAdmin.value && st && TAB_ORDER.includes(st.page)) { location.href = '/admin?embed=1'; return; }
      // 从报名名单进他人资料页：上一条历史是名单弹层，回到活动详情并重开名单（即上一界面）；
      // 重开时屏蔽 watcher 的重复 push（当前已在名单历史条目上），下一 tick 再恢复
      if(page.value === 'user_detail' && userDetailFromSheet.value && st && st.overlay === 'signupSheet'){
        userDetailFromSheet.value = false;
        page.value = 'activity_detail';
        signupSheetShow.value = true;
        nextTick(() => { isHandlingPopState = false; });
        return;
      }
      if(st && st.page && st.page !== page.value){
        // 主 tab 之间不再建历史条目：若弹出目标仍是主 tab，保持当前视图，
        // 避免启动时遗留的旧 tab state 反向切页；子页 → 主 tab 仍按 state 还原。
        if(TAB_ORDER.includes(st.page) && TAB_ORDER.includes(page.value)){
          nextTick(() => { isHandlingPopState = false; });
          return;
        }
        page.value = st.page;
        // popstate 已消费一条历史：本 tick 屏蔽异步 push watcher，nextTick 再复位（否则 watcher 看到 false 会补条目 → 返回乒乓/死循环）
        nextTick(() => { isHandlingPopState = false; });
        return;
      }
      const parentMap = {
        'mutual': 'likes', 'liked_me': 'likes', 'my_likes': 'likes', 'my_messages': 'likes', 'received_messages': 'likes',
        'my_activities': 'profile', 'my_groups': 'profile', 'points': 'profile', 'points_admin': 'profile',
        'group_admin': 'profile', 'group_detail': 'group_admin', 'group_roster': 'group_detail', 'checkin': 'group_admin',
        'profile_detail': 'profile', 'profile_edit': 'profile_detail', 'password': 'profile',
        'activity_detail': activityDetailFrom.value || 'home',
        'group_select': groupSelectFrom.value || 'my_groups',
        'group_result': groupResultFrom.value || 'my_groups',
        'onetoone_result': onetooneResultFrom.value || 'my_activities',
        'user_detail': userDetailFrom.value || 'home',
        'likes': 'cards', 'home': 'cards', 'profile': 'cards'
      };
      if (nativeAdmin.value && TAB_ORDER.includes(parentMap[page.value])) { location.href = '/admin?embed=1'; return; }
      if(parentMap[page.value]){ page.value = parentMap[page.value]; }
      // 同上：返回消费了一层历史，不再补 push
      nextTick(() => { isHandlingPopState = false; });
    }
    watch(page, (p, old) => {
      syncMe(); // 静默对账爱心余额（机器人延迟扣/退，切页时收敛显示）
      // 活动管理台：离开详情页时清弹层与定时器（不取消后端 job）
      if (p !== 'group_detail') {
        gaConfirmShow.value = false; gaUnknownShow.value = false;
        gaRunPopupShow.value = false; gaOtoRunShow.value = false; gaOtoResetShow.value = false;
        stopGaPolling(false);
      }
      if (p !== 'group_select') { showGsTray.value = false; clearGsLoadTimers(); }   // 离开志愿页：清次序托盘与慢加载计时器（F-01）
      if (p === 'group_admin') loadGroupAdminList();
      if (p === 'group_detail' && gaDetailId.value) refreshGroupDetail();
      if (p === 'group_roster' && gaDetailId.value) loadGroupRoster(true);
      if (p === 'checkin' && gaDetailId.value) loadSigninRoster(true);
      if (p === 'home') loadActivities();
      if (p === 'cards') { if (old && old !== 'cards') prevPage.value = old; loadCards(); maybeShowActivityPromo(); maybeShowUpdateNotes(); loadPraiseState(); }
      if (p === 'likes') { loadLikes(); loadNotifications().then(markNotificationsRead); refreshMyMessagesCount(); refreshReceivedCount(); }
      if (p === 'profile') { checkGroupEntry(); loadPraiseState(); if (user.value && user.value.is_admin) refreshReportedCount(); if (user.value && user.value.can_admin) refreshAdminBadge(); }
      // 任何页进入即验身份证/人脸照片，不达标则倒计时跳修改资料
      if(p !== 'profile_edit' && !showIdInvalidDialog.value){
        checkIdValidForPage(p);
      }
      if(p !== 'profile_edit' && !showFaceInvalidDialog.value){
        checkFaceValidForPage(p);
      }
    });
    // 从改密页回「我的」：还原进入前的滚动位置（覆盖返回键/popstate 与改密成功；不影响其它页）
    watch(page, (newPage, oldPage) => {
      if (oldPage === 'password' && newPage === 'profile') {
        nextTick(() => {
          // 主内容 Transition 是 out-in：离场动画结束前 profile 尚未挂载，此刻 scrollTo 会被夹到密码页高度
          const started = Date.now();
          const restore = () => { try { window.scrollTo(0, passwordReturnScroll); } catch (e) {} };
          const tick = () => {
            if (page.value !== 'profile') return;
            if (document.querySelector('.profile-header') || Date.now() - started > 600) { restore(); return; }
            requestAnimationFrame(tick);
          };
          tick();
        });
      }
    });
    watch(profileData, (v)=>{
      if(v && typeof v.id_valid === 'boolean' && v.id_valid === false && page.value !== 'profile_edit' && !showIdInvalidDialog.value){
        checkIdValidForPage(page.value);
      }
      if(v && typeof v.has_face === 'boolean' && v.has_face === false && page.value !== 'profile_edit' && !showFaceInvalidDialog.value){
        checkFaceValidForPage(page.value);
      }
    });
    // pushState for page forward navigation
    watch(page, (nv, ov) => {
      if(isHandlingPopState) return;
      if(nv === ov) return;
      if(nativeAdminEntryPending && nv === 'group_admin'){
        // native_admin 首次进入：基条目就地写成 group_admin，不额外压历史；
        // 这样 iframe 返回键能退到管理台总览，而不是用户端 tab。
        nativeAdminEntryPending = false;
        try{ hist.replace({page: nv}, navURL()); }catch(e){}
        return;
      }
      if(suppressPagePushOnce && nv === 'checkin'){ suppressPagePushOnce = false; return; }   // 塌缩条目已在位，不补 push
      if(profileFormDone && nv === 'profile_detail'){
        profileFormDone = false;
        // 资料保存成功：丢弃当前 edit 条目，top 保持 profile_detail；返回链不会残留 edit。
        try{ hist.dropCurrent({page: nv}); }catch(e){}
        return;
      }
      profileFormDone = false;   // 非目标页（保存后异常回退等）：清掉一次性旗标，避免污染后续导航
      if(passwordFormDone){
        passwordFormDone = false;
        // 改密成功：丢弃当前 password 条目，top 保持 profile。
        try{ hist.dropCurrent({page: nv}); }catch(e){}
        return;
      }
      // 四个主 tab 之间切换只是视图状态：不新增历史条目、不改 URL。
      // 新增条目会让 Safari/WebView 出现地址栏加载指示与 tabbar 闪动，返回键也会在 tab 间空转。
      if(TAB_ORDER.includes(nv) && TAB_ORDER.includes(ov)) return;
      // 从主 tab 进入子页前，把当前条目 state 就地改写为所在 tab（不新增条目、不改 URL），
      // 这样从子页返回时 popstate 能落回正确的 tab，而不是启动时的旧 tab。
      if(TAB_ORDER.includes(ov) && !TAB_ORDER.includes(nv)){
        try{ hist.replace({page: ov}, navURL()); }catch(e){}
      }
      pushHistory({page: nv});
    });
    // pushState for overlays
    watch(showPhotoViewer, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'viewer'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='viewer'){ ignoreNextPop=true; hist.back(); } });
    watch(showFilter, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'filter'}); else if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='filter' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    watch(showLikeDialog, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'likeDialog'}); else if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='likeDialog' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    watch(showFeedbackDialog, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'feedbackDialog'}); else if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='feedbackDialog' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    watch(showReportDialog, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'reportDialog'}); else if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='reportDialog' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    watch(showSuggestionDialog, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'suggestionDialog'}); else if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='suggestionDialog' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    // onetooneMsg 打开时不单独 push（返回语义沿用页面条目），仅拦截时会补挂 overlay 条目；关闭时按需消费
    watch(showOnetooneMsg, (v)=>{ if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='onetooneMsg' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    watch(showInviteDialog, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'inviteDialog'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='inviteDialog'){ ignoreNextPop=true; hist.back(); } });
    watch(showRedeemPanel, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'redeemPanel'}); else if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='redeemPanel' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    watch(showPointsRules, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'pointsRules'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='pointsRules'){ ignoreNextPop=true; hist.back(); } });
    watch(showRedemptions, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'redemptions'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='redemptions'){ ignoreNextPop=true; hist.back(); } });
    watch(showAdminHelp, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'adminHelp'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='adminHelp'){ ignoreNextPop=true; hist.back(); } });
    watch(embedOpen, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'embed'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='embed'){ ignoreNextPop=true; hist.back(); } });
    watch(signinDialogShow, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'signinDialog'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='signinDialog'){ ignoreNextPop=true; hist.back(); } });
    watch(showSelectPopup, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'selectPopup'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='selectPopup'){ ignoreNextPop=true; hist.back(); } });
    watch(showMultiPopup, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'multiPopup'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='multiPopup'){ ignoreNextPop=true; hist.back(); } });
    watch(signupSheetShow, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'signupSheet'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='signupSheet' && !(page.value==='user_detail' && userDetailFromSheet.value)){ ignoreNextPop=true; hist.back(); } });
    watch(showGsTray, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'gsTray'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='gsTray'){ ignoreNextPop=true; hist.back(); } });
    watch(showClaim, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'claim'}); else if(!v && !isHandlingPopState && hist.state && (hist.state.overlay==='claim' || hist.state.overlay==='unsavedConfirm')){ requestPopConsume(); } });
    watch(showUnsavedConfirm, (v)=>{ if(v && !isHandlingPopState) pushHistory({overlay:'unsavedConfirm'}); else if(!v && !isHandlingPopState && hist.state && hist.state.overlay==='unsavedConfirm'){ requestPopConsume(); } });
    // 活动引导弹窗：关闭动作（确认/点遮罩/返回键）统一记入本会话，保证「每个会话只弹一次」
    watch(showActivityPromo, (v)=>{
      if(v && !isHandlingPopState){ pushHistory({overlay:'activityPromo'}); }
      else if(!v){
        try { sessionStorage.setItem(ACTIVITY_PROMO_KEY, '1'); } catch(e) {}
        if(!isHandlingPopState && hist.state && hist.state.overlay==='activityPromo'){ ignoreNextPop=true; hist.back(); }
        maybeShowUpdateNotes(); // 活动引导让位后，更新说明仍有机会在首页弹一次
      }
    });
    // 更新说明弹窗：关闭动作统一记入 localStorage（每版本一次），历史回退与活动引导同款
    watch(showUpdateNotes, (v)=>{
      if(v && !isHandlingPopState){ pushHistory({overlay:'updateNotes'}); }
      else if(!v){
        try { localStorage.setItem(UPDATE_NOTES_KEY, '1'); } catch(e) {}
        if(!isHandlingPopState && hist.state && hist.state.overlay==='updateNotes'){ ignoreNextPop=true; hist.back(); }
      }
    });

    // 用户成功左右翻页，说明已学会滑动，自动关闭新手引导
    watch(cardIndex, () => { if (showSwipeGuide.value) closeSwipeGuide(); });

    // 切换卡片/照片时显示图片加载占位
    watch([cardIndex, photoIndex], () => {
      triggerPhotoLoading();
      preloadAround();
    });

    // ===== 留言 =====
    const cardTab = ref('profile');
    const messages = ref([]);
    const myMessagesList = ref([]);
    const loadingMyMessages = ref(false);
    const myMessagesLoaded = ref(false);
    const receivedMessagesList = ref([]);
    const loadingReceivedMessages = ref(false);
    const receivedMessagesLoaded = ref(false);
    const msgLoading = ref(false);
    const msgSending = ref(false);
    const msgText = ref('');
    const replyTarget = ref(null);
    let msgLoadedFor = null;  // 已加载留言的卡片 openid（切卡片前不重复请求）
    const threadedMessages = computed(() => {
      const ids = new Set(messages.value.map(m => m.id));
      // 父留言已删除的「孤儿回复」也作顶级显示，保证列表条数与角标一致
      const tops = messages.value.filter(m => !m.parent_id || !ids.has(m.parent_id));
      const replies = messages.value.filter(m => m.parent_id && ids.has(m.parent_id));
      return tops.map(t => ({ ...t, replies: replies.filter(r => r.parent_id === t.id) }));
    });
    function fmtTimeAgo(ts) {
      if (!ts) return '';
      const diff = Date.now() - ts;
      const m = Math.floor(diff / 60000);
      if (m < 1) return '刚刚';
      if (m < 60) return m + '分钟前';
      const h = Math.floor(m / 60);
      if (h < 24) return h + '小时前';
      const d = Math.floor(h / 24);
      if (d < 30) return d + '天前';
      const dt = new Date(ts);
      return dt.getFullYear() + '-' + String(dt.getMonth() + 1).padStart(2, '0') + '-' + String(dt.getDate()).padStart(2, '0');
    }
    async function loadMessages(force) {
      const c = currentCard.value;
      if (!c || !c.openid) return;
      if (!force && msgLoadedFor === c.openid) return;
      msgLoading.value = true;
      try {
        const data = await api('/api/messages?target=' + encodeURIComponent(c.openid));
        messages.value = data.messages || [];
        msgLoadedFor = c.openid;
        // 同步 tab 上的留言数角标，避免快照滞后
        if (cards.value[cardIndex.value]) cards.value[cardIndex.value].msg_count = messages.value.length;
      } catch (e) {
        messages.value = [];
      } finally {
        msgLoading.value = false;
      }
    }
    async function openMessageTab() {
      cardTab.value = 'messages';
      await loadMessages();
    }
    async function openMessageAuthor(m) {
      // 点留言人头像 → 他人基本信息页；userDetailFrom='cards' 让返回回到原留言页
      // （cardTab/messages 状态不销毁，回来仍是同一张卡的留言列表）。
      const oid = m && m.author_openid;
      if (!oid) { vant.showToast('对方资料暂不可见'); return; }
      userDetailFromSheet.value = false;
      userDetailFrom.value = 'cards';
      page.value = 'user_detail';
      userDetail.value = null;
      try {
        const data = await api('/api/users/' + oid + '/public');
        if (data && (data.liked || justLiked.has(oid))) { data.liked = true; data.can_like = false; }
        userDetail.value = data;
      } catch(e) { vant.showToast(e.message); }
    }
    async function sendMessage() {
      if (msgSending.value) return false;
      const c = currentCard.value;
      const text = msgText.value.trim();
      if (!text) { vant.showToast('请先输入留言内容'); return false; }
      if (!c || !c.openid) { vant.showToast('卡片信息异常，请左右切换到其他用户后重试'); return false; }
      const parentId = replyTarget.value ? (replyTarget.value.parentId || replyTarget.value.id) : '';
      const replyToId = replyTarget.value ? replyTarget.value.id : '';
      const replyToNickname = replyTarget.value ? replyTarget.value.author_nickname : '';
      const replyToUid = replyTarget.value ? replyTarget.value.author_uid : '';
      const tempId = 'tmp_' + Date.now() + '_' + Math.random().toString(36).slice(2, 6);
      const now = Date.now();
      const tempMsg = {
        id: tempId,
        author_nickname: (user.value && user.value.nickname) || '我',
        author_uid: (user.value && user.value.user_id) || '',
        author_avatar: (user.value && user.value.photo) || '',
        content: text,
        parent_id: parentId,
        reply_to_id: replyToId,
        reply_to_nickname: replyToNickname,
        reply_to_uid: replyToUid,
        created_at: now,
        is_mine: true,
        can_delete: true,
        _optimistic: true,
      };
      const savedText = text;
      const savedReply = replyTarget.value;
      // optimistic: immediate render
      messages.value = [...messages.value, tempMsg];
      if (cards.value[cardIndex.value]) cards.value[cardIndex.value].msg_count = messages.value.length;
      msgText.value = '';
      replyTarget.value = null;
      // scroll to bottom so user sees own message
      nextTick(() => {
        try {
          const el = document.querySelector('.msg-list');
          if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
        } catch (_) {}
      });
      msgSending.value = true;
      setPageBusy('正在发送…');   // 页面级锁：发送在途切卡/切页会串卡片计数与列表，先锁（P16）
      let realId = null;
      let ok = false;
      try {
        const data = await api('/api/messages', {
          method: 'POST',
          body: JSON.stringify({ target_openid: c.openid, content: savedText, parent_id: parentId, reply_to_id: replyToId })
        });
        realId = data && data.id;
        if (realId) {
          const idx = messages.value.findIndex(m => m.id === tempId);
          if (idx >= 0) messages.value[idx] = { ...messages.value[idx], id: realId, _optimistic: false };
        }
        // keep msg_count in sync (no reload)
        if (cards.value[cardIndex.value]) cards.value[cardIndex.value].msg_count = messages.value.length;
        vant.showToast('留言成功');
        ok = true;
        // invalidate myMessages cache so next entry sees new message; also patch if already loaded
        if (myMessagesLoaded.value) {
          // optimistic append to my list if we can guess target info
          const targetInfo = c;
          myMessagesList.value = [{
            id: realId || tempId,
            target_openid: c.openid,
            target_nickname: c.nickname || targetInfo.nickname || '未知用户',
            target_avatar: (c.photo || (c.photos && c.photos[0]) || ''),
            content: savedText,
            parent_id: parentId,
            created_at: now,
          }, ...myMessagesList.value];
        } else {
          // mark stale so next open fetches fresh
          // keep loaded false -> will fetch; do nothing
        }
      } catch (e) {
        // rollback
        messages.value = messages.value.filter(m => m.id !== tempId);
        if (cards.value[cardIndex.value]) cards.value[cardIndex.value].msg_count = messages.value.length;
        msgText.value = savedText;
        replyTarget.value = savedReply;
        if (myMessagesLoaded.value) {
          myMessagesList.value = myMessagesList.value.filter(x => x.id !== tempId && x.id !== realId);
        }
        vant.showToast(e.message || '发送失败');
      } finally {
        msgSending.value = false;
        clearPageBusy();
      }
      return ok;
    }
    async function deleteMessage(m) {
      try {
        await vant.showConfirmDialog({ title: '删除留言', message: '确定删除这条留言吗？', confirmButtonColor: '#ee0a24' });
      } catch (e) { return; }
      // optimistic: remove message + its replies (cascade) immediately
      const backup = [...messages.value];
      const backupCount = cards.value[cardIndex.value] ? cards.value[cardIndex.value].msg_count : null;
      const toDeleteIds = new Set([m.id]);
      // collect cascading replies where parent_id == m.id (backend cascade behavior)
      messages.value.forEach(msg => { if (msg.parent_id === m.id) toDeleteIds.add(msg.id); });
      messages.value = messages.value.filter(x => !toDeleteIds.has(x.id));
      if (cards.value[cardIndex.value]) cards.value[cardIndex.value].msg_count = messages.value.length;
      // also optimistic patch my/received lists if loaded
      const myBackup = myMessagesLoaded.value ? [...myMessagesList.value] : null;
      const recvBackup = receivedMessagesLoaded.value ? [...receivedMessagesList.value] : null;
      if (myBackup) myMessagesList.value = myMessagesList.value.filter(x => !toDeleteIds.has(x.id));
      if (recvBackup) receivedMessagesList.value = receivedMessagesList.value.filter(x => !toDeleteIds.has(x.id));
      try {
        await api('/api/messages/' + m.id, { method: 'DELETE' });
        vant.showToast('已删除');
      } catch (e) {
        // rollback
        messages.value = backup;
        if (cards.value[cardIndex.value] && backupCount !== null) cards.value[cardIndex.value].msg_count = backupCount;
        if (myBackup) myMessagesList.value = myBackup;
        if (recvBackup) receivedMessagesList.value = recvBackup;
        vant.showToast(e.message || '删除失败');
      }
    }
    async function refreshMyMessagesCount() {
      const isFirst = !myMessagesLoaded.value;
      if (isFirst) loadingMyMessages.value = true;
      try {
        const data = await api('/api/messages/mine');
        myMessagesList.value = data.messages || [];
        myMessagesLoaded.value = true;
      } catch (e) {}
      if (isFirst) loadingMyMessages.value = false;
    }
    async function openMyMessages() {
      page.value = 'my_messages';
      if (myMessagesLoaded.value) return;
      loadingMyMessages.value = true;
      try {
        const data = await api('/api/messages/mine');
        myMessagesList.value = data.messages || [];
        myMessagesLoaded.value = true;
      } catch (e) {
        vant.showToast(e.message || '加载失败');
      } finally {
        loadingMyMessages.value = false;
      }
    }
    async function deleteMyMessage(m) {
      try {
        await vant.showConfirmDialog({ title: '删除留言', message: '确定删除这条留言吗？', confirmButtonColor: '#ee0a24' });
      } catch (e) { return; }
      // optimistic: immediate removal
      const backup = [...myMessagesList.value];
      const msgBackup = [...messages.value];
      const cardMsgCountBackup = new Map();
      cards.value.forEach(c => { if (c && c.openid) cardMsgCountBackup.set(c.openid, c.msg_count); });
      const targetOid = m.target_openid || null;
      myMessagesList.value = myMessagesList.value.filter(x => x.id !== m.id);
      // if this message belongs to currently opened card, patch messages optimistically
      if (targetOid && msgLoadedFor === targetOid) {
        const toDeleteIds = new Set([m.id]);
        messages.value.forEach(msg => { if (msg.parent_id === m.id) toDeleteIds.add(msg.id); });
        messages.value = messages.value.filter(x => !toDeleteIds.has(x.id));
      }
      // decrement msg_count for that target card if in deck
      if (targetOid) {
        const deckCard = cards.value.find(c => c.openid === targetOid);
        if (deckCard && typeof deckCard.msg_count === 'number' && deckCard.msg_count > 0) deckCard.msg_count -= 1;
      }
      try {
        await api('/api/messages/' + m.id, { method: 'DELETE' });
        vant.showToast('已删除');
      } catch (e) {
        // rollback
        myMessagesList.value = backup;
        messages.value = msgBackup;
        // restore card counts
        cards.value.forEach(c => { if (c && c.openid && cardMsgCountBackup.has(c.openid)) c.msg_count = cardMsgCountBackup.get(c.openid); });
        vant.showToast(e.message || '删除失败');
      }
    }
    async function refreshReceivedCount() {
      const isFirst = !receivedMessagesLoaded.value;
      if (isFirst) loadingReceivedMessages.value = true;
      try {
        const data = await api('/api/messages/received');
        receivedMessagesList.value = data.messages || [];
        receivedMessagesLoaded.value = true;
      } catch (e) {}
      if (isFirst) loadingReceivedMessages.value = false;
    }
    async function openReceivedMessages() {
      page.value = 'received_messages';
      if (receivedMessagesLoaded.value) return;
      loadingReceivedMessages.value = true;
      try {
        const data = await api('/api/messages/received');
        receivedMessagesList.value = data.messages || [];
        receivedMessagesLoaded.value = true;
      } catch (e) {
        vant.showToast(e.message || '加载失败');
      } finally {
        loadingReceivedMessages.value = false;
      }
    }
    function replyTo(target, threadRoot) {
      if (!target) return;
      const parent = threadRoot || target;
      // 将目标留言附上 parentId 供发送时区分 thread 与 reply_to
      replyTarget.value = { ...target, parentId: parent.id };
      msgText.value = '';
      // 聚焦输入框
      nextTick(() => {
        try { const el = document.querySelector('.msg-input'); if (el) el.focus(); } catch (_) {}
      });
    }
    function cancelReply() {
      replyTarget.value = null;
      msgText.value = '';
    }
    watch(cards, () => {
      photoPositions.value = [];
      nextTick(() => { bindPhotoCropObserver(); schedulePhotoCropRecalc(); });
    });
    // 换卡片时重置留言面板：按当前卡片 openid 判定（而非 cardIndex）——
    // 窗口后台重排（trimCardsWindow/换页重建）会让 index 漂移但人没变，不应误清草稿；
    // 真换人前的草稿拦截由 cardsMsg 守卫（switchCard/筛选应用）负责，这里清的是已确认离开后的残值。
    watch(() => (currentCard.value && currentCard.value.openid) || null, () => {
      cardTab.value = 'profile';
      messages.value = [];
      msgLoadedFor = null;
      replyTarget.value = null;
      msgText.value = '';
      photoPositions.value = [];
      nextTick(() => { bindPhotoCropObserver(); schedulePhotoCropRecalc(); });
      setTimeout(schedulePhotoCropRecalc, 360);   // 切卡动画结束后再补一次
    });
    watch(photoIndex, () => {
      // 切换照片时布局必已完成：补算当前照片位置（兜底 @load 早于布局的时序）
      nextTick(() => {
        try {
          const imgs = document.querySelectorAll('.card-photo-item');
          const img = imgs[photoIndex.value];
          if (img && img.complete && img.naturalWidth) {
            recalcPhotoPosition({ target: img }, photoIndex.value);
          }
        } catch (_) {}
        bindPhotoCropObserver();
        schedulePhotoCropRecalc();
      });
      setTimeout(schedulePhotoCropRecalc, 360);   // 照片横滑动画结束后再补一次
    });
    // faces 数据晚到（bootstrap 先渲染、接口后补）时也要重算，否则首帧按默认居中。
    watch(() => (currentCard.value && currentCard.value.faces) || null, () => {
      nextTick(schedulePhotoCropRecalc);
      setTimeout(schedulePhotoCropRecalc, 120);
      setTimeout(schedulePhotoCropRecalc, 420);
    }, { deep: true });
    // 进入牵线页/回前台/工具栏伸缩：页面可能刚挂载，wrap 尺寸与 faces 都需重算。
    watch(page, (v) => {
      if (v !== 'cards') return;
      nextTick(() => { bindPhotoCropObserver(); schedulePhotoCropRecalc(); });
      setTimeout(schedulePhotoCropRecalc, 360);
    });

    // ===== 壳内首卡 hero：等 Vue 首卡图真正绘制后再移除 =====
    // hero 的 DOM/几何由 index.html 静态渲染（与 .swipe-card 同一套 CSS），
    // 这里只决定何时淡出：首卡图 decode+paint 后；无卡/无图/切页/换卡/门禁/失败立即移除。
    let bootHeroAnchor = null;
    let bootHeroSettled = false;
    let bootHeroHardTimer = null;
    let bootHeroLastLongTaskEnd = 0;
    try {
      new PerformanceObserver((list) => {
        for (const e of list.getEntries()) {
          bootHeroLastLongTaskEnd = Math.max(bootHeroLastLongTaskEnd, e.startTime + e.duration);
        }
      }).observe({type: 'longtask', buffered: true});
    } catch (_) {}
    function dismissBootHero(reason) {
      try { if (window.__YXQ_HERO__) window.__YXQ_HERO__.dismiss(reason); } catch (_) {}
    }
    // 硬上限从 navigationStart 起算 4s：loadHome 卡住时 settleBootHero 不会启动，
    // 这里确保 hero 不会无限覆盖；移除后露出 Vue 启动骨架（bootLoading 仍为 true）。
    function armBootHeroHardLimit() {
      if (bootHeroHardTimer) return;
      const remain = Math.max(0, 4000 - performance.now());
      bootHeroHardTimer = setTimeout(() => {
        bootHeroHardTimer = null;
        // 只在 loadHome 仍挂起时强制移除；正常慢图由 settleBootHero 的绘制/超时路径处理，
        // 避免把已经下载中的 hero 首图在 4s 时误删、反而推后 LCP。
        if (bootLoading.value && window.__YXQ_LOAD_HOME_STARTED__) dismissBootHero('load-timeout');
      }, remain);
    }
    // 等首屏长任务（Vant 执行/mount/数据渲染）结束并完成一帧绘制后再移除 hero。
    // 只等 complete 会让 hero 在长任务开始前被移除，真实卡成为新的 LCP 候选（实测 6.5s）。
    function dismissHeroAfterPaint() {
      // 2s 窗口覆盖首屏 mount/Vant/数据渲染长任务的启动；之后要求长任务静默 200ms。
      const notBefore = performance.now() + 2000;
      const started = Date.now();
      const step = () => {
        if (window.__YXQ_HERO__.dismissed()) return;
        const now = performance.now();
        if (now < notBefore || now - bootHeroLastLongTaskEnd < 200) {
          if (Date.now() - started > 10000) { dismissBootHero('paint-timeout'); return; }
          setTimeout(step, 100);
          return;
        }
        requestAnimationFrame(() => requestAnimationFrame(() => dismissBootHero('painted')));
      };
      step();
    }
    function settleBootHero() {
      if (bootHeroSettled) return;
      bootHeroSettled = true;
      if (!window.__YXQ_HERO__ || window.__YXQ_HERO__.dismissed()) return;
      if (page.value !== 'cards' || !currentCard.value) { dismissBootHero('not-deck'); return; }
      bootHeroAnchor = {
        start: cardsStart.value,
        index: cardIndex.value,
        oid: currentCard.value.openid || '',
      };
      if (!currentPhotoList.value[0]) { dismissBootHero('no-photo'); return; }
      const deadline = Date.now() + 8000;
      const step = () => {
        if (window.__YXQ_HERO__.dismissed()) return;
        if (page.value !== 'cards' || !currentCard.value ||
            (currentCard.value.openid || '') !== bootHeroAnchor.oid) {
          dismissBootHero('card-changed');
          return;
        }
        const img = document.querySelector('.swipe-card .card-photo-item');
        if (img && img.complete && img.naturalWidth) {
          // hero 的裁切要跟真实卡一致，避免淡出瞬间垂直跳。
          // 不能只复制真实卡的 computed style：真实卡的位置由 @load 时的 recalcPhotoPosition
          // 写入，首图可能先于 faces/布局就绪，命中「几乎没动」分支或稍后才重算——
          // hero 若那一刻复制就会停在默认 50% 0（实测 staging 冷启 +2.4s 才出现人脸裁切，
          // hero 已在 3.1s 被移除）。这里用同一个 computeFaceCrop 直接按 faces[0] 算，
          // 真实卡稍后也算出同一个值；两者公式一致，淡出前后不会跳。
          try {
            const heroImg = document.querySelector('#boot-skeleton .boot-photo');
            const heroWrap = heroImg && heroImg.closest('.card-photo-wrap');
            const faces = currentCard.value.faces || [];
            const pos = heroImg && heroWrap
              ? computeFaceCrop(faces[0], heroWrap.clientWidth, heroWrap.clientHeight,
                                heroImg.naturalWidth, heroImg.naturalHeight)
              : '';
            if (pos && window.__YXQ_HERO__.setObjectPosition) window.__YXQ_HERO__.setObjectPosition(pos);
          } catch (_) {}
          // 等首屏长任务结束 + 至少一帧绘制；见 dismissHeroAfterPaint 注释。
          dismissHeroAfterPaint();
          return;
        }
        if (Date.now() > deadline) { dismissBootHero('timeout'); return; }
        setTimeout(step, 60);
      };
      step();
    }
    watch(bootLoading, (v) => {
      window.__YXQ_LOAD_HOME_DONE__ = !v;
      if (!v) nextTick(settleBootHero);
    });
    watch(page, (v) => { if (v !== 'cards') dismissBootHero('tab'); });
    watch([cardIndex, cardsStart], ([idx, start]) => {
      if (!bootHeroAnchor) return;
      if (idx !== bootHeroAnchor.index || start !== bootHeroAnchor.start) dismissBootHero('card-changed');
    });
    watch([bootError, gateMessage], ([err, gate]) => { if (err || gate) dismissBootHero('gate'); });
    // 首卡人脸裁切（photoPositions[0]）可能在首图 @load 之后才算出（容器尺寸/faces 数据
    // 未就绪会重试），而 settleBootHero 只在 img.complete 时复制一次——那样 hero 会停在
    // 默认 50% 0，淡出瞬间真实卡却已上移，出现垂直跳变（批次7 集成排查发现）。
    // 这里在裁切值变化时持续同步给还活着的 hero（dismissed 后 setObjectPosition 自动 no-op）。
    watch(photoPositions, (arr) => {
      if (page.value !== 'cards') return;
      const pos = arr && arr[0];
      if (!pos) return;
      try {
        if (window.__YXQ_HERO__ && window.__YXQ_HERO__.setObjectPosition) {
          window.__YXQ_HERO__.setObjectPosition(pos);
        }
      } catch (_) {}
    }, { deep: true });

    onMounted(() => {
      window.__YXQ_APP_MOUNTED__ = true;
      window.__YXQ_LOAD_HOME_STARTED__ = false;
      window.__YXQ_LOAD_HOME_DONE__ = false;
      armBootHeroHardLimit();
      // 首屏 hero 绘制 + 应用挂载后，likes 立即预取，其余 chunk LCP 后 idle；
      // 管理台 iframe 不预取，避免占用原生管理页带宽。
      if (!nativeAdmin.value) { yxqPrefetchPageChunks(); prefetchPhotoSwipe(); }
      // 可视区域变化：更新 --yxq-vh（壳内已尽早设过）并重算人脸裁切。
      try {
        if (window.visualViewport) window.visualViewport.addEventListener('resize', onViewportCropChange);
        window.addEventListener('orientationchange', onViewportCropChange);
        window.addEventListener('resize', onViewportCropChange);
        document.addEventListener('visibilitychange', onViewportVisibility);
      } catch (e) {}
      nextTick(() => { bindPhotoCropObserver(); schedulePhotoCropRecalc(); });
      // 入口深链：必须在下方 replaceState 清掉 hash 前留存（机器人「报名活动」按钮 → #/activities 直达活动页）
      const entryHash = window.location.hash;
      bootEntryHash = entryHash;
      // 在下方 replaceState 清掉 query 前，捕获引导预览参数（仅测试环境）
      try { claimEntryFlag = new URLSearchParams(location.search).get('claim') === '1'; } catch(e) { claimEntryFlag = false; }
      // ?admin=1：同样要在清 query 前捕获
      try { adminEntryFlag = new URLSearchParams(location.search).get('admin') === '1'; } catch(e) { adminEntryFlag = false; }
      try{ hist.replace({page: page.value}, navURL()); }catch(e){}
      // 顶层 SPA：真实历史只压这一个哨兵，之后子页/浮层不再碰 History API。
      pushHistorySentinel();
      window.addEventListener('popstate', handleRealPopState);
      if (SERVER_AUTHENTICATED) {
        yxqAfterFirstPaint(bootAuthenticated);
        return;
      }
      yxqAfterFirstPaint(function(){
        doLogin().then(() => { afterHomeReady(entryHash); });
      });
    });

    onUnmounted(() => {
      stopNotificationSync();
      try {
        if (window.visualViewport) window.visualViewport.removeEventListener('resize', onViewportCropChange);
        window.removeEventListener('orientationchange', onViewportCropChange);
        window.removeEventListener('resize', onViewportCropChange);
        document.removeEventListener('visibilitychange', onViewportVisibility);
      } catch (e) {}
      if (photoCropSettleTimer) { clearTimeout(photoCropSettleTimer); photoCropSettleTimer = null; }
      if (photoCropObserver) { try { photoCropObserver.disconnect(); } catch (e) {} photoCropObserver = null; }
    });

    // ===== 牌堆位置存档/恢复（刷新后回到原卡片）=====
    function saveDeckState(){
      try{
        if(page.value !== 'cards') return;
        sessionStorage.setItem('yxq_deck_restore', JSON.stringify({
          pos: cardsStart.value + cardIndex.value,
          filters: JSON.parse(JSON.stringify(filter.value)),
          ts: Date.now()
        }));
      }catch(e){}
    }
    let deckRestored = false;
    async function restoreDeck(){
      if(deckRestored) return;
      deckRestored = true;
      if (nativeAdminRequested || nativeAdmin.value) return;   // 管理页不恢复牵线牌堆
      let savedPos = null;
      let restoredPos = null;
      const finish = (ok, reason) => {
        txQueueEvent('restore', {
          ok: !!ok,
          saved_pos: savedPos,
          restored_pos: restoredPos,
          total: cardsTotal.value,
          reason: reason || ''
        });
      };
      try{
        const raw = sessionStorage.getItem('yxq_deck_restore');
        if(!raw){ finish(false, 'no_state'); return; }
        sessionStorage.removeItem('yxq_deck_restore');
        const st = JSON.parse(raw);
        if(st && typeof st.pos === 'number') savedPos = st.pos;
        if(!st || Date.now() - (st.ts || 0) > 10 * 60 * 1000){ finish(false, 'stale'); return; }
        if(savedPos === null || savedPos < 0){ finish(false, 'invalid'); return; }
        if(st.filters) filter.value = st.filters;
        page.value = 'cards';
        await loadCards(true);
        const pos = savedPos;
        if(pos >= cardsTotal.value){ finish(false, 'out_of_range'); return; }
        if(pos < cards.value.length){
          cardIndex.value = pos;
          restoredPos = pos;
        } else {
          const offset = Math.floor(pos / 50) * 50;
          const ok = await loadPageAt(offset);
          if(ok && pos - offset < cards.value.length){
            cardIndex.value = pos - offset;
            restoredPos = pos;
          } else {
            finish(false, 'load_failed');
            return;
          }
        }
        preloadAround();
        finish(true, 'ok');
      }catch(e){
        finish(false, 'error');
      }
    }

    // ===== 新版本提示：发现新版本只在 App 内提示，用户点了才刷新 =====
    // 旧行为是检测到版本变化直接 location.replace()，用户操作中途会闪浏览器整页进度条；
    // 现在只显示一条轻量提示条（.update-bar），刷新完全由用户触发。
    const VERSION_PROMPT_COOLDOWN = 30 * 60 * 1000;   // 提示过/关掉后 30 分钟内不再打扰
    const updateReady = ref(false);
    function currentAppVersion(){
      return String(APP_VERSION || '');
    }
    function versionChanged(server){
      const current = currentAppVersion();
      // 只有服务端注入了真实版本号才比较；静态直开的壳（'legacy'/占位符）永不提示
      if(!server || server === '__APP_VERSION__') return false;
      if(!current || current === 'legacy' || current === '__APP_VERSION__') return false;
      return server !== current;
    }
    function versionPromptCoolingDown(){
      try{
        const last = Number(localStorage.getItem('yxq_last_version_prompt') || 0);
        return !!last && Date.now() - last < VERSION_PROMPT_COOLDOWN;
      }catch(e){ return false; }
    }
    function markVersionPrompted(){
      try{ localStorage.setItem('yxq_last_version_prompt', String(Date.now())); }catch(e){}
    }
    function showUpdatePrompt(){
      if(updateReady.value) return;
      updateReady.value = true;
      markVersionPrompted();
    }
    function dismissUpdatePrompt(){
      updateReady.value = false;
      markVersionPrompted();   // 关掉也记时间：30 分钟内不再弹
    }
    // 用户点「有新版本，点此更新」后才刷新；带时间戳 query 绕过飞书 TTWebView 缓存
    function applyUpdate(){
      saveDeckState();
      try{
        sessionStorage.setItem('yxq_last_reload', String(Date.now()));
        sessionStorage.setItem('yxq_reload_reason', 'version');
      }catch(e){}
      // boot 从 localStorage 读原因（跨 WebView 重启保留）；sessionStorage 仍留给旧逻辑
      try{ localStorage.setItem('yxq_reload_reason', 'version'); }catch(e){}
      updateReady.value = false;
      const sep = (location.search ? '&' : '?');
      location.replace(location.pathname + location.search + sep + 'yxqv=' + Date.now());
    }
    async function checkVersion(){
      try{
        const r = await fetch(BASE_PATH + '/api/version', {cache:'no-store'});
        if(!r.ok) return;
        const d = await r.json();
        const server = String(d.version || '');
        if(!versionChanged(server)) return;
        if(updateReady.value) return;                             // 已在提示，不重复
        const ae = document.activeElement;
        if(ae && /^(INPUT|TEXTAREA)$/.test(ae.tagName)) return;   // 输入中不打扰，下轮再看
        if(versionPromptCoolingDown()) return;
        showUpdatePrompt();
      }catch(e){}
    }
    setInterval(checkVersion, 45000);
    document.addEventListener('visibilitychange', () => { if(document.hidden) checkVersion(); });
    window.addEventListener('focus', checkVersion);

    // ===== 左右方向键滑动卡片（按住重复按键时节流 120ms，防高频重渲染卡死 WebView）=====
    let lastKeyNavTs = 0;
    function onKeydown(e){
      if(page.value !== 'cards' || !cards.value.length) return;
      const ae = document.activeElement;
      if(ae && (ae.tagName === 'INPUT' || ae.tagName === 'TEXTAREA' || ae.isContentEditable)) return;
      if(e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
      e.preventDefault();
      const now = Date.now();
      if(now - lastKeyNavTs < 60) return;
      lastKeyNavTs = now;
      if(e.key === 'ArrowRight'){ switchCard(-1); }
      else { switchCard(1); }
    }
    window.addEventListener('keydown', onKeydown);

    // ==================== 管理员·活动管理台（ga-） ====================
    // 三个页面：group_admin 总览 / group_detail 控制台 / group_roster 名单。
    // 模式（分组/一对一）由「模式卡」切换；破坏性的是「清空分组结果」与「清空一对一结果」
    // （都会删除该活动对应模式的全部历史结果），执行动作有幂等保护与互斥 guard。见设计 §2。
    const gaLoading = ref(false), gaError = ref(false), gaList = ref([]);
    const gaQ = ref('');
    const gaAdminBadge = ref(''); let gaBadgeAt = 0;

    const gaDetailId = ref(''), gaDetail = ref(null);
    // 「编辑活动」iframe 需要数字主键（/admin/activities/{id}/edit 只收数字），
    // 详情优先，尚未载入时回退列表项；activity_no 不能直接当路径参数。
    const gaDetailDbId = computed(() => {
      const d = gaDetail.value;
      if (d && d.id) return String(d.id);
      const it = gaList.value.find(x => x.activity_id === gaDetailId.value);
      return (it && it.id) ? String(it.id) : '';
    });
    const gaDetailLoading = ref(false), gaDetailError = ref(false), gaDetailTab = ref(0);
    const gaModeSwitching = ref(false);
    // 现场签到码（管理台小卡）：只接受 4 位数字
    const gaSigninCode = ref(''), gaSigninCodeBusy = ref(false);
    const gaSigninCodeOk = computed(() => /^\d{4}$/.test(String(gaSigninCode.value || '')));
    // 活动状态（管理台小卡）：五值固定顺序；busy 期间所有按钮禁用，防重复提交
    const GA_ACTIVITY_STATUS_OPTIONS = ['未开始报名', '报名中', '已满员', '已结束', '已取消'];
    const GA_ACTIVITY_STATUS_CLASS = { '未开始报名': '', '报名中': 'collecting', '已满员': 'running', '已结束': 'done', '已取消': 'danger' };
    const gaActivityStatusOptions = GA_ACTIVITY_STATUS_OPTIONS;
    const gaActivityStatus = computed(() => (gaDetail.value && gaDetail.value.activity_status) || '');
    const gaActivityStatusBusy = ref(false), gaActivityStatusTarget = ref('');
    function gaActivityStatusChipClass(s) { return GA_ACTIVITY_STATUS_CLASS[s] || ''; }

    const gaRosterTab = ref(0);       // 0=未填写（现场重点）1=已填写
    const gaRosterQuery = ref(''), gaRosterList = ref([]), gaRosterTotal = ref(0);
    const gaRosterSubmittedList = ref([]);   // /unsubmitted 的 submitted 数组（已填写 tab）
    const gaRosterSigninOn = ref(false), gaRosterOnSite = ref(0);   // 名单页签到口径（未填写 tab 的现场数）
    const gaRosterLoading = ref(false), gaRosterError = ref(false), gaRosterShown = ref(30);

    // 执行分组弹层（每组男女数入参）；清空分组结果确认层（输入活动编号后调 reset）
    const gaRunPopupShow = ref(false), gaRunM = ref(3), gaRunF = ref(3), gaRunPopupPreview = ref(null);
    const gaConfirmShow = ref(false), gaConfirmInput = ref(''), gaConfirmBusy = ref(false);

    // 一对一：执行（job）与清空结果确认层（输入活动编号后调 reset）
    const gaOtoConfirmInput = ref('');
    const gaOtoRunBusy = ref(false);
    const gaOtoRunShow = ref(false), gaOtoRunFemaleChats = ref(''), gaOtoRunPreview = ref(null);
    const gaOtoResetShow = ref(false), gaOtoResetBusy = ref(false);

    const gaRunShow = ref(false), gaRunBusy = ref(false);
    const gaJobId = ref(''), gaJobRound = ref(1), gaJobActivityName = ref(''), gaJobProgress = ref('');
    const gaJobKind = ref('group');   // 'group' | 'onetoone'：轮询端点、遮罩文案、localStorage key 后缀
    const gaMaskSecs = ref(0), gaRunTip = ref(''), gaUnknownShow = ref(false);
    const gaGroups = ref([]), gaGroupsRound = ref(0), gaResultRound = ref(0);
    const gaResultLoading = ref(false), gaResultError = ref(false);
    const gaOtoPeople = ref([]), gaOtoOpen = ref({});
    const gaOtoResultLoading = ref(false), gaOtoResultError = ref(false);
    const gaOtoResultRound = ref(0), gaOtoLoadedRound = ref(0), gaOtoResultCount = ref(0);
    // 一对一「聊天情况总览」tab（只读；进入 tab / 切轮次时拉取）
    const chatOvData = ref(null), chatOvLoading = ref(false), chatOvError = ref(false);
    const chatOvOpen = ref({}), chatOvRound = ref(0), chatOvLoadedRound = ref(0);
    let chatOvSeq = 0;
    let gaPollTimer = null, gaPollCount = 0, gaPollFails = 0, gaMaskTimer = null, gaJobStartedAt = 0;
    const GA_POLL_MS = 2000, GA_POLL_MAX = 120, GA_JOB_KEY = 'yxq_ga_job_';  // 2s 一次、最多 4 分钟：超时只提示，不报错

    // 状态模型只有「空(未开始)/已完成」：步骤条简化为两态
    const gaStepDefs = ['待执行', '已完成'];
    // 状态显示：新模型只有「空(未开始)/已完成」；旧数据可能残留「收集中」「已截止」两个
    // 历史中间态（后端已只读不再写），显示层统一映射为「待执行」，不让历史坏值露脸
    function gaStatusDisplay(s) {
      return (s === '收集中' || s === '已截止') ? '待执行' : (s || '未开始');
    }
    function gaChipClass(s) { return s === '已完成' ? 'done' : ''; }
    // 模式 chip（列表/详情共用）：mode 是后端从两个 flag 派生的值
    const GA_MODE_LABEL = { group: '分组', onetoone: '一对一', none: '未设定', conflict: '⚠ 冲突' };
    const GA_MODE_CLASS = { group: 'collecting', onetoone: 'running', none: '', conflict: 'danger' };
    function gaModeChip(a) { return GA_MODE_LABEL[a && a.mode] || '未设定'; }
    function gaModeChipClass(a) { return GA_MODE_CLASS[a && a.mode] || ''; }
    function gaStatusChipText(a) {
      if (a.mode === 'conflict') return '两模式冲突';
      return gaStatusDisplay(a.mode === 'onetoone' ? a.onetoone_status : a.group_status);
    }
    function gaStatusChipClass(a) {
      if (a.mode === 'conflict') return 'danger';
      return gaChipClass(gaStatusDisplay(a.mode === 'onetoone' ? a.onetoone_status : a.group_status));
    }
    const gaDetailMode = computed(() => (gaDetail.value && gaDetail.value.mode) || 'none');
    const gaModeText = computed(() => {
      const d = gaDetail.value;
      if (!d) return '';
      if (d.mode === 'conflict') return '⚠ 两个模式都开着，请纠偏';
      if (d.mode === 'onetoone') return '一对一模式 · ' + ((d.onetoone_rounds || []).length) + ' 轮结果 · 已匹配 ' + d.onetoone_matched_count + ' 人';
      if (d.mode === 'group') return '分组模式 · ' + (d.rounds || []).length + ' 轮结果';
      return '未设定模式';
    });
    const gaOtoRounds = computed(() => (gaDetail.value && gaDetail.value.onetoone_rounds) || []);
    const gaOtoLatestRound = computed(() => (gaDetail.value && gaDetail.value.onetoone_latest_round) || 0);
    const gaOtoNextRound = computed(() => gaOtoLatestRound.value ? gaOtoLatestRound.value + 1 : 1);
    const chatOvSummary = computed(() => (chatOvData.value && chatOvData.value.summary)
      || { people: 0, pairs: 0, chatted_pairs: 0, rate: 0, zero_people: 0, done_people: 0, mutual_pairs: 0, messages: 0 });
    const chatOvRatePct = computed(() => Math.round((chatOvSummary.value.rate || 0) * 100));
    // 清空一对一结果确认：必须输入活动编号（破坏性操作，固定要求确认）
    const gaOtoResetOk = computed(() => String(gaOtoConfirmInput.value || '').trim().toLowerCase()
      === String(gaDetailId.value || '').trim().toLowerCase());
    const gaMaskTitle = computed(() => (gaJobKind.value === 'onetoone' ? '正在执行一对一匹配…' : '正在执行分组…'));
    const gaStepIndex = computed(() => ((gaDetail.value && gaDetail.value.group_status) === '已完成') ? 1 : 0);
    const gaFilteredList = computed(() => {
      const q = gaQ.value.trim().toLowerCase();
      return gaList.value.filter(a => !q || String(a.name || '').toLowerCase().includes(q)
        || String(a.activity_id || '').toLowerCase().includes(q));
    });
    // 待执行判定：按当前模式的侧别状态；冲突态算待处理；未设定模式不计
    function gaRunPending(a) {
      if (!a || a.mode === 'none') return false;
      if (a.mode === 'conflict') return true;
      return (a.mode === 'onetoone' ? a.onetoone_status : a.group_status) !== '已完成';
    }
    const gaStats = computed(() => {
      let todo = 0, done = 0;
      gaList.value.forEach(a => {
        if (!a || a.mode === 'none') return;
        if (gaRunPending(a)) todo += 1; else done += 1;
      });
      return { todo, done };
    });
    const gaExpectedGroups = computed(() => {
      const d = gaDetail.value;
      return (!d || d.males_per_group < 1 || d.females_per_group < 1) ? null
        : Math.min(Math.floor(d.male_count / d.males_per_group), Math.floor(d.female_count / d.females_per_group));
    });
    const gaNextRound = computed(() => (gaDetail.value && gaDetail.value.latest_round) ? gaDetail.value.latest_round + 1 : 1);
    const gaRosterSubmitted = computed(() => {
      const d = gaDetail.value;
      if (!d) return 0;
      return gaDetailMode.value === 'onetoone'
        ? (d.onetoone_submitted_count || 0) : (d.submitted_count || 0);
    });
    const gaRosterFiltered = computed(() => {
      const q = gaRosterQuery.value.trim().toLowerCase();
      return gaRosterList.value.filter(r => {
        if (gaRosterSigninOn.value && r.checked_in !== true) return false;   // 签到开启：未填写只列已到现场的人
        if (!q) return true;
        return String(r.nickname || '').toLowerCase().includes(q)
          || String(r.real_name || '').toLowerCase().includes(q)
          || String(r.user_id || '').toLowerCase().includes(q);
      });
    });
    const gaRosterVisible = computed(() => gaRosterFiltered.value.slice(0, gaRosterShown.value));
    const gaSubmittedOnSite = computed(() => gaRosterSubmittedList.value.filter(r => r.checked_in === true).length);
    const gaSubmittedFiltered = computed(() => {
      const q = gaRosterQuery.value.trim().toLowerCase();
      return gaRosterSubmittedList.value.filter(r => {
        if (gaRosterSigninOn.value && r.checked_in !== true) return false;   // 签到开启：已填写只列已到现场的人
        if (!q) return true;
        return String(r.nickname || '').toLowerCase().includes(q)
          || String(r.real_name || '').toLowerCase().includes(q)
          || String(r.user_id || '').toLowerCase().includes(q);
      });
    });
    const gaSubmittedVisible = computed(() => gaSubmittedFiltered.value.slice(0, gaRosterShown.value));
    // 执行分组弹层：按输入的每组人数预估组数（人数取预览的最新数据）
    const gaRunExpected = computed(() => {
      const pv = gaRunPopupPreview.value;
      const m = Number(gaRunM.value), f = Number(gaRunF.value);
      if (!pv || !Number.isInteger(m) || m < 1 || !Number.isInteger(f) || f < 1) return 0;
      return Math.min(Math.floor((pv.male_count || 0) / m), Math.floor((pv.female_count || 0) / f));
    });
    // F-07：摘要口径——「已填志愿 X 男 Y 女」取预览的 male/female_count；
    // 「预计可分 Z 组」优先用后端预览的 expected_groups（与执行口径一致），缺失时回退既有本地估算
    const gaRunPopupHasPeople = computed(() => {
      const pv = gaRunPopupPreview.value;
      return !!(pv && (pv.male_count || pv.female_count));
    });
    const gaRunPopupExpectedGroups = computed(() => {
      const pv = gaRunPopupPreview.value;
      if (pv && pv.expected_groups != null) return pv.expected_groups;
      return gaRunExpected.value;
    });
    // 清空确认：必须输入活动编号（破坏性操作，固定要求确认）
    const gaConfirmOk = computed(() => String(gaConfirmInput.value || '').trim().toLowerCase()
      === String(gaDetailId.value || '').trim().toLowerCase());

    // 网络层失败（fetch 断连/换网）→ 浏览器原文是 "Failed to fetch"/"Load failed"，用户看不懂
    function gaNetError(e) {
      return isNetworkError(e);
    }
    function gaMsg(e, fallback) {
      const m = (e && e.message) ? e.message : '';
      if (!m) return fallback;
      if (gaNetError(e)) return NETWORK_ERROR_MESSAGE;
      return m;   // 含「请求超时，请检查网络后重试」等既有文案，原样透出
    }
    // 分组管理台只读 GET：网络层失败隔 600ms 自动重试一次。
    // 写操作（start/run/flag/preview）绝不能走这里——自动重试可能造成重复执行。
    async function gaGet(url) {
      try {
        return await api(url);
      } catch(e) {
        if (!gaNetError(e)) throw e;
        await new Promise(r => setTimeout(r, 600));
        return api(url);
      }
    }
    function gaAuthBack(e) {
      const m = gaMsg(e, '');
      if (m.includes('未登录') || m.includes('没有权限')) {
        vant.showToast(m);
        if (nativeAdmin.value) { location.href = '/admin?embed=1'; } else { page.value = 'profile'; }
        return true;
      }
      return false;
    }
    function gaRowSub(a) {
      const per = (a.males_per_group > 0 && a.females_per_group > 0)
        ? ('每组' + a.males_per_group + '男' + a.females_per_group + '女') : '还没设置每组人数';
      if (a.mode === 'conflict') return '⚠ 分组与一对一都开着，进详情纠偏';
      if (a.mode === 'onetoone') {
        if ((a.onetoone_status || '未开始') === '已完成') return '一对一已匹配 · 已填 ' + a.onetoone_submitted_count + '/' + a.signup_count;
        return '报名 ' + a.signup_count + ' 人 · 已填 ' + a.onetoone_submitted_count + '/' + a.signup_count;
      }
      if (a.group_status === '已完成') return '已分 ' + ((a.available_rounds || []).length || 1) + ' 轮 · ' + per + ' · 第' + a.latest_round + '轮';
      return '报名 ' + a.signup_count + ' 人 · 已填 ' + a.submitted_count + '/' + a.signup_count + ' · ' + per;
    }
    async function refreshAdminBadge() {
      if (!user.value || !user.value.can_admin) { gaAdminBadge.value = ''; return; }
      if (Date.now() - gaBadgeAt < 30000) return;   // 30 秒缓存，避免每次切页都打实时接口
      try {
        const data = await gaGet('/api/admin/group/activities');
        const n = (data.activities || []).filter(gaRunPending).length;
        gaAdminBadge.value = n > 0 ? (n + '场待执行') : ''; gaBadgeAt = Date.now();
      } catch(e) { /* 入口徽标静默失败，不打扰 */ }
    }
    function openGroupAdmin() { gaQ.value = ''; gaError.value = false; page.value = 'group_admin'; }
    // 编辑活动：复用内嵌管理台的 SSR 表单，iframe 语义不受顶层 /admin 303 影响。
    function openEditActivity() {
      if (!gaDetailDbId.value) return;
      openEmbed('/admin/activities/' + gaDetailDbId.value + '/edit', '编辑活动');
    }
    // 广播：管理台 SSR 活动页的 #broadcast tab（原生控制台只做入口，发送仍在管理台）。
    function openBroadcast() {
      if (!gaDetailDbId.value) return;
      openEmbed('/admin/activities/' + gaDetailDbId.value + '#broadcast', '活动广播');
    }
    async function loadGroupAdminList() {
      if (gaLoading.value) return;
      // 已有缓存（如从详情返回）：先用旧列表渲染、后台静默刷新；失败留旧数据只 toast。
      // 首次加载（无缓存）才显示 loading / 错误态（F-13）
      const silent = gaList.value.length > 0;
      if (!silent) gaLoading.value = true;
      gaError.value = false;
      try {
        const data = await gaGet('/api/admin/group/activities');
        gaList.value = data.activities || [];
        const n = gaList.value.filter(gaRunPending).length;
        gaAdminBadge.value = n > 0 ? (n + '场待执行') : ''; gaBadgeAt = Date.now();
      } catch(e) {
        if (!gaAuthBack(e)) {
          if (!silent) gaError.value = true;
          vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次'));
        }
      }
      gaLoading.value = false;
    }
    function openGroupDetail(a) {
      gaDetailId.value = a.activity_id; gaDetail.value = null; gaDetailError.value = false; gaDetailTab.value = 0;
      gaGroups.value = []; gaGroupsRound.value = 0; gaResultRound.value = 0; gaRunTip.value = '';
      gaResultLoading.value = false; gaResultError.value = false;
      gaOtoPeople.value = []; gaOtoOpen.value = {}; gaOtoResultLoading.value = false; gaOtoResultError.value = false;
      gaOtoResultRound.value = 0; gaOtoLoadedRound.value = 0; gaOtoResultCount.value = 0;
      resetChatOverview();
      gaRosterTab.value = 0; gaRosterQuery.value = ''; gaRosterList.value = []; gaRosterTotal.value = 0; gaRosterSubmittedList.value = []; gaRosterShown.value = 30; gaRosterError.value = false; gaRosterSigninOn.value = false; gaRosterOnSite.value = 0;
      page.value = 'group_detail';
    }
    function openCheckinPage(a) {
      gaDetailId.value = a.activity_id; gaCheckinName.value = a.name || '';
      gaSigninList.value = []; gaSigninLoaded.value = false; gaSigninError.value = false;
      gaSigninQuery.value = ''; gaSigninGender.value = 'all'; gaSigninStatus.value = 'all'; gaSigninShown.value = 30; gaSigninFlagOn.value = false;
      if(page.value === 'group_detail'){
        // 控制台页头入口：把当前 group_detail 条目就地改写为 checkin（塌缩历史，不新增条目），
        // 返回时自然回退 group_admin；一次性旗标让 watch(page) 跳过本 tick 的补 push
        try{ hist.replace({page:'checkin'}, navURL()); }catch(e){}
        suppressPagePushOnce = true;
      }
      page.value = 'checkin';
    }
    async function refreshGroupDetail() {
      if (gaDetailLoading.value || !gaDetailId.value) return;
      gaDetailLoading.value = true; gaDetailError.value = false;
      try {
        const d = await gaGet('/api/admin/group/status?activity_id=' + encodeURIComponent(gaDetailId.value));
        const prevMode = gaDetail.value && gaDetail.value.mode;
        gaDetail.value = d;
        gaSigninCode.value = d.signin_code || '';
        if (d.mode !== prevMode) {
          // 模式变了（本端切换/另一端改）：tab 内容整块换掉，缓存不能跨模式复用
          gaDetailTab.value = 0;
          gaGroups.value = []; gaGroupsRound.value = 0;
          gaOtoPeople.value = []; gaOtoOpen.value = {};
          gaOtoResultRound.value = 0; gaOtoLoadedRound.value = 0; gaOtoResultCount.value = 0;
          resetChatOverview();
          gaRosterList.value = []; gaRosterSubmittedList.value = []; gaRosterTotal.value = 0; gaRosterSigninOn.value = false; gaRosterOnSite.value = 0;
        }
        gaResultRound.value = gaGroupsRound.value || d.latest_round || ((d.rounds && d.rounds.length) ? d.rounds[d.rounds.length - 1] : 0);
        if (gaDetailTab.value === 2 && gaDetailMode.value === 'group'
            && gaResultRound.value && gaGroupsRound.value !== gaResultRound.value) {
          loadGaGroupResult(gaResultRound.value);   // 状态比结果 tab 先到：补拉当前轮次
        }
        gaOtoResultRound.value = gaOtoLoadedRound.value || d.onetoone_latest_round || 0;
        if (gaDetailTab.value === 2 && gaDetailMode.value === 'onetoone'
            && gaOtoResultRound.value && gaOtoLoadedRound.value !== gaOtoResultRound.value) {
          loadGaOtoResult(gaOtoResultRound.value);   // 一对一结果同样按轮次补拉
        }
        restoreGaJob();
      } catch(e) { if (!gaAuthBack(e)) { gaDetailError.value = true; vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次')); } }
      gaDetailLoading.value = false;
    }
    async function saveGaSigninCode() {
      const code = String(gaSigninCode.value || '');
      if (!/^\d{4}$/.test(code)) { vant.showToast('签到码须为4位数字'); return; }
      if (gaSigninCodeBusy.value || !gaDetailId.value) return;
      gaSigninCodeBusy.value = true;
      try {
        await api('/api/admin/activity/signin-code', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value, code: code }) });
        if (gaDetail.value) gaDetail.value.signin_code = code;
        vant.showToast('签到码已保存');
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '保存失败，再试一次'));
      }
      gaSigninCodeBusy.value = false;
    }
    async function confirmActivityStatus(target) {
      if (!gaDetail.value || gaActivityStatusBusy.value || !gaDetailId.value) return;
      if (target === gaActivityStatus.value) { vant.showToast('当前已是「' + target + '」'); return; }
      const msg = target === '已结束'
        ? '设为已结束后将停止报名，已有报名/志愿/结果数据保留。'
        : target === '已取消'
          ? '设为已取消会触发优先名额退穗（机器人收尾），确定？'
          : target === '报名中'
            ? '重新开放报名？'
            : '将活动状态改为「' + target + '」？';
      try {
        await vant.showConfirmDialog({ title: '活动状态', message: msg,
          confirmButtonText: '确认修改', cancelButtonText: '取消', confirmButtonColor: '#ff6b81' });
      } catch(e) { return; }   // 取消
      gaActivityStatusTarget.value = target;
      gaActivityStatusBusy.value = true;
      try {
        const data = await api('/api/admin/activity/status', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value, status: target }) });
        if (gaDetail.value) gaDetail.value.activity_status = data.status;
        const it = gaList.value.find(x => x.activity_id === gaDetailId.value);
        if (it) it.activity_status = data.status;
        vant.showToast('活动状态已更新');
        refreshGroupDetail();
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '更新失败，再试一次'));
      }
      gaActivityStatusBusy.value = false;
      gaActivityStatusTarget.value = '';
    }
    async function switchMode(mode) {
      const d = gaDetail.value;
      if (!d || gaModeSwitching.value || !gaDetailId.value) return;
      if (d.active_job) { vant.showToast('有任务正在执行，等它结束后再切换'); return; }
      const label = { group: '分组', onetoone: '一对一', none: '未设定' }[mode] || mode;
      const closing = [];
      if (d.group_flag === '是' && mode !== 'group') closing.push('分组');
      if (d.onetoone_flag === '是' && mode !== 'onetoone') closing.push('一对一');
      // 状态拦截已废除（新模型只有 空/已完成，历史遗留值不再拦；后端切换端点也不看状态，
      // 只拦活跃 job——即上方 active_job 与后端 409（MODE_SWITCH_BLOCKED）兜底）。
      const msg = closing.length
        ? ('将关闭「' + closing.join('」「') + '」入口；不会删除任何资料，既有结果仍保留。')
        : ('将开启「' + label + '」入口；不会删除任何资料，既有结果仍保留。');
      try {
        await vant.showConfirmDialog({ title: '切换为「' + label + '」模式', message: msg,
          confirmButtonText: '确认切换', confirmButtonColor: '#ff6b81' });
      } catch(e) { return; }   // 取消
      gaModeSwitching.value = true;
      try {
        const data = await api('/api/admin/activity/mode', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value, mode: mode }) });
        vant.showToast(mode === 'none' ? '已关闭全部模式入口' : ('已切换为「' + label + '」模式'));
        const it = gaList.value.find(x => x.activity_id === gaDetailId.value);
        if (it && data) { it.mode = data.mode; it.group_flag = data.group_flag; it.onetoone_flag = data.onetoone_flag; it.conflict = false; }
        refreshGroupDetail();
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '切换失败，再试一次'));
      }
      gaModeSwitching.value = false;
    }
    async function loadGroupRoster(force) {
      if (gaRosterLoading.value) return;
      if (!force && gaRosterList.value.length) return;
      gaRosterLoading.value = true; gaRosterError.value = false;
      try {
        const ep = gaDetailMode.value === 'onetoone' ? 'onetoone' : 'group';
        const data = await gaGet('/api/admin/' + ep + '/unsubmitted?activity_id=' + encodeURIComponent(gaDetailId.value));
        gaRosterList.value = data.unsubmitted || []; gaRosterTotal.value = data.total || 0; gaRosterShown.value = 30;
        gaRosterSubmittedList.value = data.submitted || [];
        gaRosterSigninOn.value = !!data.signin_enabled; gaRosterOnSite.value = data.unsubmitted_checked_in || 0;
        if (gaRosterTab.value === 0 && !gaRosterList.value.length && gaRosterSubmitted.value > 0) gaRosterTab.value = 1;
      } catch(e) { if (!gaAuthBack(e)) { gaRosterError.value = true; vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次')); } }
      gaRosterLoading.value = false;
    }
    function gaAttText(r) {
      if (r && r.checked_in) return '已到';
      return (r && r.attendance === '未到') ? '未到' : '未签到';
    }
    function gaAttClass(r) {
      if (r && r.checked_in) return 'ok';
      return (r && r.attendance === '未到') ? 'absent' : 'pending';
    }
    function openGroupRoster() { page.value = 'group_roster'; gaRosterQuery.value = ''; }
    async function loadGaGroupResult(r) {
      if (!gaDetailId.value || !r) return;
      const wanted = r;
      gaResultLoading.value = true; gaResultError.value = false;
      try {
        const data = await gaGet('/api/admin/group/result?activity_id=' + encodeURIComponent(gaDetailId.value) + '&round=' + wanted);
        if (gaResultRound.value !== data.round) return;   // 已切到别的轮次：丢弃过期响应
        gaGroups.value = data.groups || []; gaGroupsRound.value = data.round || 0;
      } catch(e) {
        if (!gaAuthBack(e)) { gaResultError.value = true; }
      } finally {
        if (gaResultRound.value === wanted) gaResultLoading.value = false;
      }
    }
    function selectGaResultRound(r) {
      if (gaResultRound.value === r && gaGroupsRound.value === r && !gaResultError.value) return;
      gaResultRound.value = r;
      loadGaGroupResult(r);
    }
    async function gaOpenUser(openid) {
      if (!openid) return;
      userDetailFromSheet.value = false;
      userDetailFrom.value = (page.value === 'group_roster') ? 'group_roster' : 'group_detail';
      page.value = 'user_detail'; userDetail.value = null;
      try {
        const data = await api('/api/users/' + openid + '/public');
        if (data && (data.liked || justLiked.has(openid))) { data.liked = true; data.can_like = false; }
        userDetail.value = data;
      } catch(e) { vant.showToast(gaMsg(e, '资料没载出来，再试一次')); }
    }
    function openGaResetConfirm() {
      gaConfirmInput.value = '';
      gaConfirmShow.value = true;
    }
    function cancelGaConfirm() { if (gaConfirmBusy.value) return; gaConfirmShow.value = false; }
    async function doGaReset() {
      if (!gaConfirmOk.value || gaConfirmBusy.value) return;
      gaConfirmBusy.value = true;
      try {
        const data = await api('/api/admin/group/reset', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value }) });
        gaConfirmShow.value = false;
        const cleared = (data && data.cleared_results) || 0;
        vant.showToast('已清空分组结果' + (cleared > 0 ? ('，共 ' + cleared + ' 条') : ''));
        gaGroups.value = []; gaGroupsRound.value = 0; gaResultRound.value = 0; gaResultError.value = false;
        refreshGroupDetail();
      } catch(e) { if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '清空失败，再试一次')); }
      gaConfirmBusy.value = false;
    }
    async function gaApi(url, options = {}) {
      // run 需要读 409 body 里的 job_id（既有 api() 会把它吞掉），单独一份轻量请求
      const csrf = (document.cookie.match(/(?:^|; )yxq_csrf=([^;]*)/) || [])[1] || '';
      const headers = { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' };
      if (csrf) headers['X-CSRF-Token'] = csrf;
      const controller = new AbortController();
      // 与 api() 同一阈值：run 的 409 body 要读 job_id，同样不能 15s 就掐掉（网络层失败不自动重试）
      const timeout = setTimeout(() => controller.abort(), 30000);
      try {
        const resp = await fetch(BASE_PATH + url, { credentials: 'same-origin',
          headers: { ...headers, ...(options.headers || {}) }, signal: controller.signal, ...options });
        clearTimeout(timeout);
        const data = await resp.json().catch(() => ({}));
        return { status: resp.status, data };
      } catch(e) {
        clearTimeout(timeout);
        if (e.name === 'AbortError') throw new Error('请求超时，请检查网络后重试');
        throw asNetworkError(e);
      }
    }
    function closeGaRunPopup() { if (!gaRunBusy.value) gaRunPopupShow.value = false; }
    // 执行弹层「×」/点遮罩关闭：每组人数改过 → 先走通用离开拦截（两钮）
    function requestCloseGaRunPopup(){
      if (gaRunBusy.value) return;
      if (guardOverlay('gaRun', () => { gaRunPopupShow.value = false; })) return;
      closeGaRunPopup();
    }
    async function startRunFlow() {
      if (gaRunBusy.value || !gaDetailId.value) return;
      gaRunBusy.value = true;
      const rd = gaNextRound.value;
      try {
        const pv = await api('/api/admin/group/preview', { method: 'POST', body: JSON.stringify({ activity_id: gaDetailId.value, round: rd }) });
        // 每组人数改为执行弹层输入、run 端点与执行层二次校验；预览里基于活动栏位旧值的
        //「每组男女」「人数不足」两类提示不作为硬拦截（与一对一预览过滤女生场次同款）
        const hardBlockers = (pv.blockers || []).filter(b => b.indexOf('每组男女') < 0 && b.indexOf('人数不足') < 0);
        if (hardBlockers.length) {
          vant.showConfirmDialog({ title: '还差一点', message: hardBlockers.join('\n'), showCancelButton: false, confirmButtonColor: '#ff6b81' }).catch(() => {});
        } else {
          gaRunPopupPreview.value = pv;
          const d = gaDetail.value;
          gaRunM.value = (d && d.males_per_group > 0) ? d.males_per_group : 3;
          gaRunF.value = (d && d.females_per_group > 0) ? d.females_per_group : 3;
          gaRunBaselineKey = gaRunKey();   // 进层基线：之后改动即算未提交
          gaRunPopupShow.value = true;
        }
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次'));
      }
      gaRunBusy.value = false;
    }
    async function confirmGaRun() {
      if (gaRunBusy.value) return;
      const rd = gaNextRound.value;
      const m = Number(gaRunM.value), f = Number(gaRunF.value);
      if (!Number.isInteger(m) || m < 1 || !Number.isInteger(f) || f < 1) { vant.showToast('每组人数必须是大于0的整数'); return; }
      gaRunBusy.value = true;
      gaRunPopupShow.value = false;
      await doGaRun(rd, m, f);
    }
    async function doGaRun(rd, m, f) {
      const name = (gaDetail.value && gaDetail.value.name) || gaDetailId.value;
      gaJobKind.value = 'group';
      setPageBusy('正在启动任务…');   // 启动请求在途：遮罩未开时页面不可误操作（job 遮罩随 after 接管，P16）
      try {
        const r = await gaApi('/api/admin/group/run', { method: 'POST', body: JSON.stringify({ activity_id: gaDetailId.value, round: rd, males_per_group: m, females_per_group: f }) });
        if ((r.status === 202 || r.status === 200) && r.data && r.data.job_id) {
          attachGaJob(r.data.job_id, rd, name);
        } else if (r.status === 200 && r.data && r.data.ok && r.data.result) {
          // v2 同步执行：完成后直接回 {ok:true,result}，复用 v1 job-done 分支展示结果/错误码；
          // 不建 job，不触发 202 轮询（上面 job_id 分支保留给 v1 兼容）。
          gaJobRound.value = rd;
          handleGaJobDone(r.data);
        } else if (r.status === 409 && r.data && r.data.job_id) {
          attachGaJob(r.data.job_id, r.data.round || rd, name);
          vant.showToast('这轮已经在跑了，帮你接上进度');
        } else {
          vant.showToast((r.data && r.data.error) || '没跑起来，再试一次');
        }
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次'));
      }
      gaRunBusy.value = false;
      clearPageBusy();
    }
    // ---- 一对一：清空结果确认层（破坏性，输入活动编号）----
    function openOtoResetConfirm() {
      gaOtoConfirmInput.value = '';
      gaOtoResetShow.value = true;
    }
    function cancelOtoReset() { if (!gaOtoResetBusy.value) gaOtoResetShow.value = false; }
    async function doOtoReset() {
      if (!gaOtoResetOk.value || gaOtoResetBusy.value) return;
      gaOtoResetBusy.value = true;
      try {
        const data = await api('/api/admin/onetoone/reset', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value }) });
        gaOtoResetShow.value = false;
        const cleared = (data && data.cleared_results) || 0;
        vant.showToast('已清空一对一结果' + (cleared > 0 ? ('，共 ' + cleared + ' 条') : ''));
        gaOtoPeople.value = []; gaOtoOpen.value = {};
        gaOtoResultRound.value = 0; gaOtoLoadedRound.value = 0; gaOtoResultCount.value = 0; gaOtoResultError.value = false;
        refreshGroupDetail();
      } catch(e) { if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '清空失败，再试一次')); }
      gaOtoResetBusy.value = false;
    }
    // ---- 一对一：执行（job + 轮询）----
    const gaOtoRunK = computed(() => {
      const k = Number(String(gaOtoRunFemaleChats.value || '').trim());
      return Number.isInteger(k) && k >= 1 ? k : 0;
    });
    const gaOtoRunSummary = computed(() => {
      const pv = gaOtoRunPreview.value, k = gaOtoRunK.value;
      if (!pv || !k || !pv.male_count || !pv.female_count || k > pv.male_count) return '';
      const E = pv.female_count * k;
      const base = Math.floor(E / pv.male_count), rem = E % pv.male_count;
      return '女生 ' + pv.female_count + ' 人每人 ' + k + ' 场 · 全场共 ' + E + ' 场；男生 ' + pv.male_count + ' 人每人 ' + base + (rem ? '~' + (base + 1) : '') + ' 场';
    });
    async function startOtoRunFlow() {
      if (gaOtoRunBusy.value || !gaDetailId.value) return;
      gaOtoRunBusy.value = true;
      const rd = gaOtoNextRound.value;
      try {
        const pv = await api('/api/admin/onetoone/preview', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value, round: rd }) });
        // 女生场次由弹层里输入，这里只挡「场次之外」的硬拦截（无参与者、该轮已生成…）
        const hardBlockers = (pv.blockers || []).filter(b => b.indexOf('女生场次') < 0);
        if (hardBlockers.length) {
          gaOtoRunBusy.value = false;
          vant.showConfirmDialog({ title: '还差一点', message: hardBlockers.join('\n'), showCancelButton: false, confirmButtonColor: '#ff6b81' }).catch(() => {});
          return;
        }
        gaOtoRunPreview.value = pv;
        gaOtoRunFemaleChats.value = '';
        gaOtoRunShow.value = true;
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次'));
      }
      gaOtoRunBusy.value = false;
    }
    function closeOtoRun() { if (!gaOtoRunBusy.value) gaOtoRunShow.value = false; }
    // 执行弹层「×」/点遮罩关闭：女生场次填过 → 先走通用离开拦截（两钮）
    function requestCloseOtoRun(){
      if (gaOtoRunBusy.value) return;
      if (guardOverlay('gaOtoRun', () => { gaOtoRunShow.value = false; })) return;
      closeOtoRun();
    }
    async function confirmOtoRun() {
      if (gaOtoRunBusy.value) return;
      const rd = gaOtoNextRound.value;
      const pv = gaOtoRunPreview.value || {};
      const K = gaOtoRunK.value;
      if (!K) { vant.showToast('女生场次必须是 ≥1 的整数（每位女生接待的男生数）'); return; }
      if (pv.male_count && K > pv.male_count) {
        vant.showToast('女生场次不能超过男生人数 ' + pv.male_count + '（每位女生最多与每位男生各聊一次）');
        return;
      }
      gaOtoRunBusy.value = true;
      gaOtoRunShow.value = false;
      await doOtoRun(rd, K);
    }
    async function doOtoRun(rd, K) {
      const name = (gaDetail.value && gaDetail.value.name) || gaDetailId.value;
      gaJobKind.value = 'onetoone';
      setPageBusy('正在启动任务…');   // 启动请求在途：遮罩未开时页面不可误操作（job 遮罩随 after 接管，P16）
      try {
        const r = await gaApi('/api/admin/onetoone/run', { method: 'POST', body: JSON.stringify({ activity_id: gaDetailId.value, round: rd, female_chats: K }) });
        if ((r.status === 202 || r.status === 200) && r.data && r.data.job_id) {
          attachGaJob(r.data.job_id, r.data.round || rd, name);
        } else if (r.status === 200 && r.data && r.data.ok && r.data.result) {
          // v2 同步执行：同分组，结果/错误码交给 v1 handleOtoJobDone；202/job 分支保留不触发。
          gaJobRound.value = rd;
          handleOtoJobDone(r.data);
        } else if (r.status === 409 && r.data && r.data.job_id) {
          attachGaJob(r.data.job_id, r.data.round || rd, name);
          vant.showToast('第 ' + (r.data.round || rd) + ' 轮一对一已经在跑了，帮你接上进度');
        } else {
          vant.showToast((r.data && r.data.error) || '没跑起来，再试一次');
        }
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次'));
      }
      gaOtoRunBusy.value = false;
      clearPageBusy();
    }
    async function loadGaOtoResult(round) {
      // 管理台一对一结果（改名避开用户端同名函数：同名函数声明后者覆盖前者，
      // 会把用户端「查看我的必聊」劫持到管理台端点）
      // 不拦 loading：切轮次必须发新请求，过期响应按轮次丢弃（与 loadGroupResult 同款；
      // 旧写法在加载中点另一个轮次会把新请求挡掉，旧响应又因轮次已变不清 loading → 死锁）
      if (!gaDetailId.value) return;
      const wanted = round || gaOtoResultRound.value || gaOtoLatestRound.value;
      if (!wanted) return;
      gaOtoResultLoading.value = true; gaOtoResultError.value = false;
      try {
        const data = await gaGet('/api/admin/onetoone/result?activity_id=' + encodeURIComponent(gaDetailId.value) + '&round=' + wanted);
        if (gaOtoResultRound.value !== data.round) return;   // 已切到别的轮次：丢弃过期响应
        gaOtoPeople.value = data.people || [];
        gaOtoResultCount.value = data.result_count || 0;
        gaOtoLoadedRound.value = data.round || 0;
      } catch(e) {
        if (!gaAuthBack(e)) gaOtoResultError.value = true;
      } finally {
        if (gaOtoResultRound.value === wanted) gaOtoResultLoading.value = false;
      }
    }
    function selectGaOtoResultRound(r) {
      if (gaOtoResultRound.value === r && gaOtoLoadedRound.value === r && !gaOtoResultError.value) return;
      gaOtoResultRound.value = r;
      loadGaOtoResult(r);
    }
    function resetChatOverview() {
      chatOvSeq += 1;   // 让在途响应失效，避免串到另一个活动/模式
      chatOvData.value = null; chatOvLoading.value = false; chatOvError.value = false;
      chatOvOpen.value = {}; chatOvRound.value = 0; chatOvLoadedRound.value = 0;
    }
    async function loadChatOverview(round, force) {
      if (!gaDetailId.value) return;
      const wanted = Number(round) || 0;
      if (!force && chatOvData.value && chatOvLoadedRound.value === wanted) return;
      const seq = ++chatOvSeq;
      chatOvLoading.value = true; chatOvError.value = false;
      try {
        let url = '/api/admin/onetoone/chat-overview?activity_id=' + encodeURIComponent(gaDetailId.value);
        if (wanted > 0) url += '&round=' + wanted;
        const data = await gaGet(url);
        if (seq !== chatOvSeq) return;   // 已切轮次/活动：丢弃过期响应
        chatOvData.value = data;
        chatOvRound.value = data.round || 0;
        chatOvLoadedRound.value = data.round || 0;
        chatOvOpen.value = {};
      } catch(e) {
        if (seq !== chatOvSeq) return;
        if (!gaAuthBack(e)) {
          chatOvError.value = true;
          if (!chatOvData.value) vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次'));
        }
      } finally {
        if (seq === chatOvSeq) chatOvLoading.value = false;
      }
    }
    function selectChatOvRound(r) {
      if (chatOvData.value && chatOvData.value.round === r && !chatOvError.value) return;
      chatOvOpen.value = {};
      loadChatOverview(r, true);
    }
    function toggleChatOvPerson(openid) {
      chatOvOpen.value = { ...chatOvOpen.value, [openid]: !chatOvOpen.value[openid] };
    }
    function chatOvUnchattedText(p) {
      const list = (p && p.unchatted) || [];
      if (!list.length) return '无（全部聊过）';
      return list.map(x => x.name || x.open_id).join('、');
    }
    function gaOtoToggle(openid) {
      gaOtoOpen.value = { ...gaOtoOpen.value, [openid]: !gaOtoOpen.value[openid] };
    }
    // ---- job 轮询（分组/一对一共用；kind 决定端点、文案、localStorage key 后缀）----
    function gaJobKey(kind) { return GA_JOB_KEY + gaDetailId.value + '_' + kind; }
    function attachGaJob(jobId, rd, name) {
      gaJobId.value = jobId; gaJobRound.value = rd; gaJobActivityName.value = name; gaJobProgress.value = '';
      try { localStorage.setItem(gaJobKey(gaJobKind.value), jobId); } catch(e) {}
      openGaMask(); startGaPolling();
    }
    function clearGaJobRecord() {
      try {
        localStorage.removeItem(gaJobKey(gaJobKind.value));
        localStorage.removeItem(GA_JOB_KEY + gaDetailId.value);   // 旧格式（无 kind 后缀）的键
      } catch(e) {}
    }
    function openGaMask() {
      gaRunShow.value = true; gaMaskSecs.value = 0; gaJobStartedAt = Date.now();
      clearInterval(gaMaskTimer);
      gaMaskTimer = setInterval(() => { gaMaskSecs.value = Math.floor((Date.now() - gaJobStartedAt) / 1000); }, 1000);
    }
    function closeGaMask() { gaRunShow.value = false; clearInterval(gaMaskTimer); gaMaskTimer = null; }
    function stopGaPolling(keepMask) {
      clearTimeout(gaPollTimer); gaPollTimer = null; gaPollCount = 0; gaPollFails = 0;
      if (!keepMask) closeGaMask();
    }
    function startGaPolling() {
      gaPollCount = 0; gaPollFails = 0;
      if (!gaRunShow.value) openGaMask();
      pollGaJob();
    }
    function resumeGaPolling() { if (gaJobId.value && !gaPollTimer) startGaPolling(); }
    async function pollGaJob() {
      if (!gaJobId.value) return;
      gaPollCount += 1;
      if (gaPollCount > GA_POLL_MAX) {
        stopGaPolling(false);
        gaRunTip.value = '还在跑，可能人比较多。稍后回来看看就好';
        try { vant.showToast(gaRunTip.value); } catch(e) {}
        return;
      }
      try {
        const ep = gaJobKind.value === 'onetoone' ? '/api/admin/onetoone/job' : '/api/admin/group/job';
        const data = await gaGet(ep + '?job_id=' + encodeURIComponent(gaJobId.value));
        gaPollFails = 0; gaJobProgress.value = data.progress || '';
        const st = data.state;
        if (st === 'pending' || st === 'running') { gaPollTimer = setTimeout(pollGaJob, GA_POLL_MS); return; }
        stopGaPolling(false); clearGaJobRecord();
        if (st === 'done') { handleGaJobDone(data); return; }
        if (st === 'unknown') { gaUnknownShow.value = true; return; }
        refreshGroupDetail();
        vant.showToast(data.error || '这次没跑成功，稍后再试一次就好');
      } catch(e) {
        gaPollFails += 1;
        if (gaPollFails >= 10) {  // 连续 10 次失败才放弃（可能只是短暂断网）
          stopGaPolling(false); refreshGroupDetail();
          vant.showToast('连不上，稍后回来看看就好');
          return;
        }
        gaPollTimer = setTimeout(pollGaJob, GA_POLL_MS);
      }
    }
    function handleGaJobDone(data) {
      if (gaJobKind.value === 'onetoone') { handleOtoJobDone(data); return; }
      const res = data.result || {};
      refreshGroupDetail();
      if (res.code === 'OK') {
        gaGroups.value = res.groups || []; gaGroupsRound.value = res.round || gaJobRound.value; gaResultRound.value = gaGroupsRound.value;
        gaResultError.value = false;
        vant.showToast('分好啦，共 ' + (res.group_count || gaGroups.value.length) + ' 组 🎉');
        if (page.value === 'group_detail') gaDetailTab.value = 2;
        return;
      }
      if (res.code === 'ALREADY_DONE') {
        const rd = res.round || gaJobRound.value;
        vant.showConfirmDialog({ title: '第 ' + rd + ' 轮已经分过组啦', message: '同轮次不会重复执行。要重跑的话，请先清空该活动的历史分组结果，再执行。', confirmButtonText: '清空历史结果', cancelButtonText: '先不用', confirmButtonColor: '#ff6b81' })
          .then(() => { openGaResetConfirm(); }).catch(() => {});
        return;
      }
      if (res.code === 'EXCLUSIVE_MODE') { vant.showToast('一对一模式还开着，先切到分组模式再执行'); return; }
      if (res.code === 'NOT_ENOUGH_PEOPLE') {
        vant.showToast('人还不够：' + res.males + ' 男 ' + res.females + ' 女，每组要 ' + res.males_per_group + ' 男 ' + res.females_per_group + ' 女。再等等人吧');
        return;
      }
      if (res.code === 'SEARCH_FAILED') { vant.showToast('读取志愿时网络打了个喷嚏，没分成。稍后再试'); return; }
      if (res.code === 'ALGORITHM_ERROR') { vant.showToast('分组没成功：' + (res.error || '再试一次看看')); return; }
      if (res.code === 'EMPTY_GROUPS') { vant.showToast('没有可分组的人，先确认报名和志愿'); return; }
      if (res.code === 'PARTIAL_WRITE') { vant.showToast('结果只写了一半，已经回滚。稍后再试'); return; }
      vant.showToast('这次没分成功，稍后再试一次就好');
    }
    function handleOtoJobDone(data) {
      const res = data.result || {};
      refreshGroupDetail();
      if (res.code === 'OK') {
        const rd = res.round || gaJobRound.value;
        gaOtoPeople.value = []; gaOtoOpen.value = {};
        gaOtoResultRound.value = rd;
        gaOtoLoadedRound.value = 0;
        gaDetailTab.value = 2;
        loadGaOtoResult(rd);
        vant.showToast('第 ' + rd + ' 轮匹配完成，共 ' + (res.total_pairs || 0) + ' 条必聊 🎉');
        return;
      }
      if (res.code === 'EXCLUSIVE_MODE') { vant.showToast('分组模式还开着，先切到一对一模式再执行'); return; }
      if (res.code === 'ALREADY_DONE') {
        const rd = res.round || 1;
        vant.showConfirmDialog({ title: '第 ' + rd + ' 轮已经匹配过啦', message: '同轮次不会重复执行。要重跑的话，请先清空该活动的历史一对一结果，再执行。', confirmButtonText: '清空历史结果', cancelButtonText: '先不用', confirmButtonColor: '#ff6b81' })
          .then(() => { openOtoResetConfirm(); }).catch(() => {});
        return;
      }
      if (res.code === 'BUSY') { vant.showToast('另一场任务正在执行，稍后再试'); return; }
      if (res.code === 'SEARCH_FAILED') { vant.showToast('读取历史结果时网络打了个喷嚏，已中止（未写任何数据），稍后再试'); return; }
      if (res.code === 'CLOSE_FAILED') { vant.showToast('状态写入失败，已中止匹配，稍后再试'); return; }
      if (res.code === 'SELECTIONS_SEARCH_FAILED') { vant.showToast('读取志愿失败，已中止匹配，稍后再试'); return; }
      if (res.code === 'NO_PARTICIPANTS') { vant.showToast('没有已提交志愿的参与者，稍后再试'); return; }
      if (res.code === 'ALGORITHM_ERROR') { vant.showToast('匹配没成功：' + (res.error || '再试一次看看')); return; }
      if (res.code === 'PARTIAL_WRITE' || res.code === 'PARTIAL_WRITE_INCOMPLETE' || res.code === 'PARTIAL_WRITE_VERIFY_FAILED') {
        vant.showToast('结果只写了一半，可能已回滚/可能有残留，稍后再试'); return;
      }
      if (res.code === 'DONE_STATUS_FAILED') { vant.showToast('结果已生成，但状态写入失败，请重新载入检查'); return; }
      vant.showToast('这次没匹配成功，稍后再试一次就好');
    }
    async function restoreGaJob() {
      if (gaPollTimer || gaRunShow.value) return;
      // 两种 kind 的 key 都看一眼（同一活动同时只会有一个任务在跑，共用活动锁）；
      // 「旧格式无 kind 后缀」的键只属于分组 job，兜底读一次。
      const kinds = ['group', 'onetoone'];
      for (let i = 0; i < kinds.length; i++) {
        const kind = kinds[i];
        let jobId = '';
        try {
          jobId = localStorage.getItem(gaJobKey(kind)) || '';
          if (!jobId && kind === 'group') jobId = localStorage.getItem(GA_JOB_KEY + gaDetailId.value) || '';
        } catch(e) {}
        if (!jobId) continue;
        gaJobKind.value = kind;
        try {
          const ep = kind === 'onetoone' ? '/api/admin/onetoone/job' : '/api/admin/group/job';
          const data = await gaGet(ep + '?job_id=' + encodeURIComponent(jobId));
          const st = data.state;
          if (st === 'pending' || st === 'running') {
            gaJobId.value = jobId;
            gaJobRound.value = data.round || (kind === 'onetoone' ? gaOtoNextRound.value : gaNextRound.value);
            gaJobActivityName.value = (gaDetail.value && gaDetail.value.name) || gaDetailId.value;
            gaJobProgress.value = data.progress || '';
            gaJobStartedAt = (data.started_at || data.created_at || Math.floor(Date.now() / 1000)) * 1000;
            gaMaskSecs.value = Math.max(0, Math.floor((Date.now() - gaJobStartedAt) / 1000));
            openGaMask(); startGaPolling();
            return;
          } else if (st === 'done') {
            clearGaJobRecord();
            const res = data.result || {};
            if (kind === 'group' && res.code === 'OK') {
              gaGroups.value = res.groups || []; gaGroupsRound.value = res.round || 0;
              gaResultRound.value = gaGroupsRound.value || gaResultRound.value;
            } else if (kind === 'onetoone' && res.code === 'OK') {
              gaOtoResultRound.value = res.round || gaOtoResultRound.value;
              gaOtoLoadedRound.value = 0;
            }
          } else { clearGaJobRecord(); }
        } catch(e) { clearGaJobRecord(); }
      }
    }
    function gaUnknownReload() { gaUnknownShow.value = false; gaDetailTab.value = 0; refreshGroupDetail(); }
    function gaUnknownRetry() {
      gaUnknownShow.value = false;
      if (gaJobKind.value === 'onetoone') startOtoRunFlow();
      else startRunFlow();
    }

    watch(gaDetailTab, (v) => {
      // 聊天 tab（一对一索引 3）：进入即拉一次；切轮次由 selectChatOvRound 触发
      if (v === 3 && gaDetailMode.value === 'onetoone') {
        if (!chatOvLoading.value) loadChatOverview(chatOvRound.value, true);
        return;
      }
      if (v === 1 && (gaDetailMode.value === 'group' || gaDetailMode.value === 'onetoone')
          && !gaRosterList.value.length && !gaRosterLoading.value && gaDetailId.value) loadGroupRoster();
      if (v !== 2) return;
      // 结果 tab：分组/一对一都按当前轮次补拉
      if (gaDetailMode.value === 'onetoone') {
        if (!gaOtoResultRound.value) gaOtoResultRound.value = gaOtoLatestRound.value;
        if (gaOtoResultRound.value && gaOtoLoadedRound.value !== gaOtoResultRound.value
            && !gaOtoResultLoading.value) loadGaOtoResult(gaOtoResultRound.value);
      } else if (gaGroupsRound.value !== gaResultRound.value && !gaResultLoading.value) {
        loadGaGroupResult(gaResultRound.value);
      }
    });
    watch(gaRosterQuery, () => { gaRosterShown.value = 30; });

    // ---- 现场签到独立页（R3）：页头活动名 + 开关 + 名册 + 乐观标记（两态：未签到→按时 / 已签到→清空，D13）----
    const gaCheckinName = ref('');   // 页头活动名（列表页带入，仅展示）
    const gaSigninFlagOn = ref(false), gaSigninFlagBusy = ref(false);
    const gaSigninList = ref([]), gaSigninLoading = ref(false), gaSigninError = ref(false), gaSigninLoaded = ref(false);
    const gaSigninQuery = ref(''), gaSigninShown = ref(30);
    const gaSigninGender = ref('all'), gaSigninStatus = ref('all');   // 两级筛选：性别（默认全部）× 状态（默认全部）——进页一览全貌，筛选只是收敛
    const gaSigninBusy = ref({}), gaSigninBatchBusy = ref(false);
    const gaClearConfirmShow = ref(false), gaClearConfirmInput = ref('');
    const gaSigninMarkedCount = computed(() => gaSigninList.value.filter(r => !!r.state).length);
    const gaClearConfirmOk = computed(() => String(gaClearConfirmInput.value || '').trim().toLowerCase()
      === String(gaDetailId.value || '').trim().toLowerCase());
    const gaSigninFiltered = computed(() => {
      const q = gaSigninQuery.value.trim().toLowerCase();
      const gender = gaSigninGender.value, status = gaSigninStatus.value;
      return gaSigninList.value.filter(r => {
        if (gender === 'male' && r.gender !== '男性') return false;
        if (gender === 'female' && r.gender !== '女性') return false;
        if (status === 'pending' && r.checked_in) return false;   // 未签到＝其余（空/「未到」存量）
        if (status === 'in' && !r.checked_in) return false;
        if (!q) return true;
        return String(r.nickname || '').toLowerCase().includes(q)
          || String(r.real_name || '').toLowerCase().includes(q)
          || String(r.user_id || '').toLowerCase().includes(q);
      });
    });
    const gaSigninVisible = computed(() => gaSigninFiltered.value.slice(0, gaSigninShown.value));
    // 筛选 chips 计数（F-06 口径）：一级（性别）恒按全量名册计数，不随状态筛选变；
    // 二级（状态）随性别筛选变（见 gaSigninStatusCounts）；两者都不受搜索词影响
    const gaSigninGenderCounts = computed(() => {
      const c = { all: 0, male: 0, female: 0 };
      for (const r of gaSigninList.value) {
        c.all++;
        if (r.gender === '男性') c.male++;
        else if (r.gender === '女性') c.female++;
      }
      return c;
    });
    const gaSigninStatusCounts = computed(() => {
      const gender = gaSigninGender.value;
      const c = { all: 0, pending: 0, checked: 0 };
      for (const r of gaSigninList.value) {
        if (gender === 'male' && r.gender !== '男性') continue;
        if (gender === 'female' && r.gender !== '女性') continue;
        c.all++;
        if (r.checked_in) c.checked++;
        else c.pending++;
      }
      return c;
    });

    async function loadSigninRoster(force) {
      if (gaSigninLoading.value || !gaDetailId.value) return;
      if (!force && gaSigninLoaded.value) return;
      gaSigninLoading.value = true; gaSigninError.value = false;
      try {
        const data = await gaGet('/api/admin/activity/signin?activity_id=' + encodeURIComponent(gaDetailId.value));
        gaSigninFlagOn.value = !!data.signin_enabled;
        gaSigninList.value = (data.roster || []).map(r => ({ ...r }));
        gaSigninLoaded.value = true; gaSigninShown.value = 30;
      } catch(e) {
        if (!gaAuthBack(e)) { gaSigninError.value = true; vant.showToast(gaMsg(e, '没连上，检查一下网络再试一次')); }
      }
      gaSigninLoading.value = false;
    }
    async function markSignin(r, state) {
      if (gaSigninBusy.value[r.open_id]) return;
      if (gaSigninBatchBusy.value) { vant.showToast('正在提交，请稍候'); return; }   // 全部清除在途：行内动作禁用（P16）
      const prevState = r.state, prevChecked = r.checked_in;
      gaSigninBusy.value = { ...gaSigninBusy.value, [r.open_id]: true };
      r.state = state; r.checked_in = isAttendancePresent(state);   // 乐观更新
      try {
        await api('/api/admin/activity/signin', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value, open_id: r.open_id, state: state }) });
      } catch(e) {
        r.state = prevState; r.checked_in = prevChecked;   // 失败回滚
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '标记失败，再试一次'));
      }
      const busy = { ...gaSigninBusy.value };
      delete busy[r.open_id];
      gaSigninBusy.value = busy;
    }
    async function toggleSignin(r) {
      if (gaSigninBusy.value[r.open_id]) return;
      if (gaSigninBatchBusy.value) { vant.showToast('正在提交，请稍候'); return; }   // 全部清除在途：行内动作禁用（P16）
      const name = r.real_name || r.nickname || '（没有昵称）';
      const wasChecked = !!r.checked_in;
      // 两态（D13 收口）：未签到 → 确认后写「按时」；已签到 → 确认后清空回未签到。
      // 弹窗只问「要不要」，确认才进 markSignin 的乐观更新；取消不动。
      try {
        if (wasChecked) {
          await vant.showConfirmDialog({ title: '取消签到',
            message: '取消「' + name + '」的签到？取消后将回到「未签到」。',
            confirmButtonText: '确认取消', cancelButtonText: '再想想', confirmButtonColor: '#ee0a24' });
        } else {
          await vant.showConfirmDialog({ title: '确认签到',
            message: '将「' + name + '」标记为已签到？',
            confirmButtonText: '确认签到', cancelButtonText: '再想想', confirmButtonColor: '#0a8548' });
        }
      } catch(e) { return; }
      await markSignin(r, wasChecked ? '' : '按时');
    }
    async function onToggleSigninFlag(val) {
      if (gaSigninFlagBusy.value || !gaDetailId.value) return;
      if (gaSigninBatchBusy.value) { vant.showToast('正在提交，请稍候'); return; }   // 全部清除在途：开关不动（P16）
      // 误触防护：关闭是「闸门静默失效」方向（未签到者可填志愿），关前必经确认；
      // 开启方向是正常现场操作且误开后后果可逆，不拦。取消时开关受控于 gaSigninFlagOn，原地不动。
      if (!val) {
        try {
          await vant.showConfirmDialog({ title: '关闭现场签到？',
            message: '关闭后，未签到的人也能填志愿、参与分组 / 一对一。确认关闭吗？',
            confirmButtonText: '确认关闭', cancelButtonText: '再想想', confirmButtonColor: '#ee0a24' });
        } catch(e) { return; }
      }
      gaSigninFlagBusy.value = true;
      try {
        await api('/api/admin/activity/signin/flag', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value, enabled: !!val }) });
        gaSigninFlagOn.value = !!val;
        vant.showToast(val ? '已开启现场签到' : '已关闭现场签到');
        const it = gaList.value.find(x => x.activity_id === gaDetailId.value);
        if (it) it.signin_flag = val ? '是' : '否';
        if (gaDetail.value) gaDetail.value.signin_flag = val ? '是' : '否';
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '开关失败，再试一次'));
      }
      gaSigninFlagBusy.value = false;
    }
    function openClearAllSignins() {
      if (gaSigninBatchBusy.value) return;
      if (!gaSigninMarkedCount.value) { vant.showToast('没有需要清除的签到状态'); return; }
      gaClearConfirmInput.value = '';
      gaClearConfirmShow.value = true;
    }
    function cancelClearAllSignins() { if (gaSigninBatchBusy.value) return; gaClearConfirmShow.value = false; }
    async function doClearAllSignins() {
      if (!gaClearConfirmOk.value || gaSigninBatchBusy.value) return;   // 输入的活动编号不符时按钮本就 disabled，这里是双保险
      gaSigninBatchBusy.value = true;
      try {
        const data = await api('/api/admin/activity/signin', { method: 'POST',
          body: JSON.stringify({ activity_id: gaDetailId.value, clear_all: true }) });
        gaClearConfirmShow.value = false;
        gaSigninLoaded.value = false;   // 服务端才是准数（可能部分失败）：重新拉名册与统计
        await loadSigninRoster(true);
        const cleared = data.cleared || 0, failed = data.failed || 0;
        if (failed) vant.showToast('已清除 ' + cleared + ' 人，' + failed + ' 人失败，请重试');
        else if (cleared) vant.showToast('已清除 ' + cleared + ' 人的签到状态');
        else vant.showToast('没有需要清除的签到状态');
      } catch(e) {
        if (!gaAuthBack(e)) vant.showToast(gaMsg(e, '清除失败，再试一次'));
      }
      gaSigninBatchBusy.value = false;
    }

    function openAdminConsole() {
      // ?embed=1 供 iframe 首载放行；iframe 内部导航无 query 时靠 Sec-Fetch-Dest: iframe。
      openEmbed('/admin?embed=1', '管理后台');
    }

    async function legacyLogout() {
      stopNotificationSync();
      try {
        const csrf = (document.cookie.match(/(?:^|; )yxq_csrf=([^;]*)/) || [])[1] || '';
        await fetch('/legacy/api/logout', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf, 'X-Requested-With': 'XMLHttpRequest' } });
      } catch (e) {}
      window.location.href = '/login?logged_out=1';
    }

    return {
      page, pageTransition, user, loggingIn, loginError, showPersonalGuide, loginTip,
      onPageEnter,
      updateReady, applyUpdate, dismissUpdatePrompt, sessionExpiredOpen,
      bootLoading, bootError, retryBoot,
      showClaim, claimName, claimPhone, claiming, submitClaim, backToLogin, requestBackToLogin,
      isDark, toggleTheme,
      passwordForm, passwordErrors, passwordSubmitError, passwordSubmitting, openPasswordPage, submitPassword,
      loginErrorMessage, retryLogin, subPages, isObserver, availableRoles, switchRole,
      pageBusy, pageBusyText,
      gateMessage, gateTitle, gateIcon, retryGate, syncMe,
      accountStatusLabel, toggleAccountStatus, legacyLogout, openAdminConsole,
      activities, loadingActivities, currentActivity, actionLoading, activityDetailFrom,
      activityListTime, activityRangeText,
      isAttendancePresent, isActivityEnded, canSelfSignin, showSelfSigninFor, signinBeforeFillBlocked,
      isDetailCheckedIn, detailSigninBlocked, activityDetailLoading,
      openActivity, doSignup, cancelSignup, onSignupClick, confirmPay, showPayDialog,
      cards, cardsTotal, cardsStart, loadingCards, currentCard, cardIndex, photoIndex, photoSlideDir, currentPhotoUrl, currentPhotoList, photoSrc, photoLoading,
      showSwipeGuide, closeSwipeGuide,
      showActivityPromo, gotoPromoActivity, promoPoster, showUpdateNotes,
      filter, educationOptions, incomeOptions, showFilter, hasActiveFilter, toggleMultiTag, resetFilter, applyFilter, requestCloseFilter,
      showIdInvalidDialog, idInvalidCountdown, goIdInvalidNow, guardTabChange,
      showFaceInvalidDialog, faceInvalidCountdown, goFaceInvalidNow,
      slideDir,
      onTouchStart, onTouchMove, onTouchEnd,
      onCardTouchStart, onCardTouchMove, onCardTouchEnd,
      onCardMouseDown, onCardMouseMove, onCardMouseUp, onCardMouseLeave,
      onPhotoTouchStart, onPhotoTouchMove, onPhotoTouchEnd, onPhotoClick,
      onPhotoMouseDown, onPhotoMouseMove, onPhotoMouseUp, onPhotoMouseLeave,
      canPraiseCard, cardPraised, canPraiseDetail, praiseLeft, praiseBusy, togglePraise,
      praiseReady, praiseReceivedWeek,
      openLikeDialog, openLikeDialogForUser, onLikeDialogBeforeClose, doLike, likeMessage, likeSubmitting,
      showLikeDialog, likeDialogTitle, likeTarget, likeType,
      showFeedbackDialog, feedbackReason, feedbackText, feedbackTarget,
      openFeedbackDialog, onFeedbackBeforeClose, doFeedback, feedbackSubmitting,
      showReportDialog, reportReason, reportReasonText, reportTarget, openReportDialog, onReportBeforeClose, doReport, reportSubmitting,
      reportedMessages, loadingReported, reportedCount, openReportedMessages, loadReportedMessages, handleReported,
      showSuggestionDialog, suggestionType, suggestionText, openSuggestionDialog, onSuggestionBeforeClose, suggestionSubmitting,
      likesData, loadingLikes, loadMyLikesCount, notifications, unreadCount, unreadBadge, markNotificationsRead,
      embedOpen, embedUrl, embedTitle, embedLoading, embedReady, embedFrame, openEmbed, closeEmbed, onEmbedLoad,
      embedFailed, retryEmbed, returnAdminHome,
      nativeAdmin, gaBack,
      myLikesList, loadingMyLikes, myLikesCount, openMyLikes, openMutual, openLikedMe, submitMutualWechat,
      myActivitiesList, loadingMyActivities, openMyActivities,
      myGroupsList, loadingMyGroups, openMyGroups, hasGroupEntry,
      onetooneResult, onetooneResultLoading,
      onetooneResultFrom, onetooneMsgTarget, onetooneMsgText, showOnetooneMsg,
      goOnetooneResult, switchOnetooneRound,
      openOnetooneMsg, onOnetooneMsgBeforeClose, onetooneMsgSubmitting,
      profileData, openProfileDetail, loadingProfileDetail,
      editableFields, showSelectPopup, showMultiPopup, pickerField, savingProfile,
      openProfileEdit, openPicker, openMultiPicker, pickSelect, clearPickerField, toggleMulti, saveProfile,
      showPhonePasswordDialog, phonePassword, phonePasswordError, phonePasswordBusy, confirmPhonePassword, cancelPhonePassword,
      showUnsavedConfirm, unsavedConfirmBody, discardLeaveConfirm, cancelLeaveConfirm,
      photoInput, uploadingPhoto, uploadProgress, uploadStage, faceChecking, pickPhoto, onPhotoChange, profilePhotos, profilePhotosThumb, deletePhoto, setCover,
      candidates, selectedChoices, candidateQuery, visibleCandidates, loadingCandidates, candidatesError, gsSlowLoading, gsVerySlowLoading, groupResult, groupResultLoading, myGender,
      groupSelectFrom, groupResultFrom,
      showGsTray, gsSigninBlocked, gsPrefillNotice, gsUnlistedCount, gsCountText, gsDisplayName, gsReorderBtnEl,
      gsCanSubmit,
      goGroupSelect, reloadGroupCandidates, dismissGsNotice, isSelected, gsRowTouchStart, gsRowTouchEnd, gsRowClick,
      toggleChoice, removeChoiceToast, moveChoice, submitChoices, goGroupResult, switchGroupRound,
      onPhotoError, onPhotoLoad, retryImg, photoPositions, recalcPhotoPosition,
      cardTab, messages, msgLoading, msgSending, msgText, replyTarget, threadedMessages, currentMsgCount,
      myMessagesList, loadingMyMessages, openMyMessages, deleteMyMessage, refreshMyMessagesCount,
      receivedMessagesList, loadingReceivedMessages, openReceivedMessages, refreshReceivedCount,
      openMessageTab, openMessageAuthor, sendMessage, deleteMessage, replyTo, cancelReply, fmtTimeAgo,
      showPhotoViewer, viewerLoading, cancelViewerLoading, viewPoster,
      handleBack, showInviteDialog, inviteShareText, copyInviteFull, copyInviteCode,
      pointsBalance, pointsEarned, pointsSpent, pointsPending, pointsPendingCount, pointsEntries,
      loadingPoints, pointsLoaded, loadPoints, openPoints, loadPointsBalance,
      pointsRules, pointsRulesLoaded, loadPointsRules, showPointsRules, openPointsRules,
      showRedeemPanel, redeemItem, redeemActivityId, redeemTarget, redeemCondition, redeemSubmitting,
      redeemNeedsActivity, redeemActivityOptions, redeemPanelItems, matchmakerConditionMax, openRedeemPanel, closeRedeemPanel, requestCloseRedeemPanel,
      pickRedeemItem, submitRedeem, newRequestKey,
      wishQuery, wishTargets, loadingWishTargets, loadWishTargets, pickWishTarget, clearWishTarget,
      showRedemptions, redemptionsList, loadingRedemptions, redemptionsLoaded, openRedemptions,
      inviteInfo, loadingInvite, inviteLoaded, loadInviteInfo,
      inviteConfirmDays, inviteRewardFemale, inviteRewardMale,
      showAdminHelp, adminCommands,
      openPhotoViewerForCurrent, openPhotoViewerForUser,
      gotoCards, signupSheetShow, signupList, loadingSignups, signupCache, openSignupSheet, showSignupInfo,
      userDetail, userDetailFrom, userDetailFromSheet, userDetailHideActions, canLikeUserDetail, openUserDetail, openUserDetailFromGs, openUserDetailFromPage, loadNotifications,
      hasProfileBasic, hasProfileWork, hasProfileAbout, hasProfilePartner, hasProfileContact,
      paLoading, paLedgerLoading, paStats, paConfig, paUsers, paLedger, paRedemptions, paInvites, paTab,
      paUserQuery, paLedgerFilter, paLedgerFilterNick, paFilteredUsers,
      paUserDetail, paUserDetailShow, paUserDetailLoading, paUserDetailOid,
      openPointsAdmin, paOpenUser, paShowLedger, clearPaFilter,
      gaAdminBadge, openGroupAdmin, loadGroupAdminList, gaLoading, gaError, gaList, gaQ, gaFilteredList, gaStats, gaChipClass, gaModeChip, gaModeChipClass, gaStatusChipText, gaStatusChipClass, gaRowSub, openGroupDetail, openCheckinPage,
      gaDetailId, gaDetail, gaDetailDbId, openEditActivity, openBroadcast, gaDetailLoading, gaDetailError, gaDetailTab, gaDetailMode, gaModeText, gaModeSwitching, switchMode, gaStepDefs, gaStepIndex, gaExpectedGroups, gaNextRound,
      gaSigninCode, gaSigninCodeBusy, gaSigninCodeOk, saveGaSigninCode,
      gaActivityStatusOptions, gaActivityStatus, gaActivityStatusBusy, gaActivityStatusTarget, gaActivityStatusChipClass, confirmActivityStatus,
      signinDialogShow, signinCode, signinCodeOk, signinSubmitting, openSelfSignin, closeSigninDialog, submitSelfSignin,
      gaCheckinName, gaSigninFlagOn, gaSigninFlagBusy, gaSigninList, gaSigninLoading, gaSigninError, gaSigninLoaded,
      gaSigninQuery, gaSigninGender, gaSigninStatus, gaSigninShown, gaSigninBusy, gaSigninBatchBusy, gaSigninFiltered, gaSigninVisible,
      gaSigninGenderCounts, gaSigninStatusCounts,
      gaSigninMarkedCount, loadSigninRoster, markSignin, toggleSignin, onToggleSigninFlag,
      gaClearConfirmShow, gaClearConfirmInput, gaClearConfirmOk, openClearAllSignins, cancelClearAllSignins, doClearAllSignins,
      gaStatusDisplay, refreshGroupDetail,
      gaRosterTab, gaRosterQuery, gaRosterList, gaRosterTotal, gaRosterLoading, gaRosterError, gaRosterShown, gaRosterFiltered, gaRosterVisible, gaRosterSubmitted, gaRosterSubmittedList, gaRosterSigninOn, gaRosterOnSite, gaSubmittedOnSite, gaAttText, gaAttClass, gaSubmittedFiltered, gaSubmittedVisible, loadGroupRoster, openGroupRoster, gaOpenUser,
      gaRunPopupShow, gaRunM, gaRunF, gaRunPopupPreview, gaRunExpected, gaRunPopupHasPeople, gaRunPopupExpectedGroups, closeGaRunPopup, requestCloseGaRunPopup, confirmGaRun, openGaResetConfirm, doGaReset, gaConfirmBusy,
      gaConfirmShow, gaConfirmInput, gaConfirmOk, cancelGaConfirm,
      gaOtoResetShow, gaOtoResetOk, gaOtoResetBusy, gaOtoConfirmInput, openOtoResetConfirm, cancelOtoReset, doOtoReset,
      gaOtoRunShow, gaOtoRunFemaleChats, gaOtoRunPreview, gaOtoRunK, gaOtoRunSummary, confirmOtoRun, closeOtoRun, requestCloseOtoRun,
      gaOtoRunBusy, startOtoRunFlow, gaOtoPeople, gaOtoOpen, gaOtoResultLoading, gaOtoResultError, loadGaOtoResult, selectGaOtoResultRound, gaOtoToggle,
      gaOtoRounds, gaOtoLatestRound, gaOtoNextRound, gaOtoResultRound, gaOtoLoadedRound, gaOtoResultCount,
      chatOvData, chatOvLoading, chatOvError, chatOvOpen, chatOvRound, chatOvSummary, chatOvRatePct,
      loadChatOverview, selectChatOvRound, toggleChatOvPerson, chatOvUnchattedText,
      gaRunShow, gaRunBusy, gaJobId, gaJobRound, gaJobActivityName, gaJobProgress, gaJobKind, gaMaskTitle, gaMaskSecs, gaRunTip, gaUnknownShow, gaUnknownReload, gaUnknownRetry, gaGroups, gaGroupsRound, gaResultRound, gaResultLoading, gaResultError, selectGaResultRound, startRunFlow, resumeGaPolling, refreshAdminBadge
    };
  }
});
if (typeof window.__YXQ_RENDER__ !== 'function') {
  yxqBootFailure('渲染器未加载（app.render.js 缺失或损坏）');
} else {
  // 先让壳内 hero（首卡静态图）绘制出来，再加载/执行 Vant 与挂载：
  // Vant 的 UMD 执行在 CPU 慢机上约 1s+，若与 mount 连在同一任务里，会把首图绘制
  // 推迟到长任务结束（批次4 的热启 LCP 就是这样被推到 4.6s）。双 rAF + setTimeout
  // 兜底保证页面至少提交一帧（图片命中缓存时即完成首图绘制），同时把 vendor 执行
  // 与 mount 拆成不同任务，缩短最长任务。
  var yxqMountStarted = false;
  var yxqVendorStarted = false;
  function yxqMountApp(){
    if (yxqMountStarted) return;
    yxqMountStarted = true;
    try {
      if (window.performance && performance.mark) performance.mark('yxq-mount-start');
      yxqApp.component('page-host', PageHost);
      yxqApp.component('ga-loading', GaLoading);
      yxqApp.use(vant).mount('#app');
      if (window.performance && performance.mark) {
        performance.mark('yxq-mount-end');
        performance.measure('yxq-mount', 'yxq-mount-start', 'yxq-mount-end');
      }
    } catch (err) {
      // 不 rethrow：错误页已经把原因显示出来，抛出去只会多一条 pageerror。
      yxqBootFailure(err && err.message ? err.message : String(err));
    }
  }
  function yxqStartVendor(){
    if (yxqVendorStarted) return;
    yxqVendorStarted = true;
    yxqLoadVant(function(){
      if (document.visibilityState === 'hidden') { yxqMountApp(); return; }
      requestAnimationFrame(function(){ requestAnimationFrame(yxqMountApp); });
      setTimeout(yxqMountApp, 400);   // rAF 被节流/后台标签页兜底；yxqMountApp 幂等
    });
  }
  function yxqStart(){
    if (document.visibilityState === 'hidden') { yxqStartVendor(); return; }
    requestAnimationFrame(function(){ requestAnimationFrame(yxqStartVendor); });
    setTimeout(yxqStartVendor, 1000);
  }
  yxqStart();
}
}


