// SPDX-License-Identifier: Apache-2.0
//
// Cook mode enhancements (docs/DESIGN.md FR-09, §9.3, §11.2):
//   - instant step changes (buttons, swipe, arrow keys) without reloads
//   - Screen Wake Lock, with a visible indicator and a fallback message
//   - tap-to-start timers that store the target time, so they survive the
//     screen locking or the page reloading
// Without this script the page still works: Back/Next are plain links.
//
// init() is called at the bottom of the file: module-level constants such as
// STORE are in their temporal dead zone until their line has run.

function init(shell) {
  const total = Number(shell.dataset.steps);
  const slug = shell.dataset.slug;
  const title = shell.dataset.title;
  const steps = [...shell.querySelectorAll(".cook-step")];
  const prev = document.getElementById("prev");
  const next = document.getElementById("next");
  const count = document.getElementById("cook-count");
  const doneHref = "/r/" + slug;

  let cur = Number(steps.find((s) => s.hasAttribute("aria-current"))?.dataset.step || 1);

  function show(n) {
    if (n < 1 || n > total) return;
    cur = n;
    for (const s of steps) {
      if (Number(s.dataset.step) === n) s.setAttribute("aria-current", "step");
      else s.removeAttribute("aria-current");
    }
    count.textContent = `Step ${n} of ${total}`;
    prev.classList.toggle("is-disabled", n <= 1);
    prev.toggleAttribute("aria-disabled", n <= 1);
    prev.href = stepHref(Math.max(1, n - 1));
    if (n < total) {
      next.textContent = "Next →";
      next.href = stepHref(n + 1);
      next.setAttribute("rel", "next");
    } else {
      next.textContent = "Done";
      next.href = doneHref;
      next.removeAttribute("rel");
    }
    history.replaceState(null, "", stepHref(n));
    window.scrollTo({ top: 0 });
  }

  function stepHref(n) {
    return `/r/${slug}/cook?step=${n}`;
  }

  prev.addEventListener("click", (e) => {
    e.preventDefault();
    show(cur - 1);
  });
  next.addEventListener("click", (e) => {
    if (cur < total) {
      e.preventDefault();
      show(cur + 1);
    } // on the last step, "Done" navigates normally
  });

  document.addEventListener("keydown", (e) => {
    if (e.target.closest("input, textarea, summary")) return;
    if (e.key === "ArrowRight" || e.key === " ") { e.preventDefault(); show(cur + 1); }
    if (e.key === "ArrowLeft") { e.preventDefault(); show(cur - 1); }
  });

  // Swipe left/right on the step area. Ignore mostly-vertical gestures so
  // scrolling a long step still works.
  const main = shell.querySelector(".cook-main");
  let sx = 0, sy = 0;
  main.addEventListener("touchstart", (e) => {
    sx = e.changedTouches[0].clientX;
    sy = e.changedTouches[0].clientY;
  }, { passive: true });
  main.addEventListener("touchend", (e) => {
    const dx = e.changedTouches[0].clientX - sx;
    const dy = e.changedTouches[0].clientY - sy;
    if (Math.abs(dx) > 60 && Math.abs(dx) > 2 * Math.abs(dy)) show(dx < 0 ? cur + 1 : cur - 1);
  }, { passive: true });

  setupWakeLock(document.getElementById("wake"));
  setupTimers(shell, title, () => cur);
}

// --- wake lock ---

function setupWakeLock(el) {
  el.hidden = false;
  if (!("wakeLock" in navigator)) {
    el.textContent = "Screen may sleep";
    el.title = "This browser can't keep the screen on. Raise your auto-lock time while cooking.";
    return;
  }
  let lock = null;
  async function acquire() {
    try {
      lock = await navigator.wakeLock.request("screen");
      el.textContent = "Screen stays on";
      el.classList.add("on");
      lock.addEventListener("release", () => {
        el.textContent = "Screen may sleep";
        el.classList.remove("on");
      });
    } catch {
      el.textContent = "Screen may sleep";
      el.classList.remove("on");
    }
  }
  // The lock is dropped whenever the page is hidden; take it back on return.
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") acquire();
  });
  acquire();
}

// --- timers ---

const STORE = "grimoire.timers";

function loadTimers() {
  try {
    return JSON.parse(localStorage.getItem(STORE)) || [];
  } catch {
    return [];
  }
}

function saveTimers(timers) {
  try {
    localStorage.setItem(STORE, JSON.stringify(timers));
  } catch {
    /* private mode etc.: timers still work until the page closes */
  }
}

function fmt(ms) {
  const s = Math.max(0, Math.round(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${sec}` : `${m}:${sec}`;
}

function setupTimers(shell, title, currentStep) {
  const tray = document.getElementById("timers");
  let timers = loadTimers();

  // Turn each detected duration into a button.
  for (const span of shell.querySelectorAll("span.dur")) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "dur";
    btn.textContent = span.textContent;
    btn.dataset.seconds = span.dataset.seconds;
    btn.setAttribute("aria-label", `Start a timer for ${span.textContent}`);
    span.replaceWith(btn);
    btn.addEventListener("click", () => {
      const secs = Number(btn.dataset.seconds);
      timers.push({
        id: Date.now() + Math.random(),
        label: `Step ${currentStep()} · ${btn.textContent} · ${title}`,
        end: Date.now() + secs * 1000,
        notified: false,
      });
      saveTimers(timers);
      askNotify();
      render();
    });
  }

  function render() {
    const now = Date.now();
    tray.replaceChildren(
      ...timers.map((t) => {
        const left = t.end - now;
        const row = document.createElement("div");
        row.className = "timer" + (left <= 0 ? " done" : "");
        const time = document.createElement("span");
        time.className = "timer-time";
        time.textContent = left <= 0 ? "Done!" : fmt(left);
        const label = document.createElement("span");
        label.className = "timer-label";
        label.textContent = t.label;
        const x = document.createElement("button");
        x.type = "button";
        x.textContent = "✕";
        x.setAttribute("aria-label", left <= 0 ? "Dismiss timer" : "Cancel timer");
        x.addEventListener("click", () => {
          timers = timers.filter((o) => o.id !== t.id);
          saveTimers(timers);
          render();
        });
        row.append(time, label, x);
        return row;
      }),
    );
  }

  function tick() {
    const now = Date.now();
    let changed = false;
    for (const t of timers) {
      if (t.end <= now && !t.notified) {
        t.notified = true;
        changed = true;
        alertDone(t);
      }
    }
    if (changed) saveTimers(timers);
    render();
  }

  render();
  setInterval(tick, 1000);
  document.addEventListener("visibilitychange", tick);
}

function askNotify() {
  if ("Notification" in window && Notification.permission === "default") {
    Notification.requestPermission().catch(() => {});
  }
}

function alertDone(t) {
  if ("vibrate" in navigator) navigator.vibrate([300, 150, 300, 150, 300]);
  if ("Notification" in window && Notification.permission === "granted") {
    try {
      new Notification("Timer done", { body: t.label });
    } catch {
      /* iOS only allows notifications from an installed web app */
    }
  }
}

const shell = document.querySelector(".cook-shell");
if (shell) init(shell);
