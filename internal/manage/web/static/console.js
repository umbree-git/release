"use strict";

const streamed = { promote: true, yank: true };
let current = null;
let busy = false;

function canStream() {
  return typeof ReadableStream === "function" && typeof TextDecoder === "function";
}

function sameOrigin(res) {
  try {
    return new URL(res.url, window.location.href).origin === window.location.origin;
  } catch (err) {
    return false;
  }
}

function parse(text) {
  return new DOMParser().parseFromString(text, "text/html");
}

function send(form, inline) {
  var headers = { "Content-Type": "application/x-www-form-urlencoded" };
  if (inline) {
    headers["X-Inline-Progress"] = "1";
  }
  return fetch(form.action, {
    method: "POST",
    body: new URLSearchParams(new FormData(form)),
    credentials: "same-origin",
    headers: headers
  });
}

function closeStrip() {
  if (!current || busy) {
    return;
  }
  current.node.remove();
  current.form.hidden = false;
  var button = current.form.querySelector("button");
  current = null;
  if (button) {
    button.disabled = false;
    button.focus();
  }
}

function mount(form, strip) {
  strip.classList.add("confirm-inline");
  if (form.dataset.confirmSlot) {
    var anchor = form.closest(".inline") || form;
    anchor.after(strip);
  } else {
    form.hidden = true;
    form.after(strip);
  }
  current = { form: form, node: strip };
  var first = strip.querySelector("button");
  if (first) {
    first.focus();
  }
}

function ask(form) {
  var button = form.querySelector("button");
  if (button) {
    button.disabled = true;
  }
  send(form, false).then(function (res) {
    if (!res.ok || !sameOrigin(res)) {
      throw new Error("unexpected response");
    }
    return res.text();
  }).then(function (text) {
    var strip = parse(text).getElementById("confirm-strip");
    if (!strip) {
      throw new Error("no confirm strip");
    }
    if (button) {
      button.disabled = false;
    }
    mount(form, document.adoptNode(strip));
  }).catch(function () {
    form.submit();
  });
}

function make(tag, cls, text) {
  var el = document.createElement(tag);
  if (cls) {
    el.className = cls;
  }
  if (text) {
    el.textContent = text;
  }
  return el;
}

function railSteps(log) {
  var order = [];
  var seen = {};
  log.querySelectorAll("li[data-step]").forEach(function (li) {
    var step = li.dataset.step;
    if (step === "text" || step === "done") {
      return;
    }
    if (!seen[step]) {
      seen[step] = { name: step, state: "ok" };
      order.push(seen[step]);
    }
    if (step === "error" || li.dataset.status === "error" || li.dataset.status === "failed") {
      seen[step].state = "fail";
    }
  });
  return order;
}

function drawRail(rail, log, ending) {
  var steps = railSteps(log);
  if (steps.length && !ending) {
    steps[steps.length - 1].state = "run";
  }
  if (ending === "failed" && steps.length) {
    steps[steps.length - 1].state = "fail";
  }
  rail.replaceChildren.apply(rail, steps.map(function (s) {
    var li = make("li", "step", s.name);
    li.dataset.state = s.state;
    return li;
  }));
}

function panelView(strip) {
  var view = {
    panel: make("section", "panel progress inline-progress"),
    rail: make("ol", "rail"),
    body: make("div", "inline-log"),
    note: make("p", "dim inline-note", "running…"),
    text: "",
    decoder: new TextDecoder()
  };
  view.panel.append(view.rail, view.body, view.note);
  strip.hidden = true;
  strip.after(view.panel);
  return view;
}

function render(view, ending) {
  var src = parse(view.text).querySelector("section.progress");
  if (src) {
    view.body.replaceChildren(document.adoptNode(src));
  }
  var result = view.body.querySelector("[data-result]");
  var outcome = result ? result.dataset.result : ending;
  drawRail(view.rail, view.body, outcome);
  return outcome;
}

function finish(view, outcome, message) {
  busy = false;
  if (outcome === "done") {
    view.note.textContent = "refreshing…";
    window.setTimeout(function () {
      window.location.reload();
    }, 1200);
    return;
  }
  drawRail(view.rail, view.body, "failed");
  view.note.textContent = message || "failed: read the history before acting again";
  view.note.className = "result error inline-note";
  var back = make("button", "btn", "Cancel");
  back.type = "button";
  back.addEventListener("click", function () {
    view.panel.remove();
    closeStrip();
  });
  view.panel.append(back);
}

function pump(view, reader) {
  return reader.read().then(function (chunk) {
    if (chunk.done) {
      view.text += view.decoder.decode();
      finish(view, render(view, "failed") === "done" ? "done" : "failed");
      return;
    }
    view.text += view.decoder.decode(chunk.value, { stream: true });
    render(view, "");
    return pump(view, reader);
  });
}

function progress(form, strip) {
  var view = panelView(strip);
  busy = true;
  send(form, true).then(function (res) {
    if (!sameOrigin(res)) {
      throw new Error("cross-origin response");
    }
    if (!res.ok || !res.body) {
      return res.text().then(function (t) {
        finish(view, "failed", t.trim() || "HTTP " + res.status);
      });
    }
    return pump(view, res.body.getReader());
  }).catch(function () {
    finish(view, "failed", "the connection was lost: read the history before acting again");
  });
}

document.addEventListener("submit", function (ev) {
  var form = ev.target;
  if (busy) {
    ev.preventDefault();
    return;
  }
  if (current && current.node.contains(form)) {
    var verb = current.node.dataset.verb;
    if (streamed[verb] && canStream()) {
      ev.preventDefault();
      progress(form, current.node);
    }
    return;
  }
  if (form.classList.contains("act")) {
    ev.preventDefault();
    if (current) {
      closeStrip();
    }
    ask(form);
  }
});

document.addEventListener("click", function (ev) {
  var cancel = ev.target.closest("[data-cancel]");
  if (cancel && current && current.node.contains(cancel)) {
    ev.preventDefault();
    closeStrip();
  }
});

window.addEventListener("beforeunload", function (ev) {
  if (busy) {
    ev.preventDefault();
    ev.returnValue = "";
  }
});
