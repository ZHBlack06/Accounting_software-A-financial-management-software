/* 个人记账安装/卸载界面的交互逻辑（原生 JS，无构建步骤）。 */
(function () {
  'use strict';

  var state = null;
  var since = 0;
  var busy = false;
  var settled = false;
  var pollTimer = null;

  function el(id) { return document.getElementById(id); }

  function api(path, body) {
    var init = { cache: 'no-store' };
    if (body !== undefined) {
      init.method = 'POST';
      init.headers = { 'Content-Type': 'application/json' };
      init.body = JSON.stringify(body);
    }
    return fetch(path, init).then(function (res) { return res.json(); });
  }

  function setHint(text, kind) {
    var h = state && state.mode === 'uninstall' ? el('dirHint2') : el('dirHint');
    if (!h) { return; }
    h.textContent = text || '';
    h.className = 'hint' + (kind ? ' ' + kind : '');
  }

  function setFooter(text, kind) {
    var f = el('footerMsg');
    f.textContent = text || '';
    f.className = 'footer-msg' + (kind ? ' ' + kind : '');
    f.title = text || '';
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function showProgress() {
    el('setupView').hidden = true;
    el('progressView').hidden = false;
    el('barFill').style.width = '0%';
    el('pctText').textContent = '0%';
  }

  function lockInputs() {
    var ids = ['dirInput', 'browseBtn', 'optDesktop', 'optStartMenu', 'optLaunch', 'optRemoveData'];
    ids.forEach(function (id) {
      var node = el(id);
      if (node) { node.disabled = true; }
    });
  }

  // ---------- 初始化 ----------
  function init() {
    api('/api/state').then(function (res) {
      state = res;
      document.title = res.product + ' ' + res.displayVersion +
        (res.mode === 'uninstall' ? ' 卸载程序' : ' 安装程序');
      el('heroVersion').textContent = res.displayVersion;

      if (res.mode === 'uninstall') {
        setupUninstall(res);
      } else {
        setupInstall(res);
      }
      startPolling();
    }).catch(function (err) {
      setFooter('界面初始化失败：' + err, 'error');
    });
  }

  function setupInstall(res) {
    el('heroSubtitle').textContent = '本地优先的个人记账工具 · 数据全部保存在本机';
    el('installFields').hidden = false;
    el('uninstallFields').hidden = true;
    el('dirInput').value = res.dir || res.defaultDir || '';
    el('needSize').textContent = res.payloadSizeText || '—';
    el('freeSize').textContent = res.freeSpaceText || '—';
    el('fileCount').textContent = (res.payloadFiles || 0) + ' 个文件';
    el('optLaunch').checked = true;
    el('primaryBtn').textContent = '安装';

    if (!res.freeSpaceText) {
      setHint('无法查询目标磁盘可用空间，安装过程可能因空间不足而失败。', 'error');
    } else if (res.payloadSize > 0 && res.freeSpace > 0 && res.freeSpace < res.payloadSize) {
      setHint('目标磁盘可用空间不足，请更换安装位置。', 'error');
    } else {
      setHint('建议安装到非系统盘或用户目录；安装目录会自动创建。');
    }

    if (res.existing) {
      var n = el('upgradeNotice');
      n.hidden = false;
      n.textContent = '检测到该位置已安装' + (res.existingVersion ? ' ' + res.existingVersion : '') +
        '，继续安装将覆盖程序文件，并保留 data 数据目录。';
    }
  }

  function setupUninstall(res) {
    el('heroSubtitle').textContent = '卸载将删除程序文件、快捷方式与注册表信息';
    el('installFields').hidden = true;
    el('uninstallFields').hidden = false;
    el('unDir').textContent = res.dir || '—';
    el('unSize').textContent = res.dirSizeText || '—';
    el('unData').textContent = res.dataExists
      ? '存在（默认保留）' + (res.dataDir ? '：' + res.dataDir : '')
      : '不存在' + (res.dataDir ? '：' + res.dataDir : '');
    var rd = el('removeDataText');
    if (rd) {
      rd.textContent = '同时删除用户数据' + (res.dataDir ? '（' + res.dataDir + '）' : '') +
        '，账本数据将永久丢失';
    }
    el('primaryBtn').textContent = '卸载';
    if (!res.installed) {
      setHint('该目录下未找到已安装的个人记账，仍可执行清理操作。', 'error');
    } else {
      setHint('如需彻底清除账本数据，请勾选下方选项。');
    }
  }

  // ---------- 日志轮询 ----------
  function startPolling() {
    if (pollTimer) { return; }
    pollTimer = setInterval(poll, 260);
    poll();
  }

  function poll() {
    api('/api/log?since=' + since).then(function (res) {
      if (res.lines && res.lines.length) {
        appendLines(res.lines);
        since = res.nextIndex;
      }
      var pct = res.percent || 0;
      el('barFill').style.width = pct + '%';
      el('pctText').textContent = pct + '%';
      if (res.done && !settled) {
        settled = true;
        finish(res);
      } else if (res.done) {
        settled = true;
      }
    }).catch(function () { /* 忽略瞬时错误，继续轮询 */ });
  }

  function appendLines(lines) {
    var box = el('logBox');
    var frag = document.createDocumentFragment();
    lines.forEach(function (line) {
      if (line.level === 'step') {
        el('stageText').textContent = line.message;
      }
      var div = document.createElement('div');
      div.className = 'l-' + line.level;
      div.innerHTML = '<span class="t">' + escapeHtml(line.time) + '</span>' + escapeHtml(line.message);
      frag.appendChild(div);
    });
    box.appendChild(frag);
    box.scrollTop = box.scrollHeight;
  }

  function finish(res) {
    clearInterval(pollTimer);
    pollTimer = null;
    var primary = el('primaryBtn');
    var cancel = el('cancelBtn');
    cancel.hidden = true;
    primary.disabled = false;

    if (res.ok) {
      el('barFill').style.width = '100%';
      el('pctText').textContent = '100%';
      el('stageText').textContent = state.mode === 'uninstall' ? '卸载完成' : '安装完成';
      primary.textContent = '完成';
      primary.onclick = exitApp;
      setFooter((state.mode === 'uninstall' ? '已卸载：' : '已安装到：') + (res.dir || ''), 'ok');
    } else {
      el('stageText').textContent = state.mode === 'uninstall' ? '卸载失败' : '安装失败';
      primary.textContent = '关闭';
      primary.onclick = exitApp;
      setFooter(res.error || '操作失败', 'error');
      setHint(res.error || '操作失败', 'error');
    }
  }

  function exitApp() {
    api('/api/exit', {}).finally(function () { /* 窗口即将关闭 */ });
  }

  // ---------- 事件绑定 ----------
  function startInstall() {
    if (busy) { return; }
    var dir = (el('dirInput').value || '').trim();
    if (!dir) {
      setHint('请填写安装位置。', 'error');
      return;
    }
    busy = true;
    setHint('正在安装，请勿关闭窗口…');
    setFooter('正在安装…');
    lockInputs();
    el('primaryBtn').disabled = true;
    el('primaryBtn').textContent = '安装中…';
    el('cancelBtn').disabled = true;

    api('/api/install', {
      dir: dir,
      desktop: el('optDesktop').checked,
      startMenu: el('optStartMenu').checked,
      launch: el('optLaunch').checked
    }).then(function (res) {
      if (!res.ok) {
        busy = false;
        el('primaryBtn').disabled = false;
        el('primaryBtn').textContent = '安装';
        el('cancelBtn').disabled = false;
        var ids = ['dirInput', 'browseBtn', 'optDesktop', 'optStartMenu', 'optLaunch'];
        ids.forEach(function (id) { el(id).disabled = false; });
        setHint(res.error || '安装失败', 'error');
        setFooter(res.error || '安装失败', 'error');
        return;
      }
      showProgress();
    }).catch(function (err) {
      busy = false;
      el('primaryBtn').disabled = false;
      setFooter('请求失败：' + err, 'error');
    });
  }

  function startUninstall() {
    if (busy) { return; }
    busy = true;
    lockInputs();
    el('primaryBtn').disabled = true;
    el('primaryBtn').textContent = '卸载中…';
    el('cancelBtn').disabled = true;
    setFooter('正在卸载…');

    api('/api/uninstall', { removeData: el('optRemoveData').checked }).then(function (res) {
      if (!res.ok) {
        busy = false;
        el('primaryBtn').disabled = false;
        el('primaryBtn').textContent = '卸载';
        el('cancelBtn').disabled = false;
        setHint(res.error || '卸载失败', 'error');
        return;
      }
      showProgress();
    }).catch(function (err) {
      busy = false;
      el('primaryBtn').disabled = false;
      setFooter('请求失败：' + err, 'error');
    });
  }

  el('browseBtn').addEventListener('click', function () {
    if (busy) { return; }
    el('browseBtn').disabled = true;
    api('/api/browse', { current: el('dirInput').value }).then(function (res) {
      el('browseBtn').disabled = false;
      if (res.ok && res.path) {
        el('dirInput').value = res.path;
        setHint('已选择安装位置。');
      } else if (!res.ok && res.error) {
        setHint(res.error, 'error');
      }
    }).catch(function (err) {
      el('browseBtn').disabled = false;
      setHint('打开对话框失败：' + err, 'error');
    });
  });

  el('cancelBtn').addEventListener('click', function () {
    if (!busy) { exitApp(); }
  });

  el('primaryBtn').addEventListener('click', function () {
    if (settled) { exitApp(); return; }
    if (state && state.mode === 'uninstall') {
      startUninstall();
    } else {
      startInstall();
    }
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && !busy) { exitApp(); }
  });

  init();
})();
