// SPDX-License-Identifier: Apache-2.0
//
// Editor draft autosave (docs/DESIGN.md §8.1, §11.1 "calm by default").
// Saves what you type in this browser so a closed tab or a lost
// connection doesn't lose a half-written recipe. Nothing is sent to the
// server until you press Save; then the draft is cleared.

// Constants first: init() runs at the bottom, after they exist (a const
// used before its line throws, and the try/catch would hide it).
const WEEK = 7 * 24 * 60 * 60 * 1000;

function init(form) {
  const key = "grimoire.draft." + form.dataset.draftKey;
  const banner = document.getElementById("draft-banner");

  const fields = () => [...form.querySelectorAll("input[name], textarea[name]")]
    .filter((el) => el.type !== "hidden" && el.type !== "file" && el.type !== "checkbox");

  function snapshot() {
    return fields().map((el) => [el.name, el.value]);
  }

  function load() {
    try {
      const d = JSON.parse(localStorage.getItem(key));
      if (!d || Date.now() - d.at > WEEK) return null;
      return d;
    } catch {
      return null;
    }
  }

  function save() {
    try {
      localStorage.setItem(key, JSON.stringify({ at: Date.now(), values: snapshot() }));
    } catch {
      /* storage full or blocked: autosave is a convenience only */
    }
  }

  function clear() {
    try {
      localStorage.removeItem(key);
    } catch {
      /* ignore */
    }
  }

  // Offer to restore a draft that differs from what the page shows.
  const draft = load();
  const current = JSON.stringify(snapshot());
  if (draft && JSON.stringify(draft.values) !== current && banner) {
    banner.hidden = false;
    banner.querySelector(".js-restore").addEventListener("click", () => {
      // Fields are matched by name and position (groups repeat names).
      const byName = {};
      for (const [name, value] of draft.values) (byName[name] ||= []).push(value);
      const seen = {};
      for (const el of fields()) {
        const i = (seen[el.name] = (seen[el.name] ?? -1) + 1);
        const v = byName[el.name]?.[i];
        if (v !== undefined) el.value = v;
      }
      banner.hidden = true;
    });
    banner.querySelector(".js-discard").addEventListener("click", () => {
      clear();
      banner.hidden = true;
    });
  } else if (draft) {
    clear();
  }

  let timer;
  form.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(save, 600);
  });
  // Saving (or adding a group) submits the form; the draft's job is done.
  form.addEventListener("submit", (e) => {
    if (e.submitter?.value === "save") clear();
  });
}

const form = document.querySelector("form.editor[data-draft-key]");
if (form && form.dataset.draftKey) init(form);
