(function () {
  "use strict";

  var root = document.querySelector("[data-matching-console]");
  if (!root) {
    return;
  }

  var kind = root.getAttribute("data-matching-console") || "";
  var base = root.getAttribute("data-base-url") || "";
  var activityNo = root.getAttribute("data-activity-no") || "";
  var csrf = root.getAttribute("data-csrf") || "";
  var statusEl = root.querySelector("[data-matching-status]");
  var buttons = Array.prototype.slice.call(
    root.querySelectorAll("[data-matching-action]")
  );
  var busy = false;

  var hints = {
    group_busy: "已有分组或重置正在执行，请稍后再试。",
    group_already_done: "该轮已有分组结果，请换一个轮次或先清空结果。",
    group_mode_conflict: "请先在活动工作台关闭一对一，再执行分组。",
    not_enough_people: "请核对签到名单，或调低每组男女人数。",
    empty_groups: "参与人数不足，无法生成分组。",
    group_size_invalid: "每组男女人数必须大于 0。",
    invalid_round: "轮次必须是 ≥1 的整数。",
    already_done: "该轮已有一对一结果，请换一个轮次或先清空结果。",
    exclusive_mode: "请先在活动工作台关闭分组，再执行一对一。",
    no_participants: "没有有效参与者；如开启签到，请先让参与者签到。",
    invalid_female_chats: "女生场次 K 必须是 ≥1 的整数，且不能超过男生人数。",
    csrf_invalid: "页面已过期，请刷新后重试。",
    csrf_failed: "页面已过期，请刷新后重试。",
    mode_disabled: "请先在活动工作台开启对应匹配模式。"
  };

  buttons.forEach(function (button) {
    button.setAttribute("data-label", button.textContent.trim());
  });

  function intField(name) {
    var input = root.querySelector('[data-matching-field="' + name + '"]');
    if (!input) {
      return 0;
    }
    var value = parseInt(input.value, 10);
    return isNaN(value) ? 0 : value;
  }

  function clearStatus() {
    if (!statusEl) {
      return;
    }
    while (statusEl.firstChild) {
      statusEl.removeChild(statusEl.firstChild);
    }
    statusEl.className = "admin-matching-status";
  }

  function showStatus(message, ok, code) {
    if (!statusEl) {
      return;
    }
    clearStatus();
    statusEl.className = "admin-matching-status " + (ok ? "is-ok" : "is-error");
    var line = document.createElement("p");
    line.textContent = message;
    statusEl.appendChild(line);
    if (code) {
      var codeEl = document.createElement("code");
      codeEl.textContent = "错误码：" + code;
      statusEl.appendChild(codeEl);
    }
    if (!ok && hints[code]) {
      var hint = document.createElement("p");
      hint.className = "hint";
      hint.textContent = "修复提示：" + hints[code];
      statusEl.appendChild(hint);
    }
  }

  function setBusy(on, activeButton) {
    busy = on;
    root.setAttribute("aria-busy", on ? "true" : "false");
    buttons.forEach(function (button) {
      button.disabled = on;
      button.setAttribute("aria-busy", on ? "true" : "false");
      if (on && button === activeButton) {
        button.textContent = "执行中…";
      } else if (!on) {
        button.textContent = button.getAttribute("data-label") || button.textContent;
      }
    });
  }

  function parseResponse(response) {
    return response.json().then(
      function (data) {
        return { response: response, data: data };
      },
      function () {
        return { response: response, data: null };
      }
    );
  }

  function post(path, payload) {
    return fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": csrf
      },
      body: JSON.stringify(payload)
    }).then(parseResponse);
  }

  function goTo(url) {
    window.setTimeout(function () {
      window.location.href = url;
    }, 200);
  }

  function run(button) {
    var round = intField("preview_round");
    if (root.getAttribute("data-mode-enabled") !== "true") {
      showStatus("请先在活动工作台开启对应匹配模式。", false, "mode_disabled");
      return;
    }
    if (kind === "group") {
      var femaleGroup = intField("female_per_group");
      var maleGroup = intField("male_per_group");
      if (!window.confirm(
        "确认执行第 " + (round || "下一") + " 轮分组？每组 " +
        (maleGroup || "默认") + " 男 " + (femaleGroup || "默认") + " 女。"
      )) {
        return;
      }
      setBusy(true, button);
      clearStatus();
      post("/api/admin/group/run", {
        activity_id: activityNo,
        round: round,
        male_per_group: maleGroup,
        female_per_group: femaleGroup
      }).then(function (result) {
        if (!result.response.ok || !result.data) {
          showError(result);
          return;
        }
        showStatus("分组已完成，正在刷新结果…", true, "");
        var doneRound = result.data.result && result.data.result.round ?
          result.data.result.round : round;
        goTo(base + "?round=" + doneRound + "&notice=run#result");
      }).catch(function () {
        setBusy(false, null);
        showStatus("网络请求失败，请稍后再试。", false, "network_error");
      }).then(function () {
        if (!busy) {
          setBusy(false, button);
        }
      });
      return;
    }

    var femaleChats = intField("female_chats");
    if (!window.confirm(
      "确认执行第 " + (round || "下一") + " 轮一对一？女生场次 K=" + femaleChats + "。"
    )) {
      return;
    }
    setBusy(true, button);
    clearStatus();
    post("/api/admin/onetoone/run", {
      activity_id: activityNo,
      round: round,
      female_chats: femaleChats
    }).then(function (result) {
      if (!result.response.ok || !result.data) {
        showError(result);
        return;
      }
      showStatus("一对一匹配已完成，正在刷新结果…", true, "");
      var doneRound = result.data.round ? result.data.round : round;
      goTo(base + "?round=" + doneRound + "&notice=run#result");
    }).catch(function () {
      setBusy(false, null);
      showStatus("网络请求失败，请稍后再试。", false, "network_error");
    }).then(function () {
      if (!busy) {
        setBusy(false, button);
      }
    });
  }

  function reset(button) {
    var label = kind === "group" ? "分组" : "一对一";
    if (!window.confirm("确认清空该活动的全部" + label + "结果与状态？")) {
      return;
    }
    if (!window.confirm("此操作不可恢复。再次确认清空全部" + label + "结果？")) {
      return;
    }
    setBusy(true, button);
    clearStatus();
    var path = kind === "group" ?
      "/api/admin/group/reset" : "/api/admin/onetoone/reset";
    post(path, { activity_id: activityNo }).then(function (result) {
      if (!result.response.ok || !result.data) {
        showError(result);
        return;
      }
      showStatus("已清空，正在刷新…", true, "");
      goTo(base + "?notice=reset#preview");
    }).catch(function () {
      setBusy(false, null);
      showStatus("网络请求失败，请稍后再试。", false, "network_error");
    }).then(function () {
      if (!busy) {
        setBusy(false, button);
      }
    });
  }

  function showError(result) {
    var message = "执行失败，请稍后再试。";
    var code = "";
    if (result.data && result.data.error) {
      message = result.data.error.message || message;
      code = result.data.error.code || "";
    } else if (!result.response.ok) {
      message = "服务器返回 HTTP " + result.response.status + "，请稍后再试。";
    }
    setBusy(false, null);
    showStatus(message, false, code);
  }

  buttons.forEach(function (button) {
    button.addEventListener("click", function () {
      if (busy) {
        return;
      }
      if (button.getAttribute("data-matching-action") === "reset") {
        reset(button);
      } else {
        run(button);
      }
    });
  });

  Array.prototype.forEach.call(
    root.querySelectorAll("[data-matching-preview-form]"),
    function (form) {
      form.addEventListener("submit", function () {
        var submit = form.querySelector('button[type="submit"]');
        if (submit) {
          submit.disabled = true;
          submit.textContent = "计算中…";
        }
        root.setAttribute("aria-busy", "true");
      });
    }
  );

  var filter = root.querySelector("[data-roster-filter]");
  if (filter) {
    filter.addEventListener("input", function () {
      var query = filter.value.trim().toLowerCase();
      var entries = root.querySelectorAll("[data-roster-entry]");
      Array.prototype.forEach.call(entries, function (entry) {
        var haystack = (entry.getAttribute("data-search") || "").toLowerCase();
        entry.style.display = !query || haystack.indexOf(query) !== -1 ? "" : "none";
      });
    });
  }
})();
