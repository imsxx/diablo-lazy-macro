// ================= Diablo Lazy Macro 前端交互与 API 驱动 =================
// 设计约定：
//  1) 真值源在 Go：界面只渲染 Go 下发的状态，任何写操作成功后重新拉取状态（不做本地乐观更新）。
//  2) 失败必须可见：所有接口调用统一走 callApi()，失败弹提示条并保持界面为「改动前的值」。
//  3) 键位合法性由 Go 裁决（keyitems.go）：这里只把捕获到的原始键名 / 鼠标键 token 发过去。

let state = null;
let currentTab = 0;
let capture = null; // { mode, target, options }
let confirmResolve = null; // 二级确认浮层的等待者；非空表示浮层正开着
let readySent = false;
let profilesDigest = '';
let noticeTimer = null;

const STRATEGY_NAMES = ['禁用', '按住不放', '连点', '保持Buff'];
// MAX_PROFILES 与协议上限（internal/config 的 1–4 个方案）一致：前端只用它决定「+」是否可点。
const MAX_PROFILES = 4;

// ---------------------------------------------------------------- 基础设施

function onReady(fn) {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', fn);
    return;
  }
  fn();
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function $id(id) {
  return document.getElementById(id);
}

function esc(text) {
  return String(text == null ? '' : text)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function errorText(err) {
  if (!err) return '未知错误';
  if (typeof err === 'string') return err;
  if (err.message) return err.message;
  return String(err);
}

// showNotice 在顶栏下方显示一条提示；error 会停留更久，成功提示 3 秒后自动收起。
function showNotice(message, kind) {
  const bar = $id('noticeBar');
  if (!bar) return;
  bar.textContent = message;
  bar.className = 'notice-bar ' + (kind === 'ok' ? 'ok' : 'error');
  bar.style.display = 'block';
  if (noticeTimer) clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => {
    bar.style.display = 'none';
  }, kind === 'ok' ? 3000 : 7000);
}

function showError(message) {
  showNotice(message, 'error');
}

// callApi 是唯一的前端→后端出口：接口缺失或后端拒绝都变成可见提示，绝不静默。
async function callApi(name, ...args) {
  const fn = window[name];
  if (typeof fn !== 'function') {
    showError('界面接口 ' + name + ' 不可用：后端未就绪，请重启程序');
    return { ok: false };
  }
  try {
    const value = await fn(...args);
    return { ok: true, value };
  } catch (err) {
    showError(errorText(err));
    return { ok: false };
  }
}

// flashAutoSave 让「改动即时生效」闪一下，给保存成功一个可见反馈。
function flashAutoSave() {
  const el = $id('autoSaveIndicator');
  if (!el) return;
  el.classList.add('flash');
  setTimeout(() => el.classList.remove('flash'), 700);
}

// ---------------------------------------------------------------- 启动

onReady(() => {
  bindHeaderDrag();
  window.addEventListener('keydown', handleKeyDown, true);
  bindSlotsDelegation();
  bindProfileTabs();
  bindHelperChecklist();
  bindOverlayDismiss();
  bootstrap();
});

async function bootstrap() {
  // WebView2 注入接口是异步的：轮询等待，但不再用假数据糊弄（宁可显示错误也不显示错的开关）。
  for (let i = 0; i < 60; i++) {
    if (typeof window.api_getState === 'function') {
      const r = await callApi('api_getState');
      if (r.ok && r.value) {
        applyState(r.value);
        renderAll();
        notifyReady();
        return;
      }
    }
    await sleep(25);
  }
  showError('后端接口未就绪：界面处于只读状态，请重启程序或查看运行日志');
  renderAll();
  notifyReady();
}

// notifyReady 只发一次：告诉宿主窗口「首帧深色界面已就绪，可以显示了」。
function notifyReady() {
  if (readySent) return;
  readySent = true;
  if (typeof window.api_ready === 'function') {
    window.api_ready();
  }
}

async function reloadState() {
  const r = await callApi('api_getState');
  if (!r.ok || !r.value) return false;
  applyState(r.value);
  return true;
}

