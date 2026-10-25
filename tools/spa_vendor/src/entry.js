// Vant subset bundle for the legacy SPA (dev-time build; output committed).
// Only the components actually used by web/static/legacy/index.html + app.js.
// Keep this list in sync with tools/spa_vendor/build.mjs and the Go coverage test.
import Button from 'vant/es/button/index.mjs'
import Icon from 'vant/es/icon/index.mjs'
import Empty from 'vant/es/empty/index.mjs'
import Loading from 'vant/es/loading/index.mjs'
import Field from 'vant/es/field/index.mjs'
import Tag from 'vant/es/tag/index.mjs'
import Radio from 'vant/es/radio/index.mjs'
import RadioGroup from 'vant/es/radio-group/index.mjs'
import Popup from 'vant/es/popup/index.mjs'
import DialogComponent from 'vant/es/dialog/index.mjs'
import Tab from 'vant/es/tab/index.mjs'
import Tabs from 'vant/es/tabs/index.mjs'
import Cell from 'vant/es/cell/index.mjs'
import CellGroup from 'vant/es/cell-group/index.mjs'
import Tabbar from 'vant/es/tabbar/index.mjs'
import TabbarItem from 'vant/es/tabbar-item/index.mjs'
import Switch from 'vant/es/switch/index.mjs'
import Toast from 'vant/es/toast/index.mjs'
import { showToast } from 'vant/es/toast/index.mjs'
import { showConfirmDialog, showDialog } from 'vant/es/dialog/index.mjs'

import 'vant/es/button/style/index.mjs'
import 'vant/es/icon/style/index.mjs'
import 'vant/es/empty/style/index.mjs'
import 'vant/es/loading/style/index.mjs'
import 'vant/es/field/style/index.mjs'
import 'vant/es/tag/style/index.mjs'
import 'vant/es/radio/style/index.mjs'
import 'vant/es/radio-group/style/index.mjs'
import 'vant/es/popup/style/index.mjs'
import 'vant/es/dialog/style/index.mjs'
import 'vant/es/tab/style/index.mjs'
import 'vant/es/tabs/style/index.mjs'
import 'vant/es/cell/style/index.mjs'
import 'vant/es/cell-group/style/index.mjs'
import 'vant/es/tabbar/style/index.mjs'
import 'vant/es/tabbar-item/style/index.mjs'
import 'vant/es/switch/style/index.mjs'
import 'vant/es/toast/style/index.mjs'

const components = {
  Button, Icon, Empty, Loading, Field, Tag, Radio, RadioGroup, Popup,
  Dialog: DialogComponent, Tab, Tabs, Cell, CellGroup, Tabbar, TabbarItem, Switch,
}

const names = [
  'van-button', 'van-cell', 'van-cell-group', 'van-dialog', 'van-empty',
  'van-field', 'van-icon', 'van-loading', 'van-popup', 'van-radio',
  'van-radio-group', 'van-switch', 'van-tab', 'van-tabbar',
  'van-tabbar-item', 'van-tabs', 'van-tag',
]

function install(app) {
  Object.keys(components).forEach((name) => app.use(components[name]))
  app.use(Toast)
  app.use(DialogComponent)
}

export {
  install,
  showToast,
  showConfirmDialog,
  showDialog,
  components,
  names,
}
