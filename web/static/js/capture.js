// SPDX-License-Identifier: Apache-2.0
//
// The /capture page (docs/DESIGN.md §8.3). The bookmarklet opens this page
// and, once told we're ready, posts the recipe page's HTML and address.
// We accept a page only if the message really comes from the address it
// claims, then submit it to the server like a form. Nothing is saved
// until the user checks the preview and presses Save.

const form = document.getElementById("capture-form");
const status = document.getElementById("capture-status");
const MAX = 5 * 1024 * 1024;

function say(text) {
  if (status) status.textContent = text;
}

const confirmBox = document.getElementById("capture-confirm");

if (form && window.opener) {
  window.addEventListener("message", (e) => {
    // Only the window that opened us (the bookmarklet's page) may send.
    if (e.source !== window.opener) return;
    const d = e.data;
    if (!d || d.type !== "grimoire-capture" || typeof d.url !== "string" || typeof d.html !== "string") return;
    let claimed;
    try {
      claimed = new URL(d.url).origin;
    } catch {
      return;
    }
    if (claimed !== e.origin) {
      say("That page didn't come from where it says it did, so it wasn't captured.");
      return;
    }
    if (d.html.length > MAX) {
      say("That page is too large to capture (max 5 MB).");
      return;
    }
    form.elements.url.value = d.url;
    form.elements.html.value = d.html;
    // Ask first: any site could open this page and send something, so the
    // person confirms where the page came from before we read it.
    if (confirmBox) {
      confirmBox.querySelector(".js-origin").textContent = new URL(d.url).host;
      confirmBox.hidden = false;
      say("");
    } else {
      form.submit();
    }
  });
  confirmBox?.querySelector(".js-capture").addEventListener("click", () => {
    confirmBox.hidden = true;
    say("Reading the recipe…");
    form.submit();
  });
  // Tell the bookmarklet we're listening. The message carries nothing
  // secret, so any target origin is fine.
  window.opener.postMessage("grimoire-ready", "*");
  say("Waiting for the recipe page…");
  setTimeout(() => {
    if (!form.elements.html.value) {
      say("Nothing arrived. Some sites block this; on a phone, use the Share sheet instead, or type the recipe in.");
    }
  }, 5000);
} else if (form) {
  say("Open a recipe page and click the Kitchen Grimoire bookmarklet. You'll find it in Settings.");
}