// applyState 接管一份新状态：按「变了什么」决定重绘范围，
// 既保证托盘 / 热键触发的变动能反映到界面，又不会在无关推送时打断正在编辑的输入框。
function applyState(next) {
  const prev = state;
  state = next;
  // 方案数量是动态的（默认 1 个，可加到 4 个）：当前标签必须夹在有效范围内。
  const names = next.profileNames || [];
  currentTab = Math.min(Math.max(next.currentProfileIdx || 0, 0), Math.max(names.length - 1, 0));

  const gameChanged = !prev || prev.currentGame !== next.currentGame;
  const profileChanged = !prev || prev.currentProfileIdx !== next.currentProfileIdx;
  const digest = stateDigest(next);
  const dataChanged = digest !== profilesDigest;
  profilesDigest = digest;

  if (gameChanged) {
    renderGameSelector();
    renderHelperSection();
  }
  if (gameChanged || profileChanged || dataChanged) {
    renderTabs();
    renderSlots();
    renderExtraSettings();
  }
  renderHeaderKeys();
  renderStatus();
  renderPreferences();
}

// window.onStateUpdate 是 Go 主动推送状态的入口：托盘菜单、游戏内方案切换键、悬浮窗右键菜单
// 引起的变更都从这里进界面。它不是可选的优化——缺了它，凡是界面自己没发起的变更都不会显示。
window.onStateUpdate = function (next) {
  if (!next) return;
  applyState(next);
};

function renderAll() {
  profilesDigest = stateDigest(state);
  renderGameSelector();
  renderTabs();
  renderSlots();
  renderExtraSettings();
  renderHelperSection();
  renderPreferences();
  renderHeaderKeys();
  renderStatus();
}

// stateDigest 用来判断「槽位/方案名是否变了」：变了才整体重绘，避免无关推送打断输入框。
function stateDigest(snapshot) {
  if (!snapshot) return '';
  return JSON.stringify(snapshot.profiles) + '|' + JSON.stringify(snapshot.profileNames);
}

function currentProfile() {
  if (!state || !state.profiles || !state.profiles[currentTab]) return null;
  return state.profiles[currentTab];
}

function slotAt(idx) {
  const prof = currentProfile();
  if (!prof || !prof.slots || !prof.slots[idx]) return null;
  return prof.slots[idx];
}

// ---------------------------------------------------------------- 渲染

function renderGameSelector() {
  if (!state) return;
  document.querySelectorAll('.game-btn').forEach((btn) => {
    btn.classList.toggle('active', btn.dataset.game === state.currentGame);
  });
}

function renderHeaderKeys() {
  if (!state) return;
  const toggle = $id('toggleKeyBtn');
  if (toggle) {
    toggle.textContent = state.toggleKeyLabel || 'X1';
    toggle.title = '当前启停键：' + (state.toggleKeyLabel || 'X1') + '（点击修改）';
  }
  const versionEl = $id('appVersion');
  if (versionEl && state.version) {
    versionEl.textContent = 'v' + state.version;
  }
}

// renderTabs 按后端下发的方案数量渲染标签，标签文字永远取玩家自己起的配置名；
// 末尾固定一个「+」用来新增配置（达到协议上限后置灰）。签名没变就不重建 DOM。
function renderTabs() {
  if (!state) return;
  const container = $id('profileTabs');
  if (!container) return;
  const names = state.profileNames || [];
  const canAdd = names.length < MAX_PROFILES;
  const sig = JSON.stringify([names, currentTab, canAdd]);
  if (container.dataset.sig === sig) return;
  container.dataset.sig = sig;

  const parts = names.map((name, idx) => {
    const label = name || '配置 ' + (idx + 1);
    return '<button class="tab-btn' + (idx === currentTab ? ' active' : '') + '" data-tab="' + idx +
      '" title="切换到「' + esc(label) + '」">' + esc(label) + '</button>';
  });
  const addTitle = canAdd ? '新增一个配置（最多 ' + MAX_PROFILES + ' 个）' : '最多 ' + MAX_PROFILES + ' 个配置';
  parts.push('<button class="tab-btn tab-add" data-action="add"' +
    (canAdd ? '' : ' disabled') + ' title="' + addTitle + '">+</button>');
  container.innerHTML = parts.join('');
}

