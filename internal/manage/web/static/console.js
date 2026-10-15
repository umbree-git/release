"use strict";
{
  const streamed = { promote: true, yank: true };
  let current = null;
  let busy = false;
  let pending = false;

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
    let headers = { "Content-Type": "application/x-www-form-urlencoded" };
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
    let button = current.form.querySelector("button");
    current = null;
    if (button) {
      button.disabled = false;
      button.focus();
    }
  }

  function mount(form, strip) {
    strip.classList.add("confirm-inline");
    if (form.dataset.confirmSlot) {
      let anchor = form.closest(".inline") || form;
      anchor.after(strip);
    } else {
      form.hidden = true;
      form.after(strip);
    }
    current = { form: form, node: strip };
    let first = strip.querySelector("button");
    if (first) {
      first.focus();
    }
  }

  function ask(form) {
    pending = true;
    let button = form.querySelector("button");
    if (button) {
      button.disabled = true;
    }
    send(form, false).then(function (res) {
      if (!res.ok || !sameOrigin(res)) {
        throw new Error("unexpected response");
      }
      return res.text();
    }).then(function (text) {
      let strip = parse(text).getElementById("confirm-strip");
      if (!strip) {
        throw new Error("no confirm strip");
      }
      pending = false;
      if (button) {
        button.disabled = false;
      }
      mount(form, document.adoptNode(strip));
    }).catch(function () {
      pending = false;
      form.submit();
    });
  }

  function make(tag, cls, text) {
    let el = document.createElement(tag);
    if (cls) {
      el.className = cls;
    }
    if (text) {
      el.textContent = text;
    }
    return el;
  }

  function stepList(log) {
    let order = [];
    let seen = {};
    log.querySelectorAll("li[data-step]").forEach(function (li) {
      let step = li.dataset.step;
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

  function drawSteps(list, log, ending) {
    let steps = stepList(log);
    if (steps.length && !ending) {
      steps[steps.length - 1].state = "run";
    }
    if (ending === "failed" && steps.length) {
      steps[steps.length - 1].state = "fail";
    }
    list.replaceChildren.apply(list, steps.map(function (s) {
      let li = make("li", "run-step", s.name);
      li.dataset.state = s.state;
      return li;
    }));
  }

  function panelView(strip) {
    let view = {
      panel: make("section", "panel progress inline-progress"),
      steps: make("ol", "run-steps"),
      body: make("div", "inline-log"),
      note: make("p", "dim inline-note", "running…"),
      text: "",
      decoder: new TextDecoder()
    };
    view.panel.append(view.steps, view.body, view.note);
    strip.hidden = true;
    strip.after(view.panel);
    return view;
  }

  function render(view, ending) {
    let src = parse(view.text).querySelector("section.progress");
    if (src) {
      view.body.replaceChildren(document.adoptNode(src));
    }
    let result = view.body.querySelector("[data-result]");
    let outcome = result ? result.dataset.result : ending;
    drawSteps(view.steps, view.body, outcome);
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
    drawSteps(view.steps, view.body, "failed");
    view.note.textContent = message || "failed: read the history before acting again";
    view.note.className = "result error inline-note";
    let back = make("button", "btn", "Cancel");
    back.type = "button";
    back.addEventListener("click", function () {
      view.panel.remove();
      closeStrip();
    });
    view.panel.append(back);
  }

  function refusal(res, text) {
    const type = res.headers.get("Content-Type") || "";
    let message = "";
    if (type.startsWith("text/html")) {
      message = (parse(text).body.textContent || "").trim();
    } else if (type.startsWith("text/plain")) {
      message = text.trim();
    }
    if (!message) {
      return "the console refused the request (HTTP " + res.status + "): read the history before acting again";
    }
    return message.length > 300 ? message.slice(0, 300) + "…" : message;
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
    let view = panelView(strip);
    busy = true;
    send(form, true).then(function (res) {
      if (!sameOrigin(res)) {
        throw new Error("cross-origin response");
      }
      if (!res.ok || !res.body) {
        return res.text().then(function (t) {
          finish(view, "failed", refusal(res, t));
        });
      }
      return pump(view, res.body.getReader());
    }).catch(function () {
      finish(view, "failed", "the connection was lost: read the history before acting again");
    });
  }

  document.addEventListener("submit", function (ev) {
    let form = ev.target;
    if (busy || pending) {
      ev.preventDefault();
      return;
    }
    if (current && current.node.contains(form)) {
      let verb = current.node.dataset.verb;
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
    let cancel = ev.target.closest("[data-cancel]");
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
}
