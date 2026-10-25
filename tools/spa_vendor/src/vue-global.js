// Vant's ESM build imports the Vue runtime by bare specifier. The SPA loads
// Vue as `window.Vue` first (preload + runtime-only build), so the subset
// bundle must use that single runtime copy — otherwise Vant would bundle a
// second Vue and getCurrentInstance() would return null inside components.
const V = window.Vue || {};
export const {
  Comment, Fragment, Teleport, Text, Transition,
  computed, createApp, createTextVNode, createVNode, defineComponent,
  getCurrentInstance, h, inject, isRef, isVNode, mergeProps, nextTick,
  onActivated, onBeforeUnmount, onBeforeUpdate, onDeactivated, onMounted,
  onUnmounted, onUpdated, provide, reactive, ref, resolveDirective, unref,
  vShow, watch, watchEffect, withDirectives, withKeys,
} = V;
export default V;