function renderSlots() {
  const container = $id('slotList');
  if (!container) return;
  container.innerHTML = '';
  const prof = currentProfile();
  if (!prof || !prof.slots) return;

  prof.slots.forEach((slot, idx) => {
    const row = document.createElement('div');
    row.className = 'slot-row';
    // 整行悬浮说明：把「这个策略到底做什么」直接写在玩家眼前（旧版是分散的 ToolTip，新版集中到 Go 下发文案）
    row.title = slot.explanation || '';

    // 与当前策略无关的列不再禁用，只做淡化：玩家可以先填数值再选策略，
    // 灰到点不动会让人以为界面坏了。title 里说清「填了何时生效」。
    const useInterval = slot.strategy === 2 || slot.strategy === 3;
    const useDelay = slot.strategy === 2;
    const strategyName = STRATEGY_NAMES[slot.strategy] || '禁用';
    const intervalTitle = useInterval
      ? '执行间隔（毫秒）：连点 / 保持Buff 的触发周期，最小 1ms'
      : '执行间隔（毫秒）：当前策略是「' + strategyName + '」，改成「连点」或「保持Buff」后生效（可以先填）';
    const delayTitle = '延迟区间（毫秒）：两个框分别是最小 / 最大延迟，只填一个时另一个自动补成相同值。';
    const delayHint = useDelay ? '仅「连点」生效' : '当前策略是「' + strategyName + '」，改成「连点」后生效（可以先填）';

    row.innerHTML = `
      <span class="col-slot">${esc(slot.name || '')}</span>
      <span class="col-key">
        <button class="keycap-btn sm" data-slot="${idx}" data-field="key"
          title="当前：${esc(slot.keyLabel || '未选择')} · 点击后按任意键录入（Esc 取消）">${esc(slot.keyLabel || '—')}</button>
      </span>
      <span class="col-strategy">
        <select class="select-dropdown md" data-slot="${idx}" data-field="strategy"
          title="禁用=不自动触发；按住不放=持续按住；连点=按间隔重复；保持Buff=屏幕找色守卫（暗黑3）">
          ${STRATEGY_NAMES.map((name, sIdx) => `<option value="${sIdx}" ${slot.strategy === sIdx ? 'selected' : ''}>${name}</option>`).join('')}
        </select>
      </span>
      <span class="col-interval">
        <input type="number" class="input-num sm${useInterval ? '' : ' inactive'}" data-slot="${idx}" data-field="interval"
          value="${slot.intervalMS}" min="1" max="600000" title="${esc(intervalTitle)}">
      </span>
      <span class="col-delay">
        <input type="number" class="input-num xs${useDelay ? '' : ' inactive'}" data-slot="${idx}" data-field="delayMin"
          value="${slot.delayMinMS || ''}" placeholder="最小" min="0" max="30000"
          title="${esc(delayTitle + delayHint)}">
        <span class="delay-sep">～</span>
        <input type="number" class="input-num xs${useDelay ? '' : ' inactive'}" data-slot="${idx}" data-field="delayMax"
          value="${slot.delayMaxMS || ''}" placeholder="最大" min="-30000" max="30000"
          title="${esc(delayTitle + delayHint)}">
      </span>
    `;
    container.appendChild(row);
  });
}

function renderExtraSettings() {
  const prof = currentProfile();
  if (!prof) return;

  // 键帽是纯展示按钮（不是输入控件）：必须直接写文本，不能像输入框那样跳过「有焦点」的元素，
  // 否则玩家点过它之后，改完键位这里会一直显示旧值。

  const nameInput = $id('profileNameInput');
  if (nameInput && document.activeElement !== nameInput) {
    nameInput.value = prof.name || '配置 ' + (currentTab + 1);
  }

  // 只剩 1 个配置时不允许删除（协议要求至少 1 个方案），按钮直接置灰并说明原因。
  const delBtn = $id('deleteProfileBtn');
  if (delBtn) {
    const onlyOne = !state || !state.profileNames || state.profileNames.length <= 1;
    delBtn.disabled = onlyOne;
    delBtn.title = onlyOne ? '至少保留 1 个配置' : '删除当前配置（需二次确认）';
  }

  const modeSelect = $id('profileModeSelect');
  if (modeSelect) {
    modeSelect.value = prof.mode || 'con';
    modeSelect.title = '并发按键：各技能各自计时、到点就按；循环按键：按槽位顺序一轮一轮按';
  }

  const startSelect = $id('startModeSelect');
  if (startSelect && state) startSelect.value = String(state.startMode || 0);
}

