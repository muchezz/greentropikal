// Vault — read a secret.
//
// The identifier comes from this page's own path and the decryption key from
// the fragment; neither is ever written into the markup by the server. The
// plaintext is only ever assigned with textContent, so a secret containing
// markup is displayed, never executed.
(function () {
  "use strict";

  var dot = document.getElementById("dot");
  var status = document.getElementById("status");
  var headline = document.getElementById("headline");
  var note = document.getElementById("note");
  var output = document.getElementById("output");
  var meta = document.getElementById("meta");
  var actions = document.getElementById("actions");
  var postActions = document.getElementById("post-actions");
  var warn = document.getElementById("warn");
  var revealBtn = document.getElementById("reveal");
  var copyBtn = document.getElementById("copy");
  var hideBtn = document.getElementById("hide");
  var destroyBtn = document.getElementById("destroy");

  var HIDE_AFTER_MS = 90000;

  var id = window.location.pathname.split("/").filter(Boolean).pop() || "";
  var key = window.location.hash.replace(/^#/, "");
  var plaintext = null;
  var hideTimer = null;

  var setStatus = function (text, state) {
    status.textContent = text;
    dot.className = "dot" + (state ? " dot--" + state : "");
  };

  var fail = function (headlineText, detail) {
    setStatus("Unavailable", "gone");
    headline.textContent = headlineText;
    note.textContent = detail;
    note.classList.add("error-text");
    actions.hidden = true;
    postActions.hidden = true;
    output.hidden = true;
    warn.hidden = true;
  };

  if (!/^[A-Za-z0-9_-]{22}$/.test(id)) {
    fail("This link is not valid", "Check that you copied the whole link.");
    return;
  }

  if (!key) {
    fail(
      "This link is incomplete",
      "The part after the # carries the decryption key, and it is missing. Ask the sender for the complete link — some chat apps and email clients trim it."
    );
    return;
  }

  // Check availability without spending a view, so the reader knows what they
  // are about to open.
  fetch("/api/check", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: id }),
  })
    .then(function (res) {
      return res.json().then(function (data) {
        if (!res.ok) throw new Error(data.error || "This secret is not available.");
        return data;
      });
    })
    .then(function (data) {
      var remaining = data.max_views - data.view_count;
      setStatus("Waiting to be opened", "live");
      note.textContent =
        remaining === 1
          ? "This secret can be opened once. It is destroyed as soon as you reveal it."
          : "This secret can be opened " + remaining + " more times.";
      actions.hidden = false;
      revealBtn.focus();
    })
    .catch(function (err) {
      fail("This secret is no longer available", err.message);
    });

  var scheduleHide = function () {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(hideSecret, HIDE_AFTER_MS);
  };

  var showSecret = function () {
    output.textContent = plaintext;
    output.hidden = false;
    hideBtn.textContent = "Hide";
    scheduleHide();
  };

  var hideSecret = function () {
    clearTimeout(hideTimer);
    output.hidden = true;
    output.textContent = "";
    hideBtn.textContent = "Show";
  };

  revealBtn.addEventListener("click", function () {
    revealBtn.disabled = true;
    revealBtn.textContent = "Opening…";

    fetch("/api/view", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: id, key: key }),
    })
      .then(function (res) {
        return res.json().then(function (data) {
          if (!res.ok) throw new Error(data.error || "Could not open this secret.");
          return data;
        });
      })
      .then(function (data) {
        plaintext = data.secret;
        var remaining = data.max_views - data.view_count;

        setStatus(remaining > 0 ? "Opened" : "Destroyed", remaining > 0 ? "live" : "gone");
        headline.textContent = "Here is the secret";
        note.textContent =
          remaining > 0
            ? "Opened " + data.view_count + " of " + data.max_views + " times."
            : "That was the last permitted view. This secret has been destroyed.";
        meta.textContent = "Type: " + data.type;
        meta.hidden = false;

        actions.hidden = true;
        postActions.hidden = false;
        warn.textContent =
          remaining > 0
            ? "Save it somewhere safe. Once the view limit is reached this link stops working."
            : "Save it somewhere safe. This link has been used and will not work again.";
        warn.hidden = false;
        showSecret();
      })
      .catch(function (err) {
        fail("Could not open this secret", err.message);
      });
  });

  hideBtn.addEventListener("click", function () {
    if (output.hidden) showSecret();
    else hideSecret();
  });

  copyBtn.addEventListener("click", function () {
    if (plaintext === null) return;
    var done = function () {
      copyBtn.textContent = "Copied";
      setTimeout(function () {
        copyBtn.textContent = "Copy";
      }, 2000);
    };

    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(plaintext).then(done, function () {});
      return;
    }
    var scratch = document.createElement("textarea");
    scratch.value = plaintext;
    document.body.appendChild(scratch);
    scratch.select();
    try {
      document.execCommand("copy");
      done();
    } catch (err) {
      /* clipboard unavailable; the text is on screen to copy by hand */
    }
    document.body.removeChild(scratch);
  });

  destroyBtn.addEventListener("click", function () {
    if (!window.confirm("Destroy this secret now? It cannot be recovered.")) return;

    fetch("/api/burn", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: id }),
    }).then(function () {
      // Clear the plaintext from the page and from memory either way: if the
      // record was already gone, the outcome the reader wants is the same.
      plaintext = null;
      hideSecret();
      meta.hidden = true;
      postActions.hidden = true;
      warn.hidden = true;
      setStatus("Destroyed", "gone");
      headline.textContent = "This secret has been destroyed";
      note.textContent = "The stored record has been removed. This link no longer works.";
    });
  });

  document.querySelectorAll("[data-year]").forEach(function (el) {
    el.textContent = String(new Date().getFullYear());
  });
})();
