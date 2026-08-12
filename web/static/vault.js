// Vault — create a secret.
(function () {
  "use strict";

  var form = document.getElementById("vault-form");
  if (!form) return;

  var result = document.getElementById("result");
  var errorEl = document.getElementById("form-error");
  var createBtn = document.getElementById("create");
  var linkInput = document.getElementById("secret-link");
  var copyBtn = document.getElementById("copy");
  var againBtn = document.getElementById("again");

  var showError = function (message) {
    errorEl.textContent = message;
    errorEl.hidden = false;
  };

  var clearError = function () {
    errorEl.textContent = "";
    errorEl.hidden = true;
  };

  var describeExpiry = function (iso) {
    var when = new Date(iso);
    if (isNaN(when.getTime())) return "—";
    return when.toLocaleString(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
    });
  };

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    clearError();

    var secret = document.getElementById("secret").value;
    if (!secret.trim()) {
      showError("Enter the information you want to share.");
      return;
    }

    createBtn.disabled = true;
    createBtn.textContent = "Creating…";

    fetch("/api/create", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        secret: secret,
        type: document.getElementById("kind").value,
        expiry_time: parseInt(document.getElementById("expiry").value, 10),
        max_views: parseInt(document.getElementById("views").value, 10),
      }),
    })
      .then(function (res) {
        return res.json().then(function (data) {
          if (!res.ok) throw new Error(data.error || "Could not create the secret.");
          return data;
        });
      })
      .then(function (data) {
        linkInput.value = data.url;
        document.getElementById("fact-expiry").textContent = describeExpiry(
          data.expires_at
        );
        document.getElementById("fact-views").textContent =
          data.max_views === 1 ? "once" : data.max_views + " times";

        form.hidden = true;
        result.hidden = false;
        linkInput.focus();
        linkInput.select();
      })
      .catch(function (err) {
        showError(err.message || "Could not reach the server.");
      })
      .finally(function () {
        createBtn.disabled = false;
        createBtn.textContent = "Create secure link";
      });
  });

  copyBtn.addEventListener("click", function () {
    var done = function () {
      copyBtn.textContent = "Copied";
      setTimeout(function () {
        copyBtn.textContent = "Copy";
      }, 2000);
    };

    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(linkInput.value).then(done, function () {
        linkInput.select();
      });
      return;
    }
    linkInput.select();
    try {
      document.execCommand("copy");
      done();
    } catch (err) {
      showError("Copy the link manually — your browser blocked clipboard access.");
    }
  });

  againBtn.addEventListener("click", function () {
    // Drop the previous secret and its link from the page entirely.
    form.reset();
    linkInput.value = "";
    result.hidden = true;
    form.hidden = false;
    document.getElementById("secret").focus();
  });

  document.querySelectorAll("[data-year]").forEach(function (el) {
    el.textContent = String(new Date().getFullYear());
  });
})();