function renderHelperSection() {
  if (!state) return;
  const isD3 = state.currentGame === 'd3';
  const helperCard = $id('helperCard');
  if (helperCard) helperCard.style.display = isD3 ? 'flex' : 'none';
  // 非暗黑3 时右栏留一张说明卡，而不是空一大块。
  const emptyCard = $id('extraEmpty');
  if (emptyCard) emptyCard.style.display = isD3 ? 'none' : 'flex';
  if (!isD3) return;

  const h = state.helpers || {};
  const hotkeyBtn = $id('helperHotkeyBtn');
  if (hotkeyBtn) hotkeyBtn.textContent = h.hotkeyLabel || '无';

  setValueIfIdle($id('helperSpeedSelect'), String(h.delayMS || 150));
  setValueIfIdle($id('reforgeTargetSelect'), String(h.reforgeTarget || 0));


  const checklist = $id('helperChecklist');
  if (!checklist) return;
  checklist.innerHTML = '';
  const enabledMap = h.enabled || {};
  (state.allHelpers || []).forEach((item) => {
    const label = document.createElement('label');
    label.className = 'checkbox-label helper-check-item';
    const box = document.createElement('input');
    box.type = 'checkbox';
    box.value = item.id;
    box.checked = !!enabledMap[item.id];
    const text = document.createElement('span');
    text.textContent = item.name;
    label.appendChild(box);
    label.appendChild(text);
    checklist.appendChild(label);
  });
}

function renderPreferences() {
  if (!state) return;
  setCheckedIfIdle($id('smartPauseCheck'), !!state.smartPause);
  setCheckedIfIdle($id('soundCueCheck'), !!state.soundCue);
  setCheckedIfIdle($id('forceStandCheck'), !!state.forceStandEnabled);

  const standBtn = $id('forceStandKeyBtn');
  if (standBtn) {
    standBtn.textContent = state.forceStandKeyLabel || 'Shift';
    standBtn.title = '连点左键技能前先按住的键（仅键盘键；可清除为游戏默认键）';
  }

  const hud = state.hud;
  if (!hud) return;
  setCheckedIfIdle($id('hudEnabledCheck'), !!hud.enabled);
  setValueIfIdle($id('hudModeSelect'), hud.mode || 'bar');
  setValueIfIdle($id('hudAttachSelect'), hud.attach || 'free');
  setCheckedIfIdle($id('hudLockedCheck'), !!hud.locked);
  setCheckedIfIdle($id('hudGameOnlyCheck'), !!hud.gameOnly);

  // 关闭时只淡化显示方式，不禁用：玩家可以先配好形态/位置再打开，
  // 这里锁死会让「想开却找不到开关」变成第二个坑。
  const optionsRow = $id('hudOptionsRow');
  if (optionsRow) {
    optionsRow.classList.toggle('disabled', !hud.enabled);
  }
}

function renderStatus() {
  if (!state) return;
  const profName = (state.profileNames && state.profileNames[currentTab]) || '配置 ' + (currentTab + 1);
  const activeEl = $id('activeProfileText');
  if (activeEl) activeEl.textContent = profName;

  const targetEl = $id('targetProcessText');
  if (targetEl) targetEl.textContent = state.targetProcess || '—';

  const pill = $id('macroStatusPill');
  if (!pill) return;
  const engine = state.engine || {};
  if (!engine.configured) {
    pill.className = 'status-tag dormant';
    pill.textContent = '○ 未配置（请导入分享码或编辑规则）';
    return;
  }
  pill.className = 'status-tag ' + (engine.status || 'ready');
  pill.textContent = engine.statusText || '';
}

// 轻量写值助手：正在被编辑（焦点所在）的控件不覆盖，避免推送打断输入。
function setValueIfIdle(el, value) {
  if (!el || el === document.activeElement) return;
  if (el.value !== value) el.value = value;
}

function setCheckedIfIdle(el, value) {
  if (!el || el === document.activeElement) return;
  if (el.checked !== value) el.checked = value;
}

// ---------------------------------------------------------------- 用户动作

async function switchGame(gameID) {
  if (!state || state.currentGame === gameID) return;
  const r = await callApi('api_switchGame', gameID);
  if (!r.ok) return;
  await reloadState();
  flashAutoSave();
}

async function switchTab(idx) {
  if (idx === currentTab) return;
  const r = await callApi('api_switchProfile', idx);
  if (!r.ok) return;
  await reloadState();
}

// addProfile 让 Go 追加一个空白方案并切过去：名称、槽位、标签都由后端回传的状态决定，
// 界面不做本地猜测（失败时提示条已有 Go 给出的原因，例如「最多 4 个配置」）。
async function addProfile() {
  const r = await callApi('api_addProfile');
  if (!r.ok) return;
  await reloadState();
  flashAutoSave();
  showNotice('已新增配置：改个名字并填好技能，方便在游戏里切换', 'ok');
}

// 标签是动态重建的，事件统一交给容器（委托），避免每次重建都要重新绑。
function bindProfileTabs() {
  const container = $id('profileTabs');
  if (!container) return;
  container.addEventListener('click', (e) => {
    const btn = e.target.closest('.tab-btn');
    if (!btn || btn.disabled) return;
    if (btn.dataset.action === 'add') {
      addProfile();
      return;
    }
    const idx = Number(btn.dataset.tab);
    if (Number.isInteger(idx)) switchTab(idx);
  });
}

