// NjiraLab site behaviour. Everything degrades: with JavaScript off the page
// still reads, navigates, and exposes a working email address.
(function () {
  "use strict";

  var nav = document.getElementById("nav");
  var menu = document.getElementById("nav-menu");
  var navToggle = document.querySelector("[data-nav-toggle]");
  var dropdown = document.querySelector("[data-dropdown]");
  var dropToggle = document.querySelector("[data-dropdown-toggle]");

  // Solidify the header once the page has moved off the hero.
  if (nav) {
    var onScroll = function () {
      nav.classList.toggle("is-scrolled", window.scrollY > 12);
    };
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
  }

  // Mobile drawer.
  if (navToggle && menu) {
    var setMenu = function (open) {
      menu.classList.toggle("is-open", open);
      navToggle.setAttribute("aria-expanded", String(open));
      // The sheet covers the viewport, so the page behind it must not scroll.
      document.body.classList.toggle("nav-open", open);
    };

    navToggle.addEventListener("click", function () {
      setMenu(navToggle.getAttribute("aria-expanded") !== "true");
    });

    menu.addEventListener("click", function (e) {
      if (e.target.closest("a")) setMenu(false);
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") setMenu(false);
    });

    // Reset state when the drawer breakpoint is crossed.
    var mq = window.matchMedia("(min-width: 821px)");
    mq.addEventListener("change", function () {
      setMenu(false);
    });
  }

  // Products dropdown: click to open, closes on outside click or Escape.
  if (dropdown && dropToggle) {
    var setDrop = function (open) {
      dropdown.classList.toggle("is-open", open);
      dropToggle.setAttribute("aria-expanded", String(open));
    };

    // Click to toggle, on every pointer type. Opening on hover as well would
    // race the click (hover opens, the click that follows closes it again) and
    // would leave aria-expanded describing a state the menu is not in.
    dropToggle.addEventListener("click", function (e) {
      e.stopPropagation();
      setDrop(dropToggle.getAttribute("aria-expanded") !== "true");
    });

    document.addEventListener("click", function (e) {
      if (!dropdown.contains(e.target)) setDrop(false);
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") setDrop(false);
    });
  }

  // Scroll reveal is handled entirely in CSS (see .reveal), so there is no
  // observer here and no content that depends on JavaScript to become visible.

  document.querySelectorAll("[data-year]").forEach(function (el) {
    el.textContent = String(new Date().getFullYear());
  });

  // The contact form composes a mailto: message. Nothing is posted anywhere,
  // so no contact details are stored on our side.
  var form = document.getElementById("contact-form");
  if (form) {
    form.addEventListener("submit", function (e) {
      e.preventDefault();

      var address = form
        .querySelector("[data-contact-email]")
        .getAttribute("data-contact-email");
      var get = function (id) {
        return (document.getElementById(id).value || "").trim();
      };

      var name = get("cf-name");
      var email = get("cf-email");
      var message = get("cf-message");
      if (!name || !email || !message) {
        form.reportValidity();
        return;
      }

      var body = [
        "Name: " + name,
        "Email: " + email,
        "Topic: " + get("cf-topic"),
        "",
        message,
      ].join("\n");

      window.location.href =
        "mailto:" +
        address +
        "?subject=" +
        encodeURIComponent("NjiraLab enquiry from " + name) +
        "&body=" +
        encodeURIComponent(body);
    });
  }
})();