function bindSlotsDelegation() {
  const container = $id('slotList');
  if (!container) return;

  container.addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-field="key"]');
    if (!btn) return;
    openCapture('slot', { slotIdx: Number(btn.dataset.slot) });
  });

  container.addEventListener('change', async (e) => {
    const el = e.target;
    const slotIdx = Number(el.dataset.slot);
    if (!Number.isInteger(slotIdx)) return;
    const slot = slotAt(slotIdx);
    if (!slot) return;

    let key = slot.key;
    let strategy = slot.strategy;
    let interval = slot.intervalMS;
    let delayMin = slot.delayMinMS;
    let delayMax = slot.delayMaxMS;

    switch (el.dataset.field) {
      case 'strategy':
        strategy = parseInt(el.value, 10) || 0;
        break;
      case 'interval':
        interval = parseInt(el.value, 10) || 0;
        break;
      case 'delayMin':
      case 'delayMax': {
        // 两项是同一个区间的两端：任何一端留空（0）时，自动补成另一端的值，即「固定延迟」。
        // 失焦或回车都会触发 change，所以玩家只填一个也能得到想要的结果。
        let min = Math.abs(parseInt(valueOfSlotField(slotIdx, 'delayMin'), 10) || 0);
        let max = parseInt(valueOfSlotField(slotIdx, 'delayMax'), 10) || 0;
        if (min === 0 && max !== 0) {
          min = Math.abs(max);
          setSlotField(slotIdx, 'delayMin', min);
        } else if (max === 0 && min !== 0) {
          max = min;
          setSlotField(slotIdx, 'delayMax', max);
        }
        delayMin = min;
        delayMax = max;
        break;
      }
      default:
        return;
    }
    await commitSlot(slotIdx, key, strategy, interval, delayMin, delayMax);
  });
}

// valueOfSlotField / setSlotField 只操作当前槽位行的输入框，用于「另一项自动补齐」。
function slotFieldEl(slotIdx, field) {
  return document.querySelector('#slotList input[data-slot="' + slotIdx + '"][data-field="' + field + '"]');
}

function valueOfSlotField(slotIdx, field) {
  const el = slotFieldEl(slotIdx, field);
  return el ? el.value : '';
}

function setSlotField(slotIdx, field, value) {
  const el = slotFieldEl(slotIdx, field);
  if (el) el.value = value;
}

async function commitSlot(slotIdx, key, strategy, interval, delayMin, delayMax) {
  const r = await callApi('api_updateSlot', currentTab, slotIdx, key, strategy, interval, delayMin, delayMax);
  if (!r.ok) {
    // 失败时把控件恢复成后端实际保存的值（可能是被夹取/拒绝后的结果）
    await reloadState();
    renderSlots();
    return;
  }
  await reloadState();
  flashAutoSave();
}

async function saveProfileName() {
  const prof = currentProfile();
  const input = $id('profileNameInput');
  if (!prof || !input) return;
  const name = input.value.trim() || '配置 ' + (currentTab + 1);
  const r = await callApi('api_updateProfileMeta', currentTab, name, prof.mode);
  if (!r.ok) {
    await reloadState();
    return;
  }
  await reloadState();
  flashAutoSave();
}

// askConfirm 是二级确认的唯一入口：返回 Promise<boolean>，浮层里的「取消/确认」和 Esc 都会结算它。
function askConfirm(title, message, okLabel) {
  const overlay = $id('confirmOverlay');
  if (!overlay) return Promise.resolve(false);
  if (confirmResolve) {
    // 上一次确认还开着：先按「取消」结算，避免 Promise 永远悬着。
    closeConfirm(false);
  }
  $id('confirmTitle').textContent = title || '确认操作';
  $id('confirmMessage').textContent = message || '';
  const okBtn = $id('confirmOkBtn');
  if (okBtn) okBtn.textContent = okLabel || '确认';
  overlay.style.display = 'flex';
  return new Promise((resolve) => {
    confirmResolve = resolve;
  });
}

function closeConfirm(ok) {
  const overlay = $id('confirmOverlay');
  if (overlay) overlay.style.display = 'none';
  const resolve = confirmResolve;
  confirmResolve = null;
  if (resolve) resolve(!!ok);
}

async function deleteProfile() {
  const prof = currentProfile();
  if (!prof || !state || !state.profileNames || state.profileNames.length <= 1) return;
  const name = prof.name || '配置 ' + (currentTab + 1);
  const ok = await askConfirm(
    '删除配置「' + name + '」？',
    '该配置的技能槽位与按键模式会一起删除，无法撤销。',
    '确认删除'
  );
  if (!ok) return;
  const r = await callApi('api_deleteProfile', currentTab);
  if (!r.ok) {
    await reloadState();
    return;
  }
  await reloadState();
  flashAutoSave();
  showNotice('已删除配置「' + name + '」', 'ok');
}

async function saveProfileMode() {
  const prof = currentProfile();
  const sel = $id('profileModeSelect');
  if (!prof || !sel) return;
  const r = await callApi('api_updateProfileMeta', currentTab, prof.name, sel.value);
  if (!r.ok) {
    await reloadState();
    return;
  }
  await reloadState();
  flashAutoSave();
}

async function saveStartMode() {
  const sel = $id('startModeSelect');
  if (!sel) return;
  const r = await callApi('api_updateStartMode', parseInt(sel.value, 10) || 0);
  if (!r.ok) {
    await reloadState();
    return;
  }
  await reloadState();
  flashAutoSave();
}

async function saveHelperSettings() {
  const hotkey = (state && state.helpers && state.helpers.hotkey) || '';
  const speed = parseInt($id('helperSpeedSelect').value, 10) || 0;
  const target = parseInt($id('reforgeTargetSelect').value, 10) || 0;
  // 详细日志是「日志」的开关，不属于辅助功能：它的界面在运行日志浮层里。
  const dev = !!(state && state.helpers && state.helpers.devMode);
  const enabled = [];
  document.querySelectorAll('#helperChecklist input[type="checkbox"]:checked').forEach((cb) => {
    enabled.push(cb.value);
  });

  const r = await callApi('api_updateHelpers', hotkey, speed, target, dev, enabled);
  if (!r.ok) {
    await reloadState();
    renderHelperSection();
    return;
  }
  await reloadState();
  renderHelperSection();
  flashAutoSave();
}

async function savePreferences() {
  if (!state) return;
  const r = await callApi(
    'api_updatePreference',
    $id('smartPauseCheck').checked,
    $id('soundCueCheck').checked,
    $id('forceStandCheck').checked,
    state.forceStandKey || ''
  );
  if (!r.ok) {
    await reloadState();
    return;
  }
  await reloadState();
  flashAutoSave();
}

async function saveHUDSettings() {
  const box = $id('hudEnabledCheck');
  const r = await callApi(
    'api_updateHUD',
    !!(box && box.checked),
    $id('hudModeSelect').value,
    $id('hudLockedCheck').checked,
    $id('hudGameOnlyCheck').checked,
    $id('hudAttachSelect').value
  );
  if (!r.ok) {
    await reloadState();
    return;
  }
  await reloadState();
  flashAutoSave();
}

async function resetHUDPos() {
  const r = await callApi('api_resetHUDPos');
  if (!r.ok) return;
  showNotice('悬浮窗位置已复位到左上角', 'ok');
}

function bindHelperChecklist() {
  const checklist = $id('helperChecklist');
  if (checklist) checklist.addEventListener('change', saveHelperSettings);
}

// ---------------------------------------------------------------- 按键捕获

async function openCapture(mode, target) {
  const r = await callApi('api_captureOptions', mode);
  if (!r.ok) return;
  capture = { mode: mode, target: target || {}, options: r.value || {} };
  renderCaptureBox();
  const overlay = $id('captureOverlay');
  if (overlay) overlay.style.display = 'flex';
}

// 顶栏 / 方案 / 偏好 / 助手四处键位的捕获入口（可选行为由 Go 的 captureOptions 决定）
// 统一 return，便于外部（含自动化验证）await 到浮层真正就绪。
function captureToggleKey() {
  return openCapture('toggle');
}


function captureForceStandKey() {
  return openCapture('forceStand');
}

function captureHelperKey() {
  return openCapture('helperKey');
}

// 强制站立键与偏好设置同属一份工具设置，改动一并提交。
function saveForceStand() {
  savePreferences();
}

function renderCaptureBox() {
  const box = $id('captureBox');
  if (!box || !capture) return;
  const opts = capture.options;
  const mouseButtons = [
    ['LMB', '鼠标左键'],
    ['RMB', '鼠标右键'],
    ['MMB', '鼠标中键'],
    ['X1', '侧键·后退'],
    ['X2', '侧键·前进'],
  ];

  let html = '<div class="capture-title">' + esc(opts.title || '请按下按键') + '</div>';
  if (opts.hint) html += '<div class="capture-hint">' + esc(opts.hint) + '</div>';
  if (opts.mouseAllowed) {
    html += '<div class="capture-mouse-options">' +
      mouseButtons.map(([token, label]) => '<button class="tool-btn" data-key="' + token + '">' + label + '</button>').join('') +
      '</div>';
  }
  const extras = [];
  if (opts.clearLabel) extras.push('<button class="tool-btn" data-key="">' + esc(opts.clearLabel) + '</button>');
  if (opts.defaultLabel) extras.push('<button class="tool-btn" data-key="' + esc(opts.defaultToken || '') + '">' + esc(opts.defaultLabel) + '</button>');
  if (extras.length) html += '<div class="capture-extras">' + extras.join('') + '</div>';
  html += '<div class="capture-sub">（按 Escape 或点击背景取消）</div><div class="capture-pulse"></div>';
  box.innerHTML = html;

  box.querySelectorAll('button[data-key]').forEach((btn) => {
    btn.addEventListener('click', () => submitCapturedKey(btn.dataset.key));
  });
}

function closeCapture() {
  capture = null;
  const overlay = $id('captureOverlay');
  if (overlay) overlay.style.display = 'none';
}

function bindOverlayDismiss() {
  const overlay = $id('captureOverlay');
  if (overlay) {
    overlay.addEventListener('mousedown', (e) => {
      if (e.target === overlay) closeCapture();
    });
  }
  const importOverlay = $id('importOverlay');
  if (importOverlay) {
    importOverlay.addEventListener('mousedown', (e) => {
      if (e.target === importOverlay) closeImport();
    });
  }
  const confirmOverlay = $id('confirmOverlay');
  if (confirmOverlay) {
    // 点背景等同「取消」：破坏性操作的默认结果必须是「不做事」。
    confirmOverlay.addEventListener('mousedown', (e) => {
      if (e.target === confirmOverlay) closeConfirm(false);
    });
  }
  const logOverlay = $id('logOverlay');
  if (logOverlay) {
    logOverlay.addEventListener('mousedown', (e) => {
      if (e.target === logOverlay) closeLog();
    });
  }
}

async function handleKeyDown(e) {
  if (confirmResolve) {
    if (e.key === 'Escape') {
      e.preventDefault();
      closeConfirm(false);
    }
    return;
  }
  const logOverlay = $id('logOverlay');
  if (logOverlay && logOverlay.style.display === 'flex') {
    if (e.key === 'Escape') {
      e.preventDefault();
      closeLog();
    }
    return;
  }
  if (!capture) return;
  e.preventDefault();
  e.stopPropagation();
  if (e.key === 'Escape') {
    closeCapture();
    return;
  }
  // 优先用 KeyboardEvent.code（布局无关），退回 key；合法性由 Go 判定。
  await submitCapturedKey(e.code || e.key || '');
}

// submitCapturedKey 把原始键名交给 Go 校验：不合法就在提示条里说明原因（例如「方案切换键不支持鼠标键」）。
async function submitCapturedKey(rawKey) {
  if (!capture) return;
  const current = capture;
  const r = await callApi('api_captureKey', current.mode, rawKey);
  if (!r.ok) return;
  closeCapture();
  await applyCapturedKey(current, r.value.token, r.value.label);
}

async function applyCapturedKey(target, token, label) {
  const prof = currentProfile();
  if (!prof) return;
  let result = { ok: true };

  switch (target.mode) {
    case 'slot': {
      const slot = slotAt(target.target.slotIdx);
      if (!slot) return;
      result = await callApi('api_updateSlot', currentTab, target.target.slotIdx, token,
        slot.strategy, slot.intervalMS, slot.delayMinMS, slot.delayMaxMS);
      break;
    }
    case 'toggle':
      result = await callApi('api_updateToggle', token);
      break;
    case 'forceStand':
      result = await callApi('api_updatePreference',
        $id('smartPauseCheck').checked, $id('soundCueCheck').checked,
        $id('forceStandCheck').checked, token);
      break;
    case 'helperKey': {
      const enabled = [];
      document.querySelectorAll('#helperChecklist input[type="checkbox"]:checked').forEach((cb) => {
        enabled.push(cb.value);
      });
      result = await callApi('api_updateHelpers', token,
        parseInt($id('helperSpeedSelect').value, 10) || 0,
        parseInt($id('reforgeTargetSelect').value, 10) || 0,
        !!(state && state.helpers && state.helpers.devMode), enabled);
      break;
    }
    default:
      return;
  }

  await reloadState();
  if (result.ok) {
    showNotice('已绑定为「' + (label || token) + '」', 'ok');
    flashAutoSave();
  }
}

// ---------------------------------------------------------------- 分享码

async function importCode() {
  const r = await callApi('api_previewImport');
  if (!r.ok) return;
  const preview = $id('importPreview');
  if (preview) preview.textContent = r.value || '';
  const overlay = $id('importOverlay');
  if (overlay) overlay.style.display = 'flex';
}

function closeImport() {
  const overlay = $id('importOverlay');
  if (overlay) overlay.style.display = 'none';
}

async function confirmImport() {
  closeImport();
  const r = await callApi('api_applyImport');
  if (!r.ok) return;
  await reloadState();
  showNotice('配置已导入并生效', 'ok');
}

async function exportCode() {
  const r = await callApi('api_exportClipboard');
  if (!r.ok) return;
  const len = (r.value || '').length;
  showNotice('分享码已复制到剪贴板（' + len + ' 字符），可直接发给好友', 'ok');
}

// ---------------------------------------------------------------- 日志浮层

// openLog 拉取日志全文并用界面内的浮层展示（不另开原生窗口，样式与主界面一致）。
async function openLog() {
  const r = await callApi('api_getLog');
  if (!r.ok) return;
  renderLog(r.value);
  syncLogDevMode();
  const overlay = $id('logOverlay');
  if (overlay) overlay.style.display = 'flex';
}

// syncLogDevMode 让日志浮层里的「助手详细日志」勾选态跟随后端设置。
function syncLogDevMode() {
  const box = $id('logDevCheck');
  if (!box) return;
  setCheckedIfIdle(box, !!(state && state.helpers && state.helpers.devMode));
}

// saveLogDevMode 只切日志详细度：不涉及助手功能开关，改完立即生效。
async function saveLogDevMode() {
  const box = $id('logDevCheck');
  if (!box) return;
  const r = await callApi('api_setHelperDevMode', box.checked);
  if (!r.ok) {
    await reloadState();
    syncLogDevMode();
    return;
  }
  await reloadState();
  syncLogDevMode();
  showNotice(box.checked ? '已开启助手详细日志（日志会明显变多）' : '已关闭助手详细日志', 'ok');
}

function renderLog(data) {
  const pathEl = $id('logPath');
  if (pathEl) pathEl.textContent = (data && data.path) || '—';
  const textEl = $id('logText');
  if (textEl) {
    textEl.textContent = (data && data.text) || '（暂无日志）';
    textEl.scrollTop = textEl.scrollHeight; // 打开就看到最新一条
  }
}

function closeLog() {
  const overlay = $id('logOverlay');
  if (overlay) overlay.style.display = 'none';
}

async function refreshLog() {
  const r = await callApi('api_getLog');
  if (!r.ok) return;
  renderLog(r.value);
  showNotice('日志已刷新', 'ok');
}

async function copyLog() {
  const r = await callApi('api_copyLog');
  if (!r.ok) return;
  showNotice('日志已复制到剪贴板，直接贴给我们即可', 'ok');
}

async function clearLog() {
  const r = await callApi('api_clearLog');
  if (!r.ok) return;
  await refreshLog();
  showNotice('已清空当前日志', 'ok');
}

async function openDataDir() {
  const r = await callApi('api_openDataDir');
  if (!r.ok) return;
  showNotice('已打开数据目录', 'ok');
}

// 最小化 = 收起（隐藏到托盘）：宏继续跑，双击托盘图标恢复。
function minimizeWindow() {
  callApi('api_minimize');
}

// requestQuit 是「关闭」的唯一入口：✕ 按钮与 Alt+F4（由 Go 侧转进来）都走这里。
// 先二次确认，取消就什么都不做，确认后才真正退出进程。
async function requestQuit() {
  const ok = await askConfirm(
    '退出暗黑懒人宏？',
    '退出后宏会立即停止，托盘图标也会消失。需要时重新打开程序即可。',
    '退出'
  );
  if (!ok) return;
  await callApi('api_quit');
}

// ---------------------------------------------------------------- 顶栏拖拽

function bindHeaderDrag() {
  const header = $id('appHeader');
  if (!header) return;
  header.addEventListener('mousedown', (e) => {
    if (e.button !== 0) return;
    if (e.target.closest('button, input, select, label')) return;
    if (typeof window.api_dragWindow === 'function') {
      window.api_dragWindow();
    }
  });
}
